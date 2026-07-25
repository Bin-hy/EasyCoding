package task

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"mewcode/internal/agent"
	"mewcode/internal/conversation"
)

// Status 表示后台任务的运行状态。
type Status int

const (
	StatusRunning   Status = iota // 正在运行
	StatusCompleted               // 已完成
	StatusFailed                  // 运行失败
	StatusCancelled               // 已取消
)

// String 返回状态的可读名称。
func (s Status) String() string {
	switch s {
	case StatusRunning:
		return "running"
	case StatusCompleted:
		return "completed"
	case StatusFailed:
		return "failed"
	case StatusCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// Usage 累计 token 用量。
type Usage struct {
	Input, Output, CacheWrite, CacheRead int64
}

// PartialState 是前台→后台移交时已收集的中间状态。
type PartialState struct {
	LastAssistantText string
	ToolCount         int
	LastActivity      string
	Usage             Usage
}

// BackgroundTask 是一个后台子 Agent 的完整状态快照（spec F15）。
type BackgroundTask struct {
	ID           string                  // manager 生成，如 "task_<8 字节十六进制>"
	Name         string                  // Agent 工具 name 参数，可空
	SubAgent     *agent.Agent            // 子 Agent 实例
	Conv         *conversation.Conversation // 子对话
	Task         string                  // 初始任务文本
	Status       Status                  // running/completed/failed/cancelled
	Result       string                  // 跑完的最终文本
	Err          error                   // 错误（如有）
	StartTime    time.Time               // 启动时间
	EndTime      time.Time               // 结束时间
	Cancel       context.CancelFunc      // 取消回调
	Usage        Usage                   // 累计 token
	ToolCount    int                     // 工具调用累计
	LastActivity string                  // 最近一次工具名
}

// Manager 管理后台任务。线程安全（spec F14）。
type Manager struct {
	mu      sync.Mutex
	tasks   map[string]*BackgroundTask // id → task
	byName  map[string]string          // name → id，弱引用，后启动覆盖
	done    chan string                // 完成任务的 id push 进去，TUI 消费；缓冲 32
	counter int64                      // 原子递增的 id 计数器
}

// NewManager 创建一个新的后台任务管理器。
func NewManager() *Manager {
	return &Manager{
		tasks:   make(map[string]*BackgroundTask),
		byName:  make(map[string]string),
		done:    make(chan string, 32),
	}
}

// nextID 生成唯一任务 ID。
func (m *Manager) nextID() string {
	n := atomic.AddInt64(&m.counter, 1)
	// 用 time.Now().UnixNano() ^ n 取低 8 字节十六进制
	id := time.Now().UnixNano() ^ n
	return fmt.Sprintf("task_%08x", uint32(id&0xFFFFFFFF))
}

// Get 按 ID 查找任务。
func (m *Manager) Get(id string) (*BackgroundTask, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	return t, ok
}

// List 返回所有任务（按 StartTime 升序）。
func (m *Manager) List() []*BackgroundTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*BackgroundTask, 0, len(m.tasks))
	for _, t := range m.tasks {
		result = append(result, t)
	}
	// 按 StartTime 升序排序
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[i].StartTime.After(result[j].StartTime) {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	return result
}

// SubscribeDone 返回 done channel，TUI 在主事件循环里消费（spec F14）。
func (m *Manager) SubscribeDone() <-chan string {
	return m.done
}

// Stop 触发任务取消（spec F14）。
func (m *Manager) Stop(id string) bool {
	m.mu.Lock()
	t, ok := m.tasks[id]
	m.mu.Unlock()
	if !ok || t.Cancel == nil {
		return false
	}
	t.Cancel()
	return true
}

// Launch 起一个后台 goroutine 跑 agent.RunToCompletion（spec F16）。
// Conv 应该是已经装填了消息的子对话。
// 返回任务 ID。
func (m *Manager) Launch(parentCtx context.Context, ag *agent.Agent, conv *conversation.Conversation, name, taskText string) string {
	id := m.nextID()
	ctx, cancel := context.WithCancel(parentCtx)

	bt := &BackgroundTask{
		ID:        id,
		Name:      name,
		SubAgent:  ag,
		Conv:      conv,
		Task:      taskText,
		Status:    StatusRunning,
		StartTime: time.Now(),
		Cancel:    cancel,
	}

	m.mu.Lock()
	m.tasks[id] = bt
	if name != "" {
		m.byName[name] = id // 后启动覆盖前
	}
	m.mu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				bt.Status = StatusFailed
				bt.Err = fmt.Errorf("subagent panic: %v", r)
				bt.EndTime = time.Now()
			}
			select {
			case m.done <- id:
			default:
				fmt.Fprintf(os.Stderr, "task manager: done channel full, dropping notification for %s\n", id)
			}
		}()

		// 聚合事件用的 channel
		events := make(chan agent.Event, 32)
		go aggregateTaskEvents(events, bt)

		text, err := ag.RunToCompletion(ctx, conv, taskText, events)
		close(events)

		bt.EndTime = time.Now()
		if err != nil {
			if errors.Is(err, context.Canceled) {
				bt.Status = StatusCancelled
			} else {
				bt.Status = StatusFailed
				bt.Err = err
				bt.Result = text
			}
		} else {
			bt.Status = StatusCompleted
			bt.Result = text
		}
	}()

	return id
}

