package tui

import (
	tea "charm.land/bubbletea/v2"
	"mewcode/internal/agent"
)

// agentEvent 包装 agent.Event 作为 bubbletea 消息
type agentEvent agent.Event

// waitForEvent 从 agent 事件 channel 读取事件并返回为 bubbletea 消息。
// channel 关闭时返回 Done 事件。
func waitForEvent(ch <-chan agent.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return agentEvent{Done: true}
		}
		return agentEvent(ev)
	}
}
