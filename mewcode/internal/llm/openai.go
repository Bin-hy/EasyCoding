package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
	"mewcode/internal/config"

	"github.com/openai/openai-go/v3/packages/param"
)

// toOpenAITools 将协议无关工具定义转为 OpenAI SDK 的工具参数格式。
func toOpenAITools(tools []ToolDefinition) []openai.ChatCompletionToolUnionParam {
	result := make([]openai.ChatCompletionToolUnionParam, 0, len(tools))
	for _, t := range tools {
		result = append(result, openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        t.Name,
			Description: openai.String(t.Description),
			Parameters:  shared.FunctionParameters(t.InputSchema),
		}))
	}
	return result
}

// toOpenAIMessages 将 Request 转为 OpenAI SDK 的消息参数格式。
// 首条 system 消息 = Stable + "\n\n" + Environment（单条拼接兼容端点对多条 system 支持不一）；
// Stable 居前缀使端点前缀缓存自动命中稳定部分。
// reminder 非空时追加一条尾部 user 消息（OpenAI 容忍连续 user）。
func toOpenAIMessages(req Request) []openai.ChatCompletionMessageParamUnion {
	// 构造首条 system 消息
	var systemText string
	if req.System.Stable != "" {
		systemText = req.System.Stable
	}
	if req.System.Environment != "" {
		if systemText != "" {
			systemText += "\n\n"
		}
		systemText += req.System.Environment
	}

	result := make([]openai.ChatCompletionMessageParamUnion, 0, len(req.Messages)+2)
	if systemText != "" {
		result = append(result, openai.SystemMessage(systemText))
	}

	for _, m := range req.Messages {
		switch m.Role {
		case RoleUser:
			result = append(result, openai.UserMessage(m.Content))
		case RoleAssistant:
			if len(m.ToolCalls) > 0 {
				var toolCalls []openai.ChatCompletionMessageToolCallUnionParam
				for _, tc := range m.ToolCalls {
					toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallUnionParam{
						OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
							ID: tc.ID,
							Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
								Name:      tc.Name,
								Arguments: string(tc.Input),
							},
						},
					})
				}
				assistantMsg := openai.ChatCompletionAssistantMessageParam{
					ToolCalls: toolCalls,
				}
				if m.Content != "" {
					assistantMsg.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
						OfString: param.Opt[string]{Value: m.Content},
					}
				}
				result = append(result, openai.ChatCompletionMessageParamUnion{
					OfAssistant: &assistantMsg,
				})
			} else {
				result = append(result, openai.AssistantMessage(m.Content))
			}
		case RoleTool:
			for _, tr := range m.ToolResults {
				result = append(result, openai.ToolMessage(tr.Content, tr.ToolCallID))
			}
		}
	}

	// reminder 注入：追加尾部 user 消息（OpenAI 容忍连续 user）
	if req.Reminder != "" {
		result = append(result, openai.UserMessage(req.Reminder))
	}

	return result
}

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

func (p *openaiProvider) Name() string  { return p.cfg.Name }
func (p *openaiProvider) Model() string { return p.cfg.Model }

func (p *openaiProvider) Stream(ctx context.Context, req Request) <-chan StreamEvent {
	ch := make(chan StreamEvent)

	go func() {
		defer close(ch)

		// 构造消息参数
		messages := toOpenAIMessages(req)

		params := openai.ChatCompletionNewParams{
			Model:    openai.ChatModel(p.cfg.Model),
			Messages: messages,
			StreamOptions: openai.ChatCompletionStreamOptionsParam{
				IncludeUsage: openai.Bool(true),
			},
		}

		// 注入工具定义
		if len(req.Tools) > 0 {
			params.Tools = toOpenAITools(req.Tools)
		}

		// 发起流式请求
		stream := p.client.Chat.Completions.NewStreaming(ctx, params)

		// 用 Accumulator 拼接工具调用
		acc := openai.ChatCompletionAccumulator{}
		for stream.Next() {
			evt := stream.Current()
			acc.AddChunk(evt)

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
			case ch <- StreamEvent{Err: wrapOpenAIPTL(err)}:
			case <-ctx.Done():
			}
			return
		}

		// 上抛本轮 token 用量（流结束后 acc.Usage 完整；含缓存字段 F4/N6）
		if acc.Usage.PromptTokens > 0 || acc.Usage.CompletionTokens > 0 {
			select {
			case ch <- StreamEvent{Usage: &Usage{
				InputTokens:  int64(acc.Usage.PromptTokens),
				OutputTokens: int64(acc.Usage.CompletionTokens),
				CacheWrite:   0,
				CacheRead:    int64(acc.Usage.PromptTokensDetails.CachedTokens),
			}}:
			case <-ctx.Done():
				return
			}
		}

		// 检查是否有工具调用
		if len(acc.Choices) > 0 && len(acc.Choices[0].Message.ToolCalls) > 0 {
			var calls []ToolCall
			for _, tc := range acc.Choices[0].Message.ToolCalls {
				args := tc.Function.Arguments
				if args == "" {
					args = "{}"
				}
				calls = append(calls, ToolCall{
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: json.RawMessage(args),
				})
			}
			if len(calls) > 0 {
				select {
				case ch <- StreamEvent{ToolCalls: calls}:
				case <-ctx.Done():
					return
				}
			}
		}

		select {
		case ch <- StreamEvent{Done: true}:
		case <-ctx.Done():
		}
	}()

	return ch
}

// wrapOpenAIPTL 检测 OpenAI 上下文过长错误并包装为 ErrPromptTooLong。
func wrapOpenAIPTL(err error) error {
	if err == nil {
		return nil
	}
	errStr := err.Error()
	if strings.Contains(errStr, "context_length_exceeded") ||
		strings.Contains(errStr, "maximum context length") ||
		strings.Contains(errStr, "too long") ||
		strings.Contains(errStr, "token") && strings.Contains(errStr, "exceed") {
		return fmt.Errorf("%w: %v", ErrPromptTooLong, err)
	}
	return err
}
