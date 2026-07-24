package command

import (
	"fmt"
	"sort"
	"strings"
)

// Registry 管理已注册命令的索引与查询。
type Registry struct {
	byName  map[string]*Command // 主名 + 别名都映射到同一 *Command，key 已转小写
	visible []*Command          // 按 Name 字典序排序，排除 Hidden，给 /help 与补全菜单使用
}

// New 创建空的命令注册中心。
func New() *Registry {
	return &Registry{
		byName:  make(map[string]*Command),
		visible: nil,
	}
}

// Register 注册一条命令。名字或别名冲突时 panic（启动期快速失败）。
func (r *Registry) Register(c *Command) {
	// 校验 Name
	if c.Name == "" {
		panic("command: 命令名不能为空")
	}
	if c.Name != strings.ToLower(c.Name) {
		panic(fmt.Sprintf("command: 命令名必须全小写: %q", c.Name))
	}

	// 收集要注册的所有键
	keys := make([]string, 0, 1+len(c.Aliases))
	keys = append(keys, strings.ToLower(c.Name))
	for _, alias := range c.Aliases {
		if alias == "" {
			panic(fmt.Sprintf("command: %q 的别名不能为空", c.Name))
		}
		lowerAlias := strings.ToLower(alias)
		keys = append(keys, lowerAlias)
	}

	// 冲突检测
	for _, key := range keys {
		if existing, ok := r.byName[key]; ok {
			panic(fmt.Sprintf("command: 名字/别名 %q 冲突（已有命令 %q）", key, existing.Name))
		}
	}

	// 写入索引
	for _, key := range keys {
		r.byName[key] = c
	}

	// 更新 visible 列表
	if !c.Hidden {
		r.visible = append(r.visible, c)
		sort.SliceStable(r.visible, func(i, j int) bool {
			return r.visible[i].Name < r.visible[j].Name
		})
	}
}

// Lookup 按命令名或别名查找。name 已小写化。
func (r *Registry) Lookup(name string) (*Command, bool) {
	c, ok := r.byName[strings.ToLower(name)]
	return c, ok
}

// Visible 返回已按 Name 字典序排序的可见命令副本。
func (r *Registry) Visible() []*Command {
	cp := make([]*Command, len(r.visible))
	copy(cp, r.visible)
	return cp
}

// PrefixMatch 按命令名前缀匹配可见命令。prefix 含 "/"，内部 trim 并小写。
// 仅前缀匹配 Name，不匹配别名/描述。prefix 为空返回全部 visible。
func (r *Registry) PrefixMatch(prefix string) []*Command {
	prefix = strings.TrimPrefix(prefix, "/")
	prefix = strings.ToLower(prefix)
	if prefix == "" {
		return r.Visible()
	}

	var result []*Command
	for _, c := range r.visible {
		if strings.HasPrefix(c.Name, prefix) {
			result = append(result, c)
		}
	}
	return result
}
