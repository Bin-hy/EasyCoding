package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// bashArgs 执行命令工具的参数
type bashArgs struct {
	Command string `json:"command"`
}

// bashTool 执行 shell 命令，返回 stdout/stderr/退出码
type bashTool struct{}

func (t *bashTool) Name() string { return "bash" }
func (t *bashTool) Description() string {
	return "执行 shell 命令，返回标准输出、标准错误与退出码。受超时约束，超时或非零退出以结构化结果返回，不中断会话。"
}

func (t *bashTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "要执行的 shell 命令",
			},
		},
		"required": []string{"command"},
	}
}

func (t *bashTool) Execute(ctx context.Context, args json.RawMessage) Result {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}

	var a bashArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return errorResult("参数解析失败: %v", err)
	}
	if a.Command == "" {
		return errorResult("参数 command 不能为空")
	}

	// 按平台选 shell
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", a.Command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", a.Command)
	}

	// 捕获合并 stdout/stderr
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf

	err := cmd.Run()

	// 检查超时
	if ctx.Err() != nil {
		return Result{
			Content: "命令超时",
			IsError: true,
		}
	}

	output := strings.TrimRight(outBuf.String(), "\n")

	var result strings.Builder
	if output != "" {
		result.WriteString(output)
	}

	// 退出码信息
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return errorResult("命令执行失败: %v", err)
		}
	}

	if result.Len() > 0 {
		result.WriteString("\n")
	}
	result.WriteString(fmt.Sprintf("exit_code: %d", exitCode))

	// 截断输出 ~30000 字符
	content := truncate(result.String(), 500, 30000)

	// 非零退出不设 IsError，按结果回灌让模型判断
	return Result{Content: content}
}
