package llm

import (
	"context"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"mewcode/internal/config"
	"mewcode/internal/prompt"
)

// anthropicProvider 封装 anthropic SDK
type anthropicProvider struct {
	client anthropic.Client
	cfg    config.ProviderConfig
}

func newAnthropicProvider(cfg config.ProviderConfig) (Provider, error) {
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	client := anthropic.NewClient(opts...)
	return &anthropicProvider{client: client, cfg: cfg}, nil
}

func (p *anthropicProvider) Name() string { return p.cfg.Name }
func (p *anthropicProvider) Model() string { return p.cfg.Model }

func (p *anthropicProvider) Stream(ctx context.Context, msgs []Message) <-chan StreamEvent {
	ch := make(chan StreamEvent)

	go func() {
		defer close(ch)

		// 构造消息参数：内置 system prompt + 历史消息
		var messages []anthropic.MessageParam
		for _, m := range msgs {
			msg := anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content))
			if m.Role == "assistant" {
				msg = anthropic.NewAssistantMessage(anthropic.NewTextBlock(m.Content))
			}
			messages = append(messages, msg)
		}

		params := anthropic.MessageNewParams{
			Model:     anthropic.Model(p.cfg.Model),
			MaxTokens: 4096,
			System: []anthropic.TextBlockParam{
				{Text: prompt.SystemPrompt},
			},
			Messages: messages,
		}

		// 启用扩展思考
		if p.cfg.Thinking {
			params.Thinking = anthropic.ThinkingConfigParamOfEnabled(16000)
		}

		// 发起流式请求
		stream := p.client.Messages.NewStreaming(ctx, params)

		// 迭代流式事件
		for stream.Next() {
			event := stream.Current()

			switch evt := event.AsAny().(type) {
			case *anthropic.ContentBlockDeltaEvent:
				if delta, ok := evt.Delta.AsAny().(anthropic.TextDelta); ok {
					select {
					case ch <- StreamEvent{Text: delta.Text}:
					case <-ctx.Done():
						return
					}
				}
				// ThinkingDelta 丢弃
			case *anthropic.ContentBlockStartEvent:
				// Thinking 块开始 — 不需要处理
			case *anthropic.ContentBlockStopEvent:
				// 块结束 — 不需要处理
			}
		}

		// 流结束或出错
		if err := stream.Err(); err != nil {
			select {
			case ch <- StreamEvent{Err: err}:
			case <-ctx.Done():
			}
			return
		}

		select {
		case ch <- StreamEvent{Done: true}:
		case <-ctx.Done():
		}
	}()

	return ch
}
