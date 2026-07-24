package prompt

import (
	"fmt"
	"sort"
	"strings"
)

// AssembleSystem 按 Priority 升序排列模块，跳过 Content 为空的模块，以 "\n\n" 连接。
// 排序稳定以保证跨调用逐字节一致（N1 缓存确定性）。
func AssembleSystem(mods []Module) string {
	// 防御性拷贝后排序，避免副作用
	sorted := make([]Module, len(mods))
	copy(sorted, mods)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Priority < sorted[j].Priority
	})

	var parts []string
	for _, m := range sorted {
		if m.Content != "" {
			parts = append(parts, m.Content)
		}
	}
	return strings.Join(parts, "\n\n")
}

// BuildSystemPrompt 返回完整的稳定系统提示。
// instructions 非空时填入 custom-instructions 模块（priority 80）。
// skillsCatalog 非空时填入 skills-catalog 模块（priority 90）。
// memory 非空时填入 long-term-memory 模块（priority 100）。
// 可选槽 Content 为空时自动跳过，不留多余空行。
func BuildSystemPrompt(instructions, memory, skillsCatalog string) string {
	all := append(FixedModules(), OptionalModules(instructions, memory, skillsCatalog)...)
	return AssembleSystem(all)
}

// LogoBanner MEWCODE ASCII 艺术字
const LogoBanner = `███╗   ███╗███████╗██╗    ██╗ ██████╗ ██████╗ ██████╗ ███████╗
████╗ ████║██╔════╝██║    ██║██╔════╝██╔═══██╗██╔══██╗██╔════╝
██╔████╔██║█████╗  ██║ █╗ ██║██║     ██║   ██║██║  ██║█████╗
██║╚██╔╝██║██╔══╝  ██║███╗██║██║     ██║   ██║██║  ██║██╔══╝
██║ ╚═╝ ██║███████╗╚███╔███╔╝╚██████╗╚██████╔╝██████╔╝███████╗
╚═╝     ╚═╝╚══════╝ ╚══╝╚══╝  ╚═════╝ ╚═════╝ ╚═════╝ ╚══════╝`

// RenderBanner 拼装启动横幅：MEWCODE logo + 应用名与版本 + 当前工作目录 + 就绪提示
func RenderBanner(version, cwd string) string {
	return fmt.Sprintf(`
%s

  MewCode v%s
  工作目录: %s

  就绪 — 输入 /help 查看可用命令
`, LogoBanner, version, cwd)
}
