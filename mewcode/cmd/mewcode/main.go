package main

import (
	"context"
	"fmt"
	"os"

	"mewcode/internal/config"
	"mewcode/internal/mcp"
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

	// 构造工具注册中心（6 内置工具）
	reg := tool.NewDefaultRegistry()

	// 加载 MCP 配置并连接远端 server
	root, _ := os.Getwd()
	mcpCfg, _ := mcp.LoadConfig(root)
	mgr := mcp.NewManager(context.Background(), mcpCfg, version)
	defer mgr.Close()
	for _, t := range mgr.Tools() {
		reg.Register(t)
	}

	// 构造权限引擎（前四层防御 + 三层配置加载）
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
