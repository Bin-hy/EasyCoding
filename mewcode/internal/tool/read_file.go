package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// readFileArgs 读文件工具的参数
type readFileArgs struct {
	Path string `json:"path"`
}

// readFileTool 读取文件内容，返回带行号的文本
type readFileTool struct{}

func (t *readFileTool) Name() string { return "read_file" }
func (t *readFileTool) Description() string {
	return "读取指定文件的内容，返回带行号的文本。文件不存在或不可读时返回结构化错误。"
}

func (t *readFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "要读取的文件路径",
			},
		},
		"required": []string{"path"},
	}
}

func (t *readFileTool) Execute(ctx context.Context, args json.RawMessage) Result {
	// 空 args 归一化为 {}
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}

	var a readFileArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return errorResult("参数解析失败: %v", err)
	}
	if a.Path == "" {
		return errorResult("参数 path 不能为空")
	}

	// 检查路径是否为目录
	info, err := os.Stat(a.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return errorResult("文件不存在: %s", a.Path)
		}
		return errorResult("无法读取文件: %v", err)
	}
	if info.IsDir() {
		return errorResult("路径是目录而非文件: %s", a.Path)
	}

	// 读取文件
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return errorResult("读取文件失败: %v", err)
	}

	// 加行号（cat -n 风格，%6d\t）
	lines := strings.Split(string(data), "\n")
	for i := range lines {
		lines[i] = fmt.Sprintf("%6d\t%s", i+1, lines[i])
	}
	content := strings.Join(lines, "\n")

	// 截断：≤2000 行 / ≤256KB
	const maxLines = 2000
	const maxChars = 256 * 1024
	content = truncate(content, maxLines, maxChars)

	return Result{Content: content}
}
