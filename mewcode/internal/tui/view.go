package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"mewcode/internal/agent"
	"mewcode/internal/permission"
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

	// 权限模式标签颜色
	modeDefaultStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#44CC44"))                                       // 绿
	modeAcceptStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#44CCCC"))                                       // 青
	modePlanStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#CCCC44"))                                       // 黄
	modeBypassStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF4444"))                                       // 红
	approvalStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFCC44"))                                       // 金
	approvalGrayStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))                                       // 灰
	approvalHighStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#444444")) // 高亮
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
	case stateResuming:
		return tea.NewView(m.viewResuming())
	case stateApproving:
		return tea.NewView(m.viewApproving())
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

	// 补全菜单（紧贴输入框下方）
	if m.completion.active {
		b.WriteString(m.completion.Render(m.width))
		b.WriteString("\n")
	}

	// 状态栏
	b.WriteString(m.renderStatusBar())

	return b.String()
}

// viewApproving 人在回路待批准视图。
func (m *Model) viewApproving() string {
	var b strings.Builder

	if m.pending != nil {
		b.WriteString(renderApprovalBlock(m.pending, m.approveCursor))
		b.WriteString("\n")
	}

	b.WriteString(m.renderStatusBar())
	return b.String()
}

func (m *Model) renderStreamingReply() string {
	elapsed := time.Since(m.turnStart).Round(time.Second)
	text := m.curReply.String()

	var sb strings.Builder

	if len(m.curTools) > 0 {
		// 多个工具执行中（并发批）：逐行渲染 ● name(args) Running…
		for _, tool := range m.curTools {
			line := fmt.Sprintf("● %s(%s)", tool.name, tool.args)
			sb.WriteString(toolStyle.Render(line))
			sb.WriteString(" ")
			sb.WriteString(spinnerStyle.Render(m.spinner.View()))
			sb.WriteString(" ")
			sb.WriteString(spinnerStyle.Render("Running…"))
			sb.WriteString("\n")
		}
	} else {
		sb.WriteString(spinnerStyle.Render(m.spinner.View()))
		sb.WriteString(" ")
		statusLine := fmt.Sprintf("Imagining… (%ds", int(elapsed.Seconds()))
		if m.iter > 0 {
			statusLine += fmt.Sprintf(" · 第 %d 轮", m.iter)
		}
		statusLine += ")"
		sb.WriteString(spinnerStyle.Render(statusLine))
	}

	if text != "" {
		sb.WriteString("\n")
		sb.WriteString(text)
	}

	return sb.String()
}

func (m *Model) renderStatusBar() string {
	left := modeLabel(m.mode)
	right := ""

	if m.provider != nil {
		right = m.provider.Model()
		// 累计 token 用量
		if m.usageIn > 0 || m.usageOut > 0 {
			right += fmt.Sprintf(" ↑%s ↓%s tok", formatCompact(m.usageIn), formatCompact(m.usageOut))
		}
	}

	width := m.width
	if width < 20 {
		width = 80
	}

	// 模式标签着色
	var leftStyled string
	switch m.mode {
	case permission.ModeDefault:
		leftStyled = modeDefaultStyle.Render(" " + left + " ")
	case permission.ModeAcceptEdits:
		leftStyled = modeAcceptStyle.Render(" " + left + " ")
	case permission.ModePlan:
		leftStyled = modePlanStyle.Render(" " + left + " ")
	case permission.ModeBypass:
		leftStyled = modeBypassStyle.Render(" " + left + " ")
	default:
		leftStyled = statusBarStyle.Render(left)
	}

	rightStyled := statusBarStyle.Render(right)
	padding := width - lipgloss.Width(leftStyled) - lipgloss.Width(rightStyled)
	if padding < 1 {
		padding = 1
	}

	return leftStyled + strings.Repeat(" ", padding) + rightStyled
}

// formatCompact 将大整数格式化为紧凑数字（如 1.2k）。
func formatCompact(n int64) string {
	switch {
	case n >= 1000000:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// renderNoticeBlock 渲染灰色通知提示块。
func renderNoticeBlock(text string) string {
	return statusBarStyle.Render("● " + text)
}

// renderApprovalBlock 渲染人在回路待批准块（多行三选菜单）。
func renderApprovalBlock(req *agent.ApprovalRequest, cursor int) string {
	var sb strings.Builder

	// 标题行：● 工具名(参数)
	sb.WriteString(approvalStyle.Render(fmt.Sprintf("● %s(%s)", req.Name, req.Args)))
	sb.WriteString("\n")

	// 缩进参数预览
	if req.Args != "" {
		sb.WriteString(approvalGrayStyle.Render(fmt.Sprintf("  参数：%s", req.Args)))
		sb.WriteString("\n")
	}

	// 触发原因
	sb.WriteString(approvalGrayStyle.Render(fmt.Sprintf("  原因：%s", req.Reason)))
	sb.WriteString("\n")

	// 提示
	sb.WriteString("\n")
	sb.WriteString(approvalStyle.Render("  是否继续？"))
	sb.WriteString("\n\n")

	// 三行菜单
	options := []struct {
		label   string
		desc    string
		outcome permission.Outcome
	}{
		{"1. 允许本次", "仅本次放行，不记录规则", permission.OutcomeAllowOnce},
		{"2. 永久允许（写入本地配置）", "记录精确 allow 规则，跨会话生效", permission.OutcomeAllowForever},
		{"3. 拒绝本次", "回灌错误给模型，让其调整策略", permission.OutcomeDenyOnce},
	}

	for i, opt := range options {
		if i == cursor {
			sb.WriteString(approvalHighStyle.Render(fmt.Sprintf(" > %s", opt.label)))
		} else {
			sb.WriteString(approvalGrayStyle.Render(fmt.Sprintf("   %s", opt.label)))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(approvalGrayStyle.Render("  ↑↓ 选择 · 回车确认 · Esc 取消"))

	return sb.String()
}
