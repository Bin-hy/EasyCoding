package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// bashArgs bash 工具参数
type bashArgs struct {
	Command     string `json:"command"`
	Description string `json:"description"`
	Timeout     int    `json:"timeout"`
	Workdir     string `json:"workdir"`
}

// bashTool 执行 shell 命令
type bashTool struct{}

func (t *bashTool) Name() string   { return "bash" }
func (t *bashTool) ReadOnly() bool { return false }

// Description 返回给模型的工具用途说明。
// 末尾强化：读文件/找文件/搜内容请优先用专用工具而非 bash 拼凑。
func (t *bashTool) Description() string {
	return "执行 shell 命令并返回输出。读文件、找文件、搜内容请优先用 " +
		"read_file / glob / grep 专用工具，不要用 bash 拼凑 cat/find/grep 等命令来替代。"
}

func (t *bashTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "要执行的 shell 命令",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "命令用途说明",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "超时(毫秒)，默认 120000",
			},
			"workdir": map[string]any{
				"type":        "string",
				"description": "工作目录，不填则使用当前工作目录",
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

	timeout := time.Duration(a.Timeout) * time.Millisecond
	if a.Timeout <= 0 {
		timeout = 120 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 安全检查: workdir 不能使用绝对路径或路径穿越
	if a.Workdir != "" {
		if filepath.IsAbs(a.Workdir) || strings.Contains(a.Workdir, "..") {
			return errorResult("安全限制: workdir 不支持绝对路径或路径穿越")
		}
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", a.Command)
	cmd.Dir = a.Workdir

	// 不继承环境变量（N5: 密钥不泄漏）
	cmd.Env = []string{
		"HOME=" + os.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
		"USER=" + os.Getenv("USER"),
		"TERM=" + os.Getenv("TERM"),
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}

	// 结果截断
	output = strings.TrimSpace(output)
	output = truncate(output, 200, 8000)

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return errorResult("命令超时: %s", err)
		}
		// 提取退出码
		exitCode := -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		if output == "" {
			return errorResult("exit_code: %d\n命令执行失败: %v", exitCode, err)
		}
		return Result{
			Content: fmt.Sprintf("exit_code: %d\n%s", exitCode, output),
			IsError: false,
		}
	}

	if output == "" {
		output = "(无输出)"
	}
	return Result{Content: "exit_code: 0\n" + output}
}
