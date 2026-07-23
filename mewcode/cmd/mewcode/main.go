package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"mewcode/internal/agent"
	"mewcode/internal/compact"
	"mewcode/internal/config"
	"mewcode/internal/conversation"
	"mewcode/internal/instructions"
	"mewcode/internal/mcp"
	"mewcode/internal/memory"
	"mewcode/internal/permission"
	"mewcode/internal/session"
	"mewcode/internal/tool"
	"mewcode/internal/tui"
)

// 构建时通过 ldflags 注入：go build -ldflags="-X 'main.version=x.y.z'"
var version = "0.1.0"

func main() {
	// 加载配置（两层 fallback）：
	//   1. 项目级 .mewcode/config.yaml — 开发时使用
	//   2. 用户级 ~/.mewcode/config.yaml — 安装后使用
	cfgPath, err := resolveConfigPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "配置错误: %v\n", err)
		os.Exit(1)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "配置错误: %v\n", err)
		fmt.Fprintf(os.Stderr, "请在 %s 创建配置文件，格式参考:\n", cfgPath)
		fmt.Fprintf(os.Stderr, "  providers:\n")
		fmt.Fprintf(os.Stderr, "    - name: MyProvider\n")
		fmt.Fprintf(os.Stderr, "      protocol: anthropic\n")
		fmt.Fprintf(os.Stderr, "      api_key: sk-xxx\n")
		fmt.Fprintf(os.Stderr, "      model: claude-sonnet-5\n")
		os.Exit(1)
	}

	root, _ := os.Getwd()

	// --- ch09: 加载项目指令 ---
	loader := instructions.NewLoader(root)
	instructionText, err := loader.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[instructions] 加载项目指令失败: %v\n", err)
	}
	if instructionText != "" {
		fmt.Fprintf(os.Stderr, "[instructions] 项目指令已加载 (%d 字符)\n", len(instructionText))
	}

	// --- ch09: 初始化记忆管理器 ---
	userHome, _ := os.UserHomeDir()
	projectMemDir := filepath.Join(root, ".mewcode", "memory")
	userMemDir := filepath.Join(userHome, ".mewcode", "memory")
	memMgr := memory.NewManager(projectMemDir, userMemDir, nil, "") // provider 待选定后设置
	memoryText := memMgr.LoadIndex()
	if memoryText != "" {
		fmt.Fprintf(os.Stderr, "[memory] 记忆索引已加载 (%d 字符)\n", len(memoryText))
	}

	// 构造工具注册中心（6 内置工具）
	reg := tool.NewDefaultRegistry()

	// 加载 MCP 配置并连接远端 server
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

	// --- ch09: 构造会话级运行时状态（含 SessionDir）---
	sessionCtx, err := compact.NewSessionContext(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "会话初始化失败: %v\n", err)
		os.Exit(1)
	}
	runtime := &agent.SessionRuntime{
		Replacement:   compact.NewContentReplacementState(),
		Recovery:      compact.NewRecoveryState(),
		AutoTracking:  compact.NewAutoCompactTrackingState(),
		Session:       sessionCtx,
		ContextWindow: cfg.Providers[0].EffectiveContextWindow(),
	}

	// --- ch09: 创建 Session JSONL Writer ---
	writer, err := session.NewWriter(sessionCtx.SessionDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[session] 创建 JSONL Writer 失败: %v\n", err)
		os.Exit(1)
	}
	defer writer.Close()

	// --- ch09: 后台清理过期会话 ---
	sessionsDir := filepath.Join(root, ".mewcode", "sessions")
	go func() {
		if err := session.CleanExpired(sessionsDir, 30*24*time.Hour); err != nil {
			fmt.Fprintf(os.Stderr, "[session] 过期会话清理失败: %v\n", err)
		}
	}()

	// --- 使用带回调的 Conversation ---
	modelName := ""
	if len(cfg.Providers) > 0 {
		modelName = cfg.Providers[0].Model
	}
	conv := conversation.NewWithHooks(writer.OnAppend(modelName), writer.OnReplace())

	// 启动 TUI
	m := tui.New(cfg.Providers, version, reg, eng, runtime, writer, memMgr, instructionText, memoryText)
	// 注入带回调的 Conversation（覆盖 TUI 内部创建的空 Conversation）
	m.SetConversation(conv)

	if err := m.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "运行错误: %v\n", err)
		os.Exit(1)
	}
}

// resolveConfigPath 按两层 fallback 定位配置文件：
//  1. 项目级 .mewcode/config.yaml（当前目录下）
//  2. 用户级 ~/.mewcode/config.yaml（HOME 目录下）
//
// 存在即返回，都不存在则返回用户级路径（由调用方报告缺失）。
func resolveConfigPath() (string, error) {
	// 项目级（开发模式）
	localPath := filepath.Join(".mewcode", "config.yaml")
	if _, err := os.Stat(localPath); err == nil {
		return localPath, nil
	}

	// 用户级（安装模式）
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法获取用户目录: %w", err)
	}
	return filepath.Join(home, ".mewcode", "config.yaml"), nil
}
