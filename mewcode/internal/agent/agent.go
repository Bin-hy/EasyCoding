// Package agent 承载 ReAct 循环编排：多轮调 LLM → 权限判定 → 执行工具 → 结果回灌，直到任务完成。
// 对外吐出一条 Event 流供 TUI 渲染。只依赖 llm、tool、conversation、permission，不 import SDK，保持协议无关。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"mewcode/internal/compact"
	"mewcode/internal/conversation"
	"mewcode/internal/llm"
	"mewcode/internal/memory"
	"mewcode/internal/permission"
	"mewcode/internal/prompt"
	"mewcode/internal/tool"
)

// Phase 工具事件阶段
type Phase int

const (
	PhaseStart Phase = iota // 工具开始执行
	PhaseEnd                // 工具执行完毕
)

// CompactPhase 压缩生命周期阶段
type CompactPhase int

const (
	CompactPhaseBeforeAuto      CompactPhase = iota + 1 // 自动压缩开始
	CompactPhaseAfterAuto                               // 自动压缩完成
	CompactPhaseBeforeEmergency                         // 紧急压缩开始
	CompactPhaseAfterEmergency                          // 紧急压缩完成
)

// CompactEvent 压缩生命周期事件（兑现 spec F24a / F24b）
type CompactEvent struct {
	Phase  CompactPhase
	Before int64
	After  int64
	Err    error
}

// ToolEvent 一次工具调用的开始/结束（供 TUI 渲染工具行与结果摘要）
type ToolEvent struct {
	Name    string // 工具名
	Args    string // 参数预览（用于 ● name(args)）
	Phase   Phase  // 当前阶段
	Result  string // PhaseEnd：结果摘要
	IsError bool   // PhaseEnd：是否错误
}

// Usage 一轮请求的 token 用量（透传 llm.Usage 语义）。
type Usage struct {
	Input      int64 // 本轮输入 token 数
	Output     int64 // 本轮输出 token 数
	CacheWrite int64 // 缓存写入 token（首次创建缓存）
	CacheRead  int64 // 缓存读取 token（命中复用）
}

// ApprovalRequest 人在回路待批准请求（F8）。
type ApprovalRequest struct {
	Name    string                  // 工具内部名（用于展示 ● name(args)）
	Args    string                  // 参数预览
	Reason  string                  // 触发 Ask 的原因（模式 + 类别）
	Respond chan permission.Outcome // 缓冲=1：TUI 回传用户选择，agent 单次接收
}

// Event 对外事件流元素，TUI 据非零字段分派渲染。
type Event struct {
	Text     string           // 模型文本增量（preamble 或最终答复）
	Tool     *ToolEvent       // 工具调用开始/结束
	Usage    *Usage           // 本轮 token 用量（每轮 stream 结束后一次）
	Iter     int              // >0：进入第 Iter 轮迭代（进度提示）
	Notice   string           // 系统提示（停止原因等），仅用于 UI 展示，不入对话历史
	Done     bool             // 本轮（整个 Loop）结束
	Err      error            // 出错（不中断会话）
	Approval *ApprovalRequest // 非空：请求人在回路批准，消费者须回传 Respond 后再续读事件
	Compact  *CompactEvent    // 压缩生命周期事件（非空时 TUI 优先渲染状态提示）
}

// Agent 持有 provider、注册中心与权限引擎，执行 ReAct 循环。
type Agent struct {
	provider        llm.Provider
	registry        *tool.Registry
	version         string             // 应用版本，供环境段采集
	eng             *permission.Engine // 权限引擎（前四层判定 + 配置）
	runtime         *SessionRuntime    // 跨 Run 复用的长生命周期状态
	memMgr          *memory.Manager    // 可选：记忆更新管理器
	instructionText string             // 项目指令文本（注入系统提示）
	memoryText      string             // 记忆索引文本（注入系统提示）
	runMu           sync.Mutex         // 保证 Run 与 RunForceCompact 不并发
	running         int32              // 原子标记：是否正在执行 Run
}

// IsRunning 返回 Agent 当前是否正在执行 Run。
func (a *Agent) IsRunning() bool {
	return atomic.LoadInt32(&a.running) == 1
}

