package agent

import (
	"context"

	"mewcode/internal/permission"
)

// ApprovalUpgrader 是子 Agent 把审批请求升级到父 TUI 的回调（spec F12）。
// 实现方：TaskManager 把请求转发到主 TUI 的事件流；前台 inline 模式直接复用现有 Approval 路径。
// 返回 (outcome, ok)——ok=false 时调用方应走默认 emit Approval 路径。
type ApprovalUpgrader func(ctx context.Context, req *ApprovalRequest) (permission.Outcome, bool)
