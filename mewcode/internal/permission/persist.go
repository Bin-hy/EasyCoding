package permission

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mewcode/internal/llm"

	"gopkg.in/yaml.v3"
)

// ruleFor 根据一次工具调用生成精确规则（不含通配）。
// 返回 (内存 Rule, YAML 字符串, 是否成功)。
// 命令串中的 glob 元字符（*, ?, [, ]）需转义，防止规则被意外泛化。
func ruleFor(call llm.ToolCall) (Rule, string, bool) {
	friendly := friendlyName(call.Name)
	target, isFile, ok := extractTarget(call)
	if !ok {
		return Rule{}, "", false
	}

	var pattern string
	if isFile {
		// 文件类：精确路径（相对路径需转为绝对/相对表述）
		// 直接使用 target 作为 pattern
		pattern = target
	} else {
		// 命令类：精确命令串，转义 glob 元字符
		pattern = escapeGlob(target)
	}

	yamlStr := fmt.Sprintf("%s(%s)", friendly, pattern)
	return Rule{Tool: friendly, Pattern: pattern, Allow: true}, yamlStr, true
}

// escapeGlob 转义命令串中的 glob 元字符，使规则匹配为字面匹配。
func escapeGlob(s string) string {
	// 把 * ? [ ] 转义为字面
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "*", "\\*")
	s = strings.ReplaceAll(s, "?", "\\?")
	s = strings.ReplaceAll(s, "[", "\\[")
	s = strings.ReplaceAll(s, "]", "\\]")
	return s
}

// PersistLocalAllow 把一次工具调用的精确 allow 规则写入本地层配置文件。
// 同步更新内存中的 local 规则集。
//
//goland:noinspection ALL
func (e *Engine) PersistLocalAllow(call llm.ToolCall) error {
	_, yamlStr, ok := ruleFor(call)
	if !ok {
		return fmt.Errorf("无法为工具调用生成规则: %s", call.Name)
	}

	// 确保目录存在
	dir := filepath.Dir(e.localPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}

	// 加载现有配置
	s, _ := loadSettings(e.localPath)

	// 去重：检查是否已存在
	for _, a := range s.Permissions.Allow {
		if strings.TrimSpace(a) == yamlStr {
			return nil // 已存在，幂等
		}
	}

	// 追加
	s.Permissions.Allow = append(s.Permissions.Allow, yamlStr)

	// 写回
	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}

	if err := os.WriteFile(e.localPath, data, 0o644); err != nil {
		return fmt.Errorf("写入本地配置失败: %w", err)
	}

	// 同步更新内存
	if r, ok := parseRuleWithAllow(yamlStr, true); ok {
		e.local.allow = append(e.local.allow, r)
	}

	return nil
}
