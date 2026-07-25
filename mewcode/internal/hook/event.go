package hook

// Event 生命周期事件名。
type Event string

// 11 个生命周期事件常量。
const (
	// EventSessionStart mewcode 启动初次进入会话或 /clear 新建会话后、首条 user 消息进入之前。
	EventSessionStart Event = "SessionStart"

	// EventSessionEnd 进程关闭前、/clear 关闭旧会话前、/resume 切换离开旧会话前。
	EventSessionEnd Event = "SessionEnd"

	// EventSessionResume /resume 选中历史会话、恢复完成、首条 user 消息进入之前。
	EventSessionResume Event = "SessionResume"

	// EventUserPromptSubmit TUI 提交一条非 Slash 命令的 user 消息、写入对话历史之前（可拦截）。
	EventUserPromptSubmit Event = "UserPromptSubmit"

	// EventStop Agent.Run 自然停止后、Done: true 事件 emit 之前。
	EventStop Event = "Stop"

	// EventPreUserMessage 每轮 streamOnce 调 provider.Stream 之前。
	EventPreUserMessage Event = "PreUserMessage"

	// EventPreToolUse executeBatched 对每条 tool call 准备执行之前、权限引擎 Check 之前（可拦截）。
	EventPreToolUse Event = "PreToolUse"

	// EventPostToolUse 单条 tool call 拿到 result 之后、emit PhaseEnd 之前。
	EventPostToolUse Event = "PostToolUse"

	// EventPreCompact compact.ManageContext 调用之前。
	EventPreCompact Event = "PreCompact"

	// EventPostCompact compact.ManageContext 返回后。
	EventPostCompact Event = "PostCompact"

	// EventNotification 权限 Ask 弹出审批时、Stream 返回 Err 时。
	EventNotification Event = "Notification"
)

// allEvents 所有有效事件列表，供枚举校验使用。
var allEvents = []Event{
	EventSessionStart,
	EventSessionEnd,
	EventSessionResume,
	EventUserPromptSubmit,
	EventStop,
	EventPreUserMessage,
	EventPreToolUse,
	EventPostToolUse,
	EventPreCompact,
	EventPostCompact,
	EventNotification,
}

// blockingEvents 可拦截事件集合——拦截类事件下 sync hook 可通过约定方式阻止主流程继续。
var blockingEvents = map[Event]bool{
	EventPreToolUse:       true,
	EventUserPromptSubmit: true,
}

// IsBlocking 判断事件是否为拦截类事件。拦截类事件不允许 async。
func IsBlocking(e Event) bool {
	return blockingEvents[e]
}

// ParseEvent 将字符串解析为 Event 枚举。未知事件返回 ("", false)。
func ParseEvent(s string) (Event, bool) {
	// 构建查找表（一次性）
	lookup := make(map[string]Event, len(allEvents))
	for _, ev := range allEvents {
		lookup[string(ev)] = ev
	}
	e, ok := lookup[s]
	return e, ok
}
