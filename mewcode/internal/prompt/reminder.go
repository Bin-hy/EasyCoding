package prompt

// ExecuteDirective /do 注入的用户消息——指示模型按上文已确认的计划开始执行。
const ExecuteDirective = "请按上面的计划开始执行。"

// planReminderFull 规划模式完整提醒——首轮与间隔轮次注入。
const planReminderFull = `你当前处于计划模式（Plan Mode）。你只能使用只读工具（read_file、glob、grep）来调研代码库。
你不能写文件、编辑文件或执行 shell 命令。
请产出一个清晰、分步的执行计划，然后停止，等待用户用 /do 批准后再执行任何实际操作。`

// planReminderConcise 规划模式精简提醒——非首轮、非间隔轮次注入。
const planReminderConcise = `仍在计划模式。继续只读调研，产出计划后等待 /do。`

// SystemReminder 用 <system-reminder> 标签包裹 body，使模型理解这是系统补充上下文而非用户提问。
func SystemReminder(body string) string {
	return "<system-reminder>\n" + body + "\n</system-reminder>"
}

// PlanReminder 返回包好标签的规划模式提醒。
// full=true 返回完整提醒，full=false 返回精简版。
func PlanReminder(full bool) string {
	if full {
		return SystemReminder(planReminderFull)
	}
	return SystemReminder(planReminderConcise)
}
