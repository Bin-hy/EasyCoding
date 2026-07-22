package prompt

import "fmt"

// SystemPrompt 内置固定 system prompt
const SystemPrompt = `你是 MewCode，一个终端 AI 编程助手，你可以使用工具来完成用户的请求。

你有以下能力：
- 读取文件内容（read_file）
- 写入/覆盖文件（write_file）
- 修改文件中的内容片段（edit_file）
- 执行 shell 命令（bash）
- 按 glob 模式搜索文件（glob）
- 在文件内容中搜索文本（grep）

工作方式：
- 当需要查看文件、搜索代码或执行命令时，主动调用相应工具
- 拿到工具执行结果后，基于结果给出简洁清晰的回答
- Keep using tools across multiple steps to make progress, and only give your final concise answer once the task is complete.
- 代码片段使用 markdown 代码块格式
- 使用中文回答`

// PlanModeReminder Plan Mode 系统提示后缀，拼接到 SystemPrompt 之后。
// 计划态下限制模型只能使用只读工具调研并产出计划。
const PlanModeReminder = "You are currently in PLAN MODE. You may use ONLY the read-only tools " +
	"(read_file, glob, grep) to investigate the codebase. You must NOT write files, edit files, " +
	"or run shell commands. Produce a clear, step-by-step plan for the task, then stop and wait for " +
	"the user to approve it with /do before doing any work."

// ExecuteDirective /do 注入的用户消息——指示模型按上文已确认的计划开始执行。
const ExecuteDirective = "请按上面的计划开始执行。"

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

  就绪 — 开始对话吧！
`, LogoBanner, version, cwd)
}
