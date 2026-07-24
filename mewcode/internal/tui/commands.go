package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"mewcode/internal/agent"
	"mewcode/internal/command"
	"mewcode/internal/compact"
	"mewcode/internal/conversation"
	"mewcode/internal/llm"
	"mewcode/internal/permission"
	"mewcode/internal/session"
)

// ============================================================================
// UI 接口实现 — *Model 实现 command.UI
// ============================================================================

// 只读查询方法

func (m *Model) Mode() permission.Mode { return m.mode }
func (m *Model) UsageIn() int64        { return m.usageIn }
func (m *Model) UsageOut() int64       { return m.usageOut }
func (m *Model) Idle() bool            { return m.state == stateIdle }

func (m *Model) ModelName() string {
	if m.provider != nil {
		return m.provider.Model()
	}
	return ""
}

func (m *Model) Cwd() string {
	if m.cwd != "" {
		return m.cwd
	}
	return "."
}

func (m *Model) ToolCount() int {
	return m.registry.Count()
}

func (m *Model) MemoryFiles() []string {
	if m.memMgr == nil {
		return nil
	}
	project, user := m.memMgr.ListFiles()
	var all []string
	all = append(all, project...)
	all = append(all, user...)
	return all
}

func (m *Model) SessionPath() string {
	if m.writer != nil {
		return m.writer.Path()
	}
	return ""
}

func (m *Model) SessionID() string {
	if m.runtime != nil && m.runtime.Session != nil {
		return m.runtime.Session.SessionID
	}
	return ""
}

// 写入方法

func (m *Model) Println(msg string) {
	m.pendingPrintln = append(m.pendingPrintln, msg)
}

func (m *Model) Error(msg string) {
	m.pendingPrintln = append(m.pendingPrintln, "ERROR\x00"+msg)
}

func (m *Model) SetMode(mode permission.Mode) {
	m.mode = mode
}

func (m *Model) Quit() {
	if m.cancel != nil {
		m.cancel()
	}
	m.pendingCmd = tea.Quit
}

func (m *Model) ForceCompact() {
	if m.ag == nil {
		m.Error("压缩失败：Agent 未初始化")
		return
	}
	var defs []llm.ToolDefinition
	if m.mode == permission.ModePlan {
		defs = m.registry.ReadOnlyDefinitions()
	} else {
		defs = m.registry.Definitions()
	}
	before, after, err := m.ag.RunForceCompact(context.Background(), m.conv, defs)
	if err != nil {
		m.Error(fmt.Sprintf("压缩失败：%v", err))
	} else {
		m.Println(fmt.Sprintf("已压缩，token 从 %d 降至 %d", before, after))
	}
}

func (m *Model) OpenResumeMenu() {
	m.state = stateResuming
	m.textarea.Reset()
	m.pendingCmd = m.beginResume()
}

func (m *Model) ClearAndNewSession() {
	// a. 关闭旧 writer
	if m.writer != nil {
		_ = m.writer.Close()
	}

	// b. 创建新 SessionContext
	newSesCtx, err := compact.NewSessionContext(m.cwd)
	if err != nil {
		m.Error(fmt.Sprintf("创建新会话失败: %v", err))
		return
	}

	// c. 创建新 writer
	newWriter, err := session.NewWriter(newSesCtx.SessionDir)
	if err != nil {
		m.Error(fmt.Sprintf("创建会话文件失败: %v", err))
		return
	}
	m.writer = newWriter

	// d. 重建 conversation
	m.bindConversation(newWriter)

	// e. 重置 runtime
	if m.runtime != nil {
		m.runtime.ResetForNewSession(newSesCtx)
	}

	// f. 归零计数
	m.iter = 0
	m.usageIn = 0
	m.usageOut = 0
}

func (m *Model) InjectAndSend(label, preset string) {
	m.conv.AddUser(preset)
	m.pendingCmd = tea.Batch(
		tea.Println(renderUserBlock(label)),
		m.startStreaming(),
	)
}

// bindConversation 构造带 callback 的 Conversation。
// New() 和 ClearAndNewSession 共用此方法。
func (m *Model) bindConversation(writer *session.Writer) {
	modelName := ""
	if m.provider != nil {
		modelName = m.provider.Model()
	}
	onAppend := writer.OnAppend(modelName)
	onReplace := writer.OnReplace()
	m.conv = conversation.NewFromMessages(nil, onAppend, onReplace)
}

// ============================================================================
// 命令分发
// ============================================================================

// dispatchSlash 解析并分发斜杠命令。
// 返回 (tea.Cmd, true) 表示已处理为命令，(nil, false) 表示非命令，需送给 LLM。
func (m *Model) dispatchSlash(text string) (tea.Cmd, bool) {
	name, isSlash := command.Parse(text)
	if !isSlash {
		return nil, false
	}

	// 清空上一轮的 pending 缓冲
	m.pendingPrintln = nil
	m.pendingCmd = nil

	cmd, ok := m.cmdRegistry.Lookup(name)
	if !ok {
		// 未命中：输出引导提示
		if name == "" {
			m.Println("未知命令。输入 /help 查看可用命令")
		} else {
			m.Println(fmt.Sprintf("未知命令: /%s。输入 /help 查看可用命令", name))
		}
		return m.flushPending(), true
	}

	// Idle 守护：KindUI / KindPrompt 在非 idle 状态拒绝
	if (cmd.Kind == command.KindUI || cmd.Kind == command.KindPrompt) && !m.Idle() {
		m.Error("请等待当前任务完成")
		return m.flushPending(), true
	}

	// 执行 handler
	if err := cmd.Handler(context.Background(), m); err != nil {
		m.Error(err.Error())
	}

	return m.flushPending(), true
}

// flushPending 将 pendingPrintln 和 pendingCmd 合并为一个 tea.Cmd。
func (m *Model) flushPending() tea.Cmd {
	var cmds []tea.Cmd

	// 渲染 pendingPrintln
	for _, msg := range m.pendingPrintln {
		if len(msg) >= 6 && msg[:6] == "ERROR\x00" {
			cmds = append(cmds, tea.Println(renderErrorBlock(msg[6:])))
		} else {
			cmds = append(cmds, tea.Println(renderNoticeBlock(msg)))
		}
	}
	m.pendingPrintln = nil

	// 追加 pendingCmd
	if m.pendingCmd != nil {
		cmds = append(cmds, m.pendingCmd)
		m.pendingCmd = nil
	}

	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// formatCompactNotice 将 CompactEvent 格式化为 TUI 展示文案。
func formatCompactNotice(ev *agent.CompactEvent) string {
	switch ev.Phase {
	case agent.CompactPhaseBeforeAuto:
		return "正在压缩上下文..."
	case agent.CompactPhaseBeforeEmergency:
		return "上下文撞墙，自动压缩中..."
	case agent.CompactPhaseAfterAuto, agent.CompactPhaseAfterEmergency:
		if ev.Err != nil {
			return fmt.Sprintf("压缩失败：%v", ev.Err)
		}
		return fmt.Sprintf("已压缩，token 从 %d 降至 %d", ev.Before, ev.After)
	default:
		return ""
	}
}
