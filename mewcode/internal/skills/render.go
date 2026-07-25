package skills

import (
	"fmt"
	"strings"
)

// RenderBody 把 Skill body 渲染为最终注入文本。
// 1. 替换所有 $ARGUMENTS 占位符
// 2. 若无占位符且 args 非空，在末尾追加 <args>
// 3. 若 AllowedTools 非空，在顶部插入工具白名单提示
func RenderBody(skill *Skill, args string) string {
	body := skill.PromptBody

	// 工具白名单顶部提示
	if len(skill.Meta.AllowedTools) > 0 {
		toolsHint := fmt.Sprintf(
			"This skill is designed to use only these tools: %s. Prefer them over other tools when possible.\n\n---\n\n",
			strings.Join(skill.Meta.AllowedTools, ", "),
		)
		body = toolsHint + body
	}

	// 占位符替换
	if strings.Contains(body, "$ARGUMENTS") {
		body = strings.ReplaceAll(body, "$ARGUMENTS", args)
	} else if args != "" {
		// 无占位符但传了 args，追加到末尾
		body += "\n\n## User Request\n\n" + args
	}

	return body
}
