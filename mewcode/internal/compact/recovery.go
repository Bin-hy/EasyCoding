package compact

import (
	"encoding/json"
	"fmt"
	"strings"

	"mewcode/internal/llm"
)

// boundaryNotice 固定边界提示文案。
const boundaryNotice = `## 重要提示

请注意：以上摘要仅用于提供上下文脉络。如果你需要获取文件的完整内容、确切的错误信息、或用户的原始措辞，请使用文件读取工具重新读取对应路径。不要依据摘要中的描述做代码推断或猜测。`

// BuildRecoveryAttachment 构造摘要后的恢复三段内容。
// snapshot 由调用方在摘要入口一次性拍好，避免渲染期间状态漂移。
// toolDefs 必须与下一次 Stream 请求的 Request.Tools 来自同一引用。
func BuildRecoveryAttachment(snapshot []FileReadRecord, toolDefs []llm.ToolDefinition) string {
	var b strings.Builder

	// 第一段：最近读过的文件快照
	b.WriteString("## 最近读过的文件\n")
	limit := recoveryFileLimit
	if len(snapshot) < limit {
		limit = len(snapshot)
	}
	if limit == 0 {
		b.WriteString("(无)\n")
	} else {
		for i := 0; i < limit; i++ {
			b.WriteString(renderFileBlock(snapshot[i]))
		}
	}

	// 第二段：当前可用工具列表
	b.WriteString("\n## 当前可用工具\n")
	b.WriteString(renderToolsBlock(toolDefs))

	// 第三段：边界提示
	b.WriteString("\n")
	b.WriteString(boundaryNotice)

	return b.String()
}

// renderFileBlock 渲染单个文件快照：路径 / 时间戳 / 内容片段。
func renderFileBlock(rec FileReadRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s\n", rec.Path)
	fmt.Fprintf(&b, "[read at] %s\n", rec.Timestamp.Format("2006-01-02T15:04:05Z07:00"))

	content := rec.Content
	charLimit := int(float64(recoveryTokensPerFile) * estimateCharsPerToken)
	if len(content) > charLimit {
		content = content[:charLimit]
		b.WriteString(content)
		b.WriteString("\n(content truncated)\n")
	} else {
		b.WriteString(content)
		if !strings.HasSuffix(content, "\n") {
			b.WriteString("\n")
		}
	}

	return b.String()
}

// renderToolsBlock 渲染工具列表：每行工具名 + 用途 + 参数 schema 摘要。
func renderToolsBlock(defs []llm.ToolDefinition) string {
	if len(defs) == 0 {
		return "(无)\n"
	}

	var b strings.Builder
	for _, d := range defs {
		fmt.Fprintf(&b, "- %s: %s\n", d.Name, d.Description)
		schema, err := json.Marshal(d.InputSchema)
		if err == nil {
			fmt.Fprintf(&b, "  schema: %s\n", string(schema))
		}
	}
	return b.String()
}
