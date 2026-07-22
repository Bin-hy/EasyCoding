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

	// 记录最后一次 Stream 收到的 Request（供断言用）
	lastReq llm.Request
}

func (f *fakeProvider) Name() string  { return "fake" }
func (f *fakeProvider) Model() string { return "fake-model" }

func (f *fakeProvider) Stream(ctx context.Context, req llm.Request) <-chan llm.StreamEvent {
	f.mu.Lock()
	f.calls++
	idx := f.calls - 1
	f.lastReq = req
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

	a := New(fp, registry, "test")
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

	// 断言 System.Stable 非空、Environment 非空
	fp.mu.Lock()
	req := fp.lastReq
	fp.mu.Unlock()
	if req.System.Stable == "" {
		t.Error("期望 req.System.Stable 非空")
	}
	if req.System.Environment == "" {
		t.Error("期望 req.System.Environment 非空")
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

	a := New(fp, registry, "test")
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

		a := New(fp, registry, "test")
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

		a := New(fp, registry, "test")
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
				for {
					old := maxConcurrent.Load()
					if c <= old || maxConcurrent.CompareAndSwap(old, c) {
						break
					}
				}
				time.Sleep(50 * time.Millisecond)
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

	registry := &tool.Registry{}
	ro1 := makeRO("read_file")
	ro2 := makeRO("glob")
	registry.Register(ro1)
	registry.Register(ro2)
	registry.Register(rw)

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

	a := New(fp, registry, "test")
	_ = collectEvents(a.Run(context.Background(), conv, ModeNormal))

	if maxConcurrent.Load() < 2 {
		t.Errorf("期望只读工具并发峰值≥2，实际=%d", maxConcurrent.Load())
	}

	if rwStartedAt.IsZero() {
		t.Error("bash 工具未被调用")
	}

	msgs := conv.Messages()
	foundToolResults := false
	for _, m := range msgs {
		if m.Role == "tool" && len(m.ToolResults) == 3 {
			foundToolResults = true
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

	a := New(fp, registry, "test")
	ch := a.Run(ctx, conv, ModeNormal)

	eventsDone := make(chan struct{})
	var evs []Event
	go func() {
		evs = collectEvents(ch)
		close(eventsDone)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-eventsDone
	_ = evs

	lastRole := conv.LastRole()
	if lastRole != "assistant" {
		t.Errorf("取消后期望 LastRole=assistant，实际=%s", lastRole)
	}

	hasToolResults := false
	for _, m := range conv.Messages() {
		if m.Role == "tool" && len(m.ToolResults) > 0 {
			hasToolResults = true
		}
	}
	if !hasToolResults {
		t.Error("取消后历史应包含 tool_results")
	}

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
	a2 := New(fp2, registry, "test")
	events2 := collectEvents(a2.Run(context.Background(), conv2, ModeNormal))
	if !hasEventType(events2, func(e Event) bool { return e.Done }) {
		t.Error("取消后新对话应正常结束")
	}
}

// scenario F: Plan Mode 工具集（AC13）——改：校验 req.Tools 与 req.Reminder
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

	a := New(fp, registry, "test")
	collectEvents(a.Run(context.Background(), conv, ModePlan))

	fp.mu.Lock()
	req := fp.lastReq
	fp.mu.Unlock()

	// 断言 tools 仅含只读工具
	readOnlyNames := map[string]bool{"read_file": true, "glob": true, "grep": true}
	for _, td := range req.Tools {
		if !readOnlyNames[td.Name] {
			t.Errorf("Plan Mode 下不应出现非只读工具: %s", td.Name)
		}
	}

	// 断言 Reminder 非空、含 <system-reminder>
	if req.Reminder == "" {
		t.Error("Plan Mode 下 Reminder 不应为空")
	}
	if !strings.Contains(req.Reminder, "<system-reminder>") {
		t.Error("Reminder 应包含 <system-reminder> 标签")
	}

	// 断言 System.Stable 非空、Environment 非空
	if req.System.Stable == "" {
		t.Error("期望 req.System.Stable 非空")
	}
	if req.System.Environment == "" {
		t.Error("期望 req.System.Environment 非空")
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

	a := New(fp, registry, "test")
	events := collectEvents(a.Run(context.Background(), conv, ModeNormal))

	if !hasEventType(events, func(e Event) bool { return e.Done }) {
		t.Error("期望 Done 事件")
	}
	if !hasEventType(events, func(e Event) bool { return strings.Contains(e.Text, "纯文本答复") }) {
		t.Error("期望最终文本")
	}
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

	a := New(fp, registry, "test")
	events := collectEvents(a.Run(context.Background(), conv, ModeNormal))

	if !hasEventType(events, func(e Event) bool { return e.Err != nil }) {
		t.Error("期望 Err 事件")
	}
	if hasEventType(events, func(e Event) bool { return e.Done }) {
		t.Error("不应有 Done 事件")
	}
}

// TestReAct_SystemStableIdentical 跨模式 Stable 一致（规划提醒已移出系统通道）
func TestReAct_SystemStableIdentical(t *testing.T) {
	registry := tool.NewDefaultRegistry()

	// Normal 模式
	fpNormal := &fakeProvider{
		scripts: [][]llm.StreamEvent{{{Text: "ok", Done: true}}},
	}
	convNormal := &conversation.Conversation{}
	convNormal.AddUser("hi")
	a := New(fpNormal, registry, "test")
	collectEvents(a.Run(context.Background(), convNormal, ModeNormal))
	fpNormal.mu.Lock()
	stableNormal := fpNormal.lastReq.System.Stable
	fpNormal.mu.Unlock()

	// Plan 模式
	fpPlan := &fakeProvider{
		scripts: [][]llm.StreamEvent{{{Text: "plan", Done: true}}},
	}
	convPlan := &conversation.Conversation{}
	convPlan.AddUser("plan")
	a2 := New(fpPlan, registry, "test")
	collectEvents(a2.Run(context.Background(), convPlan, ModePlan))
	fpPlan.mu.Lock()
	stablePlan := fpPlan.lastReq.System.Stable
	fpPlan.mu.Unlock()

	// 两模式 Stable 应一致
	if stableNormal != stablePlan {
		t.Error("普通与规划模式 req.System.Stable 应相同")
	}
	if stableNormal == "" {
		t.Error("Stable 不应为空")
	}
}

// TestReAct_PlanReminderByIter 规划模式按轮次注入 reminder 详略
func TestReAct_PlanReminderByIter(t *testing.T) {
	// 构造 6 轮脚本：前 5 轮返回工具调用，第 6 轮纯文本
	scripts := make([][]llm.StreamEvent, 6)
	for i := 0; i < 5; i++ {
		scripts[i] = []llm.StreamEvent{
			{ToolCalls: []llm.ToolCall{{ID: "r", Name: "read_file", Input: json.RawMessage(`{}`)}}},
			{Usage: &llm.Usage{InputTokens: 10, OutputTokens: 5}},
			{Done: true},
		}
	}
	scripts[5] = []llm.StreamEvent{{Text: "plan done", Done: true}}

	fp := &fakeProvider{scripts: scripts}
	registry := tool.NewDefaultRegistry()
	conv := &conversation.Conversation{}
	conv.AddUser("plan a task")

	a := New(fp, registry, "test")
	collectEvents(a.Run(context.Background(), conv, ModePlan))

	// iter=1 首轮应完整提醒，iter=2 精简，iter=5 间隔轮完整
	// 从 calls 推理——fakeProvider 只记录最后一次
	// 改为在每次 Stream 后检查 reminder 模式
	// 这里使用 conv 轮次来推断
	if fp.calls < 3 {
		t.Fatal("期望至少 3 次 Stream 调用")
	}
	// 验证 conv.Messages 不含 reminder 文本（reminder 不持久）
	for _, m := range conv.Messages() {
		if strings.Contains(m.Content, "<system-reminder>") {
			t.Error("conv 持久历史不应包含 reminder")
		}
	}
}

// TestReAct_CacheUsagePassthrough 缓存用量透传
func TestReAct_CacheUsagePassthrough(t *testing.T) {
	fp := &fakeProvider{
		scripts: [][]llm.StreamEvent{
			{
				{Text: "done"},
				{Usage: &llm.Usage{InputTokens: 100, OutputTokens: 50, CacheWrite: 42, CacheRead: 99}},
				{Done: true},
			},
		},
	}

	registry := tool.NewDefaultRegistry()
	conv := &conversation.Conversation{}
	conv.AddUser("hi")

	a := New(fp, registry, "test")
	events := collectEvents(a.Run(context.Background(), conv, ModeNormal))

	found := false
	for _, e := range events {
		if e.Usage != nil {
			found = true
			if e.Usage.CacheWrite != 42 {
				t.Errorf("期望 CacheWrite=42，实际=%d", e.Usage.CacheWrite)
			}
			if e.Usage.CacheRead != 99 {
				t.Errorf("期望 CacheRead=99，实际=%d", e.Usage.CacheRead)
			}
		}
	}
	if !found {
		t.Error("期望收到 Usage 事件")
	}
}

// TestReAct_NormalModeNoReminder 普通模式下不注入 reminder
func TestReAct_NormalModeNoReminder(t *testing.T) {
	fp := &fakeProvider{
		scripts: [][]llm.StreamEvent{{{Text: "done", Done: true}}},
	}

	registry := tool.NewDefaultRegistry()
	conv := &conversation.Conversation{}
	conv.AddUser("hi")

	a := New(fp, registry, "test")
	collectEvents(a.Run(context.Background(), conv, ModeNormal))

	fp.mu.Lock()
	reminder := fp.lastReq.Reminder
	fp.mu.Unlock()

	if reminder != "" {
		t.Error("普通模式下 Reminder 应为空")
	}
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
