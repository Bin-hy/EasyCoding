package session

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"mewcode/internal/llm"
)

// Entry 是 JSONL 中一行的结构。
type Entry struct {
	Type        string           `json:"type,omitempty"` // "compact" 或空
	Role        string           `json:"role,omitempty"` // "user" / "assistant" / "tool"
	Content     string           `json:"content,omitempty"`
	ToolCalls   []llm.ToolCall   `json:"tool_calls,omitempty"`
	ToolResults []llm.ToolResult `json:"tool_results,omitempty"`
	Timestamp   int64            `json:"ts"`
	Model       string           `json:"model,omitempty"` // 仅首条消息
}

// Writer 负责向 conversation.jsonl 追加写入。
type Writer struct {
	mu   sync.Mutex
	file *os.File
	enc  *json.Encoder
	path string // JSONL 文件的绝对路径
}

// Path 返回 JSONL 文件的绝对路径。
func (w *Writer) Path() string { return w.path }

// NewWriter 在 sessionDir 下创建/打开 conversation.jsonl 用于追加写入。
func NewWriter(sessionDir string) (*Writer, error) {
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建会话目录失败: %w", err)
	}
	path := sessionDir + "/conversation.jsonl"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("打开 JSONL 文件失败: %w", err)
	}
	return &Writer{
		file: f,
		enc:  json.NewEncoder(f),
		path: path,
	}, nil
}

// OpenWriter 打开已有会话的 JSONL 进行追加写入（恢复场景）。
func OpenWriter(sessionDir string) (*Writer, error) {
	path := sessionDir + "/conversation.jsonl"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("打开已有 JSONL 文件失败: %w", err)
	}
	return &Writer{
		file: f,
		enc:  json.NewEncoder(f),
		path: path,
	}, nil
}

// Append 追加一条消息到 JSONL。
// isFirst 为 true 时写入 model 字段。
func (w *Writer) Append(msg llm.Message, model string, isFirst bool) error {
	entry := Entry{
		Role:        msg.Role,
		Content:     msg.Content,
		ToolCalls:   msg.ToolCalls,
		ToolResults: msg.ToolResults,
		Timestamp:   time.Now().Unix(),
	}
	if isFirst && model != "" {
		entry.Model = model
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.enc.Encode(entry); err != nil {
		return fmt.Errorf("JSONL 编码失败: %w", err)
	}
	return w.file.Sync()
}

// WriteCompactMarker 写入压缩标记行。
func (w *Writer) WriteCompactMarker() error {
	entry := struct {
		Type      string `json:"type"`
		Timestamp int64  `json:"ts"`
	}{
		Type:      "compact",
		Timestamp: time.Now().Unix(),
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.enc.Encode(entry); err != nil {
		return fmt.Errorf("JSONL compact 标记写入失败: %w", err)
	}
	return w.file.Sync()
}

// AppendAll 逐条追加消息列表，全部标记为 isFirst=false。
func (w *Writer) AppendAll(msgs []llm.Message) error {
	for _, msg := range msgs {
		if err := w.Append(msg, "", false); err != nil {
			return err
		}
	}
	return nil
}

// Close 关闭文件句柄。
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}

// OnAppend 返回一个适合作为 Conversation onAppend 回调的函数。
func (w *Writer) OnAppend(model string) func(llm.Message) {
	isFirst := true
	return func(msg llm.Message) {
		_ = w.Append(msg, model, isFirst)
		isFirst = false
	}
}

// OnReplace 返回一个适合作为 Conversation onReplace 回调的函数。
func (w *Writer) OnReplace() func([]llm.Message) {
	return func(msgs []llm.Message) {
		_ = w.WriteCompactMarker()
		_ = w.AppendAll(msgs)
	}
}
