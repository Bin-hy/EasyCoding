package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"mewcode/internal/config"
)

// toAnthropicTools 将协议无关工具定义转为 Anthropic SDK 的工具参数格式。
// Anthropic 的 InputSchema 需要取 properties 和 required 字段。
func toAnthropicTools(tools []ToolDefinition) []anthropic.ToolUnionParam {
	result := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		schema := anthropic.ToolInputSchemaParam{
			Properties: t.InputSchema["properties"],
			Required:   toStrings(t.InputSchema["required"]),
		}
		tool := anthropic.ToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: schema,
		}
		result = append(result, anthropic.ToolUnionParam{OfTool: &tool})
	}
	return result
}

// toStrings 将 interface{} 转换为 []string（用于 required 字段）。
func toStrings(v interface{}) []string {
	if v == nil {
		return nil
	}
	arr, ok := v.([]string)
	if ok {
		return arr
	}
	ifaceArr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(ifaceArr))
	for _, item := range ifaceArr {
		s, ok := item.(string)
		if ok {
			result = append(result, s)
		}
	}
	return result
}

// toAnthropicMessages 将协议无关消息列表转为 Anthropic SDK 的消息参数格式。
// 支持 user/assistant/text、assistant+tool_use、user+tool_result 组合。
func toAnthropicMessages(msgs []Message) []anthropic.MessageParam {
	result := make([]anthropic.MessageParam, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case RoleUser:
			result = append(result, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		case RoleAssistant:
			// assistant 可带文本块和工具调用块
			if len(m.ToolCalls) > 0 {
				var blocks []anthropic.ContentBlockParamUnion
				if m.Content != "" {
					blocks = append(blocks, anthropic.NewTextBlock(m.Content))
				}
				for _, tc := range m.ToolCalls {
					blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, json.RawMessage(tc.Input), tc.Name))
				}
				result = append(result, anthropic.NewAssistantMessage(blocks...))
			} else {
				result = append(result, anthropic.NewAssistantMessage(anthropic.NewTextBlock(m.Content)))
			}
		case RoleTool:
			// Anthropic 中 tool_result 块必须放在一条 user 消息里
			var blocks []anthropic.ContentBlockParamUnion
			for _, tr := range m.ToolResults {
				blocks = append(blocks, anthropic.NewToolResultBlock(tr.ToolCallID, tr.Content, tr.IsError))
			}
			result = append(result, anthropic.NewUserMessage(blocks...))
		}
	}
	return result
}

// hasToolHistory 检查消息历史中是否包含工具交互。
func hasToolHistory(msgs []Message) bool {
	for _, m := range msgs {
		if m.Role == RoleTool || len(m.ToolCalls) > 0 {
			return true
		}
	}
	return false
}

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

func (p *anthropicProvider) Name() string  { return p.cfg.Name }
func (p *anthropicProvider) Model() string { return p.cfg.Model }

// toAnthropicSystem 将 System 转为 Anthropic TextBlockParam 列表。
// Stable 块打缓存断点（必须用 NewCacheControlEphemeralParam 构造器，空字面量会被 omitzero 丢弃）；
// Environment 块不打缓存断点。
func toAnthropicSystem(sys System) []anthropic.TextBlockParam {
	var blocks []anthropic.TextBlockParam
	if sys.Stable != "" {
		blocks = append(blocks, anthropic.TextBlockParam{
			Text:         sys.Stable,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		})
	}
	if sys.Environment != "" {
		blocks = append(blocks, anthropic.TextBlockParam{
			Text: sys.Environment,
		})
	}
	return blocks
}

// appendReminderAnthropic 把 reminder 并入最后一条 user 消息的 content 块。
// 末条非 user 时新起一条 user 消息。确保角色交替合法（N3）。
func appendReminderAnthropic(msgs []anthropic.MessageParam, reminder string) []anthropic.MessageParam {
	if len(msgs) == 0 {
		return append(msgs, anthropic.NewUserMessage(anthropic.NewTextBlock(reminder)))
	}
	last := &msgs[len(msgs)-1]
	if last.Role == anthropic.MessageParamRoleUser {
		// 把 reminder 作为新的文本块追加到已有 user 消息
		last.Content = append(last.Content, anthropic.NewTextBlock(reminder))
	} else {
		// 末条不是 user，新起一条 user 消息
		msgs = append(msgs, anthropic.NewUserMessage(anthropic.NewTextBlock(reminder)))
	}
	return msgs
}