// New 创建 Agent，支持可选 Option 注入。
func New(p llm.Provider, r *tool.Registry, version string, eng *permission.Engine, opts ...Option) *Agent {
	a := &Agent{provider: p, registry: r, version: version, eng: eng}
	for _, opt := range opts {
		opt(a)
	}
	if a.runtime == nil {
		a.runtime = &SessionRuntime{ContextWindow: 200000}
	}
	return a
}

// 迭代与停止常量（内置，不可配）
const (
	maxIterations        = 25 // 迭代上限兜底（F2）
	maxUnknownRun        = 3  // 连续「整轮只产生未知工具调用」的迭代数上限（F2）
	planReminderInterval = 4  // 规划模式下每隔 planReminderInterval 轮重复完整提醒（含首轮）
)

// 停止/收尾提示文案——既作为 Event{Notice} 推给 UI，也作为 ensureAssistantTail 写入历史的兜底文本。
const (
	noticeMaxIter      = "（已达最大迭代轮数 25，自动停止；可继续发消息推进。）"
	noticeUnknownTools = "（连续多轮只请求到未注册的工具，自动停止。）"
	noticeStreamErr    = "（请求出错，本轮已中断。）"
	noticeCancelled    = "（已取消。）"
)

// Run 执行 ReAct Agent Loop，返回事件 channel；mode 决定工具集与系统后缀。
func (a *Agent) Run(ctx context.Context, conv *conversation.Conversation, mode permission.Mode) <-chan Event {
	ch := make(chan Event)

	go func() {
		defer close(ch)

		atomic.StoreInt32(&a.running, 1)
		defer atomic.StoreInt32(&a.running, 0)

		a.runMu.Lock()
		defer a.runMu.Unlock()

		if a.runtime == nil {
			a.runtime = &SessionRuntime{ContextWindow: 200000}
		}

		// 采集环境信息与装配稳定系统提示（Run 起始一次，跨轮复用）
		env := prompt.GatherEnvironment(a.version, a.provider.Model())
		sys := prompt.BuildSystemPrompt(a.instructionText, a.memoryText)
		envText := env.Render()
		unknownRun := 0

		for iter := 1; iter <= maxIterations; iter++ {
			emergencyRetried := false

			// 进度事件
			if !emit(ctx, ch, Event{Iter: iter}) {
				finishCancelled(conv)
				return
			}

			// 按 mode 取工具集
			var defs []llm.ToolDefinition
			if mode == permission.ModePlan {
				defs = a.registry.ReadOnlyDefinitions()
			} else {
				defs = a.registry.Definitions()
			}

			// ---------- 上下文管理（ManageContext）----------
			anchor, anchorLen := a.runtime.GetAnchor()
			cw := a.runtime.ContextWindow
			est := compact.EstimateTokens(anchor, conv.Messages(), anchorLen)

			in := compact.ManageInput{
				Conv:           conv,
				Provider:       a.provider,
				ContextWindow:  cw,
				ToolDefs:       defs,
				Replacement:    a.runtime.Replacement,
				Recovery:       a.runtime.Recovery,
				AutoTracking:   a.runtime.AutoTracking,
				Session:        a.runtime.Session,
				UsageAnchor:    anchor,
				AnchorMsgLen:   anchorLen,
				EstimatedToken: est,
				Trigger:        compact.TriggerAuto,
			}

			// 判断是否需要自动压缩（预估超出阈值）
			willSummarize := est >= int64(cw-compact.SummaryReserve-compact.AutoSafetyMargin)
			if willSummarize {
				emit(ctx, ch, Event{Compact: &CompactEvent{Phase: CompactPhaseBeforeAuto}})
			}

			out, mcErr := compact.ManageContext(ctx, in)

			if willSummarize {
				emit(ctx, ch, Event{Compact: &CompactEvent{
					Phase: CompactPhaseAfterAuto, Before: out.BeforeTokens, After: out.AfterTokens, Err: mcErr,
				}})
			}
			if mcErr != nil {
				emit(ctx, ch, Event{Err: mcErr})
				ensureAssistantTail(conv, noticeStreamErr)
				return
			}
			// ---------- 上下文管理结束 ----------

			// 按轮次计算规划模式 reminder
			var reminder string
			if mode == permission.ModePlan {
				full := iter == 1 || (iter-1)%planReminderInterval == 0
				reminder = prompt.PlanReminder(full)
			}

			// 流式请求本轮
			text, calls, usage, sErr := streamOnce(ctx, a.provider, conv.Messages(), defs, sys, envText, reminder, ch)

			// 紧急压缩处理
			if sErr != nil && errors.Is(sErr, llm.ErrPromptTooLong) && !emergencyRetried {
				out2, fErr := compact.ManageContext(ctx, compact.ManageInput{
					Conv:           conv,
					Provider:       a.provider,
					ContextWindow:  cw,
					ToolDefs:       defs,
					Replacement:    a.runtime.Replacement,
					Recovery:       a.runtime.Recovery,
					AutoTracking:   a.runtime.AutoTracking,
					Session:        a.runtime.Session,
					UsageAnchor:    anchor,
					AnchorMsgLen:   anchorLen,
					EstimatedToken: est,
					Trigger:        compact.TriggerEmergency,
				})
				_ = out2
				if fErr != nil {
					emit(ctx, ch, Event{Err: fErr})
					ensureAssistantTail(conv, noticeStreamErr)
					return
				}
				a.runtime.ResetAnchor()
				est2 := compact.EstimateTokens(0, conv.Messages(), 0)
				if est2 >= int64(cw-compact.ManualSafetyMargin) {
					emit(ctx, ch, Event{Err: sErr})
					ensureAssistantTail(conv, noticeStreamErr)
					return
				}
				emergencyRetried = true
				text, calls, usage, sErr = streamOnce(ctx, a.provider, conv.Messages(), defs, sys, envText, reminder, ch)
			}

			if sErr != nil && ctx.Err() != nil {
				finishCancelled(conv)
				return
			}
			if sErr != nil {
				emit(ctx, ch, Event{Err: sErr})
				ensureAssistantTail(conv, noticeStreamErr)
				return
			}

			// 主对话路径完成后更新锚点
			if usage != nil {
				a.runtime.UpdateAnchor(compact.UsageAnchor(usage), conv.Len())
			}

			// Usage 事件
			if usage != nil {
				emit(ctx, ch, Event{Usage: &Usage{
					Input:      usage.InputTokens,
					Output:     usage.OutputTokens,
					CacheWrite: usage.CacheWrite,
					CacheRead:  usage.CacheRead,
				}})
			}

			// 无工具调用：自然完成
			if len(calls) == 0 {
				final := ensureFinal(ch, text)
				conv.AddAssistant(final)

				// 记忆更新触发（每 5 轮或显式请求）
				if a.memMgr != nil && a.runtime != nil {
					turnCount := a.runtime.IncTurn()
					recentMsgs := extractRecentTurn(conv)
					if turnCount%5 == 0 || hasMemorySignal(recentMsgs) {
						a.memMgr.UpdateAsync(ctx, recentMsgs)
					}
				}

				emit(ctx, ch, Event{Done: true})
				return
			}

			// 有工具调用：记录 assistant 回合
			conv.AddAssistantWithToolCalls(text, calls)

			// 统计未知工具
			if allUnknown(a.registry, calls) {
				unknownRun++
			} else {
				unknownRun = 0
			}

			// 保序分批并发执行（含权限判定 + ReadFile 追踪）
			results, completed := a.executeBatched(ctx, calls, mode, ch)

			// 工具结果回灌前记录 ReadFile 追踪
			a.recordFileReads(calls, results)

			// 无论是否取消都回灌工具结果
			conv.AddToolResults(results)

			// 执行中被取消——最高优先级终止
			if !completed {
				ensureAssistantTail(conv, noticeCancelled)
				return
			}

			// 连续未知工具上限
			if unknownRun >= maxUnknownRun {
				emit(ctx, ch, Event{Notice: noticeUnknownTools})
				ensureAssistantTail(conv, noticeUnknownTools)
				emit(ctx, ch, Event{Done: true})
				return
			}
		}

		// 触达迭代上限
		emit(ctx, ch, Event{Notice: noticeMaxIter})
		ensureAssistantTail(conv, noticeMaxIter)
		emit(ctx, ch, Event{Done: true})
	}()

	return ch
}

