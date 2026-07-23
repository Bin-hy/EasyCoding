package session

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"mewcode/internal/compact"
)

// SessionInfo 是会话列表中一项的摘要信息。
type SessionInfo struct {
	ID         string    // session ID（目录名）
	Title      string    // 第一条 user 消息内容（截断）
	ModifiedAt time.Time // 最后修改时间
	Model      string    // 模型标签
	Size       int64     // JSONL 文件大小
	Dir        string    // 会话目录绝对路径
}

// Description 返回列表项的描述文本。
func (s SessionInfo) Description() string {
	relTime := relativeTimeStr(s.ModifiedAt)
	model := s.Model
	if model == "" {
		model = "unknown"
	}
	size := formatSizeStr(s.Size)
	return fmt.Sprintf("%s · %s · %s", relTime, model, size)
}

// ListSessions 扫描 sessionsDir，返回按修改时间倒序排列的会话列表。
// 只返回包含 conversation.jsonl 且 ID 能解析为新格式的目录。
func ListSessions(sessionsDir string) ([]SessionInfo, error) {
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var infos []SessionInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()

		// 检查是否为新格式 session ID
		if _, err := compact.ParseSessionTime(id); err != nil {
			continue // 旧格式跳过
		}

		dir := sessionsDir + "/" + id
		jsonlPath := dir + "/conversation.jsonl"

		fi, err := os.Stat(jsonlPath)
		if err != nil {
			continue // 无 JSONL 跳过
		}

		// 读取第一条 user 消息作为标题
		title, model := readFirstUser(jsonlPath)

		infos = append(infos, SessionInfo{
			ID:         id,
			Title:      title,
			ModifiedAt: fi.ModTime(),
			Model:      model,
			Size:       fi.Size(),
			Dir:        dir,
		})
	}

	// 按修改时间倒序
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].ModifiedAt.After(infos[j].ModifiedAt)
	})

	return infos, nil
}

// readFirstUser 读取 JSONL 第一条 role=user 的消息，返回截断的标题和模型名。
func readFirstUser(path string) (title, model string) {
	f, err := os.Open(path)
	if err != nil {
		return "(无法读取)", ""
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	for dec.More() {
		var entry Entry
		if err := dec.Decode(&entry); err != nil {
			continue
		}
		if entry.Model != "" {
			model = entry.Model
		}
		if entry.Role == "user" && title == "" {
			title = entry.Content
			// 截断到 50 字符
			runes := []rune(title)
			if len(runes) > 50 {
				title = string(runes[:50]) + "..."
			}
		}
		if title != "" && model != "" {
			break
		}
	}

	if title == "" {
		title = "(空会话)"
	}
	return title, model
}

// relativeTimeStr 返回友好的相对时间描述。
func relativeTimeStr(t time.Time) string {
	elapsed := time.Since(t)
	switch {
	case elapsed < time.Minute:
		return "just now"
	case elapsed < time.Hour:
		m := int(elapsed.Minutes())
		if m == 0 {
			m = 1
		}
		return fmt.Sprintf("%d min ago", m)
	case elapsed < 24*time.Hour:
		h := int(elapsed.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	default:
		d := int(elapsed.Hours() / 24)
		if d == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", d)
	}
}

// formatSizeStr 格式化文件大小。
func formatSizeStr(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%dB", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(size)/(1024*1024))
}