func (p *anthropicProvider) Stream(ctx context.Context, req Request) <-chan StreamEvent {
	ch := make(chan StreamEvent)

	go func() {
		defer close(ch)

		// 构造消息参数
		messages := toAnthropicMessages(req.Messages)

		// reminder 织入：并入最后一条 user 消息（确保角色交替合法 N3）
		if req.Reminder != "" {
			messages = appendReminderAnthropic(messages, req.Reminder)
		}

		params := anthropic.MessageNewParams{
			Model:     anthropic.Model(p.cfg.Model),
			MaxTokens: 4096,
			System:    toAnthropicSystem(req.System),
			Messages:  messages,
		}

		// 注入工具定义
		if len(req.Tools) > 0 {
			params.Tools = toAnthropicTools(req.Tools)
		}

		// 启用扩展思考（历史含工具交互时关闭，避免 400）
		if p.cfg.Thinking && !hasToolHistory(req.Messages) {
			params.Thinking = anthropic.ThinkingConfigParamOfEnabled(16000)
		}

		// 发起流式请求
		stream := p.client.Messages.NewStreaming(ctx, params)

		// 用 Accumulator 解析工具调用
		acc := anthropic.Message{}
		for stream.Next() {
			event := stream.Current()

			// Accumulate 处理所有事件（文本、thinking、tool_use 等）
			if err := acc.Accumulate(event); err != nil {
				select {
				case ch <- StreamEvent{Err: wrapAnthropicPTL(err)}:
				case <-ctx.Done():
					return
				}
				return
			}

			// 只将文本增量上抛给上层
			switch evt := event.AsAny().(type) {
			case *anthropic.ContentBlockDeltaEvent:
				if delta, ok := evt.Delta.AsAny().(anthropic.TextDelta); ok {
					select {
					case ch <- StreamEvent{Text: delta.Text}:
					case <-ctx.Done():
						return
					}
				}
				// ThinkingDelta / InputJSONDelta 由 Accumulate 缓冲，不上抛
			}
		}

		// 流结束或出错
		if err := stream.Err(); err != nil {
			select {
			case ch <- StreamEvent{Err: wrapAnthropicPTL(err)}:
			case <-ctx.Done():
			}
			return
		}

		// 上抛本轮 token 用量（流结束后 acc.Usage 完整；含缓存字段 F4/N6）
		if acc.Usage.InputTokens > 0 || acc.Usage.OutputTokens > 0 {
			select {
			case ch <- StreamEvent{Usage: &Usage{
				InputTokens:  int64(acc.Usage.InputTokens),
				OutputTokens: int64(acc.Usage.OutputTokens),
				CacheWrite:   int64(acc.Usage.CacheCreationInputTokens),
				CacheRead:    int64(acc.Usage.CacheReadInputTokens),
			}}:
			case <-ctx.Done():
				return
			}
		}

		// 检查是否为工具调用结束
		if acc.StopReason == anthropic.StopReasonToolUse {
			var calls []ToolCall
			for _, block := range acc.Content {
				toolBlock := block.AsToolUse()
				if toolBlock.ID != "" {
					calls = append(calls, ToolCall{
						ID:    toolBlock.ID,
						Name:  toolBlock.Name,
						Input: json.RawMessage(toolBlock.Input),
					})
				}
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

// wrapAnthropicPTL 检测 Anthropic 上下文过长错误并包装为 ErrPromptTooLong。
func wrapAnthropicPTL(err error) error {
	if err == nil {
		return nil
	}
	errStr := err.Error()
	if strings.Contains(errStr, "prompt is too long") ||
		strings.Contains(errStr, "context_length") ||
		strings.Contains(errStr, "too many tokens") {
		return fmt.Errorf("%w: %v", ErrPromptTooLong, err)
	}
	return err
}
