// Package tool 提供统一的工具抽象与六个核心工具实现。
// 所有工具执行失败均以 Result{IsError:true} 返回，绝不 panic。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Result 工具执行结果——永远以值类型返回，从不返回 Go error。
type Result struct {
	Content string // 回灌给模型的文本（已截断/带行号等）
	IsError bool   // true 表示结构化错误，Content 即错误描述
}

// Tool 统一工具抽象（F1）。
// 每个工具暴露名称、给模型看的描述、参数 Schema、执行入口、只读标记。
type Tool interface {
	Name() string               // 模型看到的工具名，如 "read_file"
	Description() string        // 给模型的用途说明
	Parameters() map[string]any // 手写 JSON Schema（type/properties/required/description）
	ReadOnly() bool             // true=只读工具（可并发执行 & Plan Mode 放行）
	Execute(ctx context.Context, args json.RawMessage) Result
}

// truncate 对字符串做行数和字符数双重截断，超出尾部加 [truncated] 标注。
func truncate(s string, maxLines, maxChars int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		s = strings.Join(lines, "\n") + "\n[truncated]"
	}
	if len(s) > maxChars {
		s = s[:maxChars] + "\n[truncated]"
	}
	return s
}

// SystemTool 可选接口：实现此接口的工具标记为系统工具。
// 系统工具在工具过滤时总是可见，不受白名单约束。
type SystemTool interface {
	IsSystem() bool
}

// IsSystemTool 检测一个工具是否为系统工具。
func IsSystemTool(t Tool) bool {
	st, ok := t.(SystemTool)
	return ok && st.IsSystem()
}

// errorResult 生成一个 IsError 的结构化错误结果。
func errorResult(format string, args ...interface{}) Result {
	return Result{
		Content: fmt.Sprintf(format, args...),
		IsError: true,
	}
}
