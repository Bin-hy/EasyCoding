package subagent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"mewcode/internal/permission"
)

// Catalog 按名索引的 Agent 角色目录。并发安全（spec F5-F7）。
// 三层加载顺序：builtin → user → project，同名高优先级覆盖。
type Catalog struct {
	mu       sync.Mutex
	defs     map[string]*Definition   // name → 最高优先级定义
	bySource map[Source][]*Definition // 各层副本（调试与 /agents 展示用）
}

// LoadCatalog 按三层优先级加载所有 Agent 定义（spec F5-F7）。
// 顺序：builtin → user → project。同名定义高优先级覆盖。
// 单个文件解析失败 stderr 警告并跳过，不阻断启动。
// root 为项目根目录。
func LoadCatalog(root string) *Catalog {
	c := &Catalog{
		defs:     make(map[string]*Definition),
		bySource: make(map[Source][]*Definition),
	}

	// 1. 内置级（embed）
	c.addAll(builtinDefinitions(), SourceBuiltin)

	// 2. 用户级：~/.mewcode/agents/
	homeDir, err := os.UserHomeDir()
	if err == nil {
		userDir := filepath.Join(homeDir, ".mewcode", "agents")
		c.addAll(loadFromDir(userDir, SourceUser), SourceUser)
	}

	// 3. 项目级：<root>/.mewcode/agents/
	projDir := filepath.Join(root, ".mewcode", "agents")
	c.addAll(loadFromDir(projDir, SourceProject), SourceProject)

	// 4. 插件级：本期跳过（SourcePlugin）

	return c
}

// Resolve 按名查找定义，返回优先级最高的版本（spec F6）。
func (c *Catalog) Resolve(name string) (*Definition, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, ok := c.defs[name]
	return d, ok
}

// List 按 name 升序返回所有 Definition。
func (c *Catalog) List() []*Definition {
	c.mu.Lock()
	defer c.mu.Unlock()

	names := make([]string, 0, len(c.defs))
	for n := range c.defs {
		names = append(names, n)
	}
	sort.Strings(names)

	result := make([]*Definition, len(names))
	for i, n := range names {
		result[i] = c.defs[n]
	}
	return result
}

// ListBySource 返回指定来源的所有 Definition。
func (c *Catalog) ListBySource(s Source) []*Definition {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bySource[s]
}

// ForkDefinition 返回 Fork 路径用的临时 Definition（spec F22-F24）。
// Name 固定为 "__fork__"，表示子 Agent 从父对话克隆。
func (c *Catalog) ForkDefinition() *Definition {
	return &Definition{
		Name:           "__fork__",
		Description:    "Fork-based subagent",
		Model:          "inherit",
		MaxTurns:       25,
		PermissionMode: permission.ModeDefault,
		// Tools/DisallowedTools 留空 → 工具集继承父
	}
}

// addAll 批量添加定义，同名时后添加的覆盖前面的（spec F6）。
func (c *Catalog) addAll(defs []*Definition, source Source) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, d := range defs {
		c.defs[d.Name] = d
	}
	c.bySource[source] = defs
}

// loadFromDir 从目录扫描 *.md 文件加载 Agent 定义（spec F7）。
// 目录不存在时返回 nil；单个文件解析失败 stderr 警告并跳过。
func loadFromDir(dir string, source Source) []*Definition {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// 目录不存在等：静默跳过
		return nil
	}

	var defs []*Definition
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		def, err := ParseFile(path, source)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[subagent] warn: 跳过 %s: %v\n", path, err)
			continue
		}
		defs = append(defs, def)
	}

	// 按 name 排序
	sort.Slice(defs, func(i, j int) bool {
		return defs[i].Name < defs[j].Name
	})

	return defs
}
