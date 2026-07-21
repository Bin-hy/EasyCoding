package tool


type writeArgs struct {
	Path string `json:"path"`
	Content string `json:"content"`
}

// writeFileTool 写入（覆盖）文件，父目录不存在时自动创建
type writeFileTool struct {}

func (t *writeFileTool) Name() string {
	return "write_file"
}

func (t *writeFileTool) Description() string {
	return "写入（覆盖）文件内容。父目录不存在时自动创建，返回成功或结构化错误。"
}

func (t *writeFileTool) Parameters() map[string]any{
	// 
	return map[string]any{
		"path": map[string]any{
			"type": "string",
			"description": "要写入的文件路径",
		},
		"content": map[string]any{
			"type": "string",
			"description": "要写入的文件内容",
		},
		"required": []string{"path", "content"},
	}
}

func (t *writeFileTool) Execute(ctx context.Context, args json.RawMessage) Result {