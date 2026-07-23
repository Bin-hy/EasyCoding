package compact

import (
	"math"

	"mewcode/internal/llm"
)

// UsageAnchor 将 Stream 尾事件中的 usage 合并成单一锚点值。
// 返回 InputTokens + OutputTokens + CacheRead + CacheWrite 之和。
func UsageAnchor(u *llm.Usage) int64 {
	if u == nil {
		return 0
	}
	return u.InputTokens + u.OutputTokens + u.CacheWrite + u.CacheRead
}

// messageChars 计算消息切片中所有内容的字符（字节）总量。
// 累加 len(Content) + 每个 ToolCalls[i].Input 长度 + 每个 ToolResults[i].Content 长度。
func messageChars(msgs []llm.Message) int {
	total := 0
	for i := range msgs {
		m := &msgs[i]
		total += len(m.Content)
		for _, tc := range m.ToolCalls {
			total += len(tc.Input)
		}
		for _, tr := range m.ToolResults {
			total += len(tr.Content)
		}
	}
	return total
}

// EstimateTokens 锚定最近一次 provider usage + 之后新增消息的字符增量。
// anchor: 上一次主对话路径 Stream 真实 usage 之和（int64）
// allMsgs: 当前 Conversation.Messages() 完整切片（必须已过 layer1）
// anchorMsgLen: anchor 当时 Conversation.Len()
// 返回 anchor + ceil(sum(chars(allMsgs[anchorMsgLen:])) / estimateCharsPerToken)
func EstimateTokens(anchor int64, allMsgs []llm.Message, anchorMsgLen int) int64 {
	if anchorMsgLen < 0 {
		anchorMsgLen = 0
	}
	var tail []llm.Message
	if anchorMsgLen < len(allMsgs) {
		tail = allMsgs[anchorMsgLen:]
	}
	chars := messageChars(tail)
	return anchor + int64(math.Ceil(float64(chars)/estimateCharsPerToken))
}
