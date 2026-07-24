package command

import "mewcode/internal/permission"

// UI 是命令 handler 操作 TUI 的抽象接口。
// *tui.Model 实现此接口，handler 不直接持有 TUI 类型。
type UI interface {
	// 输出
	Println(msg string)
	Error(msg string)

	// 模式
	Mode() permission.Mode
	SetMode(m permission.Mode)

	// 对话注入（KindPrompt 命令使用）
	// displayLabel 在 scrollback 中显示，presetPrompt 是实际写入 conversation/JSONL 的文本
	InjectAndSend(displayLabel, presetPrompt string)

	// 只读查询
	UsageIn() int64
	UsageOut() int64
	ModelName() string
	Cwd() string
	ToolCount() int
	MemoryFiles() []string
	SessionPath() string
	SessionID() string

	// 影响界面动作
	Quit()
	ForceCompact()
	OpenResumeMenu()
	ClearAndNewSession()

	// 状态机查询
	Idle() bool

	// Skill 相关
	AppendAssistantMessage(text string) // fork 模式：将子 Agent 结果写入主对话历史
	ClearActiveSkills()                 // /clear 时清空已激活 Skill
}

// nopUI 测试桩：吞掉所有写入调用，查询返回零值。
type nopUI struct{}

// NopUI 返回一个不执行任何操作的 UI 实现，供单元测试使用。
func NopUI() UI {
	return &nopUI{}
}

func (n *nopUI) Println(msg string)                              {}
func (n *nopUI) Error(msg string)                                {}
func (n *nopUI) Mode() permission.Mode                           { return permission.ModeDefault }
func (n *nopUI) SetMode(m permission.Mode)                       {}
func (n *nopUI) InjectAndSend(displayLabel, presetPrompt string) {}
func (n *nopUI) UsageIn() int64                                  { return 0 }
func (n *nopUI) UsageOut() int64                                 { return 0 }
func (n *nopUI) ModelName() string                               { return "" }
func (n *nopUI) Cwd() string                                     { return "" }
func (n *nopUI) ToolCount() int                                  { return 0 }
func (n *nopUI) MemoryFiles() []string                           { return nil }
func (n *nopUI) SessionPath() string                             { return "" }
func (n *nopUI) SessionID() string                               { return "" }
func (n *nopUI) Quit()                                           {}
func (n *nopUI) ForceCompact()                                   {}
func (n *nopUI) OpenResumeMenu()                                 {}
func (n *nopUI) ClearAndNewSession()                             {}
func (n *nopUI) Idle() bool                                      { return true }
func (n *nopUI) AppendAssistantMessage(text string)              {}
func (n *nopUI) ClearActiveSkills()                               {}
