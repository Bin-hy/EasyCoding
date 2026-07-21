package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// editFileArgs 改文件工具的参数
type editFileArgs struct {
	Path      string `json:"path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

// editFileTool 对原文片段做唯一匹配替换
type editFileTool struct{}

func (t *editFileTool) Name() string { return "edit_file" }
func (t *editFileTool) Description() string {
	return "对文件中的原文片段做唯一匹配替换。匹配 0 次或多于 1 次时返回清晰错误，让模型据此重试。"
}

func (t *editFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "要修改的文件路径",
			},
			"old_string": map[string]any{
				"type":        "string",
				"description": "要替换的原文片段（需在文件中唯一匹配）",
			},
			"new_string": map[string]any{
				"type":        "string",
				"description": "替换后的新文本片段",
			},
		},
		"required": []string{"path", "old_string", "new_string"},
	}
}

func (t *editFileTool) Execute(ctx context.Context, args json.RawMessage) Result {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}

	var a editFileArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return errorResult("参数解析失败: %v", err)
	}
	if a.Path == "" {
		return errorResult("参数 path 不能为空")
	}
	if a.OldString == "" {
		return errorResult("参数 old_string 不能为空")
	}

	// 读取文件
	data, err := os.ReadFile(a.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return errorResult("文件不存在: %s", a.Path)
		}
		return errorResult("读取文件失败: %v", err)
	}

	content := string(data)
	n := strings.Count(content, a.OldString)

	switch n {
	case 0:
		return errorResult("未找到匹配的内容，请检查 old_string 是否正确")
	case 1:
		newContent := strings.Replace(content, a.OldString, a.NewString, 1)
		if err := os.WriteFile(a.Path, []byte(newContent), 0o644); err != nil {
			return errorResult("写入文件失败: %v", err)
		}
		return Result{Content: fmt.Sprintf("已成功替换 %s 中的 1 处匹配", a.Path)}
	default:
		return errorResult("匹配到 %d 处，old_string 不唯一，请提供更长上下文使其唯一", n)
	}
}
