package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// editFileArgs edit_file 工具参数
type editFileArgs struct {
	FilePath string `json:"file_path"`
	OldStr   string `json:"old_string"`
	NewStr   string `json:"new_string"`
}

// editFileTool 精确替换文件中的文本片段
type editFileTool struct{}

func (t *editFileTool) Name() string   { return "edit_file" }
func (t *editFileTool) ReadOnly() bool { return false }

// Description 返回给模型的工具用途说明。
// 末尾强化：编辑前必须先 read_file 确认目标文件和 old_string 唯一。
func (t *editFileTool) Description() string {
	return "精确替换文件中的文本片段。通过 old_string 定位要替换的内容，替换为 new_string。" +
		"编辑前请先用 read_file 读取目标文件，确认 old_string 在文件中唯一存在，确保替换精准无误。"
}

func (t *editFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "要编辑的文件路径",
			},
			"old_string": map[string]any{
				"type":        "string",
				"description": "要在文件中定位并替换的文本",
			},
			"new_string": map[string]any{
				"type":        "string",
				"description": "替换为新文本",
			},
		},
		"required": []string{"file_path", "old_string", "new_string"},
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
	if a.FilePath == "" {
		return errorResult("参数 file_path 不能为空")
	}
	if a.OldStr == "" {
		return errorResult("参数 old_string 不能为空")
	}

	// 安全检查: 路径不能包含 .. 越界
	if strings.Contains(a.FilePath, "..") {
		return errorResult("安全限制: file_path 不支持路径穿越")
	}

	content, err := os.ReadFile(a.FilePath)
	if err != nil {
		return errorResult("读取文件失败: %v", err)
	}

	text := string(content)

	// 检查 old_string 唯一性
	count := strings.Count(text, a.OldStr)
	if count == 0 {
		return errorResult("未找到匹配: old_string 在文件中不存在")
	}
	if count > 1 {
		return errorResult("old_string 在文件中不唯一: 出现 %d 次——请用 read_file 查看文件，选择唯一匹配片段", count)
	}

	// 执行替换（仅第一处——唯一时等价于唯一替换）
	newText := strings.Replace(text, a.OldStr, a.NewStr, 1)

	// 保留文件权限写回
	info, _ := os.Stat(a.FilePath)
	perm := os.FileMode(0644)
	if info != nil {
		perm = info.Mode().Perm()
	}
	if err := os.WriteFile(a.FilePath, []byte(newText), perm); err != nil {
		return errorResult("写入文件失败: %v", err)
	}

	return Result{Content: fmt.Sprintf("文件 %s 已编辑: 1 处替换", a.FilePath)}
}
