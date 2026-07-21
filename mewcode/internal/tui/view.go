package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

var (
	userStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF"))

	assistantStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF4444"))

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888")).
			Background(lipgloss.Color("#1a1a1a")).
			Padding(0, 1)

	inputBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#555555"))

	spinnerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888"))
)

func renderUserBlock(text string) string {
	return userStyle.Render("● " + text)
}

func renderAssistantBlock(rendered string) string {
	return assistantStyle.Render("●\n" + strings.TrimSpace(rendered))
}

func renderErrorBlock(errText string) string {
	return errorStyle.Render("● 错误: " + errText)
}

// View 返回界面渲染
func (m *Model) View() tea.View {
	switch m.state {
	case stateSelecting:
		return tea.NewView(m.viewSelecting())
	default:
		return tea.NewView(m.viewChat())
	}
}

func (m *Model) viewChat() string {
	var b strings.Builder

	// 流式中的回复（动态逐字显示）
	if m.state == stateStreaming {
		b.WriteString(m.renderStreamingReply())
		b.WriteString("\n\n")
	}

	// 带边框的输入框
	inputBox := inputBorderStyle.Render(m.textarea.View())
	b.WriteString(inputBox)
	b.WriteString("\n")

	// 状态栏
	b.WriteString(m.renderStatusBar())

	return b.String()
}

func (m *Model) renderStreamingReply() string {
	elapsed := time.Since(m.turnStart).Round(time.Second)
	text := m.curReply.String()

	var sb strings.Builder
	sb.WriteString(spinnerStyle.Render(m.spinner.View()))
	sb.WriteString(" ")
	sb.WriteString(spinnerStyle.Render(fmt.Sprintf("Imagining… (%ds)", int(elapsed.Seconds()))))

	if text != "" {
		sb.WriteString("\n")
		sb.WriteString(text)
	}

	return sb.String()
}

func (m *Model) renderStatusBar() string {
	left := ""
	right := ""

	if m.provider != nil {
		left = m.provider.Name()
		right = m.provider.Model()
	}

	width := m.width
	if width < 20 {
		width = 80
	}

	leftStyled := statusBarStyle.Render(left)
	rightStyled := statusBarStyle.Render(right)
	padding := width - lipgloss.Width(leftStyled) - lipgloss.Width(rightStyled)
	if padding < 1 {
		padding = 1
	}

	return leftStyled + strings.Repeat(" ", padding) + rightStyled
}
