package llm

import (
	"context"
	"encoding/json"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
	"mewcode/internal/config"
	"mewcode/internal/prompt"

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

// toOpenAIMessages 将协议无关消息列表转为 OpenAI SDK 的消息参数格式。
// 支持 system/user/assistant/assistant+tool_calls/tool 角色。
func toOpenAIMessages(msgs []Message) []openai.ChatCompletionMessageParamUnion {
	result := make([]openai.ChatCompletionMessageParamUnion, 1, len(msgs)+1)
	result[0] = openai.SystemMessage(prompt.SystemPrompt)

	for _, m := range msgs {
		switch m.Role {
		case RoleUser:
			result = append(result, openai.UserMessage(m.Content))
		case RoleAssistant:
			if len(m.ToolCalls) > 0 {
				// 手工构造 assistant 消息带 tool_calls
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
				// 仅当有文本时才设置 Content；空内容省略避免 "content":"" 被 API 拒绝
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
			// 每个 tool_result 发一条 tool 消息
			for _, tr := range m.ToolResults {
				result = append(result, openai.ToolMessage(tr.Content, tr.ToolCallID))
			}
		}
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

func (p *openaiProvider) Stream(ctx context.Context, msgs []Message, tools []ToolDefinition) <-chan StreamEvent {
	ch := make(chan StreamEvent)

	go func() {
		defer close(ch)

		// 构造消息参数
		messages := toOpenAIMessages(msgs)

		params := openai.ChatCompletionNewParams{
			Model:    openai.ChatModel(p.cfg.Model),
			Messages: messages,
		}

		// 注入工具定义
		if len(tools) > 0 {
			params.Tools = toOpenAITools(tools)
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
			case ch <- StreamEvent{Err: err}:
			case <-ctx.Done():
			}
			return
		}

		// 检查是否有工具调用
		if len(acc.Choices) > 0 && len(acc.Choices[0].Message.ToolCalls) > 0 {
			var calls []ToolCall
			for _, tc := range acc.Choices[0].Message.ToolCalls {
				// 空 args 归一为 {}
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
