package tui

import (
	tea "charm.land/bubbletea/v2"
	"mewcode/internal/llm"
)

// streamMsg 流式事件消息（包装 llm.StreamEvent 作为 bubbletea Msg）
type streamMsg llm.StreamEvent

// waitForEvent 从 channel 读取一个流式事件并返回为 bubbletea 消息
// channel 关闭时返回 Done 事件
func waitForEvent(ch <-chan llm.StreamEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return streamMsg{Done: true}
		}
		return streamMsg(ev)
	}
}
