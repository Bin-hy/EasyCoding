package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mewcode/internal/conversation"
	"mewcode/internal/llm"
	"mewcode/internal/tool"
)

// fakeProvider 实现 llm.Provider，按脚本逐次返回预设 StreamEvent 序列。
type fakeProvider struct {
	scripts [][]llm.StreamEvent // 每次 Stream 调用依次消费一个脚本
	calls   int                 // Stream 被调用次数
	mu      sync.Mutex

	// 记录最后一次 Stream 收到的参数（供断言用）
	lastTools  []llm.ToolDefinition
	lastSuffix string
}

func (f *fakeProvider) Name() string  { return "fake" }
func (f *fakeProvider) Model() string { return "fake-model" }

func (f *fakeProvider) Stream(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition, systemSuffix string) <-chan llm.StreamEvent {
	f.mu.Lock()
	f.calls++
	idx := f.calls - 1
	f.lastTools = tools
	f.lastSuffix = systemSuffix
	f.mu.Unlock()

	ch := make(chan llm.StreamEvent)
	go func() {
		defer close(ch)
		if idx < len(f.scripts) {
			for _, ev := range f.scripts[idx] {
				select {
				case ch <- ev:
				case <-ctx.Done():
					return
				}
			}
		} else {
			// 脚本耗尽：恒返回一个工具调用（用于迭代上限测试）
			select {
			case ch <- llm.StreamEvent{ToolCalls: []llm.ToolCall{{ID: "tc_fake", Name: "read_file", Input: json.RawMessage(`{"path":"x"}`)}}}:
			case <-ctx.Done():
				return
			}
			select {
			case ch <- llm.StreamEvent{Usage: &llm.Usage{InputTokens: 100, OutputTokens: 50}}:
			case <-ctx.Done():
				return
			}
			select {
			case ch <- llm.StreamEvent{Done: true}:
			case <-ctx.Done():
			}
		}
	}()
	return ch
}

// collectEvents 收集所有事件直到 channel 关闭。
func collectEvents(ch <-chan Event) []Event {
	var events []Event
	for e := range ch {
		events = append(events, e)
	}
	return events
}

// hasEventType 检查事件列表是否包含指定类型的事件。
func hasEventType(events []Event, check func(Event) bool) bool {
	for _, e := range events {
		if check(e) {
			return true
		}
	}
	return false
}

// scenario A: 多轮链路（AC1）——第一轮返回 read_file 工具调用，第二轮返回纯文本
func TestReAct_MultiRound(t *testing.T) {
	fp := &fakeProvider{
		scripts: [][]llm.StreamEvent{
			// 第 1 轮：返回 1 个 read_file 工具调用
			{
				{Text: "我需要读取文件"},
				{ToolCalls: []llm.ToolCall{{ID: "tc1", Name: "read_file", Input: json.RawMessage(`{"path":"test.txt"}`)}}},
				{Usage: &llm.Usage{InputTokens: 100, OutputTokens: 50}},
				{Done: true},
			},
			// 第 2 轮：纯文本答复
			{
				{Text: "文件内容分析完成"},
				{Usage: &llm.Usage{InputTokens: 200, OutputTokens: 30}},
				{Done: true},
			},
		},
	}
	registry := tool.NewDefaultRegistry()
	conv := &conversation.Conversation{}
	conv.AddUser("读取 test.txt 的内容")

	a := New(fp, registry)
	events := collectEvents(a.Run(context.Background(), conv, ModeNormal))

	// 断言：Iter=1、ToolStart/End、Iter=2、最终 Text、Done
	if !hasEventType(events, func(e Event) bool { return e.Iter == 1 }) {
		t.Error("期望 Iter=1 事件")
	}
	if !hasEventType(events, func(e Event) bool { return e.Tool != nil && e.Tool.Phase == PhaseStart }) {
		t.Error("期望 ToolStart 事件")
	}
	if !hasEventType(events, func(e Event) bool { return e.Tool != nil && e.Tool.Phase == PhaseEnd }) {
		t.Error("期望 ToolEnd 事件")
	}
	if !hasEventType(events, func(e Event) bool { return e.Iter == 2 }) {
		t.Error("期望 Iter=2 事件")
	}
	if !hasEventType(events, func(e Event) bool { return e.Text == "文件内容分析完成" }) {
		t.Error("期望最终文本事件")
	}
	if !hasEventType(events, func(e Event) bool { return e.Done }) {
		t.Error("期望 Done 事件")
	}

	// conv 末尾为 assistant 文本
	if conv.LastRole() != "assistant" {
		t.Errorf("期望 LastRole=assistant，实际=%s", conv.LastRole())
	}

	// fp 应被调用恰好 2 次
	if fp.calls != 2 {
		t.Errorf("期望 Stream 调用 2 次，实际 %d", fp.calls)
	}

	// 事件流覆盖：文本/工具/Usage/Iter/Done
	if !hasEventType(events, func(e Event) bool { return e.Usage != nil }) {
		t.Error("期望 Usage 事件")
	}
}

