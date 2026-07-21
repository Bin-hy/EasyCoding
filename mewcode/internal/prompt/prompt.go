package prompt

import "fmt"

// SystemPrompt 内置固定 system prompt
const SystemPrompt = `你是 MewCode，一个终端 AI 编程助手。
你运行在用户的终端中，以纯文本方式与用户交互。
回答问题时简洁清晰，代码片段使用 markdown 代码块格式。
使用中文回答。`

// CatBanner ASCII 猫咪图案
const CatBanner = `  /\_/\
 ( o.o )
  > ^ <`

// RenderBanner 拼装启动横幅：猫咪 + 应用名与版本 + 当前工作目录 + 就绪提示
func RenderBanner(version, cwd string) string {
	return fmt.Sprintf(`
%s

  MewCode v%s
  工作目录: %s

  就绪 — 开始对话吧！
`, CatBanner, version, cwd)
}
