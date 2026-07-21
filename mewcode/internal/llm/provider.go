package llm

import (
	"context"
	"fmt"

	"mewcode/internal/config"
)

// Message 一条对话消息
type Message struct {
	Role    string // "user" | "assistant"
	Content string
}

// StreamEvent 流式事件：文本增量 / 结束 / 错误（Done 与 Err 互斥）
type StreamEvent struct {
	Text string // 文本增量
	Done bool   // 本轮正常结束
	Err  error  // 出错
}

// Provider LLM 协议无关的统一接口
type Provider interface {
	Name() string                                                        // 状态栏左侧：供应商名称
	Model() string                                                       // 状态栏右侧：模型名
	Stream(ctx context.Context, msgs []Message) <-chan StreamEvent       // 发起一轮流式对话
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
