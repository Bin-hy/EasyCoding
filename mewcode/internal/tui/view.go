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

	toolStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#44CCCC")) // 青色工具名

	toolResultStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888")) // 灰色结果

	toolErrorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF4444")) // 红色错误结果

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

// toolLine 渲染 Claude Code 风格工具行：● 工具名(参数)
func toolLine(name, args string) string {
	line := fmt.Sprintf("● %s(%s)", name, args)
	return toolStyle.Render(line)
}

// toolResultSummary 渲染工具结果摘要，缩进、截断
func toolResultSummary(result string, isError bool) string {
	// UI 截断 ~8 行
	lines := strings.Split(result, "\n")
	if len(lines) > 8 {
		lines = lines[:8]
		result = strings.Join(lines, "\n") + "\n..."
	}

	// 缩进两空格
	summary := "  ⎿  " + strings.ReplaceAll(result, "\n", "\n     ")

	if isError {
		return toolErrorStyle.Render(summary)
	}
	return toolResultStyle.Render(summary)
}

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

	if m.curTool != nil {
		// 工具执行中：显示工具行 + Running...
		line := fmt.Sprintf("● %s(%s)", m.curTool.name, m.curTool.args)
		sb.WriteString(toolStyle.Render(line))
		sb.WriteString(" ")
		sb.WriteString(spinnerStyle.Render(m.spinner.View()))
		sb.WriteString(" ")
		sb.WriteString(spinnerStyle.Render("Running…"))
	} else {
		sb.WriteString(spinnerStyle.Render(m.spinner.View()))
		sb.WriteString(" ")
		sb.WriteString(spinnerStyle.Render(fmt.Sprintf("Imagining… (%ds)", int(elapsed.Seconds()))))
	}

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
