package tui

import (
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/list"
	"charm.land/lipgloss/v2"
	"mewcode/internal/config"
	"mewcode/internal/llm"
)

// providerItem 实现 list.Item 接口
type providerItem struct {
	cfg config.ProviderConfig
}

func (i providerItem) Title() string {
	return fmt.Sprintf("%s  (%s)", i.cfg.Name, i.cfg.Model)
}

func (i providerItem) Description() string {
	return fmt.Sprintf("协议: %s", i.cfg.Protocol)
}

func (i providerItem) FilterValue() string {
	return i.cfg.Name
}

type providerDelegate struct{}

func (d providerDelegate) Height() int                               { return 1 }
func (d providerDelegate) Spacing() int                              { return 0 }
func (d providerDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

func (d providerDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	pItem, ok := item.(providerItem)
	if !ok {
		return
	}

	title := pItem.Title()
	if index == m.Index() {
		selectedStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#5555FF")).
			Padding(0, 1)
		fmt.Fprint(w, selectedStyle.Render(title))
	} else {
		normalStyle := lipgloss.NewStyle().Padding(0, 1)
		fmt.Fprint(w, normalStyle.Render(title))
	}
}

func (m *Model) viewSelecting() string {
	var b strings.Builder

	b.WriteString("选择 LLM Provider:\n\n")
	b.WriteString(m.list.View())
	b.WriteString("\n")
	b.WriteString("方向键选择，Enter 确认，Ctrl+C 退出")

	return b.String()
}

func (m *Model) initList() {
	items := make([]list.Item, len(m.providers))
	for i, p := range m.providers {
		items[i] = providerItem{cfg: p}
	}

	delegate := providerDelegate{}
	width := m.width
	if width < 20 {
		width = 80
	}

	m.list = list.New(items, delegate, width, 10)
	m.list.SetShowTitle(false)
	m.list.SetShowStatusBar(false)
	m.list.SetFilteringEnabled(false)
	m.list.SetShowHelp(false)
	m.list.SetShowPagination(false)
}

func (m *Model) handleSelectingKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Code {
	case tea.KeyUp, tea.KeyDown:
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd

	case tea.KeyEnter:
		idx := m.list.Index()
		if idx < 0 || idx >= len(m.providers) {
			return m, nil
		}
		return m.selectProvider(idx)
	}

	return m, nil
}

func (m *Model) selectProvider(idx int) (tea.Model, tea.Cmd) {
	cfg := m.providers[idx]
	p, err := llm.New(cfg)
	if err != nil {
		return m, tea.Println(renderErrorBlock(fmt.Sprintf("无法初始化 provider: %v", err)))
	}

	m.provider = p
	m.state = stateIdle

	return m, tea.Batch(
		tea.Println("已选择: "+p.Name()+" ("+p.Model()+")"),
		m.textarea.Focus(),
	)
}
