package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"mewcode/internal/config"
)

// ErrPromptTooLong 表示请求上下文超出 provider 窗口上限。
var ErrPromptTooLong = errors.New("prompt too long for context window")

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

// Usage 协议无关地承载一轮请求的 token 用量。
type Usage struct {
	InputTokens  int64 // 本轮请求输入（含完整历史）token 数
	OutputTokens int64 // 本轮响应输出 token 数
	CacheWrite   int64 // 缓存写入 token（Anthropic: cache_creation_input_tokens; OpenAI: 恒 0）
	CacheRead    int64 // 缓存读取 token（Anthropic: cache_read_input_tokens; OpenAI: cached_tokens）
}

// StreamEvent 流式事件的五态语义：
//
//	Text 非空 → 文本增量（正文或 preamble）
//	ToolCalls 非空 → 模型请求执行这些工具（Done 之前发出）
//	Usage 非空 → 本轮 token 用量（Done 之前一次性发出）
//	Done → 本轮正常结束
//	Err 非空 → 出错
//
// Text 与 ToolCalls 可先后出现但不同时非空；Done/Err 互斥且为终结事件。
type StreamEvent struct {
	Text      string     // 文本增量
	ToolCalls []ToolCall // 非空：本轮模型请求执行这些工具
	Usage     *Usage     // 非空：本轮 token 用量（Done 之前一次性发出）
	Done      bool       // 本轮正常结束
	Err       error      // 出错
}

// System 承载两段系统提示内容。
type System struct {
	Stable      string // 可缓存：装配好的稳定系统提示（不含时间/环境等变化成分）
	Environment string // 不缓存：环境信息段（每轮可能变化）
}

// Request 聚合一次 Stream 调用所需的全部入参。
type Request struct {
	Messages []Message        // 持久对话历史（不含本轮 reminder）
	Tools    []ToolDefinition // 本轮工具集（普通=全量 / 规划=只读）
	System   System           // 稳定系统提示 + 环境段
	Reminder string           // 本轮 system-reminder 内容（已含标签；空=不注入）
}

// Provider LLM 协议无关的统一接口
type Provider interface {
	Name() string                                               // 状态栏左侧：供应商名称
	Model() string                                              // 状态栏右侧：模型名
	Stream(ctx context.Context, req Request) <-chan StreamEvent // 发起一轮流式对话
}

// New 按 protocol 构造对应的适配器
func New(cfg config.ProviderConfig) (Provider, error) {
	switch cfg.Protocol {
	case config.ProtocolAnthropic:
		return newAnthropicProvider(cfg)
	case config.ProtocolOpenAI:
		return newOpenAIProvider(cfg)
	default:
		return nil, fmt.Errorf("不支持的协议类型: %s", cfg.Protocol)
	}
}
