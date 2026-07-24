package command

// RegisterBuiltins 一次性注册 12 条内置命令到 reg。
// 命令按 Name 字典序排列，/help 通过闭包捕获 reg。
func RegisterBuiltins(reg *Registry) {
	// 纯本地类
	reg.Register(&Command{Name: "clear", Description: "清空当前会话并开启新会话", Kind: KindUI, Handler: handleClear})
	reg.Register(&Command{Name: "compact", Description: "手动触发上下文压缩", Kind: KindUI, Handler: handleCompact})
	reg.Register(&Command{Name: "do", Description: "切回默认模式并执行计划", Kind: KindPrompt, Handler: handleDo})
	reg.Register(&Command{Name: "exit", Description: "退出 MewCode", Kind: KindUI, Handler: handleExit})
	reg.Register(&Command{Name: "help", Description: "显示所有可用命令", Kind: KindLocal, Handler: handleHelp(reg)})
	reg.Register(&Command{Name: "memory", Description: "显示已加载的记忆文件列表", Kind: KindLocal, Handler: handleMemory})
	reg.Register(&Command{Name: "permission", Description: "显示当前权限模式", Kind: KindLocal, Handler: handlePermission})
	reg.Register(&Command{Name: "plan", Description: "切换到计划模式（只读工具）", Kind: KindUI, Handler: handlePlan})
	reg.Register(&Command{Name: "resume", Description: "恢复历史会话", Kind: KindUI, Handler: handleResume})
	reg.Register(&Command{Name: "review", Description: "请求代码审查", Kind: KindPrompt, Handler: handleReview})
	reg.Register(&Command{Name: "session", Description: "显示当前会话信息", Kind: KindLocal, Handler: handleSession})
	reg.Register(&Command{Name: "status", Description: "显示当前运行状态", Kind: KindLocal, Handler: handleStatus})
}
