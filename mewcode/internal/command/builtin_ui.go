package command

import (
	"context"

	"mewcode/internal/permission"
)

// handleExit 退出 TUI 进程。
func handleExit(ctx context.Context, ui UI) error {
	ui.Quit()
	return nil
}

// handlePlan 切换到计划模式。
func handlePlan(ctx context.Context, ui UI) error {
	ui.SetMode(permission.ModePlan)
	ui.Println("已切换到 PLAN 模式")
	return nil
}

// handleCompact 手动触发上下文压缩。仅 idle 状态可执行（dispatchSlash 已在 handler 前做 Idle 守护，此处不重复检查）。
func handleCompact(ctx context.Context, ui UI) error {
	ui.ForceCompact()
	return nil
}

// handleResume 打开历史会话恢复列表。
func handleResume(ctx context.Context, ui UI) error {
	ui.OpenResumeMenu()
	return nil
}

// handleClear 清空当前会话并开启新会话。
func handleClear(ctx context.Context, ui UI) error {
	ui.ClearAndNewSession()
	ui.Println("已清空当前会话，开启新 session")
	return nil
}
