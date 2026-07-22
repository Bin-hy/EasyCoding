package prompt

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Environment 运行环境信息，供模型感知当前上下文。
type Environment struct {
	WorkingDir string // os.Getwd()
	Platform   string // runtime.GOOS
	Date       string // time.Now().Format("2006-01-02")
	GitStatus  string // git status --porcelain 摘要；非 git 目录或取不到则留空
	Version    string // 应用版本
	Model      string // 当前使用的模型名
}

// GatherEnvironment 采集运行环境信息。git 状态外调失败时降级留空，不中断；不读环境变量（N5）。
func GatherEnvironment(version, model string) Environment {
	env := Environment{
		Version:  version,
		Model:    model,
		Date:     time.Now().Format("2006-01-02"),
		Platform: runtime.GOOS,
	}

	if wd, err := os.Getwd(); err == nil {
		env.WorkingDir = wd
	}

	// git 状态采集：2 秒超时，失败/非 git 目录 → 留空（N4 降级）
	env.GitStatus = gatherGitStatus()

	return env
}

// gatherGitStatus 带超时执行 git status --porcelain，返回摘要文本；失败返回 ""。
func gatherGitStatus() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	nonEmpty := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty++
		}
	}
	if nonEmpty == 0 {
		return "clean"
	}
	return fmt.Sprintf("%d 个文件有改动", nonEmpty)
}

// Render 渲染环境信息为文本段。空值项省略，保持简洁。
func (e Environment) Render() string {
	var parts []string

	if e.WorkingDir != "" {
		parts = append(parts, "工作目录: "+e.WorkingDir)
	}
	if e.Platform != "" {
		parts = append(parts, "平台: "+e.Platform)
	}
	if e.Date != "" {
		parts = append(parts, "日期: "+e.Date)
	}
	if e.GitStatus != "" {
		parts = append(parts, "Git 状态: "+e.GitStatus)
	}
	if e.Version != "" {
		parts = append(parts, "应用版本: "+e.Version)
	}
	if e.Model != "" {
		parts = append(parts, "模型: "+e.Model)
	}

	if len(parts) == 0 {
		return ""
	}
	return "环境信息\n" + strings.Join(parts, "\n")
}
