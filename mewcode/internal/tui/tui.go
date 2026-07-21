package tui

import (
	"context"
	_ "embed"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/glamour/v2"
	"mewcode/internal/config"
	"mewcode/internal/conversation"
	"mewcode/internal/llm"
	"mewcode/internal/prompt"
)

// sessionState 会话状态
type sessionState int

const (
	stateSelecting sessionState = iota // 多 provider 时的选择界面
	stateIdle                          // 等待用户输入
	stateStreaming                     // 等待/接收模型流
)

// embed 自定义 glamour 样式：基于 light 主题，但 document margin 为 0
//
//go:embed style.json
var styleJSON []byte

// Model 是 bubbletea 顶层模型
type Model struct {
	state    sessionState
	textarea textarea.Model
	spinner  spinner.Model
	list     list.Model
	renderer *glamour.TermRenderer

	providers []config.ProviderConfig
	provider  llm.Provider
	conv      *conversation.Conversation

	// 流式状态
	ctx       context.Context
	cancel    context.CancelFunc
	events    <-chan llm.StreamEvent
	curReply  strings.Builder
	turnStart time.Time

	width  int
	height int

	version string
}

// newRenderer 创建一个零边距的 glamour 渲染器
func newRenderer(width int) *glamour.TermRenderer {
	opts := []glamour.TermRendererOption{
		glamour.WithWordWrap(width),
	}
	// 用自定义零边距样式（如果 embed 成功），否则回退 light
	if len(styleJSON) > 0 {
		opts = append(opts, glamour.WithStylesFromJSONBytes(styleJSON))
	}
	r, err := glamour.NewTermRenderer(opts...)
	if err != nil {
		// 回退
		r, _ = glamour.NewTermRenderer(
			glamour.WithStandardStyle("light"),
			glamour.WithWordWrap(width),
		)
	}
	return r
}

// New 创建 TUI Model
func New(providers []config.ProviderConfig, version string) *Model {
	if len(providers) == 0 {
		providers = []config.ProviderConfig{{Name: "default", Protocol: "anthropic", Model: "unknown"}}
	}

	ta := textarea.New()
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.Prompt = "❯ "
	ta.Placeholder = "Send a message..."
	ta.ShowLineNumbers = false

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	m := &Model{
		textarea:  ta,
		spinner:   sp,
		renderer:  newRenderer(80),
		providers: providers,
		conv:      &conversation.Conversation{},
		version:   version,
		width:     80,
	}

	if len(providers) == 1 {
		p, err := llm.New(providers[0])
		if err != nil {
			m.provider = nil
		} else {
			m.provider = p
		}
		m.state = stateIdle
	} else {
		m.state = stateSelecting
		m.initList()
	}

	return m
}

func (m *Model) Init() tea.Cmd {
	banner := prompt.RenderBanner(m.version, ".")
	if m.state == stateSelecting {
		return tea.Println(banner)
	}
	return tea.Batch(
		tea.Println(banner),
		m.textarea.Focus(),
	)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.textarea.SetWidth(msg.Width - 4)
		if msg.Width > 20 {
			m.renderer = newRenderer(msg.Width - 4)
		}
		return m, nil

	case tea.KeyPressMsg:
		// Ctrl+C 全局退出
		if msg.String() == "ctrl+c" {
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		}

		switch m.state {
		case stateSelecting:
			return m.handleSelectingKey(msg)
		case stateIdle:
			return m.handleIdleKey(msg)
		case stateStreaming:
			return m, nil
		}

	case streamMsg:
		return m.handleStreamEvent(msg)

	case spinner.TickMsg:
		return m.handleSpinnerTick(msg)
	}

	// 默认更新 textarea
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

// handleIdleKey 处理空闲状态的键盘输入
func (m *Model) handleIdleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Code {
	case tea.KeyEnter:
		// Alt+Enter → textarea 内部处理换行
		if msg.Mod&tea.ModAlt != 0 {
			var cmd tea.Cmd
			m.textarea, cmd = m.textarea.Update(msg)
			return m, cmd
		}

		text := strings.TrimSpace(m.textarea.Value())
		m.textarea.Reset()

		if text == "" {
			return m, nil
		}

		if text == "/exit" {
			return m, tea.Quit
		}

		return m.submitMessage(text)
	}

	// 其他键转发给 textarea
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

// submitMessage 提交用户消息并发起流式请求
func (m *Model) submitMessage(text string) (tea.Model, tea.Cmd) {
	m.conv.AddUser(text)

	m.ctx, m.cancel = context.WithCancel(context.Background())

	m.events = m.provider.Stream(m.ctx, m.conv.Messages())
	m.turnStart = time.Now()
	m.curReply.Reset()
	m.state = stateStreaming

	userBlock := renderUserBlock(text)
	return m, tea.Batch(
		tea.Println(userBlock),
		waitForEvent(m.events),
		m.spinner.Tick,
	)
}

// handleStreamEvent 处理流式事件
func (m *Model) handleStreamEvent(ev streamMsg) (tea.Model, tea.Cmd) {
	switch {
	case ev.Text != "":
		m.curReply.WriteString(ev.Text)
		return m, waitForEvent(m.events)

	case ev.Err != nil:
		errorBlock := renderErrorBlock(ev.Err.Error())
		m.state = stateIdle
		m.cancel = nil
		return m, tea.Println(errorBlock)

	case ev.Done:
		fullReply := m.curReply.String()
		m.conv.AddAssistant(fullReply)

		rendered, err := m.renderer.Render(fullReply)
		if err != nil {
			rendered = fullReply
		}
		replyBlock := renderAssistantBlock(rendered)
		m.state = stateIdle
		m.cancel = nil
		return m, tea.Println(replyBlock)
	}

	return m, nil
}

// handleSpinnerTick 处理 spinner 计时
func (m *Model) handleSpinnerTick(msg spinner.TickMsg) (tea.Model, tea.Cmd) {
	if m.state != stateStreaming {
		return m, nil
	}
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return m, cmd
}

// Run 启动 TUI
func (m *Model) Run() error {
	p := tea.NewProgram(m)
	_, err := p.Run()
	return err
}
