package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"mewcode/internal/skills"
)

// InstallSkillTool 实现 tool.Tool，远程安装 Skill。
// 普通工具，受权限模式约束（写盘 + 网络）。
type InstallSkillTool struct {
	workDir    string
	catalog    *skills.Catalog
	onInstalled func(name string) // 安装后回调（注册命令）
}

// NewInstallSkillTool 创建 InstallSkill 工具实例。
func NewInstallSkillTool(workDir string, catalog *skills.Catalog, onInstalled func(name string)) *InstallSkillTool {
	return &InstallSkillTool{
		workDir:    workDir,
		catalog:    catalog,
		onInstalled: onInstalled,
	}
}

// Name 返回工具名。
func (t *InstallSkillTool) Name() string { return "InstallSkill" }

// Description 返回给模型的工具描述。
func (t *InstallSkillTool) Description() string {
	return "从 URL 远程安装 Skill。支持 skills.sh、GitHub tree、raw.githubusercontent.com 三种 URL 格式。安装后 Skill 立即可用。"
}

// Parameters 返回工具参数 Schema。
func (t *InstallSkillTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "Skill URL（skills.sh / GitHub tree / raw URL）",
			},
		},
		"required": []string{"url"},
	}
}

// ReadOnly 返回 false——InstallSkill 有写盘 + 网络副作用。
func (t *InstallSkillTool) ReadOnly() bool { return false }

// Execute 执行安装：解析 URL → 下载 → 解压 → Reload Catalog → 回调注册命令。
func (t *InstallSkillTool) Execute(ctx context.Context, args json.RawMessage) Result {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}

	var a struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errorResult("参数解析失败: %v", err)
	}
	if a.URL == "" {
		return errorResult("参数 url 不能为空")
	}

	// 解析 URL
	name, apiURL, err := skills.ParseSkillURL(a.URL)
	if err != nil {
		return errorResult("URL 解析失败: %v", err)
	}

	// 确定安装根目录
	home, err := os.UserHomeDir()
	if err != nil {
		return errorResult("获取用户目录失败: %v", err)
	}
	installRoot := filepath.Join(home, ".mewcode", "skills")

	// 安装
	report, err := skills.Install(name, apiURL, installRoot)
	if err != nil {
		return errorResult("安装失败: %v", err)
	}

	// 重载 Catalog
	if t.catalog != nil {
		t.catalog.Reload(t.workDir)
	}

	// 回调注册命令
	if t.onInstalled != nil {
		t.onInstalled(name)
	}

	return Result{Content: fmt.Sprintf(
		"Skill %s 安装成功！%d 个文件已安装到 %s。输入 /%s 即可使用。",
		report.Name, report.FileCount, report.Dir, report.Name,
	)}
}
