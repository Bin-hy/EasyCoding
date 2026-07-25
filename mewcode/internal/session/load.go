package session

import (
	"encoding/json"
	"os"

	"mewcode/internal/llm"
)

// LoadSession 从 conversation.jsonl 恢复消息列表。
// 从最后一个 compact 标记之后加载，跳过坏行，截断孤立工具调用。
func LoadSession(sessionDir string) ([]llm.Message, error) {
	path := sessionDir + "/conversation.jsonl"
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	//nolint:errcheck
	defer f.Close()

	// 第一遍扫描：compact 标记重置 entries，之后收集的消息即为最终历史
	var entries []Entry
	dec := json.NewDecoder(f)
	for dec.More() {
		var entry Entry
		if err := dec.Decode(&entry); err != nil {
			// 跳过坏行，继续
			continue
		}
		if entry.Type == "compact" {
			entries = nil // 清空，从 compact 之后重新开始
			continue
		}
		entries = append(entries, entry)
	}

	// 第二遍（实际上是在第一遍中已经过滤），从 entries 构建 messages
	msgs := entriesToMessages(entries)

	// 截断孤立工具调用
	msgs = TruncateOrphanedToolCalls(msgs)

	return msgs, nil
}

// entriesToMessages 将 Entry 切片转为 llm.Message 切片。
func entriesToMessages(entries []Entry) []llm.Message {
	var msgs []llm.Message
	for _, e := range entries {
		if e.Type == "compact" {
			continue
		}
		msgs = append(msgs, llm.Message{
			Role:        e.Role,
			Content:     e.Content,
			ToolCalls:   e.ToolCalls,
			ToolResults: e.ToolResults,
		})
	}
	return msgs
}

// TruncateOrphanedToolCalls 如果最后一条消息是带 tool_calls 的 assistant，
// 但后面没有对应的 tool 消息，则截断该条。
func TruncateOrphanedToolCalls(msgs []llm.Message) []llm.Message {
	if len(msgs) == 0 {
		return msgs
	}
	last := msgs[len(msgs)-1]
	if last.Role == llm.RoleAssistant && len(last.ToolCalls) > 0 {
		// 最后一条是带工具调用的 assistant，没有后续 tool 结果 → 截断
		return msgs[:len(msgs)-1]
	}
	return msgs
}

// LoadSessionRaw 加载整个 JSONL 的所有 Entry，不做 compact 过滤。
// 供测试使用。
func LoadSessionRaw(sessionDir string) ([]Entry, error) {
	path := sessionDir + "/conversation.jsonl"
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	//nolint:errcheck
	defer f.Close()

	var entries []Entry
	dec := json.NewDecoder(f)
	for dec.More() {
		var entry Entry
		if err := dec.Decode(&entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}
