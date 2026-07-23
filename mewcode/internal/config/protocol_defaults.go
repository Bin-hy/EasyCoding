package config

// 协议标识常量
const (
	ProtocolAnthropic = "anthropic"
	ProtocolOpenAI    = "openai"
)

// 协议默认上下文窗口大小（token）
const (
	// DefaultAnthropicContextWindow Anthropic 默认上下文窗口
	DefaultAnthropicContextWindow = 200000
	// DefaultOpenAIContextWindow OpenAI 默认上下文窗口
	DefaultOpenAIContextWindow = 128000
)
