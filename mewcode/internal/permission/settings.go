package permission

import (
	"encoding/json"
	"os"
	"path/filepath"

	"mewcode/internal/llm"

	"gopkg.in/yaml.v3"
)

// Settings 单个 YAML 文件的权限配置结构（F4）。
type Settings struct {
	DefaultMode string `yaml:"defaultMode"` // 可选：default/acceptEdits/plan/bypassPermissions
	Permissions struct {
		Allow []string `yaml:"allow"`
		Deny  []string `yaml:"deny"`
	} `yaml:"permissions"`
}

// loadSettings 从路径读取 YAML 配置。
// 文件不存在 → 空 Settings、nil err；
// TODO: 后续所有 YAML 解析都依赖 go-yaml；当前 settings.yaml 文件系统交互清晰、单测直接覆盖。
// yaml.Unmarshal 失败 → 零值 + err（调用方降级跳过该文件，N5）。
//
//goland:noinspection ALL
func loadSettings(path string) (Settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Settings{}, nil
		}
		return Settings{}, err
	}

	var s Settings
	if err := yaml.Unmarshal(data, &s); err != nil {
		return Settings{}, err
	}
	return s, nil
}

// toRuleSet 将 Settings 中的 allow/deny 字符串转为 RuleSet。
// 非法条目跳过（N5），不报错。
func toRuleSet(s Settings) RuleSet {
	var rs RuleSet
	for _, a := range s.Permissions.Allow {
		if r, ok := parseRuleWithAllow(a, true); ok {
			rs.allow = append(rs.allow, r)
		}
	}
	for _, d := range s.Permissions.Deny {
		if r, ok := parseRuleWithAllow(d, false); ok {
			rs.deny = append(rs.deny, r)
		}
	}
	return rs
}

// friendlyName 将内部工具名映射为面向用户的友好名。
// 未知工具名原样返回。
func friendlyName(internal string) string {
	switch internal {
	case "bash":
		return "Bash"
	case "read_file":
		return "Read"
	case "write_file":
		return "Write"
	case "edit_file":
		return "Edit"
	case "glob":
		return "Glob"
	case "grep":
		return "Grep"
	default:
		return internal
	}
}

// categorize 根据内部工具名和只读标记判定工具类别。
// readOnly 优先；未知工具（readOnly==false）归 CategoryExec（N7 最严）。
func categorize(internal string, readOnly bool) Category {
	if readOnly {
		return CategoryRead
	}
	switch internal {
	case "write_file", "edit_file":
		return CategoryWrite
	case "bash":
		return CategoryExec
	default:
		// 未注册工具归命令执行类（最严）
		return CategoryExec
	}
}

// extractTarget 从 ToolCall.Input 提取目标串（命令串或文件路径）。
// 返回 (target, isFile, ok)。
// - ok=false：完全无法解析（JSON 解析失败或缺必填字段）。
// - isFile=true：文件类工具，target 为文件路径或搜索根。
// - isFile=false：命令执行类，target 为命令串。
//
//goland:noinspection ALL
func extractTarget(call llm.ToolCall) (target string, isFile bool, ok bool) {
	if len(call.Input) == 0 {
		return "", false, false
	}

	// 解析为通用 map
	var m map[string]any
	if err := json.Unmarshal(call.Input, &m); err != nil {
		return "", false, false
	}

	switch call.Name {
	case "read_file", "write_file", "edit_file":
		v, exists := m["path"]
		if !exists {
			return "", true, false
		}
		s, ok2 := v.(string)
		if !ok2 || s == "" {
			return "", true, false // path 存在但为空 → 判为不可解析
		}
		return s, true, true

	case "glob", "grep":
		v, exists := m["path"]
		if !exists {
			return ".", true, true // 默认搜索根为 "."
		}
		s, ok2 := v.(string)
		if !ok2 {
			return ".", true, true // path 字段存在但类型不匹配 → 用默认值
		}
		if s == "" {
			return ".", true, true
		}
		return s, true, true

	case "bash":
		v, exists := m["command"]
		if !exists {
			// 缺 command → 视为空串，不命中黑名单，落 Ask
			return "", false, false
		}
		s, ok2 := v.(string)
		if !ok2 {
			return "", false, false
		}
		return s, false, true

	default:
		// 未知工具
		return "", false, false
	}
}

// settingsDir 返回项目根下的 .mewcode 配置目录。
func settingsDir(root string) string {
	return filepath.Join(root, ".mewcode")
}

// userSettingsPath 返回用户级配置文件路径 (~/.mewcode/settings.yaml)。
func userSettingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".mewcode", "settings.yaml")
}
