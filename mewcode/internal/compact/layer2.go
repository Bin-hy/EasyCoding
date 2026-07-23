package compact

import (
	"context"
	"math"
	"strings"

	"mewcode/internal/llm"
)

// pickRecentTail 从消息尾部累加，满足 token 和条数两个下界后停止，再做配对修正。
// 两个下界都满足后才停手（"择宽"语义）。
func pickRecentTail(msgs []llm.Message) []llm.Message {
	if len(msgs) == 0 {
		return nil
	}

	totalChars := 0
	count := 0
	startIdx := len(msgs)

	for i := len(msgs) - 1; i >= 0; i-- {
		totalChars += len(msgs[i].Content)
		for _, tc := range msgs[i].ToolCalls {
			totalChars += len(tc.Input)
		}
		for _, tr := range msgs[i].ToolResults {
			totalChars += len(tr.Content)
		}
		count++
		startIdx = i

		estTokens := int64(math.Ceil(float64(totalChars) / estimateCharsPerToken))
		if estTokens >= recentKeepTokens && count >= recentKeepMessages {
			break
		}
	}

	// 配对修正：若截断点夹在 tool_use/tool_result 中间，向前推到 assistant 之前
	if startIdx < len(msgs) && msgs[startIdx].Role == llm.RoleTool {
		// 向前找对应的 assistant 消息
		for startIdx > 0 {
			startIdx--
			if msgs[startIdx].Role == llm.RoleAssistant && len(msgs[startIdx].ToolCalls) > 0 {
				break
			}
		}
	}

	out := make([]llm.Message, len(msgs)-startIdx)
	copy(out, msgs[startIdx:])
	return out
}

// joinAfterSummary 拼接摘要消息与近期原文，处理 role 衔接。
func joinAfterSummary(summaryAndRecovery llm.Message, recent []llm.Message) []llm.Message {
	if len(recent) == 0 {
		return []llm.Message{summaryAndRecovery}
	}

	// 防御：若近期原文首条是 tool，尝试前移到第一条 assistant 或丢弃
	for len(recent) > 0 && recent[0].Role == llm.RoleTool {
		recent = recent[1:]
	}

	// 若近期原文以 user 开头，插入 assistant 衔接占位
	if recent[0].Role == llm.RoleUser {
		placeholder := llm.Message{Role: llm.RoleAssistant, Content: "（已加载上下文摘要与恢复信息。请继续。）"}
		out := make([]llm.Message, 0, 1+1+len(recent))
		out = append(out, summaryAndRecovery)
		out = append(out, placeholder)
		out = append(out, recent...)
		return out
	}

	out := make([]llm.Message, 0, 1+len(recent))
	out = append(out, summaryAndRecovery)
	out = append(out, recent...)
	return out
}

// groupByUserTurn 按"用户消息 → 后续 assistant/tool 往返"分组。
func groupByUserTurn(msgs []llm.Message) [][]llm.Message {
	var groups [][]llm.Message
	var cur []llm.Message

	for _, m := range msgs {
		if m.Role == llm.RoleUser && len(cur) > 0 {
			groups = append(groups, cur)
			cur = nil
		}
		cur = append(cur, m)
	}
	if len(cur) > 0 {
		groups = append(groups, cur)
	}
	return groups
}

// summarizeOnce 发送一次摘要请求，返回提取后的摘要文本。
func summarizeOnce(ctx context.Context, in ManageInput, msgs []llm.Message) (string, error) {
	req := llm.Request{
		Messages: BuildSummaryPrompt(msgs),
		Tools:    nil, // 摘要不传工具
	}

	stream := in.Provider.Stream(ctx, req)
	var b strings.Builder
	for ev := range stream {
		if ev.Err != nil {
			return "", ev.Err
		}
		if ev.Text != "" {
			b.WriteString(ev.Text)
		}
		// Usage 在摘要请求内部捕获但不回写 SessionRuntime
		// 其他事件忽略
	}

	return ExtractSummary(b.String()), nil
}

