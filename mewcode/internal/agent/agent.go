// Package agent 承载单轮闭环编排：请求#1（带工具）→ 收集工具调用 → 执行 → 结果回灌 → 请求#2（续答）→ 最终文本 → 停。
// 对外吐出一条 Event 流供 TUI 渲染。只依赖 llm、tool、conversation，不 import SDK，保持协议无关。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"mewcode/internal/conversation"
	"mewcode/internal/llm"
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

// Event 单轮闭环对外事件流元素，TUI 据非零字段分派渲染。
type Event struct {
	Text string     // 文本增量（preamble 或最终答复）
	Tool *ToolEvent // 工具调用开始/结束
	Done bool       // 本轮结束
	Err  error      // 出错（不中断会话）
}

// Agent 持有 provider 与注册中心，执行单轮闭环。
type Agent struct {
	provider llm.Provider
	registry *tool.Registry
}

// New 创建 Agent。
func New(p llm.Provider, r *tool.Registry) *Agent {
	return &Agent{provider: p, registry: r}
}

// Run 执行单轮闭环，返回事件 channel。
// 算法：
//  1. 请求#1：收集 preamble 文本和 ToolCalls
//  2. 无工具 → 文本直接 Done
//  3. 有工具 → 顺序执行 → 请求#2 续答 → 忽略二轮工具 → Done
func (a *Agent) Run(ctx context.Context, conv *conversation.Conversation) <-chan Event {
	ch := make(chan Event)

	go func() {
		defer close(ch)

		defs := a.registry.Definitions()

		// ---- 请求#1 ----
		preamble, calls, err := streamOnce(ctx, a.provider, conv.Messages(), defs, ch)
		if err != nil {
			ch <- Event{Err: err}
			return
		}

		// 无工具调用：纯文本回合
		if len(calls) == 0 {
			conv.AddAssistant(preamble)
			ch <- Event{Done: true}
			return
		}

		// ---- 有工具调用：记录 assistant 回合 ----
		conv.AddAssistantWithToolCalls(preamble, calls)

		// 顺序执行每个工具
		var results []llm.ToolResult
		for _, call := range calls {
			// 参数预览（取简短串）
			argsPreview := argPreview(call.Input)

			// 开始
			ch <- Event{Tool: &ToolEvent{
				Name:  call.Name,
				Args:  argsPreview,
				Phase: PhaseStart,
			}}

			// 超时执行
			tctx, cancel := context.WithTimeout(ctx, tool.DefaultTimeout)
			result := a.registry.Execute(tctx, call.Name, call.Input)
			cancel()

			// 结果摘要（UI 截断 ~8 行）
			resultSummary := argPreview(json.RawMessage(result.Content))
			resultSummary = truncateLines(resultSummary, 8)

			// 结束
			ch <- Event{Tool: &ToolEvent{
				Name:    call.Name,
				Args:    argsPreview,
				Phase:   PhaseEnd,
				Result:  resultSummary,
				IsError: result.IsError,
			}}

			results = append(results, llm.ToolResult{
				ToolCallID: call.ID,
				Content:    result.Content,
				IsError:    result.IsError,
			})
		}

		// 回灌工具结果
		conv.AddToolResults(results)

		// ---- 请求#2：续答 ----
		final, _, err := streamOnce(ctx, a.provider, conv.Messages(), defs, ch)
		if err != nil {
			ch <- Event{Err: err}
			return
		}

		// 空最终答复用占位文本（确保 conversation 中 assistant 回合非空）
		if final == "" {
			final = "（单轮工具调用已完成）"
		}
		conv.AddAssistant(final)
		ch <- Event{Done: true}
	}()

	return ch
}

// streamOnce 发起一次流式请求，将 Text 增量转发到 ch，返回累积的完整文本和工具调用列表。
// 出错时返回 error；工具调用列表可能为 nil。
func streamOnce(ctx context.Context, provider llm.Provider, msgs []llm.Message, tools []llm.ToolDefinition, ch chan<- Event) (string, []llm.ToolCall, error) {
	var text strings.Builder
	var toolCalls []llm.ToolCall

	stream := provider.Stream(ctx, msgs, tools)
	for ev := range stream {
		switch {
		case ev.Text != "":
			text.WriteString(ev.Text)
			ch <- Event{Text: ev.Text}
		case len(ev.ToolCalls) > 0:
			toolCalls = ev.ToolCalls
		case ev.Err != nil:
			return text.String(), toolCalls, ev.Err
		case ev.Done:
			return text.String(), toolCalls, nil
		}
	}
	return text.String(), toolCalls, nil
}

// argPreview 从 JSON 参数中提取关键字段做简短预览。
func argPreview(input json.RawMessage) string {
	var args map[string]interface{}
	if err := json.Unmarshal(input, &args); err != nil {
		return ""
	}
	// 优先展示：path > command > pattern > old_string > content
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
	// 否则取第一个 string 字段
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
