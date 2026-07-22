// Package permission 提供五层防御的权限判定系统。
//
// 前四层（黑名单 → 沙箱 → 规则引擎 → 模式兜底）由 Engine.Check 实现，
// 第五层（人在回路）由 agent 包编排驱动。
package permission

import "strings"

// Mode 权限模式：四档，决定规则未命中时的兜底裁决。
type Mode int

const (
	ModeDefault     Mode = iota // 只读 Allow / 文件写 Ask / 命令执行 Ask
	ModeAcceptEdits             // 文件写 Allow / 命令执行 Ask
	ModePlan                    // 仅只读工具可见（沿用 ch04）；矩阵同 default 作防御兜底
	ModeBypass                  // 全 Allow（黑名单/沙箱仍拦）
)

// String 返回模式的可读名称。
func (m Mode) String() string {
	switch m {
	case ModeDefault:
		return "default"
	case ModeAcceptEdits:
		return "acceptEdits"
	case ModePlan:
		return "plan"
	case ModeBypass:
		return "bypassPermissions"
	default:
		return "unknown"
	}
}

// ParseMode 大小写不敏感识别四档模式名。未知返回 (ModeDefault, false)。
func ParseMode(s string) (Mode, bool) {
	switch strings.ToLower(s) {
	case "default":
		return ModeDefault, true
	case "acceptedits":
		return ModeAcceptEdits, true
	case "plan":
		return ModePlan, true
	case "bypasspermissions":
		return ModeBypass, true
	default:
		return ModeDefault, false
	}
}

// Decision 单次权限判定的结果。
type Decision int

const (
	Allow Decision = iota
	Deny
	Ask // 需人在回路确认
)

// Category 工具按副作用分类。
type Category int

const (
	CategoryRead  Category = iota // 只读：read_file / glob / grep
	CategoryWrite                 // 文件写：write_file / edit_file
	CategoryExec                  // 命令执行：bash / 未知工具
)

// Outcome 人在回路三选一结果。
type Outcome int

const (
	OutcomeDenyOnce     Outcome = iota // 拒绝本次
	OutcomeAllowOnce                   // 允许本次（不留规则）
	OutcomeAllowForever                // 永久允许（+写本地层精确匹配规则）
)
