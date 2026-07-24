package skills

import "sync"

// ActiveSkills 管理当前会话中已激活的 Skill 列表。并发安全。
type ActiveSkills struct {
	mu      sync.Mutex
	entries []ActiveEntry // 保持激活顺序
	names   map[string]int // name → entries 索引；重复激活时覆盖
}

// NewActiveSkills 创建一个空的 ActiveSkills 实例。
func NewActiveSkills() *ActiveSkills {
	return &ActiveSkills{
		names: make(map[string]int),
	}
}

// Activate 将 name + body 写入激活列表。重复激活同名 Skill 时覆盖原位置。
func (a *ActiveSkills) Activate(name, body string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if idx, ok := a.names[name]; ok {
		// 已存在：覆盖
		a.entries[idx].Body = body
		return
	}

	a.names[name] = len(a.entries)
	a.entries = append(a.entries, ActiveEntry{Name: name, Body: body})
}

// Clear 清空所有已激活 Skill。
func (a *ActiveSkills) Clear() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = nil
	a.names = make(map[string]int)
}

// Snapshot 返回当前激活列表的快照拷贝。
func (a *ActiveSkills) Snapshot() []ActiveEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	cp := make([]ActiveEntry, len(a.entries))
	copy(cp, a.entries)
	return cp
}

// ToPromptEntries 将内部 ActiveEntry 转换为 prompt 包可用的类型。
// 通过 adapter.go 桥接以避免循环依赖。
func (a *ActiveSkills) ToPromptEntries() []ActiveSkillEntry {
	snapshot := a.Snapshot()
	result := make([]ActiveSkillEntry, len(snapshot))
	for i, e := range snapshot {
		result[i] = ActiveSkillEntry{Name: e.Name, Body: e.Body}
	}
	return result
}

// ActiveSkillEntry prompt 包的活跃 Skill 条目类型。
// prompt 包定义同名字段（避免循环依赖）。
type ActiveSkillEntry struct {
	Name string
	Body string
}
