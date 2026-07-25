package permission

import (
	"fmt"
	"strings"
)

// Rule 单条权限规则：工具名(模式) → allow 或 deny。
//
// 匹配语法升级（v2）：
//   - "=value"  → 精确匹配（整串相等）
//   - "~regex"  → 正则匹配
//   - "!inner"  → 反向匹配（对内层 Matcher 取反，支持 !=value、!~regex、!glob）
//   - "value"   → glob 通配（缺省类型，向后兼容）
//
// Matcher 为 nil 表示匹配该工具全部调用（pattern 为空串）。
type Rule struct {
	Tool    string  // 友好名：Bash/Read/Write/Edit/Glob/Grep
	Matcher Matcher // 编译后的匹配器；nil 表示全匹配
	Allow   bool    // true=allow，false=deny
	raw     string  // 原始模式串，供错误日志与调试
}

// RuleSet 单层规则集（一个配置文件或会话内存）。
type RuleSet struct {
	allow []Rule
	deny  []Rule
}

// parseRule 解析 "Tool(pattern)" 或 "Tool" 格式的规则字符串。
//
// 返回 (Rule, error)；error 非 nil 表示解析或 Matcher 编译失败。
// 格式非法（括号不配对等）返回 error；Matcher 编译失败（如非法正则）也返回 error。
// 成功时 Rule.Tool 非空，Rule.Matcher 非 nil 除非 pattern 为空（全匹配）。
func parseRule(s string) (Rule, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Rule{}, fmt.Errorf("empty rule string")
	}

	// 查找括号位置
	parenOpen := strings.IndexByte(s, '(')
	parenClose := strings.LastIndexByte(s, ')')

	var tool, pattern string

	if parenOpen == -1 && parenClose == -1 {
		// 不带模式：Tool → 匹配该工具全部调用
		tool = s
		pattern = ""
	} else if parenOpen > 0 && parenClose > parenOpen && parenClose == len(s)-1 {
		// 带模式：Tool(pattern)
		tool = s[:parenOpen]
		pattern = s[parenOpen+1 : parenClose]
	} else {
		// 括号不配对
		return Rule{}, fmt.Errorf("invalid rule format: %s", s)
	}

	if tool == "" {
		return Rule{}, fmt.Errorf("empty tool name in rule: %s", s)
	}

	// 空 pattern → nil Matcher（全匹配）
	if pattern == "" {
		return Rule{Tool: tool, raw: pattern}, nil
	}

	// 编译 Matcher：Bash 工具走命令串 glob，其它走文件路径 glob
	isCommand := tool == "Bash"
	m, err := CompileMatcher(pattern, isCommand)
	if err != nil {
		return Rule{}, fmt.Errorf("rule %q parse failed: %w", s, err)
	}

	return Rule{Tool: tool, Matcher: m, raw: pattern}, nil
}

// parseRuleWithAllow 解析规则字符串并指定 allow/deny。
func parseRuleWithAllow(s string, allow bool) (Rule, error) {
	r, err := parseRule(s)
	if err != nil {
		return Rule{}, err
	}
	r.Allow = allow
	return r, nil
}

// matchRule 对单条规则的目标串做匹配。
// r.Matcher == nil 表示全匹配（恒返回 true）。
func matchRule(r Rule, target string) bool {
	if r.Matcher == nil {
		return true
	}
	return r.Matcher.Match(target)
}

// matchPattern 对目标串做模式匹配（底层函数，供 matcherGlob 内部调用）。
//
//   - pattern=="" 恒匹配（全匹配）。
//   - 命令串（isFile=false）：* 匹配任意字符序列（含空格），** 等价于 *，其余字符逐字比对。
//   - 文件路径（isFile=true）：按 / 分段匹配，* 匹配段内任意字符，** 跨任意层级。
func matchPattern(pattern, target string, isFile bool) bool {
	if pattern == "" {
		return true
	}

	if isFile {
		// 文件路径按 / 分段匹配
		return matchFilePattern(pattern, target)
	}

	// 命令串 glob：* 匹配任意字符，** 等价于 *
	return matchCommandPattern(pattern, target)
}

// matchFilePattern 文件路径 glob 匹配：* 段内、** 跨段。
func matchFilePattern(pattern, target string) bool {
	patParts := strings.Split(pattern, "/")
	targetParts := strings.Split(target, "/")
	return matchSegments(patParts, targetParts)
}

// matchCommandPattern 命令串 glob 匹配：* 匹配任意字符序列（含空格），其余字面。
func matchCommandPattern(pattern, target string) bool {
	// 将 pattern 中的 ** 替换为 *（命令串中二者等价）
	pattern = strings.ReplaceAll(pattern, "**", "*")

	pi, ti := 0, 0
	for pi < len(pattern) && ti < len(target) {
		switch pattern[pi] {
		case '*':
			// * 匹配剩余全部
			if pi == len(pattern)-1 {
				return true
			}
			// * 贪婪向前找到下一字符
			next := pattern[pi+1]
			for ti < len(target) && target[ti] != next {
				ti++
			}
			pi++ // 跳过 *
			if ti >= len(target) {
				return false
			}
		case target[ti]:
			pi++
			ti++
		default:
			return false
		}
	}

	// 剩余 pattern 全部是 * 则匹配
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}

	return pi == len(pattern) && ti == len(target)
}

// matchSegments 对分段后的模式与路径做 DP 匹配。
// ** 跨任意层级；* 段内匹配；其余字面。
func matchSegments(pat, path []string) bool {
	if len(pat) == 0 {
		return len(path) == 0
	}

	// 模式只剩一个 **，匹配所有剩余路径
	if len(pat) == 1 && pat[0] == "**" {
		return true
	}

	if len(path) == 0 {
		// path 已耗尽，pat 是否只剩 **
		for _, p := range pat {
			if p != "**" {
				return false
			}
		}
		return true
	}

	if pat[0] == "**" {
		// ** 匹配 0 层（跳过 **）或 1+ 层（跳过 path 首段）
		return matchSegments(pat[1:], path) || matchSegments(pat, path[1:])
	}

	// 段内匹配（使用 filepath.Match 语义兼容的简单 glob）
	matched, _ := matchSegment(pat[0], path[0])
	if !matched {
		return false
	}
	return matchSegments(pat[1:], path[1:])
}

// matchSegment 单段 glob 匹配（支持 * ? 通配）。
func matchSegment(pattern, name string) (bool, error) {
	pi, ni := 0, 0
	for pi < len(pattern) && ni < len(name) {
		switch pattern[pi] {
		case '*':
			// * 匹配剩余全部
			if pi == len(pattern)-1 {
				return true, nil
			}
			// 贪婪匹配到下一字符
			next := pattern[pi+1]
			for ni < len(name) && name[ni] != next {
				ni++
			}
			pi++
		case '?':
			pi++
			ni++
		default:
			if pattern[pi] != name[ni] {
				return false, nil
			}
			pi++
			ni++
		}
	}

	// 剩余 pattern 全是 * 则匹配
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}

	return pi == len(pattern) && ni == len(name), nil
}

// match 在规则集内按 friendly+target 匹配，先 deny 再 allow；
// 命中返回 (Allow|Deny, true)，未命中返回 (_, false)。
func (rs RuleSet) match(friendly, target string, isFile bool) (Decision, bool) {
	// deny 优先
	for _, r := range rs.deny {
		if r.Tool == friendly && matchRule(r, target) {
			return Deny, true
		}
	}

	// allow
	for _, r := range rs.allow {
		if r.Tool == friendly && matchRule(r, target) {
			return Allow, true
		}
	}

	return Decision(0), false
}
