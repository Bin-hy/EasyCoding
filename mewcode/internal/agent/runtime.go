package agent

import (
	"sync"

	"mewcode/internal/compact"
)

// SessionRuntime 跨 Run 调用的长生命周期状态容器。
// TUI Model 持有，每轮 Run 时传入 Agent。
type SessionRuntime struct {
	Replacement   *compact.ContentReplacementState
	Recovery      *compact.RecoveryState
	AutoTracking  *compact.AutoCompactTrackingState
	Session       *compact.SessionContext
	ContextWindow int
	UsageAnchor   int64      // 主对话路径 Stream 真实 usage 之和；摘要请求不更新
	AnchorMsgLen  int        // anchor 当时 Conversation.Len()
	mu            sync.Mutex // 保护 UsageAnchor / AnchorMsgLen 的读写
}

// UpdateAnchor 更新 token 估算锚点。
func (r *SessionRuntime) UpdateAnchor(anchor int64, msgLen int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.UsageAnchor = anchor
	r.AnchorMsgLen = msgLen
}

// ResetAnchor 重置锚点（紧急压缩后使用）。
func (r *SessionRuntime) ResetAnchor() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.UsageAnchor = 0
	r.AnchorMsgLen = 0
}

// GetAnchor 获取当前锚点值与对应的消息长度。
func (r *SessionRuntime) GetAnchor() (int64, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.UsageAnchor, r.AnchorMsgLen
}

// Option 函数式选项，用于 New 的可选参数注入。
type Option func(*Agent)

// WithRuntime 注入跨 Run 复用的长生命周期状态。
func WithRuntime(r *SessionRuntime) Option {
	return func(a *Agent) {
		a.runtime = r
	}
}
