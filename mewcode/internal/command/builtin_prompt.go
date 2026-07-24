package command

import (
	"context"

	"mewcode/internal/permission"
	"mewcode/internal/prompt"
)

// reviewDirective /review 命令注入的固定提示词文本。
const reviewDirective = "请审查当前上下文中的代码变更/已读取的文件，指出潜在 bug、可读性问题和可简化处。"

// handleDo 切回默认模式 + 注入执行指令 + 触发回合。
func handleDo(ctx context.Context, ui UI) error {
	ui.SetMode(permission.ModeDefault)
	ui.InjectAndSend("/do", prompt.ExecuteDirective)
	return nil
}

// handleReview 向对话注入代码审查请求并触发回合。
func handleReview(ctx context.Context, ui UI) error {
	ui.InjectAndSend("/review", reviewDirective)
	return nil
}
