package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ProviderConfig 单个 LLM 供应商配置
type ProviderConfig struct {
	Name          string `yaml:"name"`           // 状态栏左侧：供应商可读名称
	Protocol      string `yaml:"protocol"`       // "anthropic" | "openai"
	BaseURL       string `yaml:"base_url"`       // 自定义端点地址，空则用 SDK 默认
	APIKey        string `yaml:"api_key"`        // 认证密钥
	Model         string `yaml:"model"`          // 模型名，状态栏右侧显示
	Thinking      bool   `yaml:"thinking"`       // 是否启用扩展思考（仅 anthropic 生效）
	ContextWindow int    `yaml:"context_window"` // 上下文窗口 token 数，0 走协议默认
}

// EffectiveContextWindow 返回有效的上下文窗口大小。
// 配置 > 0 返回配置值；否则按 protocol 给默认值。
func (p ProviderConfig) EffectiveContextWindow() int {
	if p.ContextWindow > 0 {
		return p.ContextWindow
	}
	switch p.Protocol {
	case ProtocolAnthropic:
		return DefaultAnthropicContextWindow
	case ProtocolOpenAI:
		return DefaultOpenAIContextWindow
	default:
		return DefaultAnthropicContextWindow
	}
}

// Config 顶层配置
type Config struct {
	Providers []ProviderConfig `yaml:"providers"`
}

// Load 加载并校验配置文件
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("无法读取配置文件 %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("配置文件 YAML 格式错误: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// validate 校验配置合法性
func (c *Config) validate() error {
	if len(c.Providers) == 0 {
		return fmt.Errorf("配置错误: providers 列表不能为空")
	}

	for i, p := range c.Providers {
		if p.Name == "" {
			return fmt.Errorf("providers[%d].name 不能为空", i)
		}
		if p.Protocol == "" {
			return fmt.Errorf("providers[%d].protocol 不能为空（%s）", i, p.Name)
		}
		if p.Protocol != ProtocolAnthropic && p.Protocol != ProtocolOpenAI {
			return fmt.Errorf("providers[%d].protocol 必须为 \"anthropic\" 或 \"openai\"（%s）", i, p.Name)
		}
		if p.APIKey == "" {
			return fmt.Errorf("providers[%d].api_key 不能为空（%s）", i, p.Name)
		}
		if p.Model == "" {
			return fmt.Errorf("providers[%d].model 不能为空（%s）", i, p.Name)
		}
	}

	return nil
}
