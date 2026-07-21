package main

import (
	"fmt"
	"os"

	"mewcode/internal/config"
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

	// 启动 TUI
	m := tui.New(cfg.Providers, version, reg)
	if err := m.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "运行错误: %v\n", err)
		os.Exit(1)
	}
}