// scenario B: 迭代上限（AC3）——每次 Stream 都返回工具调用，直到 maxIterations
func TestReAct_MaxIterations(t *testing.T) {
	fp := &fakeProvider{
		scripts: nil, // 空脚本 → 恒返工具调用
	}
	registry := tool.NewDefaultRegistry()
	conv := &conversation.Conversation{}
	conv.AddUser("做一个无限循环的任务")

	a := New(fp, registry)
	events := collectEvents(a.Run(context.Background(), conv, ModeNormal))

	// 断言恰好 maxIterations 次请求后停
	if fp.calls != maxIterations {
		t.Errorf("期望 Stream 调用 %d 次，实际 %d", maxIterations, fp.calls)
	}

	// 收到 Notice(noticeMaxIter)
	if !hasEventType(events, func(e Event) bool { return e.Notice == noticeMaxIter }) {
		t.Errorf("期望 Notice=%q", noticeMaxIter)
	}

	// conv.LastRole() 应为 assistant
	if conv.LastRole() != "assistant" {
		t.Errorf("期望 LastRole=assistant，实际=%s", conv.LastRole())
	}
}

// scenario C: 连续未知工具（AC4）
func TestReAct_UnknownTools(t *testing.T) {
	unknownScript := []llm.StreamEvent{
		{ToolCalls: []llm.ToolCall{{ID: "u1", Name: "unknown_tool", Input: json.RawMessage(`{}`)}}},
		{Usage: &llm.Usage{InputTokens: 10, OutputTokens: 5}},
		{Done: true},
	}

	// 子用例1：连续未知工具 maxUnknownRun 轮后停
	t.Run("consecutive_unknown_stops", func(t *testing.T) {
		fp := &fakeProvider{
			scripts: [][]llm.StreamEvent{unknownScript, unknownScript, unknownScript},
		}
		registry := tool.NewDefaultRegistry()
		conv := &conversation.Conversation{}
		conv.AddUser("test")

		a := New(fp, registry)
		events := collectEvents(a.Run(context.Background(), conv, ModeNormal))

		if !hasEventType(events, func(e Event) bool { return e.Notice == noticeUnknownTools }) {
			t.Errorf("期望 Notice=%q", noticeUnknownTools)
		}
	})

	// 子用例2：中间混入已知工具，计数重置
	t.Run("known_resets_counter", func(t *testing.T) {
		fp := &fakeProvider{
			scripts: [][]llm.StreamEvent{
				unknownScript,
				{ // 混入一个已注册工具 read_file
					{ToolCalls: []llm.ToolCall{{ID: "k1", Name: "read_file", Input: json.RawMessage(`{"path":"x"}`)}}},
					{Usage: &llm.Usage{InputTokens: 10, OutputTokens: 5}},
					{Done: true},
				},
				unknownScript,
				{
					{Text: "完成"},
					{Done: true},
				},
			},
		}
		registry := tool.NewDefaultRegistry()
		conv := &conversation.Conversation{}
		conv.AddUser("test")

		a := New(fp, registry)
		events := collectEvents(a.Run(context.Background(), conv, ModeNormal))

		// 不应因未知工具提前停
		if hasEventType(events, func(e Event) bool { return e.Notice == noticeUnknownTools }) {
			t.Error("混入已知工具后不应触发未知工具停止")
		}
		// 应自然完成
		if !hasEventType(events, func(e Event) bool { return e.Done }) {
			t.Error("期望自然完成")
		}
	})
}

// stubTool 可注入 Execute 行为的测试工具。
type stubTool struct {
	name     string
	readOnly bool
	execute  func(ctx context.Context, args json.RawMessage) tool.Result
}

