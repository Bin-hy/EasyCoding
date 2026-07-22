// Package agent 承载 ReAct 循环编排：多轮调 LLM → 权限判定 → 执行工具 → 结果回灌，直到任务完成。
// 对外吐出一条 Event 流供 TUI 渲染。只依赖 llm、tool、conversation、permission，不 import SDK，保持协议无关。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"mewcode/internal/conversation"
	"mewcode/internal/llm"
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
}

// Agent 持有 provider、注册中心与权限引擎，执行 ReAct 循环。
type Agent struct {
	provider llm.Provider
	registry *tool.Registry
	version  string             // 应用版本，供环境段采集
	eng      *permission.Engine // 权限引擎（前四层判定 + 配置）
}

// New 创建 Agent。
func New(p llm.Provider, r *tool.Registry, version string, eng *permission.Engine) *Agent {
	return &Agent{provider: p, registry: r, version: version, eng: eng}
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

		// 采集环境信息与装配稳定系统提示（Run 起始一次，跨轮复用）
		env := prompt.GatherEnvironment(a.version, a.provider.Model())
		sys := prompt.BuildSystemPrompt()

		// 按 mode 取工具集
		var defs []llm.ToolDefinition
		if mode == permission.ModePlan {
			defs = a.registry.ReadOnlyDefinitions()
		} else {
			defs = a.registry.Definitions()
		}

		envText := env.Render()
		unknownRun := 0

		for iter := 1; iter <= maxIterations; iter++ {
			// 进度事件（F9）
			if !emit(ctx, ch, Event{Iter: iter}) {
				finishCancelled(conv)
				return
			}

			// 按轮次计算规划模式 reminder
			var reminder string
			if mode == permission.ModePlan {
				full := iter == 1 || (iter-1)%planReminderInterval == 0
				reminder = prompt.PlanReminder(full)
			}

			// 流式请求本轮
			text, calls, usage, ok := streamOnce(ctx, a.provider, conv.Messages(), defs, sys, envText, reminder, ch)
			if !ok && ctx.Err() != nil {
				// ctx 取消
				finishCancelled(conv)
				return
			}
			if !ok {
				// 流出错（Err 已在 streamOnce 内发出）
				ensureAssistantTail(conv, noticeStreamErr)
				return
			}

			// Usage 事件（含缓存字段 F4）
			if usage != nil {
				emit(ctx, ch, Event{Usage: &Usage{
					Input:      usage.InputTokens,
					Output:     usage.OutputTokens,
					CacheWrite: usage.CacheWrite,
					CacheRead:  usage.CacheRead,
				}})
			}

			// 无工具调用：自然完成（F2-1）
			if len(calls) == 0 {
				final := ensureFinal(ch, text)
				conv.AddAssistant(final)
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

			// 保序分批并发执行（F5），含权限判定
			results, completed := a.executeBatched(ctx, calls, mode, ch)

			// 无论是否取消都回灌工具结果（F6）
			conv.AddToolResults(results)

			// 执行中被取消——最高优先级终止
			if !completed {
				ensureAssistantTail(conv, noticeCancelled)
				return
			}

			// 连续未知工具上限（F2-4）
			if unknownRun >= maxUnknownRun {
				emit(ctx, ch, Event{Notice: noticeUnknownTools})
				ensureAssistantTail(conv, noticeUnknownTools)
				emit(ctx, ch, Event{Done: true})
				return
			}
		}

		// 触达迭代上限（F2-2）
		emit(ctx, ch, Event{Notice: noticeMaxIter})
		ensureAssistantTail(conv, noticeMaxIter)
		emit(ctx, ch, Event{Done: true})
	}()

	return ch
}

// streamOnce 发起一次流式请求，将 Text 增量转发到 ch，返回累积的完整文本、工具调用列表、本轮用量。
// 出错时返回 ok=false；ctx 取消也算不 ok。
func streamOnce(ctx context.Context, provider llm.Provider, msgs []llm.Message, tools []llm.ToolDefinition, sys, envText, reminder string, ch chan<- Event) (text string, calls []llm.ToolCall, usage *llm.Usage, ok bool) {
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
			emit(ctx, ch, Event{Err: ev.Err})
			return textBuilder.String(), calls, nil, false
		case ev.Usage != nil:
			usage = ev.Usage
		case len(ev.ToolCalls) > 0:
			calls = append(calls, ev.ToolCalls...)
		case ev.Text != "":
			textBuilder.WriteString(ev.Text)
			if !emit(ctx, ch, Event{Text: ev.Text}) {
				return textBuilder.String(), calls, nil, false
			}
		}
	}

	// ctx 取消则算失败
	if ctx.Err() != nil {
		return textBuilder.String(), calls, nil, false
	}

	return textBuilder.String(), calls, usage, true
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
				// 只读永不 Ask（N3），无需处理 Ask 分支
			}

			// 先按序 emit 所有 Start 事件
			for k := i; k < j; k++ {
				argsPreview := argPreview(calls[k].Input)
				if !emit(ctx, ch, Event{Tool: &ToolEvent{
					Name:  calls[k].Name,
					Args:  argsPreview,
					Phase: PhaseStart,
				}}) {
					// ctx 已取消，补齐剩余结果
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
					continue // 跳过被拒项
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
					// ctx 已取消
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
				// Start + End 事件（错误）
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
				// 第五层：人在回路
				outcome, ok2 := a.requestApproval(ctx, call, reason, ch)
				if !ok2 {
					// ctx 取消
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
						// 永久放行：写本地配置
						if err := a.eng.PersistLocalAllow(call); err != nil {
							// 仅记录，不阻断执行
							emit(ctx, ch, Event{Notice: fmt.Sprintf("（写入本地规则失败: %v）", err)})
						}
					}

					// 执行工具
					// Start 事件
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

					// End 事件
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
				// 直接执行
				// Start 事件
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

				// End 事件
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

	// 即使所有工具执行完成，若 ctx 已取消则报 incomplete
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
// 返回 (Outcome, true) 表示收到决策；(0, false) 表示 ctx 已取消。
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

// allUnknown 判断所有调用是否都请求了注册中心不存在的工具。空列表返回 false。
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

// ensureFinal 确保最终文本非空；为空时返回占位文本（避免空 assistant 回合破坏下一轮请求）。
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
// 确保取消/出错/上限后角色仍交替，下一轮请求不报 400（F6）。
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