// RunForceCompact 供 TUI /compact 调用，在 Agent 空闲时执行手动压缩。
func (a *Agent) RunForceCompact(ctx context.Context, conv *conversation.Conversation, defs []llm.ToolDefinition) (before, after int64, err error) {
	a.runMu.Lock()
	defer a.runMu.Unlock()

	if a.runtime == nil {
		a.runtime = &SessionRuntime{ContextWindow: 200000}
	}

	cw := a.runtime.ContextWindow
	anchor, anchorLen := a.runtime.GetAnchor()
	est := compact.EstimateTokens(anchor, conv.Messages(), anchorLen)

	in := compact.ManageInput{
		Conv:           conv,
		Provider:       a.provider,
		ContextWindow:  cw,
		ToolDefs:       defs,
		Replacement:    a.runtime.Replacement,
		Recovery:       a.runtime.Recovery,
		AutoTracking:   a.runtime.AutoTracking,
		Session:        a.runtime.Session,
		UsageAnchor:    anchor,
		AnchorMsgLen:   anchorLen,
		EstimatedToken: est,
		Trigger:        compact.TriggerManual,
	}

	out, err := compact.ManageContext(ctx, in)
	if err != nil {
		return est, 0, err
	}
	return out.BeforeTokens, out.AfterTokens, nil
}

