package command

import (
	"context"
	"fmt"

	"mewcode/internal/skills"
)

// RegisterSkillsAsCommands 将 Catalog 中每个 Skill 注册为 /<name> 命令。
// inline skill → KindPrompt，fork skill → KindSkillFork。
// 返回已注册的 skill 名称列表（供 reload 清理使用）。
func RegisterSkillsAsCommands(reg *Registry, catalog *skills.Catalog, exec *skills.Executor) []string {
	names := catalog.Names()
	for _, name := range names {
		sk, ok := catalog.Get(name)
		if !ok {
			continue
		}

		kind := KindPrompt
		desc := sk.Meta.Description
		if desc == "" {
			desc = "执行 " + name
		}
		if sk.Meta.Mode == "fork" {
			kind = KindSkillFork
		}
		desc += " [skill]"

		// 闭包捕获 name 和 executor
		skillName := name
		reg.Register(&Command{
			Name:        skillName,
			Description: desc,
			Kind:        kind,
			Hidden:      false,
			Handler:     makeSkillHandler(skillName, exec, kind),
		})
	}
	return names
}

// makeSkillHandler 创建 Skill 命令的 handler 闭包。
func makeSkillHandler(name string, exec *skills.Executor, kind Kind) Handler {
	return func(ctx context.Context, ui UI) error {
		switch kind {
		case KindPrompt:
			// inline: 渲染 body → 作为 user message 注入
			_, body, _, err := exec.Execute(ctx, name, "", nil)
			if err != nil {
				return fmt.Errorf("skill %s 执行失败: %w", name, err)
			}
			ui.InjectAndSend("/"+name, body)
			return nil

		case KindSkillFork:
			// fork: 需要 SkillForkHost 接口，由 TUI 层在 dispatchSlash 中注入
			return fmt.Errorf("fork skill %q 暂不支持通过命令直接调用，请使用自然语言触发 LoadSkill", name)

		default:
			return fmt.Errorf("不支持的 Skill 命令类型: %v", kind)
		}
	}
}
