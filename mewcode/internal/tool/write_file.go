package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// writeFileArgs 写文件工具的参数
type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// writeFileTool 写入（覆盖）文件，父目录不存在时自动创建
type writeFileTool struct{}

func (t *writeFileTool) Name() string { return "write_file" }
func (t *writeFileTool) Description() string {
	return "写入（覆盖）文件内容。父目录不存在时自动创建，返回成功或结构化错误。"
}

func (t *writeFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "要写入的文件路径",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "要写入的文件内容",
			},
		},
		"required": []string{"path", "content"},
	}
}

func (t *writeFileTool) Execute(ctx context.Context, args json.RawMessage) Result {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}

	var a writeFileArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return errorResult("参数解析失败: %v", err)
	}
	if a.Path == "" {
		return errorResult("参数 path 不能为空")
	}

	// 创建父目录
	dir := filepath.Dir(a.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errorResult("创建父目录失败: %v", err)
	}

	// 写入文件
	if err := os.WriteFile(a.Path, []byte(a.Content), 0o644); err != nil {
		return errorResult("写入文件失败: %v", err)
	}

	return Result{Content: fmt.Sprintf("已写入 %s（%d 字节）", a.Path, len(a.Content))}
}
