package compact

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mewcode/internal/llm"
)

// spillSingle 把单条 tool_result 内容写入 SpillDir/<tool_use_id>。
// 幂等：文件已存在则不重写、不报错。
func spillSingle(session *SessionContext, toolUseID, content string) error {
	path := filepath.Join(session.SpillDir, toolUseID)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// headPreview 取 content 的前 20 行或前 2048 字节中的较短者。
func headPreview(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) > previewHeadLines {
		lines = lines[:previewHeadLines]
	}
	head := strings.Join(lines, "\n")
	if len(head) > previewHeadBytes {
		head = head[:previewHeadBytes]
	}
	return head
}

// buildPreview 构造工具结果替换体字符串。
func buildPreview(originalBytes int, head, spillPath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[content offloaded] original size: %d bytes\n", originalBytes)
	fmt.Fprintf(&b, "[saved to] %s\n", spillPath)
	b.WriteString("[head preview]\n")
	b.WriteString(head)
	b.WriteString("\n\n完整内容已保存到上述路径，如需查看请用文件读取工具读取该路径，不要凭头部预览猜测全文")
	return b.String()
}

// toolResultItem 表示待处理的工具结果项。
type toolResultItem struct {
	idx     int    // 在 ToolResults 切片中的位置
	id      string // tool_use_id
	content string // Content 字符串
	size    int    // len(Content)
}

// OffloadAndSnip 遍历对话中的 RoleTool 消息，对其 ToolResults 做单条/聚合落盘与预览替换。
// 返回新的 []llm.Message，不修改入参。
func OffloadAndSnip(msgs []llm.Message, state *ContentReplacementState, session *SessionContext) ([]llm.Message, error) {
	out := make([]llm.Message, len(msgs))
	for i := range msgs {
		out[i] = msgs[i]
		if len(msgs[i].ToolCalls) > 0 {
			out[i].ToolCalls = make([]llm.ToolCall, len(msgs[i].ToolCalls))
			copy(out[i].ToolCalls, msgs[i].ToolCalls)
		}
		if len(msgs[i].ToolResults) > 0 {
			out[i].ToolResults = make([]llm.ToolResult, len(msgs[i].ToolResults))
			copy(out[i].ToolResults, msgs[i].ToolResults)
		}
	}

	for m := range out {
		msg := &out[m]
		if msg.Role != llm.RoleTool || len(msg.ToolResults) == 0 {
			continue
		}

		// 第一步：对已决策项直接取账本结果，收集未决策项
		var undecided []toolResultItem
		for j := range msg.ToolResults {
			tr := &msg.ToolResults[j]
			if state.IsSeen(tr.ToolCallID) {
				// 已决策：用 DecideOnce（内部短路返回存量结果）拿结果
				tr.Content = state.DecideOnce(tr.ToolCallID, tr.Content, func() (string, string) {
					return "kept", ""
				})
			} else {
				undecided = append(undecided, toolResultItem{
					idx:     j,
					id:      tr.ToolCallID,
					content: tr.Content,
					size:    len(tr.Content),
				})
			}
		}

		if len(undecided) == 0 {
			continue
		}

		// 第二步：未决策项按字节倒序排序
		sort.Slice(undecided, func(i, j int) bool {
			return undecided[i].size > undecided[j].size
		})

		// 第三步：计算剩余聚合字节（仅未决策项）
		remaining := 0
		for _, it := range undecided {
			remaining += it.size
		}

		// 第四步：按倒序处理每个未决策项
		for _, it := range undecided {
			needSpill := it.size > singleResultLimit || remaining > messageAggregateLimit

			tr := &msg.ToolResults[it.idx]
			if needSpill {
				state.DecideOnce(it.id, it.content, func() (string, string) {
					if err := spillSingle(session, it.id, it.content); err != nil {
						return "skip", ""
					}
					spillPath := filepath.Join(session.SpillDir, it.id)
					preview := buildPreview(it.size, headPreview(it.content), spillPath)
					return "replaced", preview
				})
				// 账本已写入，Content 需要在 DecideOnce 之后额外更新
				// DecideOnce 返回 preview 或原 content
				// 因为已经写入账本，再次调用 DecideOnce 会短路返回 preview：
				tr.Content = state.DecideOnce(it.id, it.content, func() (string, string) {
					return "kept", ""
				})
				remaining -= it.size
			} else {
				state.DecideOnce(it.id, it.content, func() (string, string) {
					return "kept", ""
				})
				// Content 保持原文
			}
		}
	}

	return out, nil
}
