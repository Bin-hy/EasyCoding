package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"mewcode/internal/conversation"
	"mewcode/internal/llm"
	"mewcode/internal/subagent"
	"mewcode/internal/tool"
)

// autoBackgroundDuration 前台子 Agent 超时自动切后台的阈值（spec F17.2）。
// 可被测试修改。
var autoBackgroundDuration = 120 * time.Second

// AgentCatalog 是 subagent.Catalog 的接口抽象，避免 agent 包直接依赖 subagent（用于测试 mock）。
// subagent.Catalog 直接实现此接口。
type AgentCatalog interface {
	Resolve(name string) (*subagent.Definition, bool)
	ForkDefinition() *subagent.Definition
	List() []*subagent.Definition
}

// AgentArgs Agent 工具的 JSON Schema 参数映射（spec F1）。
type AgentArgs struct {
	Prompt          string `json:"prompt"`           // 必填：交给子 Agent 的任务指令
	Description     string `json:"description"`      // 必填：一句话描述，供 UI 展示
	SubagentType    string `json:"subagent_type"`    // 可选：预定义角色名，留空走 Fork
	Model           string `json:"model"`            // 可选：haiku/sonnet/opus/inherit
	RunInBackground bool   `json:"run_in_background"` // 可选：强制后台启动
	Name            string `json:"name"`             // 可选：命名子 Agent
}

// TaskManager 是 task.Manager 的接口抽象，避免 agent 包直接依赖 task。
type TaskManager interface {
	Launch(ctx context.Context, ag *Agent, conv *conversation.Conversation, name, task string) string
	// AdoptRunning 把正在前台跑的 agent 移交到后台。partial 可以是 *task.PartialState 或 *agent.PartialState。
	AdoptRunning(ctx context.Context, ag *Agent, conv *conversation.Conversation, name string, ev <-chan Event, cancel context.CancelFunc) string
}

// AgentTool 是注册到 tool.Registry 的统一 Agent 工具（spec F1-F3）。
type AgentTool struct {
	catalog        AgentCatalog
	taskMgr        TaskManager
	parent         *Agent
	bgEnabled      bool // N6 配置开关
	parentConvFn   func() []llm.Message // 获取父对话消息（Fork 路径用）
}

// NewAgentTool 构造 Agent 工具。
// parent 可为 nil（后续通过 SetParent 回填）。
func NewAgentTool(catalog AgentCatalog, taskMgr TaskManager, parent *Agent, bgEnabled bool) *AgentTool {
	return &AgentTool{
		catalog:   catalog,
		taskMgr:   taskMgr,
		parent:    parent,
		bgEnabled: bgEnabled,
	}
}

// SetParent 回填父 Agent 引用（main.go 在 tui.New 之后调用）。
func (t *AgentTool) SetParent(a *Agent) {
	t.parent = a
}

// SetParentConvFn 设置获取父对话消息的回调（TUI 层注入）。
func (t *AgentTool) SetParentConvFn(fn func() []llm.Message) {
	t.parentConvFn = fn
}

// Name 返回 "Agent"（spec F1）。
func (t *AgentTool) Name() string { return "Agent" }

// Description 返回给模型的工具说明，动态列出已知 subagent_type（spec F1）。
func (t *AgentTool) Description() string {
	if t.catalog == nil {
		return "将子任务委派给独立的子 Agent。"
	}
	names := make([]string, 0)
	for _, d := range t.catalog.List() {
		names = append(names, d.Name)
	}
	prefix := "将子任务委派给独立的子 Agent。"
	if len(names) > 0 {
		prefix += " 可用的 subagent_type: " + strings.Join(names, ", ") + "。"
	}
	prefix += " 不传 subagent_type 则为 Fork 模式（继承父对话历史）。后台任务通过 run_in_background=true 启用。"
	return prefix
}

// Parameters 返回 Agent 工具的 JSON Schema（spec F1）。
func (t *AgentTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt": map[string]any{
				"type":        "string",
				"description": "交给子 Agent 的任务指令",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "一句话描述任务，供 UI 展示",
			},
			"subagent_type": map[string]any{
				"type":        "string",
				"description": "指定预定义角色名（如 Explore/Plan/general-purpose），留空走 Fork 路径",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "模型覆盖：haiku/sonnet/opus/inherit。留空沿用 Agent 定义",
				"enum":        []string{"haiku", "sonnet", "opus", "inherit"},
			},
			"run_in_background": map[string]any{
				"type":        "boolean",
				"description": "强制后台启动。Fork 路径忽略此字段（无条件后台）",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "给本次启动的子 Agent 命名，供 SendMessage 用",
			},
		},
		"required": []string{"prompt", "description"},
	}
}

