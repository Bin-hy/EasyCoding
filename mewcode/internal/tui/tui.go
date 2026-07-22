package tui

import (
	"context"
	_ "embed"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"mewcode/internal/agent"
	"mewcode/internal/config"
	"mewcode/internal/conversation"
	"mewcode/internal/llm"
	"mewcode/internal/prompt"
	"mewcode/internal/tool"
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

// toolDisplay 当前执行中工具的显示信息
type toolDisplay struct {
	name string
	args string
}

// Model 是 bubbletea 顶层模型
type Model struct {
	state    sessionState
	textarea textarea.Model
	spinner  spinner.Model
	list     list.Model
	renderer *glamour.TermRenderer

	providers []config.ProviderConfig
	provider  llm.Provider
	registry  *tool.Registry
	conv      *conversation.Conversation

	// 流式状态
	ctx        context.Context
	cancel     context.CancelFunc // 程序级取消（idle 时 Ctrl+C 退出）
	turnCancel context.CancelFunc // 本轮取消（streaming 时 Esc/Ctrl+C）
	events     <-chan agent.Event
	curReply   strings.Builder
	curTools   []toolDisplay // 替换单个 curTool，支持并发批
	turnStart  time.Time
	mode       agent.Mode // 当前模式（Normal / Plan），跨轮保持
	iter       int        // 当前迭代轮次
	usageIn    int64      // 会话累计输入 token
	usageOut   int64      // 会话累计输出 token

	width  int
	height int

	version string
}

// newRenderer 创建一个零边距的 glamour 渲染器
func newRenderer(width int) *glamour.TermRenderer {
	opts := []glamour.TermRendererOption{
		glamour.WithWordWrap(width),
	}
	if len(styleJSON) > 0 {
		opts = append(opts, glamour.WithStylesFromJSONBytes(styleJSON))
	}
	r, err := glamour.NewTermRenderer(opts...)
	if err != nil {
		r, _ = glamour.NewTermRenderer(
			glamour.WithStandardStyle("light"),
			glamour.WithWordWrap(width),
		)
	}
	return r
}

// New 创建 TUI Model
func New(providers []config.ProviderConfig, version string, registry *tool.Registry) *Model {
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
		registry:  registry,
		conv:      &conversation.Conversation{},
		version:   version,
		width:     80,
		mode:      agent.ModeNormal,
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
		// Ctrl+C：streaming 时取消本轮，否则退出程序
		if msg.String() == "ctrl+c" {
			if m.state == stateStreaming {
				if m.turnCancel != nil {
					m.turnCancel()
				}
				return m, waitForEvent(m.events)
			}
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		}

		// Esc：streaming 时取消本轮
		if msg.Code == tea.KeyEscape {
			if m.state == stateStreaming {
				if m.turnCancel != nil {
					m.turnCancel()
				}
				return m, waitForEvent(m.events)
			}
			return m, nil
		}

		switch m.state {
		case stateSelecting:
			return m.handleSelectingKey(msg)
		case stateIdle:
			return m.handleIdleKey(msg)
		case stateStreaming:
			return m, nil
		}

	case agentEvent:
		return m.handleAgentEvent(msg)

	case spinner.TickMsg:
		return m.handleSpinnerTick(msg)
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

// handleIdleKey 处理空闲状态的键盘输入
func (m *Model) handleIdleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Code {
	case tea.KeyEnter:
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

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

// submitMessage 提交用户消息并通过 agent 发起流式请求。
// 识别 /plan、/do 特殊命令。
func (m *Model) submitMessage(text string) (tea.Model, tea.Cmd) {
	switch {
	case text == "/plan":
		m.mode = agent.ModePlan
		return m, tea.Println(renderNoticeBlock("已进入计划模式（只读工具，产出计划后请用 /do 执行）"))

	case text == "/do":
		m.mode = agent.ModeNormal
		m.conv.AddUser(prompt.ExecuteDirective)
		// fall through to start streaming

	default:
		m.conv.AddUser(text)
	}

	// 启动 per-turn 上下文
	turnCtx, turnCancel := context.WithCancel(context.Background())
	m.turnCancel = turnCancel

	a := agent.New(m.provider, m.registry, m.version)
	m.events = a.Run(turnCtx, m.conv, m.mode)
	m.turnStart = time.Now()
	m.curReply.Reset()
	m.curTools = nil
	m.iter = 0
	m.state = stateStreaming

	userBlock := renderUserBlock(text)
	return m, tea.Batch(
		tea.Println(userBlock),
		waitForEvent(m.events),
		m.spinner.Tick,
	)
}

// handleAgentEvent 处理 agent 事件
func (m *Model) handleAgentEvent(ev agentEvent) (tea.Model, tea.Cmd) {
	switch {
	case ev.Err != nil:
		errorBlock := renderErrorBlock(ev.Err.Error())
		return m.finishTurn(), tea.Println(errorBlock)

	case ev.Tool != nil && ev.Tool.Phase == agent.PhaseStart:
		// 首个工具前先提交 preamble
		if len(m.curTools) == 0 && m.curReply.Len() > 0 {
			preamble := m.curReply.String()
			m.curReply.Reset()
			preambleCmd := tea.Println(renderAssistantBlock(preamble))
			m.curTools = append(m.curTools, toolDisplay{name: ev.Tool.Name, args: ev.Tool.Args})
			return m, tea.Batch(preambleCmd, waitForEvent(m.events))
		}
		m.curTools = append(m.curTools, toolDisplay{name: ev.Tool.Name, args: ev.Tool.Args})
		return m, waitForEvent(m.events)

	case ev.Tool != nil && ev.Tool.Phase == agent.PhaseEnd:
		// FIFO 弹出队首（agent 保证 start/end 都按调用序发出）
		if len(m.curTools) > 0 {
			tool := m.curTools[0]
			m.curTools = m.curTools[1:]
			tl := toolLine(tool.name, tool.args)
			rs := toolResultSummary(ev.Tool.Result, ev.Tool.IsError)
			return m, tea.Batch(
				tea.Println(tl),
				tea.Println(rs),
				waitForEvent(m.events),
			)
		}
		return m, waitForEvent(m.events)

	case ev.Usage != nil:
		m.usageIn += ev.Usage.Input
		m.usageOut += ev.Usage.Output
		return m, waitForEvent(m.events)

	case ev.Iter > 0:
		m.iter = ev.Iter
		return m, waitForEvent(m.events)

	case ev.Notice != "":
		return m, tea.Batch(
			tea.Println(renderNoticeBlock(ev.Notice)),
			waitForEvent(m.events),
		)

	case ev.Text != "":
		m.curReply.WriteString(ev.Text)
		return m, waitForEvent(m.events)

	case ev.Done:
		// 最终答复
		fullReply := m.curReply.String()
		var cmds []tea.Cmd
		if fullReply != "" {
			rendered, err := m.renderer.Render(fullReply)
			if err != nil {
				rendered = fullReply
			}
			cmds = append(cmds, tea.Println(renderAssistantBlock(rendered)))
		}
		return m.finishTurn(), tea.Batch(cmds...)
	}

	return m, nil
}

// finishTurn 清理本轮状态，回空闲态（保留 mode、usage 跨轮）。
func (m *Model) finishTurn() *Model {
	m.curReply.Reset()
	m.curTools = nil
	m.events = nil
	m.iter = 0
	m.turnCancel = nil
	m.state = stateIdle
	return m
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
