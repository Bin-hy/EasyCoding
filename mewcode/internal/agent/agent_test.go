package agent

import (
	"context"
	"encoding/json"
	"testing"

	"mewcode/internal/conversation"
	"mewcode/internal/llm"
	"mewcode/internal/tool"
)

// fakeProvider 测试用 fake LLM 适配器，按脚本返回流式事件。
type fakeProvider struct {
	scripts [][]llm.StreamEvent // 第 N 次请求返回第 N 组事件
	reqNum  int
}

func (f *fakeProvider) Name() string  { return "fake" }
func (f *fakeProvider) Model() string { return "fake-model" }

func (f *fakeProvider) Stream(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition) <-chan llm.StreamEvent {
	ch := make(chan llm.StreamEvent)
	go func() {
		defer close(ch)
		if f.reqNum >= len(f.scripts) {
			ch <- llm.StreamEvent{Done: true}
			return
		}
		events := f.scripts[f.reqNum]
		f.reqNum++
		for _, ev := range events {
			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}

// TestAgent_SingleRound_EndToEnd 模拟"读 X 并总结"的端到端链路 (AC8)
func TestAgent_SingleRound_EndToEnd(t *testing.T) {
	// 注册一个 read_file 工具
	r := tool.NewDefaultRegistry()

	// fake provider 脚本：
	// 请求#1：返回一个 read_file 工具调用
	// 请求#2：返回最终文本总结
	provider := &fakeProvider{
		scripts: [][]llm.StreamEvent{
			{
				{Text: "好的，让我读一下文件。"},
				{ToolCalls: []llm.ToolCall{
					{
						ID:    "call_1",
						Name:  "read_file",
						Input: json.RawMessage(`{"path":"/tmp/test.txt"}`),
					},
				}},
				{Done: true},
			},
			{
				{Text: "文件内容是 hello world。"},
				{Done: true},
			},
		},
	}

	conv := &conversation.Conversation{}
	conv.AddUser("读 /tmp/test.txt 并总结")

	agent := New(provider, r)
	events := agent.Run(context.Background(), conv)

	var gotToolStart, gotToolEnd bool
	var finalText string

	for ev := range events {
		switch {
		case ev.Tool != nil && ev.Tool.Phase == PhaseStart:
			gotToolStart = true
			if ev.Tool.Name != "read_file" {
				t.Errorf("期望工具名 read_file，实际 %s", ev.Tool.Name)
			}
		case ev.Tool != nil && ev.Tool.Phase == PhaseEnd:
			gotToolEnd = true
		case ev.Text != "" && ev.Done == false:
			finalText += ev.Text
		case ev.Err != nil:
			t.Fatalf("不应出错: %v", ev.Err)
		}
	}

	if !gotToolStart {
		t.Error("应有 PhaseStart 事件")
	}
	if !gotToolEnd {
		t.Error("应有 PhaseEnd 事件")
	}
	if finalText == "" {
		t.Error("应有最终文本答复")
	}

	// 验证 conversation 末尾为 assistant 文本
	msgs := conv.Messages()
	if len(msgs) < 3 {
		t.Fatalf("conversation 至少应有 3 条消息（user + assistant + assistant），实际 %d", len(msgs))
	}
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleAssistant {
		t.Errorf("最后一条应为 assistant，实际 %s", last.Role)
	}
}

// TestAgent_SingleRoundLimit 验证单轮上限——第二轮工具调用被忽略 (AC9)
func TestAgent_SingleRoundLimit(t *testing.T) {
	r := tool.NewDefaultRegistry()

	provider := &fakeProvider{
		scripts: [][]llm.StreamEvent{
			{
				{ToolCalls: []llm.ToolCall{
					{ID: "call_1", Name: "read_file", Input: json.RawMessage(`{"path":"/a"}`)},
				}},
				{Done: true},
			},
			// 请求#2 仍返回工具调用——agent 应忽略
			{
				{Text: "还需要更多工具。"},
				{ToolCalls: []llm.ToolCall{
					{ID: "call_2", Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)},
				}},
				{Done: true},
			},
		},
	}

	conv := &conversation.Conversation{}
	conv.AddUser("需要两步工具的任务")

	agent := New(provider, r)
	events := agent.Run(context.Background(), conv)

	toolCount := 0
	for ev := range events {
		if ev.Tool != nil && ev.Tool.Phase == PhaseStart {
			toolCount++
		}
	}

	if toolCount != 1 {
		t.Errorf("应只执行 1 轮工具调用，实际 %d 轮", toolCount)
	}

	// 最终 conversation 末尾应是 assistant（来自请求#2）
	msgs := conv.Messages()
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleAssistant {
		t.Errorf("末尾消息应为 assistant，实际 %s", last.Role)
	}
}

// TestAgent_NoToolCall_PureText 纯文本回复（无工具调用）场景
func TestAgent_NoToolCall_PureText(t *testing.T) {
	r := tool.NewDefaultRegistry()

	provider := &fakeProvider{
		scripts: [][]llm.StreamEvent{
			{
				{Text: "你好！有什么可以帮你的？"},
				{Done: true},
			},
		},
	}

	conv := &conversation.Conversation{}
	conv.AddUser("你好")

	agent := New(provider, r)
	events := agent.Run(context.Background(), conv)

	var reply string
	for ev := range events {
		switch {
		case ev.Text != "":
			reply += ev.Text
		case ev.Err != nil:
			t.Fatalf("不应出错: %v", ev.Err)
		}
	}

	if reply == "" {
		t.Error("应有文本回复")
	}

	msgs := conv.Messages()
	if len(msgs) != 2 { // user + assistant
		t.Errorf("应有 2 条消息，实际 %d", len(msgs))
	}
}

// TestAgent_EmptyPreamble_Continuation 验证无 preamble（纯工具调用首轮）时续答正常 (AC8)
func TestAgent_EmptyPreamble_Continuation(t *testing.T) {
	r := tool.NewDefaultRegistry()

	provider := &fakeProvider{
		scripts: [][]llm.StreamEvent{
			// 请求#1：无文本 preamble，直接返回工具调用
			{
				{ToolCalls: []llm.ToolCall{
					{ID: "call_1", Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)},
				}},
				{Done: true},
			},
			// 请求#2：续答返回文本
			{
				{Text: "当前目录下有这些文件。"},
				{Done: true},
			},
		},
	}

	conv := &conversation.Conversation{}
	conv.AddUser("列出文件")

	agent := New(provider, r)
	events := agent.Run(context.Background(), conv)

	var toolCount int
	var finalText string
	var gotDone bool

	for ev := range events {
		switch {
		case ev.Tool != nil && ev.Tool.Phase == PhaseStart:
			toolCount++
		case ev.Text != "":
			finalText += ev.Text
		case ev.Done:
			gotDone = true
		case ev.Err != nil:
			t.Fatalf("不应出错: %v", ev.Err)
		}
	}

	if toolCount != 1 {
		t.Errorf("应执行 1 个工具，实际 %d", toolCount)
	}
	if !gotDone {
		t.Error("应收到 Done 事件")
	}
	if finalText == "" {
		t.Error("续答应有文本答复")
	}

	msgs := conv.Messages()
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleAssistant {
		t.Errorf("末尾应为 assistant，实际 %s", last.Role)
	}
	if last.Content == "" || last.Content == "（单轮工具调用已完成）" {
		t.Errorf("续答不应为空或占位文本，实际: %q", last.Content)
	}
}
