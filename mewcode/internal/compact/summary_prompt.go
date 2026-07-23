package compact

import (
	"fmt"
	"strings"

	"mewcode/internal/llm"
)

// summaryInstruction 摘要 prompt 模板。
const summaryInstruction = `You are summarizing a coding agent conversation. Output in two phases.

## Phase 1: Analysis (will be discarded)
Write your analysis draft inside <analysis> tags. Think through:
- What was the user trying to accomplish?
- What key decisions were made?
- What files were modified or read?
- What errors were encountered and how were they fixed?
- What is the current state of work?

## Phase 2: Formal Summary (will be kept)
Write the formal summary inside <summary> tags. Follow these 9 sections exactly:

## 1 主要请求和意图
## 2 关键技术概念
## 3 文件和代码段
## 4 错误和修复
## 5 问题解决过程
## 6 所有用户消息原文
## 7 待办任务
## 8 当前工作（最详细）
## 9 可能的下一步

IMPORTANT:
- Do NOT call any tools. Output plain text only.
- In section 6, preserve every user message in its original language, in chronological order.
- Section 8 should be the most detailed — describe exactly what is being worked on right now and at which step the work stopped.
- Write in the same language as the user's messages.`

// BuildSummaryPrompt 把对话消息嵌入摘要 prompt 模板，返回一条 user 消息。
func BuildSummaryPrompt(msgs []llm.Message) []llm.Message {
	serialized := serializeConversation(msgs)
	content := summaryInstruction + "\n\n[conversation]\n" + serialized
	return []llm.Message{{Role: llm.RoleUser, Content: content}}
}

// serializeConversation 把对话扁平化为可读文本。
// - user/assistant 消息: role: <content>
// - assistant 工具调用: [call <name> id=<id>]
// - tool 消息: [result id=<id> isError=<bool>] <content>
func serializeConversation(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleUser:
			b.WriteString("user: ")
			b.WriteString(m.Content)
			b.WriteString("\n")
		case llm.RoleAssistant:
			b.WriteString("assistant: ")
			if m.Content != "" {
				b.WriteString(m.Content)
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "\n[call %s id=%s]", tc.Name, tc.ID)
			}
			b.WriteString("\n")
		case llm.RoleTool:
			for _, tr := range m.ToolResults {
				fmt.Fprintf(&b, "[result id=%s isError=%v] %s\n", tr.ToolCallID, tr.IsError, tr.Content)
			}
		}
	}
	return b.String()
}

// ExtractSummary 从模型返回文本中提取 <summary>...</summary> 之间的正文。
// 找不到时返回原文降级使用。
func ExtractSummary(raw string) string {
	start := strings.Index(raw, "<summary>")
	if start == -1 {
		return raw
	}
	start += len("<summary>")

	end := strings.Index(raw[start:], "</summary>")
	if end == -1 {
		return raw
	}

	return strings.TrimSpace(raw[start : start+end])
}
