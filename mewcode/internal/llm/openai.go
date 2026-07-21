package llm

import (
	"context"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"mewcode/internal/config"
	"mewcode/internal/prompt"
)

// openaiProvider 封装 openai SDK
type openaiProvider struct {
	client openai.Client
	cfg    config.ProviderConfig
}

func newOpenAIProvider(cfg config.ProviderConfig) (Provider, error) {
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	client := openai.NewClient(opts...)
	return &openaiProvider{client: client, cfg: cfg}, nil
}

func (p *openaiProvider) Name() string { return p.cfg.Name }
func (p *openaiProvider) Model() string { return p.cfg.Model }

func (p *openaiProvider) Stream(ctx context.Context, msgs []Message) <-chan StreamEvent {
	ch := make(chan StreamEvent)

	go func() {
		defer close(ch)

		// 构造消息：首条为 system prompt，后续为对话历史
		messages := []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(prompt.SystemPrompt),
		}
		for _, m := range msgs {
			if m.Role == "user" {
				messages = append(messages, openai.UserMessage(m.Content))
			} else if m.Role == "assistant" {
				messages = append(messages, openai.AssistantMessage(m.Content))
			}
		}

		params := openai.ChatCompletionNewParams{
			Model:    openai.ChatModel(p.cfg.Model),
			Messages: messages,
		}
		// thinking 字段 OpenAI 忽略，不设置 reasoning effort

		// 发起流式请求
		stream := p.client.Chat.Completions.NewStreaming(ctx, params)

		// 迭代流式事件
		for stream.Next() {
			evt := stream.Current()
			if len(evt.Choices) > 0 {
				delta := evt.Choices[0].Delta
				if delta.Content != "" {
					select {
					case ch <- StreamEvent{Text: delta.Content}:
					case <-ctx.Done():
						return
					}
				}
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
