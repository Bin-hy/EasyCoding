package command

import (
	"context"
	"fmt"
	"strings"

	"mewcode/internal/skills"
)

// SkillCommandDeps /skill 命令所需的依赖。
type SkillCommandDeps struct {
	Catalog  *skills.Catalog
	Executor *skills.Executor
	WorkDir  string
	CmdReg   *Registry
}

// RegisterSkillCmd 注册 /skill 管理命令。
func RegisterSkillCmd(reg *Registry, deps *SkillCommandDeps) {
	// /skill list —— 列出已加载 skill
	reg.Register(&Command{
		Name:        "skill",
		Description: "Skill 管理：list / info <name> / reload",
		Kind:        KindLocal,
		Hidden:      false,
		Handler:     handleSkill(deps),
	})
}

// handleSkill 处理 /skill 命令。
// deps 仅用于闭包捕获，实际子命令分发在 TUI 层的 handleSkillCmd 中完成。
func handleSkill(deps *SkillCommandDeps) Handler {
	_ = deps // 由 TUI 层 HandleSkillSub 使用
	return func(ctx context.Context, ui UI) error {
		return showSkillHelp(ui)
	}
}

// showSkillHelp 显示 /skill 帮助信息。
func showSkillHelp(ui UI) error {
	ui.Println("用法: /skill list | info <name> | reload")
	ui.Println("  list    — 列出所有已加载的 Skill")
	ui.Println("  info <n>— 查看 Skill 详细信息")
	ui.Println("  reload  — 重新扫描 Skill 目录")
	return nil
}

// HandleSkillSub 处理 /skill <subcommand> 调用（由 TUI 层解析后调用）。
func HandleSkillSub(ui UI, args string, deps *SkillCommandDeps) error {
	parts := strings.Fields(args)
	if len(parts) == 0 {
		return showSkillHelp(ui)
	}

	switch parts[0] {
	case "list":
		return handleSkillList(ui, deps)
	case "info":
		if len(parts) < 2 {
			return fmt.Errorf("用法: /skill info <name>")
		}
		return handleSkillInfo(ui, deps, parts[1])
	case "reload":
		return handleSkillReload(ui, deps)
	default:
		return fmt.Errorf("未知子命令: %s。可用: list / info / reload", parts[0])
	}
}

// handleSkillList 列出所有已加载 Skill。
func handleSkillList(ui UI, deps *SkillCommandDeps) error {
	if deps == nil || deps.Executor == nil {
		ui.Println("Skill 系统未初始化")
		return nil
	}

	summaries := deps.Executor.ListSummaries()
	if len(summaries) == 0 {
		ui.Println("（无已加载的 Skill）")
		return nil
	}

	ui.Println(fmt.Sprintf("已加载 %d 个 Skill:\n", len(summaries)))
	for _, s := range summaries {
		ui.Println(fmt.Sprintf("  /%-20s [%s] %s - %s", s.Name, s.Source, s.Mode, s.Description))
	}
	return nil
}

// handleSkillInfo 查看单个 Skill 详细信息。
func handleSkillInfo(ui UI, deps *SkillCommandDeps, name string) error {
	if deps == nil || deps.Executor == nil {
		return fmt.Errorf("Skill 系统未初始化")
	}

	sk, err := deps.Executor.GetSkillDetail(name)
	if err != nil {
		return err
	}

	ui.Println(fmt.Sprintf("Name:        %s", sk.Meta.Name))
	ui.Println(fmt.Sprintf("Description: %s", sk.Meta.Description))
	ui.Println(fmt.Sprintf("Mode:        %s", sk.Meta.Mode))
	if sk.Meta.Mode == "fork" {
		ui.Println(fmt.Sprintf("ForkContext: %s", sk.Meta.ForkContext))
	}
	if sk.Meta.Model != "" {
		ui.Println(fmt.Sprintf("Model:       %s", sk.Meta.Model))
	}
	if len(sk.Meta.AllowedTools) > 0 {
		ui.Println(fmt.Sprintf("AllowedTools: %s", strings.Join(sk.Meta.AllowedTools, ", ")))
	}
	ui.Println(fmt.Sprintf("Source:      %s (%s)", sk.Source, sk.SourceDir))
	return nil
}

// handleSkillReload 重新扫描 Skill 目录并重建命令。
func handleSkillReload(ui UI, deps *SkillCommandDeps) error {
	if deps == nil || deps.Executor == nil || deps.CmdReg == nil {
		return fmt.Errorf("Skill 系统未完全初始化")
	}

	added, removed := deps.Executor.ReloadCatalog(deps.WorkDir)

	// 清理旧命令
	if len(removed) > 0 {
		deps.CmdReg.RemoveNames(removed)
	}

	// 注册新命令
	if len(added) > 0 {
		RegisterSkillsAsCommands(deps.CmdReg, deps.Catalog, deps.Executor)
	}

	ui.Println(fmt.Sprintf("Skill 目录已重载：新增 %d，移除 %d", len(added), len(removed)))
	return nil
}
