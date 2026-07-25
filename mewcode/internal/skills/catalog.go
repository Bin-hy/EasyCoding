package skills

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Catalog 技能目录，按名索引。并发安全。
type Catalog struct {
	mu     sync.RWMutex
	byName map[string]*Skill
	order  []string // 按 name 字典序的稳定迭代序
}

// LoadCatalog 两级路径扫描：先用户级 ~/.mewcode/skills/，再项目级 <workDir>/.mewcode/skills/。
// 后扫到的同名覆盖前者（项目级优先）。
// 解析失败的单个 Skill 跳过 + 记 debug log，不阻断整体加载。
func LoadCatalog(workDir string) *Catalog {
	c := &Catalog{
		byName: make(map[string]*Skill),
		order:  nil,
	}

	// 用户级路径
	home, err := os.UserHomeDir()
	if err == nil {
		userDir := filepath.Join(home, ".mewcode", "skills")
		c.scanDir(userDir, SourceUser)
	}

	// 项目级路径（后扫，同名覆盖）
	projDir := filepath.Join(workDir, ".mewcode", "skills")
	c.scanDir(projDir, SourceProject)

	// 稳定排序
	c.reorder()

	return c
}

// Reload 重新扫描两级路径，原子替换整个目录。
// 先在不持锁的情况下完成所有 I/O，再在写锁下原子替换。
// 返回新增和移除的 skill 名称列表。
func (c *Catalog) Reload(workDir string) (added, removed []string) {
	// 在不持锁的情况下完成所有文件 I/O
	newByName := make(map[string]*Skill)
	var newOrder []string

	home, err := os.UserHomeDir()
	if err == nil {
		userDir := filepath.Join(home, ".mewcode", "skills")
		c.scanInto(userDir, SourceUser, newByName, &newOrder)
	}

	projDir := filepath.Join(workDir, ".mewcode", "skills")
	c.scanInto(projDir, SourceProject, newByName, &newOrder)

	// 排序
	sort.Strings(newOrder)

	// 写锁下原子替换
	c.mu.Lock()
	defer c.mu.Unlock()

	oldNames := make(map[string]bool)
	for _, name := range c.order {
		oldNames[name] = true
	}

	// 计算 diff
	newNames := make(map[string]bool)
	for _, name := range newOrder {
		newNames[name] = true
		if !oldNames[name] {
			added = append(added, name)
		}
	}
	for name := range oldNames {
		if !newNames[name] {
			removed = append(removed, name)
		}
	}

	c.byName = newByName
	c.order = newOrder
	return added, removed
}

// Get 按名查找 Skill（轻量版，PromptBody 可能为空）。
func (c *Catalog) Get(name string) (*Skill, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.byName[strings.ToLower(name)]
	return s, ok
}

// GetFull 按名查找 Skill 并强制重读 body（热重载）。
// 重读失败时回退到缓存版本并记 debug log。
func (c *Catalog) GetFull(name string) (*Skill, error) {
	c.mu.RLock()
	s, ok := c.byName[strings.ToLower(name)]
	c.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown skill: %s", name)
	}

	// 重读 body
	if err := loadSkillBody(s); err != nil {
		log.Printf("[skills] debug: 重读 %s body 失败，回退缓存: %v", name, err)
		// 回退：保持缓存值，不报错
	}

	return s, nil
}

// List 按 name 字典序返回所有 Skill 的切片。
func (c *Catalog) List() []*Skill {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]*Skill, len(c.order))
	for i, name := range c.order {
		result[i] = c.byName[name]
	}
	return result
}

// Names 返回所有已加载 Skill 的名称列表。
func (c *Catalog) Names() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]string, len(c.order))
	copy(result, c.order)
	return result
}

// ValidationIssue 记录工具白名单校验问题。
type ValidationIssue struct {
	Skill      string
	Tool       string
	NotDefined bool
}

// ValidateTools 遍历所有 Skill 的 allowed_tools，确认引用的工具都存在。
// 返回所有不匹配项；空列表表示全部通过。
func (c *Catalog) ValidateTools(toolExists func(string) bool) []ValidationIssue {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var issues []ValidationIssue
	for _, name := range c.order {
		sk := c.byName[name]
		for _, t := range sk.Meta.AllowedTools {
			if !toolExists(t) {
				issues = append(issues, ValidationIssue{
					Skill:      sk.Meta.Name,
					Tool:       t,
					NotDefined: true,
				})
			}
		}
	}
	return issues
}

// scanDir 扫描单个目录下的 Skill 子目录，写入 Catalog（内部无锁，调用方加锁）。
func (c *Catalog) scanDir(root string, source SkillSource) {
	c.scanInto(root, source, c.byName, &c.order)
}

// scanInto 与 scanDir 相同，但写入外部 map 与 order 切片（供 Reload 使用）。
func (c *Catalog) scanInto(root string, source SkillSource, into map[string]*Skill, order *[]string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		// 目录不存在等场景：静默跳过
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		skill, err := parseSkillDir(dir, source)
		if err != nil {
			log.Printf("[skills] debug: 跳过 %s: %v", dir, err)
			continue
		}
		key := strings.ToLower(skill.Meta.Name)
		into[key] = skill
		// 避免重复 name 在 order 里出现多次
		found := false
		for _, n := range *order {
			if strings.EqualFold(n, skill.Meta.Name) {
				found = true
				break
			}
		}
		if !found {
			*order = append(*order, skill.Meta.Name)
		}
	}
}

// reorder 对 order 切片做稳定排序。
func (c *Catalog) reorder() {
	sort.Strings(c.order)
}
