package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"mewcode/internal/command"
)

// completionMaxRows 补全菜单最大可见行数。
const completionMaxRows = 8

// completionMenu 管理斜杠命令补全菜单的状态。
type completionMenu struct {
	items  []*command.Command
	cursor int
	offset int
	active bool
}

// Update 根据当前输入刷新候选列表。
func (c *completionMenu) Update(input string, reg *command.Registry) {
	input = strings.TrimSpace(input)
	if input == "" || input[0] != '/' {
		c.active = false
		c.items = nil
		c.cursor = 0
		c.offset = 0
		return
	}

	c.items = reg.PrefixMatch(input)
	c.active = true
	if c.cursor >= len(c.items) && len(c.items) > 0 {
		c.cursor = len(c.items) - 1
	}
	if len(c.items) == 0 {
		c.cursor = 0
	}
	c.clampOffset()
}

func (c *completionMenu) MoveUp() {
	if !c.active || len(c.items) == 0 {
		return
	}
	if c.cursor > 0 {
		c.cursor--
	}
	c.clampOffset()
}

func (c *completionMenu) MoveDown() {
	if !c.active || len(c.items) == 0 {
		return
	}
	if c.cursor < len(c.items)-1 {
		c.cursor++
	}
	c.clampOffset()
}

func (c *completionMenu) Selected() *command.Command {
	if !c.active || len(c.items) == 0 {
		return nil
	}
	return c.items[c.cursor]
}

func (c *completionMenu) Hide() {
	c.active = false
	c.items = nil
	c.cursor = 0
	c.offset = 0
}

func (c *completionMenu) clampOffset() {
	if c.offset > c.cursor {
		c.offset = c.cursor
	}
	if c.cursor >= c.offset+completionMaxRows {
		c.offset = c.cursor - completionMaxRows + 1
	}
}

func (c *completionMenu) Render(width int) string {
	if !c.active {
		return ""
	}

	if len(c.items) == 0 {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888")).
			Render("  无匹配命令")
	}

	maxNameLen := 0
	for _, item := range c.items {
		if len(item.Name) > maxNameLen {
			maxNameLen = len(item.Name)
		}
	}

	start := c.offset
	end := start + completionMaxRows
	if end > len(c.items) {
		end = len(c.items)
	}

	var sb strings.Builder

	if start > 0 {
		sb.WriteString(lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888")).
			Render(fmt.Sprintf("  ↑ %d more", start)))
		sb.WriteString("\n")
	}

	highlightStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#CCCCCC"))
	normalStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#CCCCCC"))

	for i := start; i < end; i++ {
		item := c.items[i]
		pad := maxNameLen - len(item.Name)
		line := fmt.Sprintf("  /%s  %s%s", item.Name, strings.Repeat(" ", pad+2), item.Description)

		if i == c.cursor {
			sb.WriteString(highlightStyle.Render(line))
		} else {
			sb.WriteString(normalStyle.Render(line))
		}
		sb.WriteString("\n")
	}

	if end < len(c.items) {
		sb.WriteString(lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888")).
			Render(fmt.Sprintf("  ↓ %d more", len(c.items)-end)))
	}

	return sb.String()
}

// handleCompletionKey 处理补全菜单按键。
func (m *Model) handleCompletionKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if !m.completion.active {
		return nil, false
	}

	switch msg.Code {
	case tea.KeyUp:
		m.completion.MoveUp()
		return nil, true

	case tea.KeyDown:
		m.completion.MoveDown()
		return nil, true

	case tea.KeyEscape:
		m.completion.Hide()
		return nil, true

	case tea.KeyTab:
		sel := m.completion.Selected()
		if sel == nil {
			m.completion.Hide()
			return nil, true
		}
		m.textarea.SetValue("/" + sel.Name)
		text := strings.TrimSpace(m.textarea.Value())
		m.textarea.Reset()
		m.completion.Hide()
		if cmd, handled := m.dispatchSlash(text); handled {
			return cmd, true
		}
		return nil, true

	case tea.KeyEnter:
		// 自动补全选中命令名到 textarea，再透传给 handleIdleKey 正常分发
		if len(m.completion.items) == 0 {
			m.completion.Hide()
			return nil, true
		}
		if sel := m.completion.Selected(); sel != nil {
			m.textarea.SetValue("/" + sel.Name)
		}
		m.completion.Hide()
		return nil, false
	}

	return nil, false
}

// syncCompletionFromInput 根据 textarea 当前内容同步补全菜单。
func (m *Model) syncCompletionFromInput() {
	value := m.textarea.Value()
	m.completion.Update(value, m.cmdRegistry)
}
