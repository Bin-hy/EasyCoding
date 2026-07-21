package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"mewcode/internal/config"
)

// 消息角色常量
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool" // 携带工具执行结果的回合
)

// ToolCall 协议无关地承载模型发起的一次工具调用（流式拼接完成后）
type ToolCall struct {
	ID    string          // provider 侧调用 id；回灌结果时配对
	Name  string          // 工具名（注册中心按名查找）
	Input json.RawMessage // 拼接完成的 JSON 参数
}

// ToolResult 协议无关地承载一次工具执行结果
type ToolResult struct {
	ToolCallID string // 对应 ToolCall.ID
	Content    string // 执行产出（成功内容或结构化错误文本）
	IsError    bool   // 是否为错误结果（F9）
}

// ToolDefinition 注册中心导出的协议无关工具定义
type ToolDefinition struct {
	Name        string         // 工具名
	Description string         // 给模型的用途说明
	InputSchema map[string]any // 完整 JSON Schema 对象：type/properties/required
}

// Message 一条对话消息
type Message struct {
	Role        string       // RoleUser | RoleAssistant | RoleTool
	Content     string       // 文本内容
	ToolCalls   []ToolCall   // 仅 assistant：本回合请求的工具调用
	ToolResults []ToolResult // 仅 RoleTool：工具执行结果（一条消息可含多个）
}

// StreamEvent 流式事件的四态语义：
//
//	Text 非空 → 文本增量（正文或 preamble）
//	ToolCalls 非空 → 模型请求执行这些工具（Done 之前发出）
//	Done → 本轮正常结束
//	Err 非空 → 出错
//
// Text 与 ToolCalls 可先后出现但不同时非空；Done/Err 互斥且为终结事件。
type StreamEvent struct {
	Text      string     // 文本增量
	ToolCalls []ToolCall // 非空：本轮模型请求执行这些工具
	Done      bool       // 本轮正常结束
	Err       error      // 出错
}

// Provider LLM 协议无关的统一接口
type Provider interface {
	Name() string                                                                          // 状态栏左侧：供应商名称
	Model() string                                                                         // 状态栏右侧：模型名
	Stream(ctx context.Context, msgs []Message, tools []ToolDefinition) <-chan StreamEvent // 发起一轮流式对话
}

// New 按 protocol 构造对应的适配器
func New(cfg config.ProviderConfig) (Provider, error) {
	switch cfg.Protocol {
	case "anthropic":
		return newAnthropicProvider(cfg)
	case "openai":
		return newOpenAIProvider(cfg)
	default:
		return nil, fmt.Errorf("不支持的协议类型: %s", cfg.Protocol)
	}
}
