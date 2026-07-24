package skills

import (
	"context"
	"fmt"

	"mewcode/internal/conversation"
)

// SkillHost inline 模式的宿主接口，由 Agent 实现。
type SkillHost interface {
	ActivateSkill(name, body string)
}

// SkillForkHost fork 模式的宿主接口，由 TUI/main 层实现。
// RunSubAgent 在独立 conversation 中运行子 Agent，返回最终 assistant 文本。
type SkillForkHost interface {
	RunSubAgent(ctx context.Context, forkConv *conversation.Conversation, allowedTools []string) (string, error)
}

// Executor 执行 Skill 的入口：根据 mode 分发到 inline 或 fork 路径。
type Executor struct {
	catalog *Catalog
	host    SkillHost // inline 模式宿主（Agent）
}

// NewExecutor 创建一个 Skill 执行器。
func NewExecutor(catalog *Catalog, host SkillHost) *Executor {
	return &Executor{catalog: catalog, host: host}
}

// Execute 根据 Skill mode 分发执行。
// inline：渲染 body → ActivateSkill → 返回 body 供 TUI 作为 user message 注入。
// fork：渲染 body → 新 conversation → RunSubAgent → 返回 finalText 作为 assistant 消息。
func (e *Executor) Execute(ctx context.Context, name, args string, forkHost SkillForkHost) (isFork bool, body string, forkResult string, err error) {
	skill, err := e.catalog.GetFull(name)
	if err != nil {
		return false, "", "", err
	}

	body = RenderBody(skill, args)

	if skill.Meta.Mode == "fork" {
		result, fErr := e.runFork(ctx, skill, body, forkHost)
		return true, "", result, fErr
	}

	// inline：通过 host 激活
	e.host.ActivateSkill(skill.Meta.Name, body)
	return false, body, "", nil
}

// runFork 执行 fork 模式：在独立 conversation 中运行子 Agent。
func (e *Executor) runFork(ctx context.Context, skill *Skill, body string, forkHost SkillForkHost) (string, error) {
	if forkHost == nil {
		return "", fmt.Errorf("fork mode 需要 SkillForkHost，但未提供")
	}

	// 构造 fork conversation
	forkConv := conversation.New()
	forkConv.AddUser(body)

	// 子 Agent 运行
	finalText, err := forkHost.RunSubAgent(ctx, forkConv, skill.Meta.AllowedTools)
	if err != nil {
		return "", fmt.Errorf("fork 执行失败 [%s]: %w", skill.Meta.Name, err)
	}

	return finalText, nil
}

// SkillSummary 用于 UI 层展示的 Skill 摘要。
type SkillSummary struct {
	Name        string
	Description string
	Source      string
	Mode        string
	FilePath    string
}

// ListSummaries 返回所有 Skill 的摘要列表。
func (e *Executor) ListSummaries() []SkillSummary {
	list := e.catalog.List()
	result := make([]SkillSummary, len(list))
	for i, sk := range list {
		mode := sk.Meta.Mode
		if mode == "" {
			mode = "inline"
		}
		result[i] = SkillSummary{
			Name:        sk.Meta.Name,
			Description: sk.Meta.Description,
			Source:      sk.Source.String(),
			Mode:        mode,
			FilePath:    sk.SourceDir,
		}
	}
	return result
}

// GetSkillDetail 获取指定 Skill 的完整信息。
func (e *Executor) GetSkillDetail(name string) (*Skill, error) {
	skill, err := e.catalog.GetFull(name)
	if err != nil {
		return nil, err
	}
	return skill, nil
}

// ReloadCatalog 重新扫描并重载目录。
func (e *Executor) ReloadCatalog(workDir string) (added, removed []string) {
	return e.catalog.Reload(workDir)
}

// RenderAndActivate 渲染并激活（shared helper）。
func (e *Executor) RenderAndActivate(name, args string) (string, error) {
	skill, err := e.catalog.GetFull(name)
	if err != nil {
		return "", err
	}
	body := RenderBody(skill, args)
	e.host.ActivateSkill(skill.Meta.Name, body)
	return body, nil
}

// ValidateTools 调用 Catalog.ValidateTools，传入 lookup 函数。
func (e *Executor) ValidateTools(toolExists func(string) bool) []ValidationIssue {
	return e.catalog.ValidateTools(toolExists)
}
