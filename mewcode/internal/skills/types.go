// Package skills 提供 Skill 系统的核心数据结构、解析、加载、执行与激活态管理。
package skills

// SkillMeta Skill 的 frontmatter 元信息。
// 字段与 SKILL.md 的 YAML frontmatter 一一对应。
type SkillMeta struct {
	Name         string   `yaml:"name"`                    // 唯一标识名，用于 /<name> 与 LoadSkill 查找
	Description  string   `yaml:"description"`             // 一句话说明，注入第一阶段 system prompt
	AllowedTools []string `yaml:"allowed_tools,omitempty"` // 工具白名单；空表示不限制
	Mode         string   `yaml:"mode,omitempty"`          // "inline" / "fork"；空或 "inline" 视作 inline
	ForkContext  string   `yaml:"fork_context,omitempty"`  // "none" / "recent" / "full"；仅 fork 模式生效，默认 "none"
	Model        string   `yaml:"model,omitempty"`         // 可选：指定 fork 模式使用的模型
}

// SkillSource 表示 Skill 的来源：用户级或项目级。
type SkillSource int

const (
	SourceUser    SkillSource = iota // ~/.mewcode/skills/
	SourceProject                    // <workDir>/.mewcode/skills/
)

// String 返回 SkillSource 的英文标识。
func (s SkillSource) String() string {
	switch s {
	case SourceUser:
		return "user"
	case SourceProject:
		return "project"
	default:
		return "unknown"
	}
}

// Skill 表示一个已加载的能力包。
type Skill struct {
	Meta       SkillMeta   // frontmatter 元信息
	PromptBody string      // SKILL.md 去 frontmatter 后的正文；阶段 1 为空，阶段 2 / 执行时填充
	SourceDir  string      // SKILL.md 所在目录的绝对路径
	Source     SkillSource // 来源：用户级或项目级
}

// ActiveEntry 记录一个已激活 Skill 的名字和正文。
type ActiveEntry struct {
	Name string // Skill 名
	Body string // 激活时的正文快照
}
