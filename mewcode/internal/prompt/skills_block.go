package prompt

import (
	"fmt"
	"strings"
)

// SkillCatalogItem 第一阶段 Skill 目录条目。
type SkillCatalogItem struct {
	Name        string
	Description string
}

// ActiveSkillEntry 第二阶段活跃 Skill 条目。
type ActiveSkillEntry struct {
	Name string
	Body string
}

// RenderSkillsCatalog 渲染「可用 Skill 列表」段，注入 system prompt。
// items 空时返回空字符串。
func RenderSkillsCatalog(items []SkillCatalogItem) string {
	if len(items) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## 可用 Skill（调用 LoadSkill 工具激活）\n\n")
	sb.WriteString("以下 Skill 可通过 LoadSkill 工具按需激活。激活后 SOP 将钉在环境上下文最显眼位置。\n\n")
	for _, item := range items {
		sb.WriteString(fmt.Sprintf("- **/%s**: %s\n", item.Name, item.Description))
	}
	return sb.String()
}

// RenderActiveSkillsBlock 渲染「活跃 Skill」环境注入块。
// entries 空时返回空字符串。
func RenderActiveSkillsBlock(entries []ActiveSkillEntry) string {
	if len(entries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Active Skills\n\n")
	sb.WriteString("以下 Skill 已激活，其 SOP 指令优先于通用系统指令：\n\n")
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("### Skill: %s\n\n%s\n\n", e.Name, e.Body))
	}
	return strings.TrimRight(sb.String(), "\n")
}
