package subagent

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"mewcode/internal/permission"
)

// agentNameRegex 校验 name 字段合法性：小写字母/数字/连字符，长度 1-32。
// 允许大写字母以兼容现有 Explore/Plan 等内置角色名。
var agentNameRegex = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9\-_]{0,31}$`)

// validModels 为合法模型值集合。
var validModels = map[string]bool{
	"":        true,
	"inherit": true,
	"haiku":   true,
	"sonnet":  true,
	"opus":    true,
}

// agentFM 是 frontmatter 的 YAML 反序列化目标结构体（spec F4）。
type agentFM struct {
	Name            string   `yaml:"name"`
	Description     string   `yaml:"description"`
	Tools           []string `yaml:"tools,omitempty"`
	DisallowedTools []string `yaml:"disallowedTools,omitempty"`
	Model           string   `yaml:"model,omitempty"`
	MaxTurns        int      `yaml:"maxTurns,omitempty"`
	PermissionMode  string   `yaml:"permissionMode,omitempty"`
	Background      bool     `yaml:"background,omitempty"`
}

// ParseDefinition 从字节切片解析一个 Agent 定义（spec F4,F5）。
// data 是完整的 Markdown 文件内容；filePath 和 source 用于调试与来源追踪。
// frontmatter 不合法时返回 error；合法但部分字段不匹配时 stderr 警告并 fallback 到默认值。
func ParseDefinition(data []byte, filePath string, source Source) (*Definition, error) {
	meta, body, err := parseFrontmatterAndBody(string(data))
	if err != nil {
		return nil, fmt.Errorf("解析 %s frontmatter 失败: %w", filePath, err)
	}

	var fm agentFM
	if err := yaml.Unmarshal([]byte(meta), &fm); err != nil {
		return nil, fmt.Errorf("解析 %s YAML 失败: %w", filePath, err)
	}

	// 校验必填字段
	if fm.Name == "" {
		return nil, fmt.Errorf("%s: frontmatter 缺少必填字段 name", filePath)
	}
	if !agentNameRegex.MatchString(fm.Name) {
		return nil, fmt.Errorf("%s: name %q 不合法（需匹配 %s）", filePath, fm.Name, agentNameRegex.String())
	}
	if fm.Description == "" {
		return nil, fmt.Errorf("%s: frontmatter 缺少必填字段 description", filePath)
	}

	// 校验 model 字段；非法值 stderr 警告并 fallback 到 inherit
	model := fm.Model
	if model == "" {
		model = "inherit"
	}
	if !validModels[model] {
		log.Printf("[subagent] warn: %s 中 model=%q 不合法，降级为 inherit", filePath, fm.Model)
		model = "inherit"
	}

	// 解析 permissionMode
	var (
		pMode   permission.Mode
		dontAsk bool
	)
	switch strings.ToLower(fm.PermissionMode) {
	case "":
		pMode = permission.ModeDefault
	case "dontask":
		// dontAsk 是子 Agent 专属模式——自动批准所有规则未命中的工具（spec F4）
		pMode = permission.ModeDefault
		dontAsk = true
	default:
		if m, ok := permission.ParseMode(fm.PermissionMode); ok {
			pMode = m
		} else {
			log.Printf("[subagent] warn: %s 中 permissionMode=%q 不合法，降级为 default", filePath, fm.PermissionMode)
			pMode = permission.ModeDefault
		}
	}

	// maxTurns: 0 表示沿用全局默认
	maxTurns := fm.MaxTurns
	if maxTurns < 0 {
		maxTurns = 0
	}

	return &Definition{
		Name:            fm.Name,
		Description:     fm.Description,
		Tools:           fm.Tools,
		DisallowedTools: fm.DisallowedTools,
		Model:           model,
		MaxTurns:        maxTurns,
		PermissionMode:  pMode,
		DontAsk:         dontAsk,
		Background:      fm.Background,
		SystemPrompt:    strings.TrimSpace(body),
		FilePath:        filePath,
		Source:          source,
	}, nil
}

// ParseFile 从文件路径解析一个 Agent 定义（spec F5）。
func ParseFile(path string, source Source) (*Definition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	return ParseDefinition(data, path, source)
}

// parseFrontmatterAndBody 从 Markdown 内容中分离 YAML frontmatter 和 body。
// frontmatter 以 "---" 开头行和闭合行界定（闭合的 "---" 必须独占一行）；
// 无 frontmatter 时返回空 meta 和完整 body。
//
// 这与 skills/parser.go 的 splitFrontmatter 逻辑几乎一致，但 subagent 独立实现一份
// 以避免循环依赖（plan.md 技术决策：Markdown 解析器复用）。
func parseFrontmatterAndBody(content string) (meta string, body string, err error) {
	// 跳过 UTF-8 BOM
	content = strings.TrimPrefix(content, "\xef\xbb\xbf")
	content = strings.TrimPrefix(content, "ï»¿")

	// 不支持前导空白——"---" 必须在第一列
	if !strings.HasPrefix(content, "---") {
		return "", content, nil
	}

	// 查找首行 "---" 之后的内容
	afterFirst := content[3:]
	// 跳过紧随的 \n（如果有）
	afterFirst = strings.TrimPrefix(afterFirst, "\n")

	// 查找独占一行的闭合 "---"
	endIdx := strings.Index(afterFirst, "\n---\n")
	if endIdx == -1 {
		// 尝试以 "\n---" 结尾（文件末尾）
		if strings.HasSuffix(afterFirst, "\n---") {
			return afterFirst[:len(afterFirst)-4], "", nil
		}
		// 没有闭合 delimiter，整个内容视作 body
		return "", content, nil
	}

	frontmatterText := afterFirst[:endIdx]
	bodyText := strings.TrimSpace(afterFirst[endIdx+5:]) // +5 跳过 "\n---\n"

	return frontmatterText, bodyText, nil
}
