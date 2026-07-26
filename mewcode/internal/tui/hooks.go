package tui

import (
	"context"

	"mewcode/internal/hook"
)

// ─── Session 生命周期 dispatch ──────────────────────────

// basePayload 构造 TUI 侧通用 payload（会话级事件使用）。
func (m *Model) baseHookPayload(event hook.Event) hook.Payload {
	sessionID := ""
	if m.runtime != nil && m.runtime.Session != nil {
		sessionID = m.runtime.Session.SessionID
	}
	return hook.Payload{
		"event":      string(event),
		"session_id": sessionID,
		"cwd":        m.cwd,
		"mode":       m.mode.String(),
	}
}

// dispatchSessionStart 派发 SessionStart 事件并注入 reminders。
func (m *Model) dispatchSessionStart() {
	if m.hookEngine == nil {
		return
	}
	result := m.hookEngine.Dispatch(context.Background(), hook.EventSessionStart, m.baseHookPayload(hook.EventSessionStart))
	if m.runtime != nil && len(result.InjectedPrompts) > 0 {
		m.runtime.AppendReminders(result.InjectedPrompts)
	}
}

// dispatchSessionEnd 派发 SessionEnd 事件。
func (m *Model) dispatchSessionEnd() {
	if m.hookEngine == nil {
		return
	}
	m.hookEngine.Dispatch(context.Background(), hook.EventSessionEnd, m.baseHookPayload(hook.EventSessionEnd))
}

// ─── UserPromptSubmit ────────────────────────────────────

// dispatchUserPromptSubmit 派发 UserPromptSubmit 事件。
// 返回 (blocked, reason, blockingHookName)。
func (m *Model) dispatchUserPromptSubmit(text string) (bool, string, string) {
	if m.hookEngine == nil {
		return false, "", ""
	}
	payload := m.baseHookPayload(hook.EventUserPromptSubmit)
	payload["prompt"] = text
	result := m.hookEngine.Dispatch(context.Background(), hook.EventUserPromptSubmit, payload)
	if m.runtime != nil && len(result.InjectedPrompts) > 0 {
		m.runtime.AppendReminders(result.InjectedPrompts)
	}
	return result.Blocked, result.Reason, result.BlockingHookName
}

// ─── /hooks 命令 handler ─────────────────────────────────

// hookSources 返回已加载的 hook 来源文件列表。
func (m *Model) hookSources() []string {
	if m.hookEngine == nil {
		return nil
	}
	return m.hookEngine.Sources()
}

// hookRules 返回已加载的 hook 规则列表。
func (m *Model) hookRules() []hook.Rule {
	if m.hookEngine == nil {
		return nil
	}
	return m.hookEngine.Rules()
}

// HookSources 实现 command.UI 接口的 HookSources 方法。
func (m *Model) HookSources() []string {
	return m.hookSources()
}

// HookRules 实现 command.UI 接口的 HookRules 方法。
func (m *Model) HookRules() []interface{} {
	rules := m.hookRules()
	result := make([]interface{}, len(rules))
	for i, r := range rules {
		result[i] = r
	}
	return result
}
