package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Store 管理单级（项目级或用户级）的笔记文件和索引。
type Store struct {
	dir string // .mewcode/memory/ 或 ~/.mewcode/memory/
	mu  sync.Mutex
}

// NewStore 创建笔记存储。
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// EnsureDir 确保存储目录存在。
func (s *Store) EnsureDir() error {
	return os.MkdirAll(s.dir, 0o755)
}

// ListFiles 列出存储目录下所有 .md 文件名（含 MEMORY.md）。
// 目录不存在视为空 slice，其他错误 log 后视为空 slice。
// 返回值已按文件名字典序排序。
func (s *Store) ListFiles() []string {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if !os.IsNotExist(err) {
			// 非"目录不存在"的错误记录日志
			fmt.Fprintf(os.Stderr, "[memory] 读取记忆目录失败: %v\n", err)
		}
		return nil
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".md") {
			files = append(files, name)
		}
	}

	// 已在内存中排序，但为了确定性再做一次
	sort.Strings(files)
	return files
}

// LoadIndex 读取 MEMORY.md 内容。不存在返回空字符串。
func (s *Store) LoadIndex() (string, error) {
	indexPath := filepath.Join(s.dir, "MEMORY.md")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// Apply 执行 create/update/delete 操作列表。
func (s *Store) Apply(actions []UpdateAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.EnsureDir(); err != nil {
		return err
	}

	for _, a := range actions {
		switch a.Action {
		case "create":
			if err := s.create(a); err != nil {
				return fmt.Errorf("创建笔记失败: %w", err)
			}
		case "update":
			if err := s.update(a); err != nil {
				return fmt.Errorf("更新笔记失败: %w", err)
			}
		case "delete":
			if err := s.delete(a); err != nil {
				return fmt.Errorf("删除笔记失败: %w", err)
			}
		}
	}
	return nil
}

// create 创建新笔记文件并在索引中追加一行。
func (s *Store) create(a UpdateAction) error {
	filename := a.Type + "_" + a.Slug + ".md"
	filePath := filepath.Join(s.dir, filename)

	now := time.Now()
	nowStr := now.Format(time.RFC3339)

	// 构造 frontmatter + 内容
	fullContent := fmt.Sprintf(`---
type: %s
title: %s
created: %s
updated: %s
---

%s
`, a.Type, a.Title, nowStr, nowStr, a.Content)

	if err := os.WriteFile(filePath, []byte(fullContent), 0o644); err != nil {
		return err
	}

	// 追加索引行
	return s.appendIndex(a.Type, a.Title, extractOneLine(a.Content))
}

// update 重写文件内容和 frontmatter，更新索引中对应行。
func (s *Store) update(a UpdateAction) error {
	filePath := filepath.Join(s.dir, a.Filename)

	// 读取现有文件获取 created 时间
	existing, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	createdTime := extractCreated(string(existing))

	now := time.Now()
	nowStr := now.Format(time.RFC3339)

	fullContent := fmt.Sprintf(`---
type: %s
title: %s
created: %s
updated: %s
---

%s
`, a.Type, a.Title, createdTime, nowStr, a.Content)

	if err := os.WriteFile(filePath, []byte(fullContent), 0o644); err != nil {
		return err
	}

	// 更新索引中对应行
	return s.updateIndexLine(a.Filename, a.Type, a.Title, extractOneLine(a.Content))
}

// delete 删除文件并移除索引中对应行。
func (s *Store) delete(a UpdateAction) error {
	filePath := filepath.Join(s.dir, a.Filename)
	// 文件不存在也不报错
	_ = os.Remove(filePath)
	return s.removeIndexLine(a.Filename)
}

// appendIndex 在 MEMORY.md 末尾追加一行摘要。
func (s *Store) appendIndex(noteType, title, desc string) error {
	indexPath := filepath.Join(s.dir, "MEMORY.md")
	line := fmt.Sprintf("- [%s] %s — %s\n", noteType, title, desc)

	f, err := os.OpenFile(indexPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	//nolint:errcheck // 预存 defer close 不检查
	defer f.Close()

	_, err = f.Write([]byte(line))
	return err
}

// updateIndexLine 更新 MEMORY.md 中文件名对应的行。
func (s *Store) updateIndexLine(filename, noteType, title, desc string) error {
	indexPath := filepath.Join(s.dir, "MEMORY.md")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	newLine := fmt.Sprintf("- [%s] %s — %s", noteType, title, desc)
	lines := strings.Split(string(data), "\n")
	found := false
	for i, line := range lines {
		if strings.Contains(line, filename) {
			lines[i] = newLine
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, newLine)
	}

	return os.WriteFile(indexPath, []byte(strings.Join(lines, "\n")), 0o644)
}

// removeIndexLine 从 MEMORY.md 中移除文件名对应的行。
func (s *Store) removeIndexLine(filename string) error {
	indexPath := filepath.Join(s.dir, "MEMORY.md")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	lines := strings.Split(string(data), "\n")
	var filtered []string
	for _, line := range lines {
		if !strings.Contains(line, filename) {
			filtered = append(filtered, line)
		}
	}

	return os.WriteFile(indexPath, []byte(strings.Join(filtered, "\n")), 0o644)
}

// extractOneLine 从内容中提取第一行作为简短描述。
func extractOneLine(content string) string {
	lines := strings.SplitN(strings.TrimSpace(content), "\n", 2)
	if len(lines) > 0 {
		line := strings.TrimSpace(lines[0])
		if len(line) > 80 {
			return line[:77] + "..."
		}
		return line
	}
	return ""
}

// extractCreated 从 frontmatter 中提取 created 字段的值。
func extractCreated(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "created:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "created:"))
		}
	}
	return time.Now().Format(time.RFC3339)
}
