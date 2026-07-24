package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"mewcode/internal/llm"
)

// DefaultTimeout 单个工具执行的默认超时（N1，不可配）。
const DefaultTimeout = 30 * time.Second

// Registry 集中登记工具、按名查找、导出定义、按名执行。
type Registry struct {
	order []string // 保持注册顺序，导出稳定
	tools map[string]Tool
}

// Register 注册一个工具。同名后注册覆盖先注册。
func (r *Registry) Register(t Tool) {
	if r.tools == nil {
		r.tools = make(map[string]Tool)
	}
	name := t.Name()
	if _, exists := r.tools[name]; !exists {
		r.order = append(r.order, name)
	}
	r.tools[name] = t
}

// Count 返回当前已注册工具数量（O(1)）。
func (r *Registry) Count() int {
	return len(r.tools)
}

// Get 按名查找工具。
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Definitions 按注册顺序导出协议无关工具定义列表（F3/AC1）。
func (r *Registry) Definitions() []llm.ToolDefinition {
	defs := make([]llm.ToolDefinition, 0, len(r.order))
	for _, name := range r.order {
		t := r.tools[name]
		defs = append(defs, llm.ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.Parameters(),
		})
	}
	return defs
}

// ReadOnlyDefinitions 按注册顺序仅导出只读工具的定义（Plan Mode 使用）。
func (r *Registry) ReadOnlyDefinitions() []llm.ToolDefinition {
	defs := make([]llm.ToolDefinition, 0, len(r.order))
	for _, name := range r.order {
		t := r.tools[name]
		if t.ReadOnly() {
			defs = append(defs, llm.ToolDefinition{
				Name:        t.Name(),
				Description: t.Description(),
				InputSchema: t.Parameters(),
			})
		}
	}
	return defs
}

// IsReadOnly 判断指定工具是否只读。未知工具返回 false。
func (r *Registry) IsReadOnly(name string) bool {
	t, ok := r.Get(name)
	return ok && t.ReadOnly()
}

// Execute 按名查找工具并执行。未知工具兜底为 IsError。
func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage) Result {
	t, ok := r.Get(name)
	if !ok {
		return Result{
			Content: fmt.Sprintf("未知工具: %s", name),
			IsError: true,
		}
	}
	return t.Execute(ctx, args)
}

// NewDefaultRegistry 构造并注册 6 个核心工具。
func NewDefaultRegistry() *Registry {
	r := &Registry{}
	r.Register(&readFileTool{})
	r.Register(&writeFileTool{})
	r.Register(&editFileTool{})
	r.Register(&bashTool{})
	r.Register(&globTool{})
	r.Register(&grepTool{})
	return r
}