// ptlRetry 摘要请求自身 PTL 的重试策略：
//
//	前 3 次：每次丢最旧的 1 组
//	之后：按 20% 比例丢（至少 1 组）
//	直到成功或全部丢光
func ptlRetry(ctx context.Context, in ManageInput, msgs []llm.Message) (string, error) {
	groups := groupByUserTurn(msgs)

	// 前 ptlRetryLimit(=3) 次直接重试
	for retry := 0; retry < ptlRetryLimit && len(groups) > 1; retry++ {
		groups = groups[1:] // 丢最旧 1 组
		flatMsgs := flattenGroups(groups)
		if len(flatMsgs) == 0 {
			continue
		}
		summary, err := summarizeOnce(ctx, in, flatMsgs)
		if err == nil {
			return summary, nil
		}
		// 仍然 PTL → 继续
	}

	// 超过 3 次后按比例丢
	for len(groups) > 1 {
		drop := int(math.Ceil(float64(len(groups)) * ptlDropPercentage))
		if drop < 1 {
			drop = 1
		}
		if drop > len(groups) {
			drop = len(groups)
		}
		groups = groups[drop:]
		flatMsgs := flattenGroups(groups)
		if len(flatMsgs) == 0 {
			break
		}
		summary, err := summarizeOnce(ctx, in, flatMsgs)
		if err == nil {
			return summary, nil
		}
	}

	return "", context.DeadlineExceeded // sentinel: 全部丢光
}

// flattenGroups 将分组消息列表展平为单一切片。
func flattenGroups(groups [][]llm.Message) []llm.Message {
	total := 0
	for _, g := range groups {
		total += len(g)
	}
	out := make([]llm.Message, 0, total)
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// runSummary 完整摘要流程：构造 prompt → 发请求 → 解析 → 拼接恢复段 → 近期原文 → 角色衔接。
func runSummary(ctx context.Context, in ManageInput) ([]llm.Message, error) {
	oldMsgs := in.Conv.Messages()

	// 入口拍快照，整个 runSummary 生命周期只用这一份
	recoverySnapshot := in.Recovery.Snapshot()

	// 摘要请求（含 PTL 重试）
	summaryText, err := summarizeOnce(ctx, in, oldMsgs)
	if err != nil {
		summaryText, err = ptlRetry(ctx, in, oldMsgs)
		if err != nil {
			return nil, err
		}
	}

	// 三段恢复
	recoveryText := BuildRecoveryAttachment(recoverySnapshot, in.ToolDefs)

	// 合并摘要 + 恢复到一条 user 消息
	combinedContent := "## 历史会话摘要\n" + summaryText + "\n\n" + recoveryText
	summaryAndRecovery := llm.Message{Role: llm.RoleUser, Content: combinedContent}

	// 近期原文 + 拼接
	recentTail := pickRecentTail(oldMsgs)
	return joinAfterSummary(summaryAndRecovery, recentTail), nil
}

// AutoCompact 自动摘要：成功后清零失败计数；整轮失败累加失败计数。
func AutoCompact(ctx context.Context, in ManageInput) ([]llm.Message, int64, int64, error) {
	beforeTok := in.EstimatedToken

	newMsgs, err := runSummary(ctx, in)
	if err != nil {
		in.AutoTracking.RecordFailure()
		return nil, beforeTok, 0, err
	}

	in.AutoTracking.RecordSuccess()
	afterTok := EstimateTokens(0, newMsgs, 0)
	return newMsgs, beforeTok, afterTok, nil
}

// ForceCompact 手动/紧急摘要：不走熔断器，失败不计入熔断计数。
func ForceCompact(ctx context.Context, in ManageInput) ([]llm.Message, int64, int64, error) {
	beforeTok := in.EstimatedToken

	newMsgs, err := runSummary(ctx, in)
	if err != nil {
		return nil, beforeTok, 0, err
	}

	afterTok := EstimateTokens(0, newMsgs, 0)
	return newMsgs, beforeTok, afterTok, nil
}
