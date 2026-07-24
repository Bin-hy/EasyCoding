package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"mewcode/internal/llm"
)

// maxIndexSize 索引文件最大大小（字节）。
const maxIndexSize = 25 * 1024 // 25KB

// Manager 编排项目级和用户级笔记的加载和更新。
type Manager struct {
	projectStore *Store
	userStore    *Store
	provider     llm.Provider
	model        string
	mu           sync.Mutex // 保护并发更新
}

// NewManager 创建记忆管理器。
func NewManager(projectDir, userDir string, provider llm.Provider, model string) *Manager {
	return &Manager{
		projectStore: NewStore(projectDir),
		userStore:    NewStore(userDir),
		provider:     provider,
		model:        model,
	}
}

// LoadIndex 合并两级索引，截断到 25KB。
// 项目级在前、用户级在后。
func (m *Manager) LoadIndex() string {
	projectIndex, _ := m.projectStore.LoadIndex()
	userIndex, _ := m.userStore.LoadIndex()

	var parts []string
	if strings.TrimSpace(projectIndex) != "" {
		parts = append(parts, projectIndex)
	}
	if strings.TrimSpace(userIndex) != "" {
		parts = append(parts, userIndex)
	}

	result := strings.Join(parts, "\n")
	if len(result) > maxIndexSize {
		result = result[:maxIndexSize]
		// 找到最后一个换行符处截断
		if lastNL := strings.LastIndexByte(result, '\n'); lastNL > 0 {
			result = result[:lastNL]
		}
		result += "\n(index truncated)"
	}

	return result
}

// SetProvider 延迟设置 provider（启动时 provider 未选定）。
func (m *Manager) SetProvider(provider llm.Provider, model string) {
	m.provider = provider
	m.model = model
}

// UpdateAsync 异步发起记忆更新。
// recentMsgs 为最近一轮对话的消息（从最后一条 user 到最终 assistant）。
func (m *Manager) UpdateAsync(ctx context.Context, recentMsgs []llm.Message) {
	go func() {
		m.mu.Lock()
		defer m.mu.Unlock()

		if m.provider == nil {
			return
		}

		// 构造记忆更新请求
		indexText := m.LoadIndex()
		sysPrompt := buildMemoryUpdatePrompt(indexText)

		// 构造请求消息
		msgs := make([]llm.Message, 0, len(recentMsgs)+1)
		// 将最近消息转为 user 消息注入
		var convText strings.Builder
		for _, msg := range recentMsgs {
			fmt.Fprintf(&convText, "[%s]: %s\n", msg.Role, msg.Content)
		}
		msgs = append(msgs, llm.Message{
			Role:    llm.RoleUser,
			Content: convText.String(),
		})

		req := llm.Request{
			Messages: msgs,
			System: llm.System{
				Stable: sysPrompt,
			},
			// 不传工具定义（F38）
		}

		stream := m.provider.Stream(ctx, req)
		var fullText strings.Builder
		for ev := range stream {
			if ev.Err != nil {
				fmt.Fprintf(os.Stderr, "[memory] 记忆更新 LLM 调用失败: %v\n", ev.Err)
				return
			}
			if ev.Text != "" {
				fullText.WriteString(ev.Text)
			}
			if ev.Done {
				break
			}
		}

		// 解析 JSON 数组
		text := fullText.String()
		// 提取 JSON（可能被 markdown 代码块包裹）
		text = extractJSON(text)

		var actions []UpdateAction
		if err := json.Unmarshal([]byte(text), &actions); err != nil {
			fmt.Fprintf(os.Stderr, "[memory] 记忆更新 JSON 解析失败: %v\n", err)
			return
		}

		if len(actions) == 0 {
			return
		}

		// 按 level 分发执行
		for _, a := range actions {
			switch a.Level {
			case "project":
				if err := m.projectStore.Apply([]UpdateAction{a}); err != nil {
					fmt.Fprintf(os.Stderr, "[memory] 项目级笔记操作失败: %v\n", err)
				}
			case "user":
				if err := m.userStore.Apply([]UpdateAction{a}); err != nil {
					fmt.Fprintf(os.Stderr, "[memory] 用户级笔记操作失败: %v\n", err)
				}
			default:
				fmt.Fprintf(os.Stderr, "[memory] 未知笔记级别: %s\n", a.Level)
			}
		}
	}()
}

// ListFiles 列出项目层与用户层记忆目录下的 .md 文件名（含 MEMORY.md）。
// 目录不存在视为空 slice；其他错误 log 后视为空 slice。
// 返回值已按文件名字典序排序。
func (m *Manager) ListFiles() (project []string, user []string) {
	project = m.projectStore.ListFiles()
	user = m.userStore.ListFiles()
	return
}

// extractJSON 从文本中提取 JSON 数组部分。
func extractJSON(text string) string {
	// 尝试找到第一个 [ 和最后一个 ]
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start >= 0 && end > start {
		return text[start : end+1]
	}
	return text
}