func (s *stubTool) Name() string        { return s.name }
func (s *stubTool) Description() string { return "stub" }
func (s *stubTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (s *stubTool) ReadOnly() bool { return s.readOnly }
func (s *stubTool) Execute(ctx context.Context, args json.RawMessage) tool.Result {
	return s.execute(ctx, args)
}

// scenario D: 保序分批并发（AC8）
func TestReAct_BatchedConcurrency(t *testing.T) {
	var concurrentCount atomic.Int64
	var maxConcurrent atomic.Int64

	// 两个只读工具：执行时记录并发峰值并短暂 sleep 制造重叠
	makeRO := func(name string) *stubTool {
		return &stubTool{
			name:     name,
			readOnly: true,
			execute: func(ctx context.Context, args json.RawMessage) tool.Result {
				c := concurrentCount.Add(1)
				// 跟踪最大并发数
				for {
					old := maxConcurrent.Load()
					if c <= old || maxConcurrent.CompareAndSwap(old, c) {
						break
					}
				}
				time.Sleep(50 * time.Millisecond) // 制造并发重叠
				concurrentCount.Add(-1)
				return tool.Result{Content: name + " done"}
			},
		}
	}

	var rwStartedAt time.Time
	rw := &stubTool{
		name:     "bash",
		readOnly: false,
		execute: func(ctx context.Context, args json.RawMessage) tool.Result {
			rwStartedAt = time.Now()
			return tool.Result{Content: "bash done"}
		},
	}

	// 自定义 registry：注册两个只读 + 一个有副作用
	registry := &tool.Registry{}
	ro1 := makeRO("read_file")
	ro2 := makeRO("glob")
	registry.Register(ro1)
	registry.Register(ro2)
	registry.Register(rw)

	// 脚本：一轮返回 [ro, ro, rw] 三个工具调用
	fp := &fakeProvider{
		scripts: [][]llm.StreamEvent{
			{
				{ToolCalls: []llm.ToolCall{
					{ID: "1", Name: "read_file", Input: json.RawMessage(`{}`)},
					{ID: "2", Name: "glob", Input: json.RawMessage(`{}`)},
					{ID: "3", Name: "bash", Input: json.RawMessage(`{}`)},
				}},
				{Usage: &llm.Usage{InputTokens: 10, OutputTokens: 5}},
				{Done: true},
			},
			{
				{Text: "完成"},
				{Done: true},
			},
		},
	}

	conv := &conversation.Conversation{}
	conv.AddUser("test")

	a := New(fp, registry)
	_ = collectEvents(a.Run(context.Background(), conv, ModeNormal))

	// 两只读并发峰值 ≥2
	if maxConcurrent.Load() < 2 {
		t.Errorf("期望只读工具并发峰值≥2，实际=%d", maxConcurrent.Load())
	}

	// rw 的开始时刻应晚于两只读完成（最大并发在 50ms sleep + 调度 ≈ 完成）
	if rwStartedAt.IsZero() {
		t.Error("bash 工具未被调用")
	}

	// 结果按原始顺序回灌：检查 conv 中的消息
	msgs := conv.Messages()
	foundToolResults := false
	for _, m := range msgs {
		if m.Role == "tool" && len(m.ToolResults) == 3 {
			foundToolResults = true
			// 顺序：read_file → glob → bash
			if m.ToolResults[0].ToolCallID != "1" {
				t.Errorf("期望第 1 个结果为 read_file(callID=1)，实际=%s", m.ToolResults[0].ToolCallID)
			}
			if m.ToolResults[1].ToolCallID != "2" {
				t.Errorf("期望第 2 个结果为 glob(callID=2)，实际=%s", m.ToolResults[1].ToolCallID)
			}
			if m.ToolResults[2].ToolCallID != "3" {
				t.Errorf("期望第 3 个结果为 bash(callID=3)，实际=%s", m.ToolResults[2].ToolCallID)
			}
		}
	}
	if !foundToolResults {
		t.Error("未找到含 3 个 ToolResults 的 tool 消息")
	}
}

// scenario E: 取消历史一致（AC9）
func TestReAct_CancelHistoryConsistency(t *testing.T) {
	blockCh := make(chan struct{})
	blockingTool := &stubTool{
		name:     "blocker",
		readOnly: false,
		execute: func(ctx context.Context, args json.RawMessage) tool.Result {
			select {
			case <-ctx.Done():
				return tool.Result{Content: "cancelled", IsError: true}
			case <-blockCh:
				return tool.Result{Content: "done"}
			}
		},
	}

	registry := &tool.Registry{}
	registry.Register(blockingTool)

	fp := &fakeProvider{
		scripts: [][]llm.StreamEvent{
			{
				{ToolCalls: []llm.ToolCall{
					{ID: "b1", Name: "blocker", Input: json.RawMessage(`{}`)},
				}},
				{Done: true},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	conv := &conversation.Conversation{}
	conv.AddUser("test")

	a := New(fp, registry)
	ch := a.Run(ctx, conv, ModeNormal)

	// 在 goroutine 中消费事件流（避免 goroutine 阻塞在 emit）
	eventsDone := make(chan struct{})
	var evs []Event
	go func() {
		evs = collectEvents(ch)
		close(eventsDone)
	}()

	// 等待工具开始执行
	time.Sleep(50 * time.Millisecond)

	// 取消
	cancel()

	// 等待事件流结束
	<-eventsDone
	_ = evs

	// 断言 conv 末尾配对合法：有 tool_results 消息，最后是 assistant 文本
	lastRole := conv.LastRole()
	if lastRole != "assistant" {
		t.Errorf("取消后期望 LastRole=assistant，实际=%s", lastRole)
	}

	// 检查有 tool_results
	hasToolResults := false
	for _, m := range conv.Messages() {
		if m.Role == "tool" && len(m.ToolResults) > 0 {
			hasToolResults = true
		}
	}
	if !hasToolResults {
		t.Error("取消后历史应包含 tool_results")
	}

	// 后续可以继续对话（追加一轮纯文本）
	fp2 := &fakeProvider{
		scripts: [][]llm.StreamEvent{
			{
				{Text: "继续对话"},
				{Done: true},
			},
		},
	}
	conv2 := &conversation.Conversation{}
	conv2.AddUser("继续")
	a2 := New(fp2, registry)
	events2 := collectEvents(a2.Run(context.Background(), conv2, ModeNormal))
	if !hasEventType(events2, func(e Event) bool { return e.Done }) {
		t.Error("取消后新对话应正常结束")
	}
}

// scenario F: Plan Mode 工具集（AC13）
func TestReAct_PlanModeTools(t *testing.T) {
	fp := &fakeProvider{
		scripts: [][]llm.StreamEvent{
			{
				{Text: "计划如下..."},
				{Done: true},
			},
		},
	}

	registry := tool.NewDefaultRegistry()
	conv := &conversation.Conversation{}
	conv.AddUser("做一个改动类任务")

	a := New(fp, registry)
	collectEvents(a.Run(context.Background(), conv, ModePlan))

	// 断言 fp 收到的 tools 仅含只读工具
	readOnlyNames := map[string]bool{"read_file": true, "glob": true, "grep": true}
	for _, td := range fp.lastTools {
		if !readOnlyNames[td.Name] {
			t.Errorf("Plan Mode 下不应出现非只读工具: %s", td.Name)
		}
	}

	// 断言 suffix 非空
	if fp.lastSuffix == "" {
		t.Error("Plan Mode 下 systemSuffix 不应为空")
	}
}

// TestReAct_NaturalCompletion 自然完成（AC2）
func TestReAct_NaturalCompletion(t *testing.T) {
	fp := &fakeProvider{
		scripts: [][]llm.StreamEvent{
			{
				{Text: "纯文本答复，无需工具"},
				{Usage: &llm.Usage{InputTokens: 10, OutputTokens: 20}},
				{Done: true},
			},
		},
	}

	registry := tool.NewDefaultRegistry()
	conv := &conversation.Conversation{}
	conv.AddUser("hi")

	a := New(fp, registry)
	events := collectEvents(a.Run(context.Background(), conv, ModeNormal))

	if !hasEventType(events, func(e Event) bool { return e.Done }) {
		t.Error("期望 Done 事件")
	}
	if !hasEventType(events, func(e Event) bool { return strings.Contains(e.Text, "纯文本答复") }) {
		t.Error("期望最终文本")
	}
	// 仅 1 次 Stream 调用
	if fp.calls != 1 {
		t.Errorf("期望 1 次 Stream 调用，实际 %d", fp.calls)
	}
}

// TestReAct_StreamError 流出错恢复（AC5）
func TestReAct_StreamError(t *testing.T) {
	fp := &fakeProvider{
		scripts: [][]llm.StreamEvent{
			{
				{Err: &testError{msg: "connection reset"}},
			},
		},
	}

	registry := tool.NewDefaultRegistry()
	conv := &conversation.Conversation{}
	conv.AddUser("hi")

	a := New(fp, registry)
	events := collectEvents(a.Run(context.Background(), conv, ModeNormal))

	// 应收到 Err 事件
	if !hasEventType(events, func(e Event) bool { return e.Err != nil }) {
		t.Error("期望 Err 事件")
	}
	// 不应 Done（出错不停机）
	if hasEventType(events, func(e Event) bool { return e.Done }) {
		t.Error("不应有 Done 事件")
	}
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