// ReadOnly 返回 false——子 Agent 可能做任何事。
func (t *AgentTool) ReadOnly() bool { return false }

// Execute 执行 Agent 工具的主流程（spec F2）。
func (t *AgentTool) Execute(ctx context.Context, args json.RawMessage) tool.Result {
	var aArgs AgentArgs
	if err := json.Unmarshal(args, &aArgs); err != nil {
		return tool.Result{Content: fmt.Sprintf("Agent 工具参数解析失败: %v", err), IsError: true}
	}

	if aArgs.Prompt == "" {
		return tool.Result{Content: "prompt is required", IsError: true}
	}
	if aArgs.Description == "" {
		return tool.Result{Content: "description is required", IsError: true}
	}

	if t.parent == nil {
		return tool.Result{Content: "Agent 工具未就绪（parent 未注入）", IsError: true}
	}

	// Resolve 定义（spec F2）
	var def *subagent.Definition
	isFork := false
	if aArgs.SubagentType != "" {
		d, ok := t.catalog.Resolve(aArgs.SubagentType)
		if !ok {
			return tool.Result{Content: fmt.Sprintf("未知 subagent_type: %s", aArgs.SubagentType), IsError: true}
		}
		def = d
	} else {
		def = t.catalog.ForkDefinition()
		isFork = true
	}

	// 决定后台（spec F17/F18）
	background := def.Background || aArgs.RunInBackground || isFork
	if background && !t.bgEnabled {
		return tool.Result{Content: "后台模式已被配置禁用（enableSubAgentBackground=false），无法 Fork 或后台执行", IsError: true}
	}

	// 工具过滤（spec F30）
	allowed := tool.ApplyAgentToolFilter(tool.FilterParams{
		All:        t.parent.registry.Names(),
		Source:     int(def.Source),
		Background: background,
		Allowed:    def.Tools,
		Disallowed: def.DisallowedTools,
	})

	// 子 Agent 构造（spec F10）
	subRuntime := &SessionRuntime{ContextWindow: t.parent.runtime.ContextWindow}
	subAgent := New(t.parent.provider, t.parent.registry, t.parent.version, t.parent.eng,
		WithRuntime(subRuntime),
		WithAllowedTools(allowed),
		WithSystemPrompt(def.SystemPrompt),
		WithMaxTurns(def.MaxTurns),
		WithPermissionMode(def.PermissionMode),
		WithDontAsk(def.DontAsk),
		WithHookEngine(t.parent.hookEngine),
	)

	// 子 Conversation
	var subConv *conversation.Conversation
	if isFork {
		// Fork 路径：克隆父对话
		if t.parentConvFn == nil {
			return tool.Result{Content: "Fork 模式需要父对话消息，但未注入 parentConvFn", IsError: true}
		}
		parentMsgs := t.parentConvFn()
		forked := BuildForkedMessages(parentMsgs, aArgs.Prompt)
		subConv = conversation.NewFromMessages(forked, nil, nil)
	} else {
		subConv = conversation.New()
	}

	// 后台路径（spec F16）
	if background {
		taskID := t.taskMgr.Launch(ctx, subAgent, subConv, aArgs.Name, aArgs.Prompt)
		return tool.Result{Content: fmt.Sprintf(`{"task_id":"%s","status":"async_launched"}`, taskID)}
	}

	// 前台路径（spec F2）
	timeoutCtx, cancel := context.WithTimeout(ctx, autoBackgroundDuration)
	defer cancel()

	events := make(chan Event, 32)

	finalText, err := subAgent.RunToCompletion(timeoutCtx, subConv, aArgs.Prompt, events)
	close(events)

	if timeoutCtx.Err() != nil {
		// 超时自动切后台（spec F17.2）
		if t.taskMgr != nil {
			taskID := t.taskMgr.AdoptRunning(ctx, subAgent, subConv, aArgs.Name, events, cancel)
			return tool.Result{Content: fmt.Sprintf(`{"task_id":"%s","status":"timed_out_to_background"}`, taskID)}
		}
		return tool.Result{Content: fmt.Sprintf("子 Agent 执行超时: %v", err), IsError: true}
	}

	if err != nil {
		return tool.Result{Content: fmt.Sprintf("子 Agent 执行错误: %v", err), IsError: true}
	}

	return tool.Result{Content: finalText}
}

// isSubAgentContext 检查 ctx 是否携带子 Agent 标记——防嵌套。
func isSubAgentContext(ctx context.Context) bool {
	v := ctx.Value(subAgentCtxKey{})
	if v == nil {
		return false
	}
	b, _ := v.(bool)
	return b
}

// withSubAgentContext 标记 ctx 为子 Agent 上下文。
func withSubAgentContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, subAgentCtxKey{}, true)
}

type subAgentCtxKey struct{}