// recordFileReads 在工具结果回灌前记录 ReadFile 调用的纯净字节。
func (a *Agent) recordFileReads(calls []llm.ToolCall, results []llm.ToolResult) {
	if a.runtime == nil || a.runtime.Recovery == nil {
		return
	}
	for i := range calls {
		if calls[i].Name != "read_file" {
			continue
		}
		if i >= len(results) || results[i].IsError {
			continue
		}
		var args map[string]any
		if err := json.Unmarshal(calls[i].Input, &args); err != nil {
			continue
		}
		path, ok := args["path"].(string)
		if !ok || path == "" {
			continue
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		b, err := os.ReadFile(absPath)
		if err != nil {
			continue
		}
		a.runtime.Recovery.RecordFile(absPath, string(b))
	}
}

// streamOnce 发起一次流式请求，返回累积文本、工具调用列表、本轮用量和错误。
func streamOnce(ctx context.Context, provider llm.Provider, msgs []llm.Message, tools []llm.ToolDefinition, sys, envText, reminder string, ch chan<- Event) (text string, calls []llm.ToolCall, usage *llm.Usage, err error) {
	var textBuilder strings.Builder

	req := llm.Request{
		Messages: msgs,
		Tools:    tools,
		System:   llm.System{Stable: sys, Environment: envText},
		Reminder: reminder,
	}

	stream := provider.Stream(ctx, req)
	for ev := range stream {
		switch {
		case ev.Err != nil:
			return textBuilder.String(), calls, nil, ev.Err
		case ev.Usage != nil:
			usage = ev.Usage
		case len(ev.ToolCalls) > 0:
			calls = append(calls, ev.ToolCalls...)
		case ev.Text != "":
			textBuilder.WriteString(ev.Text)
			if !emit(ctx, ch, Event{Text: ev.Text}) {
				return textBuilder.String(), calls, nil, ctx.Err()
			}
		}
	}

	// ctx 取消则算失败
	if ctx.Err() != nil {
		return textBuilder.String(), calls, nil, ctx.Err()
	}

	return textBuilder.String(), calls, usage, nil
}

// executeBatched 保序分批并发执行工具调用（F5），含权限判定（F6）。
// 返回结果列表和是否全部完成（false 表示执行中被取消）。
func (a *Agent) executeBatched(ctx context.Context, calls []llm.ToolCall, mode permission.Mode, ch chan<- Event) ([]llm.ToolResult, bool) {
	results := make([]llm.ToolResult, len(calls))
	i := 0

	for i < len(calls) {
		// 取消检查
		if ctx.Err() != nil {
			for k := i; k < len(calls); k++ {
				if results[k].ToolCallID == "" {
					results[k] = llm.ToolResult{
						ToolCallID: calls[k].ID,
						Content:    noticeCancelled,
						IsError:    true,
					}
				}
			}
			return results, false
		}

		if a.registry.IsReadOnly(calls[i].Name) {
			// 吃入连续只读区间 [i, j)
			j := i
			for j < len(calls) && a.registry.IsReadOnly(calls[j].Name) {
				j++
			}

			// 权限检查：逐个 Check，标记被拒项
			preDenied := make(map[int]string) // idx → deny reason
			for k := i; k < j; k++ {
				d, reason := a.eng.Check(mode, calls[k], true)
				if d == permission.Deny {
					preDenied[k] = reason
				}
			}

			// 先按序 emit 所有 Start 事件
			for k := i; k < j; k++ {
				argsPreview := argPreview(calls[k].Input)
				if !emit(ctx, ch, Event{Tool: &ToolEvent{
					Name:  calls[k].Name,
					Args:  argsPreview,
					Phase: PhaseStart,
				}}) {
					for m := k; m < len(calls); m++ {
						if results[m].ToolCallID == "" {
							results[m] = llm.ToolResult{
								ToolCallID: calls[m].ID,
								Content:    noticeCancelled,
								IsError:    true,
							}
						}
					}
					return results, false
				}
			}

			// 被拒项预先写结果，不纳入并发执行
			for k := i; k < j; k++ {
				if reason, denied := preDenied[k]; denied {
					results[k] = llm.ToolResult{
						ToolCallID: calls[k].ID,
						Content:    reason,
						IsError:    true,
					}
				}
			}

			// 并发执行未被拒的只读工具
			var wg sync.WaitGroup
			for k := i; k < j; k++ {
				if _, denied := preDenied[k]; denied {
					continue
				}
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					tctx, cancel := context.WithTimeout(ctx, tool.DefaultTimeout)
					defer cancel()
					r := a.registry.Execute(tctx, calls[idx].Name, calls[idx].Input)
					results[idx] = llm.ToolResult{
						ToolCallID: calls[idx].ID,
						Content:    r.Content,
						IsError:    r.IsError,
					}
				}(k)
			}
			wg.Wait()

			// 再按原始顺序 emit End 事件（含被拒项）
			for k := i; k < j; k++ {
				result := results[k]
				argsPreview := argPreview(calls[k].Input)
				resultSummary := argPreview(json.RawMessage(result.Content))
				resultSummary = truncateLines(resultSummary, 8)
				if !emit(ctx, ch, Event{Tool: &ToolEvent{
					Name:    calls[k].Name,
					Args:    argsPreview,
					Phase:   PhaseEnd,
					Result:  resultSummary,
					IsError: result.IsError,
				}}) {
					for m := j; m < len(calls); m++ {
						if results[m].ToolCallID == "" {
							results[m] = llm.ToolResult{
								ToolCallID: calls[m].ID,
								Content:    noticeCancelled,
								IsError:    true,
							}
						}
					}
					return results, false
				}
			}
			i = j
		} else {
			// 串行执行单个有副作用工具（含权限判定 + 人在回路）
			call := calls[i]
			argsPreview := argPreview(call.Input)

			// 前四层判定
			d, reason := a.eng.Check(mode, call, false)

			switch d {
			case permission.Deny:
				if !emit(ctx, ch, Event{Tool: &ToolEvent{
					Name:  call.Name,
					Args:  argsPreview,
					Phase: PhaseStart,
				}}) {
					for m := i; m < len(calls); m++ {
						if results[m].ToolCallID == "" {
							results[m] = llm.ToolResult{
								ToolCallID: calls[m].ID,
								Content:    noticeCancelled,
								IsError:    true,
							}
						}
					}
					return results, false
				}

				results[i] = llm.ToolResult{
					ToolCallID: call.ID,
					Content:    reason,
					IsError:    true,
				}

				if !emit(ctx, ch, Event{Tool: &ToolEvent{
					Name:    call.Name,
					Args:    argsPreview,
					Phase:   PhaseEnd,
					Result:  reason,
					IsError: true,
				}}) {
					for m := i + 1; m < len(calls); m++ {
						if results[m].ToolCallID == "" {
							results[m] = llm.ToolResult{
								ToolCallID: calls[m].ID,
								Content:    noticeCancelled,
								IsError:    true,
							}
						}
					}
					return results, false
				}
				i++

			case permission.Ask:
				outcome, ok2 := a.requestApproval(ctx, call, reason, ch)
				if !ok2 {
					for m := i; m < len(calls); m++ {
						if results[m].ToolCallID == "" {
							results[m] = llm.ToolResult{
								ToolCallID: calls[m].ID,
								Content:    noticeCancelled,
								IsError:    true,
							}
						}
					}
					return results, false
				}

				switch outcome {
				case permission.OutcomeDenyOnce:
					results[i] = llm.ToolResult{
						ToolCallID: call.ID,
						Content:    "用户拒绝执行：" + reason,
						IsError:    true,
					}

				case permission.OutcomeAllowOnce, permission.OutcomeAllowForever:
					if outcome == permission.OutcomeAllowForever {
						if err := a.eng.PersistLocalAllow(call); err != nil {
							emit(ctx, ch, Event{Notice: fmt.Sprintf("（写入本地规则失败: %v）", err)})
						}
					}

					if !emit(ctx, ch, Event{Tool: &ToolEvent{
						Name:  call.Name,
						Args:  argsPreview,
						Phase: PhaseStart,
					}}) {
						for m := i; m < len(calls); m++ {
							if results[m].ToolCallID == "" {
								results[m] = llm.ToolResult{
									ToolCallID: calls[m].ID,
									Content:    noticeCancelled,
									IsError:    true,
								}
							}
						}
						return results, false
					}

					tctx, cancel := context.WithTimeout(ctx, tool.DefaultTimeout)
					result := a.registry.Execute(tctx, call.Name, call.Input)
					cancel()

					results[i] = llm.ToolResult{
						ToolCallID: call.ID,
						Content:    result.Content,
						IsError:    result.IsError,
					}

					resultSummary := argPreview(json.RawMessage(result.Content))
					resultSummary = truncateLines(resultSummary, 8)
					if !emit(ctx, ch, Event{Tool: &ToolEvent{
						Name:    call.Name,
						Args:    argsPreview,
						Phase:   PhaseEnd,
						Result:  resultSummary,
						IsError: result.IsError,
					}}) {
						for m := i + 1; m < len(calls); m++ {
							if results[m].ToolCallID == "" {
								results[m] = llm.ToolResult{
									ToolCallID: calls[m].ID,
									Content:    noticeCancelled,
									IsError:    true,
								}
							}
						}
						return results, false
					}
				}
				i++

			case permission.Allow:
				if !emit(ctx, ch, Event{Tool: &ToolEvent{
					Name:  call.Name,
					Args:  argsPreview,
					Phase: PhaseStart,
				}}) {
					for m := i; m < len(calls); m++ {
						if results[m].ToolCallID == "" {
							results[m] = llm.ToolResult{
								ToolCallID: calls[m].ID,
								Content:    noticeCancelled,
								IsError:    true,
							}
						}
					}
					return results, false
				}

				tctx, cancel := context.WithTimeout(ctx, tool.DefaultTimeout)
				result := a.registry.Execute(tctx, call.Name, call.Input)
				cancel()

				results[i] = llm.ToolResult{
					ToolCallID: call.ID,
					Content:    result.Content,
					IsError:    result.IsError,
				}

				resultSummary := argPreview(json.RawMessage(result.Content))
				resultSummary = truncateLines(resultSummary, 8)
				if !emit(ctx, ch, Event{Tool: &ToolEvent{
					Name:    call.Name,
					Args:    argsPreview,
					Phase:   PhaseEnd,
					Result:  resultSummary,
					IsError: result.IsError,
				}}) {
					for m := i + 1; m < len(calls); m++ {
						if results[m].ToolCallID == "" {
							results[m] = llm.ToolResult{
								ToolCallID: calls[m].ID,
								Content:    noticeCancelled,
								IsError:    true,
							}
						}
					}
					return results, false
				}
				i++
			}
		}
	}

	if ctx.Err() != nil {
		for k := i; k < len(calls); k++ {
			if results[k].ToolCallID == "" {
				results[k] = llm.ToolResult{
					ToolCallID: calls[k].ID,
					Content:    noticeCancelled,
					IsError:    true,
				}
			}
		}
		return results, false
	}

	return results, true
}

