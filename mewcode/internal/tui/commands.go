package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"mewcode/internal/agent"
	"mewcode/internal/llm"
	"mewcode/internal/permission"
	"mewcode/internal/prompt"
)

// commandHandler 命令处理器签名。
type commandHandler func(ctx context.Context, model *Model) tea.Cmd

// builtinCommands 注册表：/exit /plan /do /compact。
var builtinCommands = map[string]commandHandler{
	"/exit":    handleExit,
	"/plan":    handlePlan,
	"/do":      handleDo,
	"/compact": handleCompact,
}

// dispatchCommand 检查输入是否以 / 开头，命中则返回对应处理器。
// 不以 / 开头返回 nil, false。
func dispatchCommand(input string) (commandHandler, bool) {
	if len(input) == 0 || input[0] != '/' {
		return nil, false
	}
	if handler, ok := builtinCommands[input]; ok {
		return handler, true
	}
	return handleUnknown, true
}

func handleExit(ctx context.Context, model *Model) tea.Cmd {
	if model.cancel != nil {
		model.cancel()
	}
	return tea.Quit
}

func handlePlan(ctx context.Context, model *Model) tea.Cmd {
	model.mode = permission.ModePlan
	return tea.Println(renderNoticeBlock("已进入计划模式（只读工具，产出计划后请用 /do 执行）"))
}

func handleDo(ctx context.Context, model *Model) tea.Cmd {
	model.mode = permission.ModeDefault
	model.conv.AddUser(prompt.ExecuteDirective)
	return model.startStreaming()
}

func handleCompact(ctx context.Context, model *Model) tea.Cmd {
	if model.ag == nil {
		return tea.Println(renderNoticeBlock("压缩失败：Agent 未初始化"))
	}
	// 按当前 mode 取工具集
	var defs []llm.ToolDefinition
	if model.mode == permission.ModePlan {
		defs = model.registry.ReadOnlyDefinitions()
	} else {
		defs = model.registry.Definitions()
	}

	before, after, err := model.ag.RunForceCompact(ctx, model.conv, defs)
	if err != nil {
		return tea.Println(renderNoticeBlock(fmt.Sprintf("压缩失败：%v", err)))
	}
	return tea.Println(renderNoticeBlock(fmt.Sprintf("已压缩，token 从 %d 降至 %d", before, after)))
}

func handleUnknown(ctx context.Context, model *Model) tea.Cmd {
	return tea.Println(renderNoticeBlock("未知命令，可用命令: /exit /plan /do /compact"))
}

// formatCompactNotice 将 CompactEvent 格式化为 TUI 展示文案。
func formatCompactNotice(ev *agent.CompactEvent) string {
	switch ev.Phase {
	case agent.CompactPhaseBeforeAuto:
		return "正在压缩上下文..."
	case agent.CompactPhaseBeforeEmergency:
		return "上下文撞墙，自动压缩中..."
	case agent.CompactPhaseAfterAuto, agent.CompactPhaseAfterEmergency:
		if ev.Err != nil {
			return fmt.Sprintf("压缩失败：%v", ev.Err)
		}
		return fmt.Sprintf("已压缩，token 从 %d 降至 %d", ev.Before, ev.After)
	default:
		return ""
	}
}
