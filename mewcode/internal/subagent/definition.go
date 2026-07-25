package subagent

import "mewcode/internal/permission"

// Source 表示 Agent 定义文件的来源。
type Source int

const (
	SourceBuiltin Source = iota // 二进制内嵌：subagent/builtin/*.md
	SourceUser                  // 用户级：~/.mewcode/agents/*.md
	SourceProject               // 项目级：<root>/.mewcode/agents/*.md
	SourcePlugin                // 插件级：占位，本期不实现
)

// String 返回 Source 的可读名称。
func (s Source) String() string {
	switch s {
	case SourceBuiltin:
		return "builtin"
	case SourceUser:
		return "user"
	case SourceProject:
		return "project"
	case SourcePlugin:
		return "plugin"
	default:
		return "unknown"
	}
}

// Definition 是一个 Agent 角色的完整定义，从 Markdown+YAML frontmatter 解析而来（spec F4）。
//
// frontmatter YAML 字段:
//   - name(必填): 角色名，小写字母/数字/连字符，长度 1-32
//   - description(必填): 一句话描述
//   - tools(可选): 工具白名单
//   - disallowedTools(可选): 工具黑名单
//   - model(可选): haiku/sonnet/opus/inherit，缺省 inherit
//   - maxTurns(可选): 最大迭代轮数，0=沿用全局默认
//   - permissionMode(可选): default/acceptEdits/plan/bypassPermissions/dontAsk，缺省 default
//   - background(可选): 强制后台
//
// body 是子 Agent 的系统提示（SystemPrompt），定义它的身份、职责和工作风格。
type Definition struct {
	Name            string          // frontmatter.name（role name）
	Description     string          // frontmatter.description（one-line summary）
	Tools           []string        // frontmatter.tools 白名单；空=不收窄
	DisallowedTools []string        // frontmatter.disallowedTools 黑名单
	Model           string          // "haiku"/"sonnet"/"opus"/"inherit"；缺省 "inherit"
	MaxTurns        int             // 最大迭代轮数；0=沿用全局默认 maxIterations(25)
	PermissionMode  permission.Mode // 权限模式
	DontAsk         bool            // 是否启用"绕过 Ask"的兜底模式（permissionMode=dontAsk 时 true）
	Background      bool            // 强制后台（Agent 工具忽略 run_in_background 参数）
	SystemPrompt    string          // Markdown body（去 frontmatter 后的全文）
	FilePath        string          // 定义文件绝对路径（调试用）
	Source          Source          // 来源：builtin/user/project/plugin
}

// IsFork 判断是否为 Fork 路径的临时 Definition（spec F22-F24）。
// Fork Definition 的 Name 为 "__fork__"，表示子 Agent 从父对话克隆而来。
func (d *Definition) IsFork() bool {
	return d.Name == "__fork__"
}
