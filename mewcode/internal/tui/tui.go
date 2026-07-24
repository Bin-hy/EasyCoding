package tui

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"mewcode/internal/agent"
	"mewcode/internal/command"
	"mewcode/internal/config"
	"mewcode/internal/conversation"
	"mewcode/internal/llm"
	"mewcode/internal/memory"
	"mewcode/internal/permission"
	"mewcode/internal/prompt"
	"mewcode/internal/session"
	"mewcode/internal/tool"
)

// sessionState 会话状态
type sessionState int

const (
	stateSelecting sessionState = iota // 多 provider 时的选择界面
	stateIdle                          // 等待用户输入
	stateStreaming                     // 等待/接收模型流
	stateApproving                     // 人在回路待批准
	stateResuming                      // 会话恢复列表选择
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
	engine    *permission.Engine    // 权限引擎
	runtime   *agent.SessionRuntime // 跨 Run 复用的长生命周期状态
	ag        *agent.Agent          // 常驻 Agent 实例

	// ch09: 会话持久化 & 记忆
	writer          *session.Writer // JSONL 会话写入器
	memMgr          *memory.Manager // 记忆更新管理器
	instructionText string          // 项目指令文本
	memoryText      string          // 记忆索引文本
	sessionsDir     string          // 会话存档根目录
	resumeList      list.Model      // 会话恢复列表 UI

	// 流式状态
	cancel     context.CancelFunc // 程序级取消（idle 时 Ctrl+C 退出）
	turnCancel context.CancelFunc // 本轮取消（streaming 时 Esc/Ctrl+C）
	events     <-chan agent.Event
	curReply   strings.Builder
	curTools   []toolDisplay // 替换单个 curTool，支持并发批
	turnStart  time.Time
	mode       permission.Mode // 当前权限模式，跨轮保持
	iter       int             // 当前迭代轮次
	usageIn    int64           // 会话累计输入 token
	usageOut   int64           // 会话累计输出 token

	// 人在回路状态
	pending       *agent.ApprovalRequest // 当前待批准请求
	approveCursor int                    // 待批准菜单光标位置 (0/1/2)

	// 命令系统
	cmdRegistry    *command.Registry // 命令注册中心
	completion     completionMenu    // 斜杠命令补全菜单
	pendingPrintln []string          // handler 的 Println/Error 缓冲
	pendingCmd     tea.Cmd           // handler 请求的异步操作

	cwd    string // 当前工作目录
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

// New 创建 TUI Model。
func New(providers []config.ProviderConfig, version string, registry *tool.Registry, engine *permission.Engine, runtime *agent.SessionRuntime, writer *session.Writer, memMgr *memory.Manager, instructionText, memoryText string) *Model {
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

	startMode := permission.ModeDefault
	if engine != nil {
		startMode = engine.StartMode()
	}

	cwd, _ := os.Getwd()
	sessionsDir := cwd + "/.mewcode/sessions"

	// 构造命令注册中心
	cmdReg := command.New()
	command.RegisterBuiltins(cmdReg)

	m := &Model{
		textarea:        ta,
		spinner:         sp,
		renderer:        newRenderer(80),
		providers:       providers,
		registry:        registry,
		engine:          engine,
		conv:            conversation.New(),
		version:         version,
		width:           80,
		mode:            startMode,
		runtime:         runtime,
		writer:          writer,
		memMgr:          memMgr,
		instructionText: instructionText,
		memoryText:      memoryText,
		sessionsDir:     sessionsDir,
		cmdRegistry:     cmdReg,
		cwd:             cwd,
	}

	if len(providers) == 1 {
		p, err := llm.New(providers[0])
		if err != nil {
			m.provider = nil
		} else {
			m.provider = p
		}
		m.state = stateIdle
		// 构造常驻 Agent（含记忆管理器）
		opts := []agent.Option{agent.WithRuntime(runtime)}
		if memMgr != nil {
			opts = append(opts, agent.WithMemoryManager(memMgr))
		}
		if instructionText != "" {
			opts = append(opts, agent.WithInstructionText(instructionText))
		}
		if memoryText != "" {
			opts = append(opts, agent.WithMemoryText(memoryText))
		}
		m.ag = agent.New(m.provider, m.registry, m.version, m.engine, opts...)
	} else {
		m.state = stateSelecting
		m.initList()
	}

	return m
}

// SetConversation 注入带回调的 Conversation（由 main.go 创建）。
func (m *Model) SetConversation(conv *conversation.Conversation) {
	m.conv = conv
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
		// Ctrl+C：streaming/approving 时取消本轮，否则退出程序
		if msg.String() == "ctrl+c" {
			if m.state == stateStreaming || m.state == stateApproving {
				if m.state == stateApproving && m.pending != nil {
					// 兜底解除 agent 阻塞
					select {
					case m.pending.Respond <- permission.OutcomeDenyOnce:
					default:
					}
				}
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

		// Esc：streaming/approving 时取消本轮
		if msg.Code == tea.KeyEscape {
			if m.state == stateStreaming || m.state == stateApproving {
				if m.state == stateApproving && m.pending != nil {
					// 兜底解除 agent 阻塞
					select {
					case m.pending.Respond <- permission.OutcomeDenyOnce:
					default:
					}
				}
				if m.turnCancel != nil {
					m.turnCancel()
				}
				return m, waitForEvent(m.events)
			}
			return m, nil
		}

		// Shift+Tab：循环切换权限模式（仅 idle 态生效）
		if msg.String() == "shift+tab" && m.state == stateIdle {
			m.mode = nextMode(m.mode)
			notice := fmt.Sprintf("已切换到 %s 模式", modeLabel(m.mode))
			return m, tea.Println(renderNoticeBlock(notice))
		}

		switch m.state {
		case stateSelecting:
			return m.handleSelectingKey(msg)
		case stateIdle:
			return m.handleIdleKey(msg)
		case stateStreaming:
			return m, nil
		case stateApproving:
			return m.updateApproving(msg)
		case stateResuming:
			return m.updateResuming(msg)
		}

	case agentEvent:
		if m.state == stateApproving {
			return m.updateApproving(msg)
		}
		return m.handleAgentEvent(msg)

	case spinner.TickMsg:
		return m.handleSpinnerTick(msg)

	case resumeListMsg, resumeDoneMsg:
		if m.state == stateResuming {
			return m.updateResuming(msg)
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

// nextMode 循环切换权限模式：default → acceptEdits → plan → bypassPermissions → default
func nextMode(m permission.Mode) permission.Mode {
	return (m + 1) % 4
}

// modeLabel 返回模式在状态栏的显示标签。
func modeLabel(m permission.Mode) string {
	switch m {
	case permission.ModeDefault:
		return "DEFAULT"
	case permission.ModeAcceptEdits:
		return "ACCEPT EDITS"
	case permission.ModePlan:
		return "PLAN"
	case permission.ModeBypass:
		return "BYPASS"
	default:
		return "UNKNOWN"
	}
}

// handleAgentEvent 处理 agent 事件
func (m *Model) handleAgentEvent(ev agentEvent) (tea.Model, tea.Cmd) {
	switch {
	case ev.Compact != nil:
		notice := formatCompactNotice(ev.Compact)
		return m, tea.Batch(tea.Println(renderNoticeBlock(notice)), waitForEvent(m.events))

	case ev.Err != nil:
		errorBlock := renderErrorBlock(ev.Err.Error())
		return m.finishTurn(), tea.Println(errorBlock)

	case ev.Approval != nil:
		// 人在回路待批准
		m.pending = ev.Approval
		m.approveCursor = 0
		m.state = stateApproving
		return m, nil // 不 waitForEvent，agent 正阻塞等回传

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

// updateApproving 处理待批准态的按键输入。返回 (Model, Cmd)。
func (m *Model) updateApproving(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "up", "k":
			if m.approveCursor > 0 {
				m.approveCursor--
			}
			return m, nil
		case "down", "j":
			if m.approveCursor < 2 {
				m.approveCursor++
			}
			return m, nil
		case "enter", " ":
			return m.commitApproval(outcomeForIndex(m.approveCursor))
		case "1":
			return m.commitApproval(permission.OutcomeAllowOnce)
		case "2":
			return m.commitApproval(permission.OutcomeAllowForever)
		case "3":
			return m.commitApproval(permission.OutcomeDenyOnce)
		}
	}
	return m, nil
}

// outcomeForIndex 将光标索引映射到 Outcome。
func outcomeForIndex(idx int) permission.Outcome {
	switch idx {
	case 0:
		return permission.OutcomeAllowOnce
	case 1:
		return permission.OutcomeAllowForever
	case 2:
		return permission.OutcomeDenyOnce
	default:
		return permission.OutcomeDenyOnce
	}
}

// commitApproval 向 agent 回传决策，切回 streaming 态。
func (m *Model) commitApproval(outcome permission.Outcome) (tea.Model, tea.Cmd) {
	if m.pending == nil {
		return m, nil
	}
	m.pending.Respond <- outcome
	m.pending = nil
	m.state = stateStreaming
	m.approveCursor = 0
	return m, waitForEvent(m.events)
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

// handleIdleKey 处理空闲状态的键盘输入
func (m *Model) handleIdleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// 先尝试补全菜单键位拦截
	if cmd, consumed := m.handleCompletionKey(msg); consumed {
		return m, cmd
	}

	switch msg.Code {
	case tea.KeyEnter:
		if msg.Mod&tea.ModAlt != 0 {
			var cmd tea.Cmd
			m.textarea, cmd = m.textarea.Update(msg)
			m.syncCompletionFromInput()
			return m, cmd
		}

		text := strings.TrimSpace(m.textarea.Value())
		m.textarea.Reset()
		m.completion.Hide()

		if text == "" {
			return m, nil
		}

		// 命令分发：以 / 开头走命令路径
		if cmd, handled := m.dispatchSlash(text); handled {
			return m, cmd
		}

		return m.submitMessage(text)
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	m.syncCompletionFromInput()
	return m, cmd
}

// submitMessage 提交用户消息并通过 agent 发起流式请求。
func (m *Model) submitMessage(text string) (tea.Model, tea.Cmd) {
	m.conv.AddUser(text)

	// 启动 per-turn 上下文
	turnCtx, turnCancel := context.WithCancel(context.Background())
	m.turnCancel = turnCancel

	// 复用常驻 Agent 实例
	m.events = m.ag.Run(turnCtx, m.conv, m.mode)
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

// Run 启动 TUI

// startStreaming 开始流式处理（用于 /do 等已在 conv 添加 user 消息的场景）。
func (m *Model) startStreaming() tea.Cmd {
	turnCtx, turnCancel := context.WithCancel(context.Background())
	m.turnCancel = turnCancel

	m.events = m.ag.Run(turnCtx, m.conv, m.mode)
	m.turnStart = time.Now()
	m.curReply.Reset()
	m.curTools = nil
	m.iter = 0
	m.state = stateStreaming

	return tea.Batch(
		waitForEvent(m.events),
		m.spinner.Tick,
	)
}
func (m *Model) Run() error {
	p := tea.NewProgram(m)
	_, err := p.Run()
	return err
}
