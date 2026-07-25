// Package subagent 提供 Agent 角色定义、Markdown+YAML 解析、Catalog 多来源加载、内置角色 embed。
//
// 子 Agent 是独立上下文中执行任务的受限 Agent。主 Agent 通过 Agent 工具将子任务委派给子 Agent。
// 角色(Agent Definition)通过 Markdown + YAML frontmatter 文件定义，支持多层来源加载与优先级覆盖。
//
// 核心类型:
//   - Definition: Agent 角色的完整定义（frontmatter + body）
//   - Catalog: 按名索引的角色目录（三层加载 + 优先级覆盖）
//   - Source: 定义来源枚举（builtin / user / project / plugin）
package subagent
