package main

import (
	"fmt"
	"os"

	"mewcode/internal/config"
	"mewcode/internal/permission"
	"mewcode/internal/tool"
	"mewcode/internal/tui"
)

const version = "0.1.0"

func main() {
	// 加载配置
	cfg, err := config.Load(".mewcode/config.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "配置错误: %v\n", err)
		os.Exit(1)
	}

	// 构造工具注册中心
	reg := tool.NewDefaultRegistry()

	// 构造权限引擎（前四层防御 + 三层配置加载）
	root, _ := os.Getwd()
	eng, err := permission.NewEngine(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "权限引擎降级: %v\n", err)
		// eng 必非 nil，继续运行
	}

	// 启动 TUI（注入权限引擎）
	m := tui.New(cfg.Providers, version, reg, eng)
	if err := m.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "运行错误: %v\n", err)
		os.Exit(1)
	}
}
