package permission

import (
	"fmt"
	"regexp"
)

// Matcher 是规则匹配的统一接口。
// 四种实现：matcherExact（精确）、matcherGlob（glob 通配）、
// matcherRegex（正则）、matcherNot（反向取反）。
type Matcher interface {
	// Match 判断目标串是否匹配当前模式。
	Match(s string) bool
	// String 返回模式的可读描述，供调试与 /hooks 输出使用。
	String() string
}

// matcherExact 精确匹配：目标串与模式串完全相等。
type matcherExact struct {
	value string
}

func (m *matcherExact) Match(s string) bool {
	return s == m.value
}

func (m *matcherExact) String() string {
	return "=" + m.value
}

// matcherGlob glob 通配匹配。
// - isCommand=true：使用命令串 glob（* 匹配任意字符序列含空格，** 等价 *）。
// - isCommand=false：使用文件路径 glob（按 / 分段，* 段内匹配，** 跨段）。
type matcherGlob struct {
	pattern   string
	isCommand bool
}

func (m *matcherGlob) Match(s string) bool {
	// matchPattern 的 isFile 参数：文件路径传 true，命令串传 false。
	return matchPattern(m.pattern, s, !m.isCommand)
}

func (m *matcherGlob) String() string {
	return m.pattern
}

// matcherRegex 正则匹配：使用 Go regexp 包编译的模式。
type matcherRegex struct {
	re  *regexp.Regexp
	src string // 原始正则模式串，供 String() 展示
}

func (m *matcherRegex) Match(s string) bool {
	return m.re.MatchString(s)
}

func (m *matcherRegex) String() string {
	return "~" + m.src
}

// matcherNot 反向匹配：对内层 Matcher 的结果取反。
// 支持嵌套，如 !=value、!~regex、!glob 均合法。
type matcherNot struct {
	inner Matcher
}

func (m *matcherNot) Match(s string) bool {
	return !m.inner.Match(s)
}

func (m *matcherNot) String() string {
	return "!" + m.inner.String()
}

// CompileMatcher 解析单条匹配描述串，返回对应的 Matcher 或 error。
//
// 描述串前缀规则：
//   - "=value"  → 精确匹配（整串相等）
//   - "~regex"  → 正则匹配
//   - "!inner"  → 反向匹配（对内层 Matcher 取反）
//   - "value"   → glob 通配（缺省类型，沿用现有 wildcard/matchPath 语义）
//
// isCommand 参数仅在缺省 glob 类型时生效，决定使用命令串 glob 还是文件路径 glob。
// 空串返回错误。
func CompileMatcher(pattern string, isCommand bool) (Matcher, error) {
	if pattern == "" {
		return nil, fmt.Errorf("empty matcher pattern")
	}

	switch pattern[0] {
	case '=':
		// 精确匹配：去除 = 前缀后匹配剩余串
		return &matcherExact{value: pattern[1:]}, nil

	case '~':
		// 正则匹配：去除 ~ 前缀后编译正则
		src := pattern[1:]
		re, err := regexp.Compile(src)
		if err != nil {
			return nil, fmt.Errorf("invalid regex pattern %q: %w", src, err)
		}
		return &matcherRegex{re: re, src: src}, nil

	case '!':
		// 反向匹配：去除 ! 前缀后递归解析内层
		inner, err := CompileMatcher(pattern[1:], isCommand)
		if err != nil {
			return nil, fmt.Errorf("invalid not pattern: %w", err)
		}
		return &matcherNot{inner: inner}, nil

	default:
		// 缺省 glob 通配
		return &matcherGlob{pattern: pattern, isCommand: isCommand}, nil
	}
}
