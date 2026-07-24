package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"mewcode/internal/skills"
)

// LoadSkillTool 实现 tool.Tool，按需加载 Skill 的完整 SOP。
// 标记为系统工具（SystemTool），始终可见，不受工具白名单约束。
type LoadSkillTool struct {
	executor *skills.Executor
}

// NewLoadSkillTool 创建 LoadSkill 工具实例。
func NewLoadSkillTool(exec *skills.Executor) *LoadSkillTool {
	return &LoadSkillTool{executor: exec}
}

// Name 返回工具名。
func (t *LoadSkillTool) Name() string { return "LoadSkill" }

// Description 返回给模型的工具描述。
func (t *LoadSkillTool) Description() string {
	return "按需加载 Skill 的完整 SOP 指令到环境上下文。输入 Skill 名称，该 Skill 的完整指令将钉在后续对话中。"
}

// Parameters 返回工具参数 Schema。
func (t *LoadSkillTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "要激活的 Skill 名称",
			},
		},
		"required": []string{"name"},
	}
}

// ReadOnly 返回 true——LoadSkill 无外部副作用。
func (t *LoadSkillTool) ReadOnly() bool { return true }

// IsSystem 返回 true——系统工具，不受白名单约束。
func (t *LoadSkillTool) IsSystem() bool { return true }

// Execute 执行：catalog 查找 → 重读 body → 激活 → 返回简短确认。
func (t *LoadSkillTool) Execute(ctx context.Context, args json.RawMessage) Result {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}

	var a struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errorResult("参数解析失败: %v", err)
	}
	if a.Name == "" {
		return errorResult("参数 name 不能为空")
	}

	if t.executor == nil {
		return errorResult("Skill 执行器未初始化")
	}

	_, err := t.executor.RenderAndActivate(a.Name, "")
	if err != nil {
		return errorResult("激活 Skill 失败: %v", err)
	}

	return Result{Content: fmt.Sprintf("Skill %s 已激活，SOP 已钉到环境上下文。", a.Name)}
}