// requestApproval 发送人在回路待批准请求，阻塞等待 TUI 回传决策。
func (a *Agent) requestApproval(ctx context.Context, call llm.ToolCall, reason string, ch chan<- Event) (permission.Outcome, bool) {
	respond := make(chan permission.Outcome, 1)
	req := &ApprovalRequest{
		Name:    call.Name,
		Args:    argPreview(call.Input),
		Reason:  reason,
		Respond: respond,
	}
	if !emit(ctx, ch, Event{Approval: req}) {
		return 0, false
	}

	select {
	case o := <-respond:
		return o, true
	case <-ctx.Done():
		return 0, false
	}
}

// emit 尝试向 ch 发送事件，返回 true。若 ctx 已取消则返回 false。
func emit(ctx context.Context, ch chan<- Event, e Event) bool {
	select {
	case ch <- e:
		return true
	case <-ctx.Done():
		return false
	}
}

// allUnknown 判断所有调用是否都请求了注册中心不存在的工具。
func allUnknown(registry *tool.Registry, calls []llm.ToolCall) bool {
	if len(calls) == 0 {
		return false
	}
	for _, c := range calls {
		if _, ok := registry.Get(c.Name); ok {
			return false
		}
	}
	return true
}

// ensureFinal 确保最终文本非空。
func ensureFinal(ch chan<- Event, text string) string {
	if text != "" {
		return text
	}
	placeholder := "（任务已完成）"
	select {
	case ch <- Event{Text: placeholder}:
	default:
	}
	return placeholder
}

