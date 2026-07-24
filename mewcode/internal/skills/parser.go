package skills

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// parseSkillDir 解析单个 Skill 目录，返回 *Skill。
// 目录必须包含 SKILL.md；解析失败返回 nil + error。
func parseSkillDir(dir string, source SkillSource) (*Skill, error) {
	mdPath := filepath.Join(dir, "SKILL.md")
	meta, body, err := parseSkillMD(mdPath)
	if err != nil {
		return nil, err
	}
	return &Skill{
		Meta:      meta,
		PromptBody: body,
		SourceDir: dir,
		Source:    source,
	}, nil
}

// loadSkillBody 从磁盘强制重读 SKILL.md 的 body。
// 用于阶段 2（热重载）：每次执行时获取最新 SOP 指令。
func loadSkillBody(s *Skill) error {
	_, body, err := parseSkillMD(filepath.Join(s.SourceDir, "SKILL.md"))
	if err != nil {
		return err
	}
	s.PromptBody = body
	return nil
}

// parseSkillMD 解析单个 SKILL.md 文件，返回 frontmatter 和 body。
// frontmatter 是两行 "---" 之间的 YAML，body 是之后的内容。
func parseSkillMD(path string) (SkillMeta, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SkillMeta{}, "", fmt.Errorf("读取 SKILL.md 失败 %s: %w", path, err)
	}

	content := string(data)
	meta, body, err := splitFrontmatter(content)
	if err != nil {
		return SkillMeta{}, "", fmt.Errorf("解析 frontmatter 失败 %s: %w", path, err)
	}

	// 校验 name 合法性
	if meta.Name == "" {
		return SkillMeta{}, "", fmt.Errorf("SKILL.md 缺少必填字段 name: %s", path)
	}
	if strings.ContainsAny(meta.Name, " \t\n\r/") {
		return SkillMeta{}, "", fmt.Errorf("Skill name 不能包含空格或斜杠: %q (%s)", meta.Name, path)
	}
	// 强制转小写（与 command registry 的约定一致）
	meta.Name = strings.ToLower(meta.Name)

	// 校验 mode 取值；非法值打 warning 后按 inline 处理
	switch meta.Mode {
	case "", "inline", "fork":
		// 合法
	default:
		log.Printf("[skills] warn: Skill %q mode=%q 不合法，降级为 inline", meta.Name, meta.Mode)
		meta.Mode = "inline"
	}

	// 校验 fork_context 取值；非法值降级为 "none"
	switch meta.ForkContext {
	case "", "none", "recent", "full":
		// 合法
	default:
		log.Printf("[skills] warn: Skill %q fork_context=%q 不合法，降级为 none", meta.Name, meta.ForkContext)
		meta.ForkContext = "none"
	}

	return meta, body, nil
}

// splitFrontmatter 从 Markdown 内容中分离 YAML frontmatter 和 body。
// frontmatter 以 "---" 开头行和闭合行界定（闭合的 "---" 必须独占一行）；
// 无 frontmatter 时返回空 meta 和完整 body。
//
// 已知局限：若 YAML 多行字符串值内含 "\n---\n"，会被误判为闭合标记。
// 对于 SKILL.md 的典型用法（简短的 key-value frontmatter），此边界情况极少触发。
func splitFrontmatter(content string) (SkillMeta, string, error) {
	// 不支持前导空白——"---" 必须在第一列
	if !strings.HasPrefix(content, "---") {
		return SkillMeta{}, content, nil
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
			frontmatterText := afterFirst[:len(afterFirst)-4]
			var meta SkillMeta
			if err := yaml.Unmarshal([]byte(frontmatterText), &meta); err != nil {
				return SkillMeta{}, "", fmt.Errorf("YAML 解析失败: %w", err)
			}
			return meta, "", nil
		}
		// 没有闭合 delimeter，整个内容视作 body
		return SkillMeta{}, content, nil
	}

	frontmatterText := afterFirst[:endIdx]
	body := strings.TrimSpace(afterFirst[endIdx+5:]) // +5 跳过 "\n---\n"

	var meta SkillMeta
	if err := yaml.Unmarshal([]byte(frontmatterText), &meta); err != nil {
		return SkillMeta{}, "", fmt.Errorf("YAML 解析失败: %w", err)
	}

	return meta, body, nil
}
