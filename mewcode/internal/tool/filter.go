package tool

// ALL_AGENT_DISALLOWED_TOOLS 是任何子 Agent 永远不能用的工具名列表（spec F26）。
// 本期最小列表：Agent。后续可扩展 AskUserQuestion / TaskStop 等。
var ALL_AGENT_DISALLOWED_TOOLS = []string{"Agent"}

// CUSTOM_AGENT_DISALLOWED_TOOLS 是自定义(user/project/plugin 来源)Agent 比内置 Agent 多禁用的工具（spec F27）。
// 本期为空，接口预留。
var CUSTOM_AGENT_DISALLOWED_TOOLS = []string{}

// ASYNC_AGENT_ALLOWED_TOOLS 是后台 Agent 工具白名单（spec F28）。
// 不含 Agent / TaskStop / SendMessage / TaskList / TaskGet 等任何元工具。
// MCP 工具与 Skill 工具按命名约定动态识别（以 "mcp__" 起头），不在这个列表中但被动态放行。
var ASYNC_AGENT_ALLOWED_TOOLS = []string{
	"read_file", "write_file", "edit_file",
	"glob", "grep",
	"bash",
	"load_skill", "install_skill",
}

// FilterParams 是过滤一个 Agent 的工具列表的参数（spec F30）。
type FilterParams struct {
	All        []string // registry 的全部工具名（按注册顺序）
	Source     int      // Agent 定义来源（与 subagent.Source 数值对齐，用 int 避免反向依赖）
	Background bool     // 是否为后台 Agent
	Allowed    []string // Agent 定义 frontmatter.tools 白名单；空=不收窄
	Disallowed []string // Agent 定义 frontmatter.disallowedTools 黑名单
}

// ApplyAgentToolFilter 按 spec F30 顺序应用五层过滤，返回最终 allowed 列表。
//
// 过滤顺序（spec F30）:
//  1. 起点 = registry 的全部工具名
//  2. 去掉 ALL_AGENT_DISALLOWED_TOOLS
//  3. 如果是后台 → 取交集 ASYNC_AGENT_ALLOWED_TOOLS + MCP/Skill 工具
//  4. 去掉 Agent 定义的 disallowedTools 黑名单
//  5. 如果 Agent 定义了 tools 白名单 → 取交集
func ApplyAgentToolFilter(p FilterParams) []string {
	// 1. 起点：全部工具副本
	result := make([]string, len(p.All))
	copy(result, p.All)

	// 2. 去掉 ALL_AGENT_DISALLOWED_TOOLS
	result = removeTools(result, ALL_AGENT_DISALLOWED_TOOLS)

	// 3. 如果是后台 → 与 ASYNC_AGENT_ALLOWED_TOOLS + MCP/Skill 取交集
	if p.Background {
		result = intersectAsyncTools(result)
	}

	// 4. 去掉定义的 disallowedTools 黑名单
	if len(p.Disallowed) > 0 {
		result = removeTools(result, p.Disallowed)
	}

	// 5. 如果定义了 tools 白名单 → 取交集
	if len(p.Allowed) > 0 {
		result = intersectTools(result, p.Allowed)
	}

	return result
}

// removeTools 从 names 中删除黑名单中的工具名。
func removeTools(names, blacklist []string) []string {
	if len(blacklist) == 0 {
		return names
	}
	blackSet := make(map[string]bool, len(blacklist))
	for _, n := range blacklist {
		blackSet[n] = true
	}
	filtered := make([]string, 0, len(names))
	for _, n := range names {
		if !blackSet[n] {
			filtered = append(filtered, n)
		}
	}
	return filtered
}

// intersectTools 保留 names 与 allowlist 交集中的工具名。
func intersectTools(names, allowlist []string) []string {
	allowSet := make(map[string]bool, len(allowlist))
	for _, n := range allowlist {
		allowSet[n] = true
	}
	filtered := make([]string, 0, len(names))
	for _, n := range names {
		if allowSet[n] {
			filtered = append(filtered, n)
		}
	}
	return filtered
}

// intersectAsyncTools 保留 names 与 ASYNC_AGENT_ALLOWED_TOOLS + MCP/Skill 工具的交集。
func intersectAsyncTools(names []string) []string {
	allowSet := make(map[string]bool, len(ASYNC_AGENT_ALLOWED_TOOLS))
	for _, n := range ASYNC_AGENT_ALLOWED_TOOLS {
		allowSet[n] = true
	}
	filtered := make([]string, 0, len(names))
	for _, n := range names {
		// MCP 工具（mcp__ 前缀）或 Skill 工具动态放行
		if allowSet[n] || isAsyncAllowed(n) {
			filtered = append(filtered, n)
		}
	}
	return filtered
}

// isAsyncAllowed 判断工具名是否在后台 Agent 中被动态放行（spec F28）。
// MCP 工具（mcp__ 前缀）和 Skill 工具在后台模式下被保留。
func isAsyncAllowed(name string) bool {
	// MCP 工具以 "mcp__" 为前缀
	if len(name) >= 5 && name[:5] == "mcp__" {
		return true
	}
	// Skill 工具：load_skill 和 install_skill 已在白名单中
	return false
}
