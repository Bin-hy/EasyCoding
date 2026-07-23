package conversation

import (
	"sync"

	"mewcode/internal/llm"
)

// Conversation 进程内维护单会话多轮历史（user/assistant 交替）。
// 所有公开方法并发安全。
type Conversation struct {
	mu       sync.Mutex
	messages []llm.Message
}

// AddUser 追加一条用户消息。
func (c *Conversation) AddUser(text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, llm.Message{Role: "user", Content: text})
}

// AddAssistant 追加一条助手消息。
func (c *Conversation) AddAssistant(text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, llm.Message{Role: "assistant", Content: text})
}

// AddAssistantWithToolCalls 追加一条带工具调用的助手消息。
func (c *Conversation) AddAssistantWithToolCalls(text string, calls []llm.ToolCall) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, llm.Message{
		Role:      llm.RoleAssistant,
		Content:   text,
		ToolCalls: calls,
	})
}

// AddToolResults 追加工具执行结果回合。
func (c *Conversation) AddToolResults(results []llm.ToolResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, llm.Message{
		Role:        llm.RoleTool,
		ToolResults: results,
	})
}

// Messages 返回当前完整对话历史（深拷贝）。
func (c *Conversation) Messages() []llm.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]llm.Message, len(c.messages))
	for i := range c.messages {
		result[i] = c.messages[i]
		// 深拷贝子切片
		if len(c.messages[i].ToolCalls) > 0 {
			result[i].ToolCalls = make([]llm.ToolCall, len(c.messages[i].ToolCalls))
			copy(result[i].ToolCalls, c.messages[i].ToolCalls)
		}
		if len(c.messages[i].ToolResults) > 0 {
			result[i].ToolResults = make([]llm.ToolResult, len(c.messages[i].ToolResults))
			copy(result[i].ToolResults, c.messages[i].ToolResults)
		}
	}
	return result
}

// Len 返回当前消息数量。
func (c *Conversation) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.messages)
}

// LastRole 返回最后一条消息的角色；空历史返回 ""。
func (c *Conversation) LastRole() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.messages) == 0 {
		return ""
	}
	return c.messages[len(c.messages)-1].Role
}

// ReplaceMessages 用传入的消息切片整体替换对话历史（深拷贝，不暴露外部引用）。
func (c *Conversation) ReplaceMessages(msgs []llm.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	newSlice := make([]llm.Message, len(msgs))
	for i := range msgs {
		newSlice[i] = msgs[i]
		if len(msgs[i].ToolCalls) > 0 {
			newSlice[i].ToolCalls = make([]llm.ToolCall, len(msgs[i].ToolCalls))
			copy(newSlice[i].ToolCalls, msgs[i].ToolCalls)
		}
		if len(msgs[i].ToolResults) > 0 {
			newSlice[i].ToolResults = make([]llm.ToolResult, len(msgs[i].ToolResults))
			copy(newSlice[i].ToolResults, msgs[i].ToolResults)
		}
	}
	c.messages = newSlice
}
