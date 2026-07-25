package agent

import (
	"sync"

	"mewcode/internal/compact"
	"mewcode/internal/hook"
	"mewcode/internal/llm"
	"mewcode/internal/memory"
	"mewcode/internal/permission"
	"mewcode/internal/skills"
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
	TurnCount     int        // 会话轮次计数（用于记忆更新触发）
	ActiveSkills  *skills.ActiveSkills // 已激活 Skill 列表（跨轮保持）

	// Hook 系统扩展
	HookEngine       *hook.Engine // Hook 事件分派引擎
	PendingReminders []string     // 待注入的 prompt 提醒文本（每轮取出后清空）

	mu sync.Mutex // 保护 UsageAnchor / AnchorMsgLen / TurnCount / PendingReminders 的读写
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

// IncTurn 递增轮次计数。
func (r *SessionRuntime) IncTurn() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.TurnCount++
	return r.TurnCount
}

// ResetForNewSession 清空所有 compact 子状态、锚点和轮次计数，替换 Session 引用。
// ContextWindow 保留不变。同时清空 ActiveSkills、PendingReminders，重置 Hook 引擎 only_once 集合。
func (r *SessionRuntime) ResetForNewSession(sesCtx *compact.SessionContext) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Replacement = nil
	r.Recovery = nil
	r.AutoTracking = nil
	r.Session = sesCtx
	r.UsageAnchor = 0
	r.AnchorMsgLen = 0
	r.TurnCount = 0
	r.PendingReminders = nil
	if r.ActiveSkills != nil {
		r.ActiveSkills.Clear()
	}
	if r.HookEngine != nil {
		r.HookEngine.ResetForNewSession()
	}
}

// Option 函数式选项，用于 New 的可选参数注入。
type Option func(*Agent)

// WithRuntime 注入跨 Run 复用的长生命周期状态。
func WithRuntime(r *SessionRuntime) Option {
	return func(a *Agent) {
		a.runtime = r
	}
}

// WithMemoryManager 注入记忆更新管理器。
func WithMemoryManager(m *memory.Manager) Option {
	return func(a *Agent) {
		a.memMgr = m
	}
}

// WithInstructionText 注入项目指令文本。
func WithInstructionText(text string) Option {
	return func(a *Agent) {
		a.instructionText = text
	}
}

// WithMemoryText 注入记忆索引文本。
func WithMemoryText(text string) Option {
	return func(a *Agent) {
		a.memoryText = text
	}
}

// WithCatalog 注入 Skill 目录引用。
func WithCatalog(c *skills.Catalog) Option {
	return func(a *Agent) {
		a.catalog = c
	}
}

// WithHookEngine 注入 Hook 事件分派引擎。
func WithHookEngine(e *hook.Engine) Option {
	return func(a *Agent) {
		a.hookEngine = e
	}
}

// WithSystemPrompt 注入子 Agent 角色系统提示，覆盖默认 mewcode 主 Agent 系统提示（spec F10）。
func WithSystemPrompt(text string) Option {
	return func(a *Agent) {
		a.systemPrompt = text
	}
}

// WithMaxTurns 限制本 Agent 的最大迭代轮数（spec F10）。
// n<=0 时忽略（沿用全局 maxIterations）。
func WithMaxTurns(n int) Option {
	return func(a *Agent) {
		if n > 0 {
			a.maxTurns = n
		}
	}
}

// WithPermissionMode 设置子 Agent 的权限模式（spec F10）。
func WithPermissionMode(m permission.Mode) Option {
	return func(a *Agent) {
		a.permissionMode = m
		a.permissionModeSet = true
	}
}

// WithDontAsk 设置子 Agent dontAsk 模式——自动批准所有规则未命中的工具（spec F10）。
func WithDontAsk(enabled bool) Option {
	return func(a *Agent) {
		a.dontAsk = enabled
	}
}

// WithApprovalUpgrader 设置子 Agent 审批升级到父 TUI 的回调（spec F10）。
func WithApprovalUpgrader(fn ApprovalUpgrader) Option {
	return func(a *Agent) {
		a.approvalUpgrader = fn
	}
}

// WithProvider 覆盖 Agent 的 LLM Provider（spec F10）。
func WithProvider(p llm.Provider) Option {
	return func(a *Agent) {
		a.provider = p
	}
}

// WithAllowedTools 注入工具白名单（子 Agent 专用；非空时限制工具集）。
func WithAllowedTools(allowed []string) Option {
	return func(a *Agent) {
		a.allowedTools = allowed
	}
}

// AppendReminders 追加待注入的提醒文本。
func (r *SessionRuntime) AppendReminders(prompts []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.PendingReminders = append(r.PendingReminders, prompts...)
}

// TakeReminders 取出并清空所有待注入的提醒文本。
func (r *SessionRuntime) TakeReminders() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	reminders := r.PendingReminders
	r.PendingReminders = nil
	return reminders
}
