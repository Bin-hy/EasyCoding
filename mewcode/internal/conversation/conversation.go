package conversation

import "mewcode/internal/llm"

// Conversation 进程内维护单会话多轮历史（user/assistant 交替）
type Conversation struct {
	messages []llm.Message
}

// AddUser 追加一条用户消息
func (c *Conversation) AddUser(text string) {
	c.messages = append(c.messages, llm.Message{Role: "user", Content: text})
}

// AddAssistant 追加一条助手消息
func (c *Conversation) AddAssistant(text string) {
	c.messages = append(c.messages, llm.Message{Role: "assistant", Content: text})
}

// Messages 返回当前完整对话历史（副本）
func (c *Conversation) Messages() []llm.Message {
	result := make([]llm.Message, len(c.messages))
	copy(result, c.messages)
	return result
}