// ensureAssistantTail 若历史末尾不是 assistant 角色，补一条兜底文本。
func ensureAssistantTail(conv *conversation.Conversation, fallback string) {
	if conv.LastRole() != llm.RoleAssistant {
		conv.AddAssistant(fallback)
	}
}

// finishCancelled 取消路径统一收尾。
func finishCancelled(conv *conversation.Conversation) {
	ensureAssistantTail(conv, noticeCancelled)
}

// argPreview 从 JSON 参数中提取关键字段做简短预览。
func argPreview(input json.RawMessage) string {
	var args map[string]interface{}
	if err := json.Unmarshal(input, &args); err != nil {
		return ""
	}
	preferKeys := []string{"path", "command", "pattern", "old_string"}
	for _, key := range preferKeys {
		if v, ok := args[key]; ok {
			s := fmt.Sprint(v)
			if len(s) > 60 {
				s = s[:60] + "..."
			}
			return s
		}
	}
	for _, v := range args {
		s := fmt.Sprint(v)
		if len(s) > 60 {
			s = s[:60] + "..."
		}
		return s
	}
	return ""
}

// truncateLines 对多行文本取前 n 行。
func truncateLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
		return strings.Join(lines, "\n") + "\n..."
	}
	return s
}

// extractRecentTurn 从对话历史中提取最近一轮的消息。
func extractRecentTurn(conv *conversation.Conversation) []llm.Message {
	msgs := conv.Messages()
	// 从后往前找最后一条 user 消息
	var startIdx int
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleUser {
			startIdx = i
			break
		}
	}
	return msgs[startIdx:]
}

// memorySignalKeywords 显式记忆请求关键词。
var memorySignalKeywords = []string{"记住", "记忆", "别忘", "remember", "memo"}

// hasMemorySignal 检查最近消息中是否包含显式记忆请求关键词。
func hasMemorySignal(msgs []llm.Message) bool {
	for _, msg := range msgs {
		if msg.Role == llm.RoleUser {
			lower := strings.ToLower(msg.Content)
			for _, kw := range memorySignalKeywords {
				if strings.Contains(lower, kw) {
					return true
				}
			}
		}
	}
	return false
}
