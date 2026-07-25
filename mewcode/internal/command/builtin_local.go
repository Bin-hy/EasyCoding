package command

import (
	"context"
	"fmt"
	"strings"

	"mewcode/internal/hook"
)

// handleHelp 返回 /help 的 handler，通过闭包捕获 Registry。
func handleHelp(reg *Registry) Handler {
	return func(ctx context.Context, ui UI) error {
		cmds := reg.Visible()
		if len(cmds) == 0 {
			ui.Println("无已注册命令")
			return nil
		}

		// 计算最长命令名长度做对齐填充
		maxNameLen := 0
		for _, c := range cmds {
			if len(c.Name) > maxNameLen {
				maxNameLen = len(c.Name)
			}
		}

		var lines []string
		for _, c := range cmds {
			pad := maxNameLen - len(c.Name)
			padding := ""
			for i := 0; i < pad+2; i++ {
				padding += " "
			}
			lines = append(lines, fmt.Sprintf("/%s%s%s", c.Name, padding, c.Description))
		}
		ui.Println(joinLines(lines))
		return nil
	}
}

// handleStatus 输出当前运行状态 6 项信息。
func handleStatus(ctx context.Context, ui UI) error {
	keyWidth := 11 // "Directory:" 是最长的 key
	var lines []string
	lines = append(lines, "MewCode Status")
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("%-*s %s", keyWidth, "Mode:", ui.Mode().String()))
	lines = append(lines, fmt.Sprintf("%-*s %d in / %d out", keyWidth, "Tokens:", ui.UsageIn(), ui.UsageOut()))
	lines = append(lines, fmt.Sprintf("%-*s %d enabled", keyWidth, "Tools:", ui.ToolCount()))
	lines = append(lines, fmt.Sprintf("%-*s %d files", keyWidth, "Memories:", len(ui.MemoryFiles())))
	lines = append(lines, fmt.Sprintf("%-*s %s", keyWidth, "Model:", ui.ModelName()))
	lines = append(lines, fmt.Sprintf("%-*s %s", keyWidth, "Directory:", ui.Cwd()))
	ui.Println(joinLines(lines))
	return nil
}

// handleMemory 列出已加载的记忆文件名。
func handleMemory(ctx context.Context, ui UI) error {
	files := ui.MemoryFiles()
	if len(files) == 0 {
		ui.Println("无已加载的记忆文件")
		return nil
	}

	lines := append([]string{}, files...)
	ui.Println(joinLines(lines))
	return nil
}

// handlePermission 输出当前权限模式。
func handlePermission(ctx context.Context, ui UI) error {
	ui.Println(ui.Mode().String())
	return nil
}

// handleSession 输出当前会话标识信息。
func handleSession(ctx context.Context, ui UI) error {
	ui.Println(fmt.Sprintf("Session: %s\nPath: %s", ui.SessionID(), ui.SessionPath()))
	return nil
}

// joinLines 将字符串切片用 "\n" 连接。
func joinLines(lines []string) string {
	result := ""
	for i, line := range lines {
		if i > 0 {
			result += "\n"
		}
		result += line
	}
	return result
}

// handleHooks 列出已加载的 hook 列表。
func handleHooks(_ context.Context, ui UI) error {
	sources := ui.HookSources()
	rawRules := ui.HookRules()

	if len(rawRules) == 0 {
		ui.Println("No hooks loaded.")
		return nil
	}

	// 类型断言：从 []interface{} 转回 []hook.Rule
	rules := make([]hook.Rule, 0, len(rawRules))
	for _, r := range rawRules {
		if hr, ok := r.(hook.Rule); ok {
			rules = append(rules, hr)
		}
	}

	// 按 event 分组（保留声明顺序）
	grouped := make(map[hook.Event][]string)
	var eventOrder []hook.Event
	for _, r := range rules {
		if _, exists := grouped[r.Event]; !exists {
			eventOrder = append(eventOrder, r.Event)
		}
		flags := ""
		if r.OnlyOnce {
			flags += " [once]"
		}
		if r.Async {
			flags += " [async]"
		}
		grouped[r.Event] = append(grouped[r.Event], fmt.Sprintf("  %s  %s  %s%s", r.Name, r.Event, r.Action.Type, flags))
	}

	for _, ev := range eventOrder {
		ui.Println(string(ev))
		for _, line := range grouped[ev] {
			ui.Println(line)
		}
	}

	ui.Println(fmt.Sprintf("Loaded from: %s", strings.Join(sources, ", ")))
	return nil
}
