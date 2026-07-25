// Package command 提供斜杠命令的注册、解析与分发。
// 命令 handler 通过 UI 抽象接口操作 TUI，不直接持有 TUI 类型。
package command

import (
	"context"
)

// Kind 表示命令的执行模式分类。
type Kind int

const (
	KindLocal     Kind = iota // 纯本地：只打印信息，不改 Model，不进对话历史
	KindUI                    // 影响界面：可改 Model 状态，不进对话历史
	KindPrompt               // 提示词：向对话注入 user 消息 + 触发 LLM 回合
	KindSkillFork            // Skill fork：异步执行后以 assistant 消息写入对话
)

// Handler 命令处理函数签名。
// ctx 为命令执行的上下文；ui 提供操作 TUI 的抽象方法集。
type Handler func(ctx context.Context, ui UI) error

// Command 表示一条已注册的斜杠命令。
type Command struct {
	Name        string   // 不带 "/" 前缀，全小写，唯一
	Aliases     []string // 不带 "/" 前缀，全小写，全局唯一（含 Name）
	Description string   // 一句话描述，用于 /help 与补全菜单
	Kind        Kind
	Hidden      bool // true 时 /help 与补全菜单都不显示，但 dispatcher 仍可命中
	Handler     Handler
}
