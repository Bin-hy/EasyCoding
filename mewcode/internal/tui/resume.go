package tui

import (
	"context"
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/list"

	"mewcode/internal/compact"
	"mewcode/internal/conversation"
	"mewcode/internal/llm"
	"mewcode/internal/session"
)

// sessionItem 实现 list.DefaultItem 接口。
type sessionItem struct {
	info session.SessionInfo
}

func (s sessionItem) Title() string       { return s.info.Title }
func (s sessionItem) Description() string { return s.info.Description() }
func (s sessionItem) FilterValue() string { return s.info.Title }

// beginResume 启动会话列表加载。
func (m *Model) beginResume() tea.Cmd {
	return func() tea.Msg {
		infos, err := session.ListSessions(m.sessionsDir)
		if err != nil {
			return resumeListMsg{err: err}
		}
		return resumeListMsg{infos: infos}
	}
}

// resumeListMsg 会话列表加载完成。
type resumeListMsg struct {
	infos []session.SessionInfo
	err   error
}

// doResumeSession 执行会话恢复流程。
func (m *Model) doResumeSession(info session.SessionInfo) tea.Cmd {
	return func() tea.Msg {
		// 加载消息
		msgs, err := session.LoadSession(info.Dir)
		if err != nil {
			return resumeDoneMsg{err: fmt.Errorf("加载会话失败: %w", err)}
		}

		// 估算 token 并压缩（超阈值时）
		if m.ag != nil && m.runtime != nil {
			est := estimateTokens(msgs)
			cw := m.runtime.ContextWindow
			if est > int64(cw-8000) {
				tempConv := conversation.NewFromMessages(msgs, nil, nil)
				defs := m.registry.Definitions()
				_, _, cErr := m.ag.RunForceCompact(context.Background(), tempConv, defs)
				if cErr == nil {
					msgs = tempConv.Messages()
				}
			}
		}

		// 检查时间跨度（超过 6 小时追加提醒）
		if elapsed := time.Since(info.ModifiedAt); elapsed > 6*time.Hour {
			reminder := fmt.Sprintf("[系统提示] 本会话已暂停 %s。部分上下文可能已过时，如需最新信息请重新读取相关文件。", formatDuration(elapsed))
			msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: reminder})
		}

		// 构造 Conversation（带回调，恢复后的消息追加到原 JSONL）
		modelName := ""
		if m.provider != nil {
			modelName = m.provider.Model()
		}
		onAppend := m.writer.OnAppend(modelName)
		newConv := conversation.NewFromMessages(msgs, onAppend, m.writer.OnReplace())

		// 重建 SessionContext
		root, _ := os.Getwd()
		newCtx, err := compact.OpenSessionContext(root, info.ID)
		if err != nil {
			return resumeDoneMsg{err: fmt.Errorf("打开会话目录失败: %w", err)}
		}

		// 重新打开 Writer（追加模式）
		newWriter, err := session.OpenWriter(info.Dir)
		if err != nil {
			return resumeDoneMsg{err: fmt.Errorf("打开 JSONL 失败: %w", err)}
		}

		return resumeDoneMsg{
			conv:       newConv,
			writer:     newWriter,
			sessionCtx: newCtx,
			sessionID:  info.ID,
			msgCount:   len(msgs),
		}
	}
}

// resumeDoneMsg 会话恢复完成。
type resumeDoneMsg struct {
	conv       *conversation.Conversation
	writer     *session.Writer
	sessionCtx *compact.SessionContext
	sessionID  string
	msgCount   int
	err        error
}

// updateResuming 处理 stateResuming 下的消息。
func (m *Model) updateResuming(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case resumeListMsg:
		if msg.err != nil {
			return m, tea.Println(renderNoticeBlock(fmt.Sprintf("加载会话列表失败: %v", msg.err)))
		}
		if len(msg.infos) == 0 {
			m.state = stateIdle
			return m, tea.Println(renderNoticeBlock("无可用历史会话"))
		}
		// 构建列表
		items := make([]list.Item, len(msg.infos))
		for i, info := range msg.infos {
			items[i] = sessionItem{info: info}
		}
		dlg := list.NewDefaultDelegate()
		m.resumeList = list.New(items, dlg, m.width, m.height-4)
		m.resumeList.Title = "选择要恢复的会话 (↑↓ 导航 / 输入搜索 / Enter 选择 / Esc 取消)"
		m.resumeList.SetShowStatusBar(false)
		m.resumeList.SetFilteringEnabled(true)
		return m, nil

	case resumeDoneMsg:
		if msg.err != nil {
			m.state = stateIdle
			return m, tea.Println(renderNoticeBlock(fmt.Sprintf("恢复失败: %v", msg.err)))
		}
		// 替换当前会话状态
		m.conv = msg.conv
		m.writer = msg.writer
		m.runtime.Session = msg.sessionCtx
		m.state = stateIdle
		notice := fmt.Sprintf("已恢复会话 %s，共 %d 条消息", msg.sessionID, msg.msgCount)
		return m, tea.Println(renderNoticeBlock(notice))

	case tea.KeyPressMsg:
		switch msg.Code {
		case tea.KeyEnter:
			if item, ok := m.resumeList.SelectedItem().(sessionItem); ok {
				m.state = stateIdle // 先退出列表态
				return m, m.doResumeSession(item.info)
			}
		case tea.KeyEsc:
			m.state = stateIdle
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.resumeList, cmd = m.resumeList.Update(msg)
	return m, cmd
}

// viewResuming 渲染 stateResuming 下的视图。
func (m *Model) viewResuming() string {
	return m.resumeList.View()
}

// estimateTokens 粗略估算消息 token 数。
func estimateTokens(msgs []llm.Message) int64 {
	var chars int
	for _, msg := range msgs {
		chars += len(msg.Content)
		for _, tc := range msg.ToolCalls {
			chars += len(tc.Name) + len(string(tc.Input))
		}
		for _, tr := range msg.ToolResults {
			chars += len(tr.Content)
		}
	}
	return int64(float64(chars) * 0.25)
}

// relativeTime 返回友好的相对时间。
func relativeTime(t time.Time) string {
	elapsed := time.Since(t)
	switch {
	case elapsed < time.Minute:
		return "just now"
	case elapsed < time.Hour:
		m := int(elapsed.Minutes())
		if m == 0 {
			m = 1
		}
		return fmt.Sprintf("%d min ago", m)
	case elapsed < 24*time.Hour:
		h := int(elapsed.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	default:
		d := int(elapsed.Hours() / 24)
		if d == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", d)
	}
}

// formatDuration 格式化时间段。
func formatDuration(d time.Duration) string {
	hours := int(d.Hours())
	if hours < 24 {
		return fmt.Sprintf("%d小时", hours)
	}
	return fmt.Sprintf("%d天", hours/24)
}

// formatSize 格式化文件大小。
func formatSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%dB", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(size)/(1024*1024))
}