// AdoptRunning 把一个正在前台跑的 agent 移交到后台（spec F17.2/F17.3）。
// 调用方应已经把"用户的 ESC/120 秒超时"对应的 cancel 准备好。
// Manager 接管 ev 事件流继续消费，直到 Done 或 Err。
func (m *Manager) AdoptRunning(parentCtx context.Context, ag *agent.Agent, conv *conversation.Conversation, name string, ev <-chan agent.Event, cancel context.CancelFunc) string {
	id := m.nextID()

	bt := &BackgroundTask{
		ID:        id,
		Name:      name,
		SubAgent:  ag,
		Conv:      conv,
		Task:      "",   // 前台 task 已由 RunToCompletion 执行，此处不重复
		Status:    StatusRunning,
		StartTime: time.Now(),
		Cancel:    cancel,
	}

	m.mu.Lock()
	m.tasks[id] = bt
	if name != "" {
		m.byName[name] = id
	}
	m.mu.Unlock()

	// 起 goroutine 继续消费 events 直到 channel 关闭
	go func() {
		defer func() {
			if r := recover(); r != nil {
				bt.Status = StatusFailed
				bt.Err = fmt.Errorf("subagent panic: %v", r)
				bt.EndTime = time.Now()
			}
			select {
			case m.done <- id:
			default:
				fmt.Fprintf(os.Stderr, "task manager: done channel full, dropping notification for %s\n", id)
			}
		}()

		for ev := range ev {
			aggregateEvent(ev, bt)
		}

		bt.EndTime = time.Now()
		// 状态已在 RunToCompletion 结束时设定
		if bt.Status == StatusRunning {
			bt.Status = StatusCompleted
		}
	}()

	return id
}

// ErrTaskBusy 表示任务正忙，无法处理 SendMessage。
var ErrTaskBusy = errors.New("task is busy")

// ErrTaskNotFound 表示未找到指定名称的任务。
var ErrTaskNotFound = errors.New("task not found")

// SendMessage 给一个仍存活的后台 Agent 续派任务（spec F14）。
// 按 name 找到仍存活的后台 Agent，把 message 作为新 user 消息追加到 Conv 并重新跑一轮。
func (m *Manager) SendMessage(parentCtx context.Context, name, message string) (string, error) {
	m.mu.Lock()
	id, ok := m.byName[name]
	if !ok {
		m.mu.Unlock()
		return "", ErrTaskNotFound
	}
	bt, ok := m.tasks[id]
	m.mu.Unlock()
	if !ok {
		return "", ErrTaskNotFound
	}
	if bt.Status != StatusCompleted {
		return "", ErrTaskBusy
	}

	// 重新激活：追加 user 消息，重置状态
	bt.Conv.AddUser(message)
	bt.Status = StatusRunning

	// 重新起 goroutine 跑
	ctx, cancel := context.WithCancel(parentCtx)
	bt.Cancel = cancel

	go func() {
		defer func() {
			if r := recover(); r != nil {
				bt.Status = StatusFailed
				bt.Err = fmt.Errorf("subagent panic: %v", r)
				bt.EndTime = time.Now()
			}
			select {
			case m.done <- id:
			default:
				fmt.Fprintf(os.Stderr, "task manager: done channel full, dropping notification for %s\n", id)
			}
		}()

		events := make(chan agent.Event, 32)
		go aggregateTaskEvents(events, bt)

		text, err := bt.SubAgent.RunToCompletion(ctx, bt.Conv, "", events) // task 已在 Conv 中
		close(events)

		bt.EndTime = time.Now()
		if err != nil {
			if errors.Is(err, context.Canceled) {
				bt.Status = StatusCancelled
			} else {
				bt.Status = StatusFailed
				bt.Err = err
				bt.Result = text
			}
		} else {
			bt.Status = StatusCompleted
			bt.Result = text
		}
	}()

	return id, nil
}

// aggregateTaskEvents 消费事件流并更新 BackgroundTask 的统计信息。
func aggregateTaskEvents(ch <-chan agent.Event, bt *BackgroundTask) {
	for ev := range ch {
		aggregateEvent(ev, bt)
	}
}

// aggregateEvent 处理单个事件。
func aggregateEvent(ev agent.Event, bt *BackgroundTask) {
	if ev.Tool != nil && ev.Tool.Phase == agent.PhaseEnd {
		bt.ToolCount++
		bt.LastActivity = ev.Tool.Name
	}
	if ev.Usage != nil {
		bt.Usage.Input += ev.Usage.Input
		bt.Usage.Output += ev.Usage.Output
		bt.Usage.CacheWrite += ev.Usage.CacheWrite
		bt.Usage.CacheRead += ev.Usage.CacheRead
	}
}
