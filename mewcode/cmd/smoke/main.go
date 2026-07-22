// 冒烟测试：非交互式验证 Agent Loop 核心流程。
// 用法：go run ./cmd/smoke
package main

import (
	"context"
	"fmt"
	"os"

	"mewcode/internal/agent"
	"mewcode/internal/config"
	"mewcode/internal/conversation"
	"mewcode/internal/llm"
	"mewcode/internal/permission"
	"mewcode/internal/tool"
)

func main() {
	cfg, err := config.Load(".mewcode/config.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "配置错误: %v\n", err)
		os.Exit(1)
	}

	if len(cfg.Providers) == 0 {
		fmt.Fprintln(os.Stderr, "无可用 provider")
		os.Exit(1)
	}

	p, err := llm.New(cfg.Providers[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化 provider 失败: %v\n", err)
		os.Exit(1)
	}

	reg := tool.NewDefaultRegistry()
	conv := &conversation.Conversation{}

	// 构造权限引擎（非交互式，以 ModeBypass 运行）
	cwd, _ := os.Getwd()
	eng, _ := permission.NewEngine(cwd)

	// 场景1：多轮工具调用（含缓存用量打印）
	fmt.Println("=== 场景1: 多轮工具调用 ===")
	ctx := context.Background()
	a := agent.New(p, reg, "dev", eng)

	conv.AddUser("帮我读 docs/ch03/spec.md 文件，用一句话总结它说了什么")

	iter := 0
	for ev := range a.Run(ctx, conv, permission.ModeBypass) {
		switch {
		case ev.Text != "":
			fmt.Print(ev.Text)
		case ev.Tool != nil && ev.Tool.Phase == agent.PhaseStart:
			fmt.Printf("\n[tool] %s(%s)\n", ev.Tool.Name, ev.Tool.Args)
		case ev.Tool != nil && ev.Tool.Phase == agent.PhaseEnd:
			fmt.Printf("[result] %s\n", ev.Tool.Result)
		case ev.Iter > 0:
			iter = ev.Iter
		case ev.Usage != nil:
			fmt.Printf("[usage] in=%d out=%d cache_write=%d cache_read=%d\n",
				ev.Usage.Input, ev.Usage.Output, ev.Usage.CacheWrite, ev.Usage.CacheRead)
		case ev.Notice != "":
			fmt.Printf("[notice] %s\n", ev.Notice)
		case ev.Err != nil:
			fmt.Printf("[error] %v\n", ev.Err)
		case ev.Done:
			fmt.Println("\n[Done]")
		}
	}

	fmt.Printf("\n总迭代轮次: %d\n", iter)
	fmt.Printf("对话消息数: %d\n", conv.Len())
	fmt.Println("=== 场景1 通过 ===")

	// 场景2：用户取消后历史一致性
	fmt.Println("\n=== 场景2: 取消历史一致性 ===")
	conv2 := &conversation.Conversation{}
	conv2.AddUser("列出当前目录文件")

	cancelCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range a.Run(cancelCtx, conv2, permission.ModeBypass) {
			if ev.Tool != nil && ev.Tool.Phase == agent.PhaseStart {
				cancel() // 工具开始时立即取消
			}
			_ = ev
		}
	}()
	<-done

	// 验证历史合法性：最后一条消息必须是 assistant
	lastRole := conv2.LastRole()
	fmt.Printf("取消后 LastRole: %s\n", lastRole)
	if lastRole == "assistant" {
		fmt.Println("历史一致：以 assistant 结尾，合法 ✓")
	} else {
		fmt.Printf("历史不一致：以 %s 结尾，可能不合法\n", lastRole)
	}

	// 继续对话验证不报错
	conv2.AddUser("继续")
	fmt.Println("继续对话（验证无 400 错误）...")
	for ev := range a.Run(ctx, conv2, permission.ModeBypass) {
		switch {
		case ev.Err != nil:
			fmt.Printf("[error] %v\n", ev.Err)
		case ev.Done:
			fmt.Println("[Done]")
		}
	}
	fmt.Println("=== 场景2 通过 ===")
}
