package subagent

import (
	"embed"
	"fmt"
	"sort"
)

//go:embed builtin/*.md
var builtinFS embed.FS

// builtinDefinitions 加载所有内嵌的 Agent 定义文件（spec F32）。
// 解析失败直接 panic——内嵌文件是代码的一部分，构建期错误即灾难。
func builtinDefinitions() []*Definition {
	entries, err := builtinFS.ReadDir("builtin")
	if err != nil {
		panic(fmt.Sprintf("subagent: 读取内嵌 builtin 目录失败: %v", err))
	}

	var defs []*Definition
	for _, entry := range entries {
		if entry.IsDir() || entry.Name()[0] == '.' {
			continue
		}
		data, err := builtinFS.ReadFile("builtin/" + entry.Name())
		if err != nil {
			panic(fmt.Sprintf("subagent: 读取内嵌文件 builtin/%s 失败: %v", entry.Name(), err))
		}
		def, err := ParseDefinition(data, "builtin:"+entry.Name(), SourceBuiltin)
		if err != nil {
			panic(fmt.Sprintf("subagent: 解析内嵌定义 builtin/%s 失败: %v", entry.Name(), err))
		}
		defs = append(defs, def)
	}

	// 按 name 升序排序
	sort.Slice(defs, func(i, j int) bool {
		return defs[i].Name < defs[j].Name
	})

	return defs
}
