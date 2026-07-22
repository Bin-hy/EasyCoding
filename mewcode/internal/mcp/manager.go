package mcp

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"sync"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"mewcode/internal/tool"
)

// 连接超时与关闭兜底。包级 var 便于单测改为短值。
var (
	connectTimeout = 30 * time.Second
	closeDeadline  = 5 * time.Second
)

// Manager 管理所有 MCP server 的连接、工具适配与生命周期。
type Manager struct {
	mu       sync.Mutex
	sessions []*session
	tools    []tool.Tool
}

// session 封装一个已建立连接的 MCP server 会话。
type session struct {
	name string
	cs   *sdkmcp.ClientSession
}

// headerRoundTripper 在每次 HTTP 请求中注入自定义 headers。
type headerRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

// RoundTrip 注入 headers 后代理到 base RoundTripper。
func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	for k, v := range h.headers {
		req.Header.Set(k, v)
	}
	return h.base.RoundTrip(req)
}

// mergeOSEnv 合并宿主环境变量与额外 env map（后者覆盖同名键）。
func mergeOSEnv(extra map[string]string) []string {
	envMap := make(map[string]string)
	for _, kv := range os.Environ() {
		// 按第一个 = 分割
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				envMap[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	for k, v := range extra {
		envMap[k] = v
	}

	result := make([]string, 0, len(envMap))
	for k, v := range envMap {
		result = append(result, k+"="+v)
	}
	return result
}

// NewManager 并发连接所有配置中的 MCP server。
//
// 每个 server 受 connectTimeout 超时约束；连接失败或超时仅跳过该 server
// 不阻塞启动。传入 ctx 控制所有连接尝试的整体生命周期。
func NewManager(ctx context.Context, cfg Config, version string) *Manager {
	mgr := &Manager{}
	var wg sync.WaitGroup

	for name, srv := range cfg.Servers {
		wg.Add(1)
		go func(name string, srv ServerConfig) {
			defer wg.Done()

			ctx2, cancel := context.WithTimeout(ctx, connectTimeout)
			defer cancel()

			var transport sdkmcp.Transport
			switch srv.Type {
			case "stdio":
				cmd := exec.CommandContext(ctx2, srv.Command, srv.Args...)
				cmd.Env = mergeOSEnv(srv.Env)
				cmd.Stderr = os.Stderr
				transport = &sdkmcp.CommandTransport{Command: cmd}
			case "http":
				hc := &http.Client{
					Transport: &headerRoundTripper{
						base:    http.DefaultTransport,
						headers: srv.Headers,
					},
				}
				transport = &sdkmcp.StreamableClientTransport{
					Endpoint:             srv.URL,
					HTTPClient:           hc,
					DisableStandaloneSSE: true,
				}
			}

			client := sdkmcp.NewClient(&sdkmcp.Implementation{
				Name:    "mewcode",
				Version: version,
			}, nil)

			cs, err := client.Connect(ctx2, transport, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[mcp] warn: connect server %s failed: %v\n", name, err)
				return
			}

			lst, err := cs.ListTools(ctx2, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[mcp] warn: list tools for server %s failed: %v\n", name, err)
				cs.Close() // 释放连接
				return
			}

			// 适配所有工具
			var adapted []tool.Tool
			for _, t := range lst.Tools {
				mt, ok := adaptTool(name, t, cs)
				if ok {
					adapted = append(adapted, mt)
				}
			}

			// 写入共享状态
			mgr.mu.Lock()
			mgr.sessions = append(mgr.sessions, &session{name: name, cs: cs})
			mgr.tools = append(mgr.tools, adapted...)
			mgr.mu.Unlock()

			fmt.Fprintf(os.Stderr, "[mcp] info: server %s connected, %d tools registered\n", name, len(adapted))
		}(name, srv)
	}

	wg.Wait()

	// 稳定排序（先 server 名再 tool 名，fullName 已含 server 前缀）
	sort.Slice(mgr.tools, func(i, j int) bool {
		return mgr.tools[i].Name() < mgr.tools[j].Name()
	})

	return mgr
}

// Tools 返回适配好的工具列表（按名排序）。调用方不应修改返回的切片。
func (m *Manager) Tools() []tool.Tool {
	m.mu.Lock()
	defer m.mu.Unlock()
	// 返回拷贝防外部修改
	cp := make([]tool.Tool, len(m.tools))
	copy(cp, m.tools)
	return cp
}

// Close 关闭所有会话。并发执行，总超时 closeDeadline 兜底。
func (m *Manager) Close() {
	m.mu.Lock()
	sessions := make([]*session, len(m.sessions))
	copy(sessions, m.sessions)
	m.mu.Unlock()

	if len(sessions) == 0 {
		return
	}

	var wg sync.WaitGroup
	for _, s := range sessions {
		wg.Add(1)
		go func(cs *sdkmcp.ClientSession) {
			defer wg.Done()
			cs.Close()
		}(s.cs)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// 全部正常关闭
	case <-time.After(closeDeadline):
		// 兜底超时，不等了
	}
}
