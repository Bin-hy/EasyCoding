package task

import (
	"context"
	"encoding/json"
	"fmt"

	"mewcode/internal/tool"
)

// TaskListTool 返回当前 Manager 中所有非 Terminated 任务的简要列表（spec F20）。
type TaskListTool struct {
	mgr *Manager
}

// NewTaskListTool 创建 TaskList 工具。
func NewTaskListTool(m *Manager) tool.Tool {
	return &TaskListTool{mgr: m}
}

func (t *TaskListTool) Name() string        { return "TaskList" }
func (t *TaskListTool) ReadOnly() bool      { return true }
func (t *TaskListTool) Description() string { return "返回当前所有后台任务的简要列表（id、name、status、tool_count、last_activity）" }
func (t *TaskListTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

type taskListItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	ToolCount    int    `json:"tool_count"`
	LastActivity string `json:"last_activity"`
}

func (t *TaskListTool) Execute(ctx context.Context, args json.RawMessage) tool.Result {
	tasks := t.mgr.List()
	items := make([]taskListItem, 0, len(tasks))
	for _, bt := range tasks {
		items = append(items, taskListItem{
			ID:           bt.ID,
			Name:         bt.Name,
			Status:       bt.Status.String(),
			ToolCount:    bt.ToolCount,
			LastActivity: bt.LastActivity,
		})
	}
	data, err := json.Marshal(items)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("序列化任务列表失败: %v", err), IsError: true}
	}
	return tool.Result{Content: string(data)}
}

// TaskGetTool 返回指定任务的完整状态（spec F20）。
type TaskGetTool struct {
	mgr *Manager
}

// NewTaskGetTool 创建 TaskGet 工具。
func NewTaskGetTool(m *Manager) tool.Tool {
	return &TaskGetTool{mgr: m}
}

func (t *TaskGetTool) Name() string        { return "TaskGet" }
func (t *TaskGetTool) ReadOnly() bool      { return true }
func (t *TaskGetTool) Description() string { return "返回指定后台任务的完整状态（含 Result/Err）" }
func (t *TaskGetTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "任务 ID",
			},
		},
		"required": []string{"task_id"},
	}
}

func (t *TaskGetTool) Execute(ctx context.Context, args json.RawMessage) tool.Result {
	var params struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return tool.Result{Content: fmt.Sprintf("参数解析失败: %v", err), IsError: true}
	}
	if params.TaskID == "" {
		return tool.Result{Content: "task_id is required", IsError: true}
	}

	bt, ok := t.mgr.Get(params.TaskID)
	if !ok {
		return tool.Result{Content: fmt.Sprintf("未找到任务: %s", params.TaskID), IsError: true}
	}

	data, err := json.Marshal(map[string]any{
		"id":            bt.ID,
		"name":          bt.Name,
		"status":        bt.Status.String(),
		"task":          bt.Task,
		"result":        bt.Result,
		"tool_count":    bt.ToolCount,
		"last_activity": bt.LastActivity,
		"usage": map[string]int64{
			"input":       bt.Usage.Input,
			"output":      bt.Usage.Output,
			"cache_write": bt.Usage.CacheWrite,
			"cache_read":  bt.Usage.CacheRead,
		},
		"start_time": bt.StartTime.Format("2006-01-02 15:04:05"),
		"end_time":   bt.EndTime.Format("2006-01-02 15:04:05"),
	})
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("序列化任务信息失败: %v", err), IsError: true}
	}
	return tool.Result{Content: string(data)}
}

// TaskStopTool 触发任务取消（spec F20）。
type TaskStopTool struct {
	mgr *Manager
}

// NewTaskStopTool 创建 TaskStop 工具。
func NewTaskStopTool(m *Manager) tool.Tool {
	return &TaskStopTool{mgr: m}
}

func (t *TaskStopTool) Name() string        { return "TaskStop" }
func (t *TaskStopTool) ReadOnly() bool      { return false }
func (t *TaskStopTool) Description() string { return "取消指定的后台任务" }
func (t *TaskStopTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "要取消的任务 ID",
			},
		},
		"required": []string{"task_id"},
	}
}

func (t *TaskStopTool) Execute(ctx context.Context, args json.RawMessage) tool.Result {
	var params struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return tool.Result{Content: fmt.Sprintf("参数解析失败: %v", err), IsError: true}
	}
	if params.TaskID == "" {
		return tool.Result{Content: "task_id is required", IsError: true}
	}

	if ok := t.mgr.Stop(params.TaskID); !ok {
		return tool.Result{Content: fmt.Sprintf("未找到任务或无法取消: %s", params.TaskID), IsError: true}
	}
	return tool.Result{Content: `{"status":"cancellation_requested"}`}
}

// SendMessageTool 给后台 Agent 续派任务（spec F20）。
type SendMessageTool struct {
	mgr *Manager
}

// NewSendMessageTool 创建 SendMessage 工具。
func NewSendMessageTool(m *Manager) tool.Tool {
	return &SendMessageTool{mgr: m}
}

func (t *SendMessageTool) Name() string        { return "SendMessage" }
func (t *SendMessageTool) ReadOnly() bool      { return false }
func (t *SendMessageTool) Description() string { return "给一个已完成后台 Agent 续派新任务" }
func (t *SendMessageTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "目标后台 Agent 的名称",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "要发送的新任务消息",
			},
		},
		"required": []string{"name", "message"},
	}
}

func (t *SendMessageTool) Execute(ctx context.Context, args json.RawMessage) tool.Result {
	var params struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return tool.Result{Content: fmt.Sprintf("参数解析失败: %v", err), IsError: true}
	}
	if params.Name == "" || params.Message == "" {
		return tool.Result{Content: "name and message are required", IsError: true}
	}

	id, err := t.mgr.SendMessage(ctx, params.Name, params.Message)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("SendMessage 失败: %v", err), IsError: true}
	}
	return tool.Result{Content: fmt.Sprintf(`{"task_id":"%s","status":"resumed"}`, id)}
}
