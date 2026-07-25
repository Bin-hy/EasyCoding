package agent

import (
	"strings"

	"mewcode/internal/llm"
)

// ForkBoilerplateTag 是 Fork Boilerplate 的标记标签，用于上下文检测（spec F23）。
const ForkBoilerplateTag = "<fork_boilerplate>"

// ForkBoilerplate 是 Fork 子 Agent 首条 user 消息的前缀，约束其行为（spec F23）。
//
// 核心约束：
//  1. 不能再 Fork（调用 Agent 工具会被拦截）
//  2. 不要对话、不要提问、不要请求确认
//  3. 直接使用工具：读文件、搜索代码、做修改
//  4. 严格限制在被分配的任务范围内
//  5. 最终报告以 "Scope:" 开头，500 字以内
const ForkBoilerplate = `<fork_boilerplate>
你是一个 Fork 出来的工作进程。你不是主 Agent。
规则（不可协商）：
1. 不能再 Fork（调用 Agent 工具会被拦截）。
2. 不要对话、不要提问、不要请求确认。
3. 直接使用工具：读文件、搜索代码、做修改。
4. 严格限制在你被分配的任务范围内。
5. 最终报告以 "Scope:" 开头，500 字以内。
</fork_boilerplate>

`

// BuildForkedMessages 把父对话克隆到 Fork 子对话（spec F22）。
//
// 行为：
//  1. 深拷贝 parentMsgs（所有 Message + 内部 ToolCalls/ToolResults 切片）
//  2. 扫描末尾 assistant 消息的 ToolCalls，如果对应的 RoleTool 消息缺失，
//     生成一条 placeholder ToolResults（每个 ID 一条 "[forked, skipped]" 错误内容）
//  3. 追加 user 消息 = ForkBoilerplate + task
//
// 返回新消息列表，直接用 conversation.NewFromMessages 装载即可。
func BuildForkedMessages(parentMsgs []llm.Message, task string) []llm.Message {
	// 1. 深拷贝
	cloned := cloneMessages(parentMsgs)

	// 2. 处理悬空 tool_use
	cloned = fixPendingToolCalls(cloned)

	// 3. 追加 user 消息 = ForkBoilerplate + task
	userMsg := llm.Message{
		Role:    llm.RoleUser,
		Content: ForkBoilerplate + task,
	}
	cloned = append(cloned, userMsg)

	return cloned
}

// IsForkContext 判定一个消息列表是否来自 Fork 上下文（spec F24.3）。
// QuerySource 检测的兜底机制——caller 链丢失时靠这个。
func IsForkContext(msgs []llm.Message) bool {
	for _, msg := range msgs {
		if strings.Contains(msg.Content, ForkBoilerplateTag) {
			return true
		}
		for _, tc := range msg.ToolCalls {
			if strings.Contains(string(tc.Input), ForkBoilerplateTag) {
				return true
			}
		}
		for _, tr := range msg.ToolResults {
			if strings.Contains(tr.Content, ForkBoilerplateTag) {
				return true
			}
		}
	}
	return false
}

// cloneMessages 深拷贝消息列表。
func cloneMessages(msgs []llm.Message) []llm.Message {
	cloned := make([]llm.Message, len(msgs))
	for i, msg := range msgs {
		cloned[i] = msg
		// 深拷贝 ToolCalls
		if msg.ToolCalls != nil {
			cloned[i].ToolCalls = make([]llm.ToolCall, len(msg.ToolCalls))
			copy(cloned[i].ToolCalls, msg.ToolCalls)
		}
		// 深拷贝 ToolResults
		if msg.ToolResults != nil {
			cloned[i].ToolResults = make([]llm.ToolResult, len(msg.ToolResults))
			copy(cloned[i].ToolResults, msg.ToolResults)
		}
	}
	return cloned
}

// fixPendingToolCalls 扫描末尾 assistant 消息的悬空 tool_use，
// 生成 placeholder ToolResult 使消息格式合法（spec F22.2）。
func fixPendingToolCalls(msgs []llm.Message) []llm.Message {
	if len(msgs) == 0 {
		return msgs
	}

	// 找到最后一条 assistant 消息
	lastIdx := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleAssistant && len(msgs[i].ToolCalls) > 0 {
			lastIdx = i
			break
		}
	}
	if lastIdx == -1 {
		return msgs
	}

	// 收集哪些 ToolCallID 已被后续 RoleTool 消息消费
	consumed := make(map[string]bool)
	for i := lastIdx + 1; i < len(msgs); i++ {
		if msgs[i].Role == llm.RoleTool {
			for _, tr := range msgs[i].ToolResults {
				consumed[tr.ToolCallID] = true
			}
		}
	}

	// 收集未配对的 ToolCall
	var pending []llm.ToolResult
	for _, tc := range msgs[lastIdx].ToolCalls {
		if !consumed[tc.ID] {
			pending = append(pending, llm.ToolResult{
				ToolCallID: tc.ID,
				Content:    "[forked, skipped]",
				IsError:    true,
			})
		}
	}

	if len(pending) == 0 {
		return msgs
	}

	// 追加一条 RoleTool 消息
	toolMsg := llm.Message{
		Role:        llm.RoleTool,
		ToolResults: pending,
	}

	return append(msgs, toolMsg)
}
