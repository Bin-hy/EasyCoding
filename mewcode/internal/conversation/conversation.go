package conversation

import (
	"sync"

	"mewcode/internal/llm"
)

// Conversation 进程内维护单会话多轮历史（user/assistant 交替）。
// 所有公开方法并发安全。
type Conversation struct {
	mu        sync.Mutex
	messages  []llm.Message
	onAppend  func(llm.Message)   // 可选：消息追加回调
	onReplace func([]llm.Message) // 可选：消息替换回调
}

// New 创建空会话（无回调），向后兼容。
func New() *Conversation {
	return &Conversation{}
}

// NewWithHooks 创建带回调的会话。
func NewWithHooks(onAppend func(llm.Message), onReplace func([]llm.Message)) *Conversation {
	return &Conversation{onAppend: onAppend, onReplace: onReplace}
}

// NewFromMessages 从已有消息列表创建会话（恢复场景），可选回调。
// msgs 会被深拷贝，不暴露外部引用。
func NewFromMessages(msgs []llm.Message, onAppend func(llm.Message), onReplace func([]llm.Message)) *Conversation {
	c := &Conversation{onAppend: onAppend, onReplace: onReplace}
	c.messages = make([]llm.Message, len(msgs))
	for i := range msgs {
		c.messages[i] = msgs[i]
		if len(msgs[i].ToolCalls) > 0 {
			c.messages[i].ToolCalls = make([]llm.ToolCall, len(msgs[i].ToolCalls))
			copy(c.messages[i].ToolCalls, msgs[i].ToolCalls)
		}
		if len(msgs[i].ToolResults) > 0 {
			c.messages[i].ToolResults = make([]llm.ToolResult, len(msgs[i].ToolResults))
			copy(c.messages[i].ToolResults, msgs[i].ToolResults)
		}
	}
	return c
}

// AddUser 追加一条用户消息。
func (c *Conversation) AddUser(text string) {
	c.mu.Lock()
	c.messages = append(c.messages, llm.Message{Role: "user", Content: text})
	msg := c.messages[len(c.messages)-1] // 拷贝一份给回调（已解锁后使用）
	c.mu.Unlock()

	if c.onAppend != nil {
		c.onAppend(msg)
	}
}

// AddAssistant 追加一条助手消息。
func (c *Conversation) AddAssistant(text string) {
	c.mu.Lock()
	c.messages = append(c.messages, llm.Message{Role: "assistant", Content: text})
	msg := c.messages[len(c.messages)-1]
	c.mu.Unlock()

	if c.onAppend != nil {
		c.onAppend(msg)
	}
}

// AddAssistantWithToolCalls 追加一条带工具调用的助手消息。
func (c *Conversation) AddAssistantWithToolCalls(text string, calls []llm.ToolCall) {
	c.mu.Lock()
	c.messages = append(c.messages, llm.Message{
		Role:      llm.RoleAssistant,
		Content:   text,
		ToolCalls: calls,
	})
	msg := c.messages[len(c.messages)-1]
	c.mu.Unlock()

	if c.onAppend != nil {
		c.onAppend(msg)
	}
}

// AddToolResults 追加工具执行结果回合。
func (c *Conversation) AddToolResults(results []llm.ToolResult) {
	c.mu.Lock()
	c.messages = append(c.messages, llm.Message{
		Role:        llm.RoleTool,
		ToolResults: results,
	})
	msg := c.messages[len(c.messages)-1]
	c.mu.Unlock()

	if c.onAppend != nil {
		c.onAppend(msg)
	}
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
	c.mu.Unlock()

	if c.onReplace != nil {
		c.onReplace(msgs)
	}
}
