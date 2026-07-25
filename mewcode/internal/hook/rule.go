package hook

import (
	"time"

	"mewcode/internal/permission"
)

// ─── 核心数据结构 ────────────────────────────────────────

// Rule 一条 Hook 规则：事件 + 条件 + 动作。
type Rule struct {
	Name     string        // 必填：用于日志、only_once 跟踪、冲突检测
	Event    Event         // 必填：触发事件
	If       *Condition    // 可选：nil 表示无条件触发
	Action   Action        // 必填：动作定义
	OnlyOnce bool          // 可选：会话内只跑一次
	Async    bool          // 可选：是否后台异步执行
	Timeout  time.Duration // 可选：命令/HTTP 最大执行时长，0 用默认 30s

	source string // 来源文件路径，供 /hooks 显示
}

// ─── 条件表达式 ──────────────────────────────────────────

// CombineMode 条件组合模式：全部满足 或 任一满足，二选一不混用。
type CombineMode int

const (
	CombineAllOf CombineMode = iota
	CombineAnyOf
)

// Condition 条件表达式。顶层只能设 all_of 或 any_of 之一。
// nil 表示无条件触发。
type Condition struct {
	Mode  CombineMode
	Atoms []AtomCondition
}

// AtomCondition 单条原子条件：字段路径 + 匹配器。
type AtomCondition struct {
	Field   string             // 形如 "tool_input.path"，用 . 分隔嵌套
	Matcher permission.Matcher // 编译后的匹配器（复用 permission 包的四种实现）
}

// ─── 动作类型 ────────────────────────────────────────────

// ActionType 动作类型枚举。
type ActionType string

const (
	ActionShell    ActionType = "shell"
	ActionPrompt   ActionType = "prompt"
	ActionHTTP     ActionType = "http"
	ActionSubagent ActionType = "subagent"
)

// Action 动作定义：类型 + 各类型独有字段。
type Action struct {
	Type     ActionType
	Shell    *ShellAction
	Prompt   *PromptAction
	HTTP     *HTTPAction
	Subagent *SubagentAction
}

// ShellAction shell 命令动作。
// Command 由 sh -c 解释执行；事件 payload 序列化为单行 JSON 经 stdin 传入。
type ShellAction struct {
	Command string
}

// PromptAction 提示词注入动作。
// Text 在下一轮 LLM 请求的 reminder 区注入。
type PromptAction struct {
	Text string
}

// HTTPAction HTTP 请求动作。
// Body 为空时序列化 payload 为 JSON 作为请求体；非空时作为 Go text/template 模板渲染。
type HTTPAction struct {
	URL     string
	Method  string            // 默认 POST
	Headers map[string]string
	Body    string            // 模板字符串，支持 {{.field}} 取 payload 字段
}

// SubagentAction 子 Agent 动作（本期占位）。
type SubagentAction struct {
	AgentName string
	Prompt    string
}

// ─── 事件载荷 ────────────────────────────────────────────

// Payload 事件分派时携带的上下文数据。
// 条件求值与动作输入都使用它。序列化为 JSON 时保证 key 字典序（Go json.Marshal 默认行为）。
type Payload map[string]any
