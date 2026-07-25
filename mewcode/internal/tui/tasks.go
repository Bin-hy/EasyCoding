package tui

import (
	"fmt"

	"mewcode/internal/task"
)

// consumeTaskDone 消费后台任务完成通知，拼接到 PendingReminders（spec F19）。
func (m *Model) consumeTaskDone() {
	for id := range m.taskMgr.SubscribeDone() {
		bt, ok := m.taskMgr.Get(id)
		if !ok {
			continue
		}
		notif := buildTaskNotification(bt)
		if m.runtime != nil {
			m.runtime.AppendReminders([]string{notif})
		}
	}
}

// buildTaskNotification 构建 <task-notification> 块（spec F19）。
func buildTaskNotification(bt *task.BackgroundTask) string {
	errStr := ""
	if bt.Err != nil {
		errStr = fmt.Sprintf("\nError: %v", bt.Err)
	}
	return fmt.Sprintf(`<task-notification>
Task %s (name="%s"): %s%s
Result: %s
</task-notification>`, bt.ID, bt.Name, bt.Status.String(), errStr, bt.Result)
}
