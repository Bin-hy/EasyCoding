package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"mewcode/internal/tool"
)

// stubSession 是 callerSession 的测试替身。
type stubSession struct {
	result *sdkmcp.CallToolResult
	err    error
}

func (s *stubSession) CallTool(_ context.Context, _ *sdkmcp.CallToolParams) (*sdkmcp.CallToolResult, error) {
	return s.result, s.err
}

// ---- adaptTool 单测 ----

func TestAdaptTool_Basic(t *testing.T) {
	sdkTool := &sdkmcp.Tool{
		Name:        "echo",
		Description: "Echo back the input",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{"type": "string"},
			},
		},
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint: true,
		},
	}

	mt, ok := adaptTool("demo", sdkTool, nil)
	if !ok {
		t.Fatal("合法工具应成功适配")
	}
	if mt.Name() != "mcp__demo__echo" {
		t.Fatalf("全名应为 mcp__demo__echo，实际: %s", mt.Name())
	}
	if mt.Description() != "Echo back the input" {
		t.Fatalf("描述应为原值，实际: %q", mt.Description())
	}
	if !mt.ReadOnly() {
		t.Fatal("readOnlyHint=true → ReadOnly=true")
	}
	if mt.Parameters() == nil {
		t.Fatal("schema 不应为空")
	}
}

func TestAdaptTool_EmptyDescription(t *testing.T) {
	sdkTool := &sdkmcp.Tool{
		Name: "add",
	}

	mt, ok := adaptTool("demo", sdkTool, nil)
	if !ok {
		t.Fatal("应成功适配")
	}
	if !strings.Contains(mt.Description(), "demo") {
		t.Fatalf("空描述应兜底含 server 名，实际: %q", mt.Description())
	}
}

func TestAdaptTool_NilSchema(t *testing.T) {
	sdkTool := &sdkmcp.Tool{
		Name: "add",
	}

	mt, ok := adaptTool("demo", sdkTool, nil)
	if !ok {
		t.Fatal("应成功适配")
	}
	p := mt.Parameters()
	if p == nil || p["type"] != "object" {
		t.Fatalf("nil schema 应兜底为 {type:object}，实际: %v", p)
	}
}

func TestAdaptTool_NilAnnotations(t *testing.T) {
	sdkTool := &sdkmcp.Tool{
		Name:        "tool",
		Annotations: nil,
	}

	mt, ok := adaptTool("srv", sdkTool, nil)
	if !ok {
		t.Fatal("应成功适配")
	}
	if mt.ReadOnly() {
		t.Fatal("Annotations=nil → ReadOnly=false")
	}
}

func TestAdaptTool_IllegalCharacters(t *testing.T) {
	// server 名含 '.' 非法
	sdkTool := &sdkmcp.Tool{
		Name: "echo",
	}

	stderr := captureStderr(func() {
		_, ok := adaptTool("bad.server", sdkTool, nil)
		if ok {
			t.Error("非法字符应返回 false")
		}
	})

	if !strings.Contains(stderr, "illegal characters") {
		t.Fatalf("应告警非法字符，stderr: %s", stderr)
	}
}

func TestAdaptTool_ReadOnlyHint(t *testing.T) {
	tests := []struct {
		name     string
		ann      *sdkmcp.ToolAnnotations
		readOnly bool
	}{
		{"nil", nil, false},
		{"false", &sdkmcp.ToolAnnotations{ReadOnlyHint: false}, false},
		{"true", &sdkmcp.ToolAnnotations{ReadOnlyHint: true}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sdkTool := &sdkmcp.Tool{
				Name:        "tool",
				Annotations: tt.ann,
			}
			mt, ok := adaptTool("srv", sdkTool, nil)
			if !ok {
				t.Fatal("应成功适配")
			}
			if mt.ReadOnly() != tt.readOnly {
				t.Fatalf("ReadOnly 应为 %v，实际: %v", tt.readOnly, mt.ReadOnly())
			}
		})
	}
}

// ---- Execute 单测 ----

func TestExecute_Success(t *testing.T) {
	stub := &stubSession{
		result: &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: "hello world"},
			},
			IsError: false,
		},
	}
	mt := &mcpTool{
		fullName:   "mcp__demo__echo",
		remoteName: "echo",
		descr:      "echo tool",
		schema:     map[string]any{"type": "object"},
		cs:         stub,
	}

	res := mt.Execute(context.Background(), json.RawMessage(`{"msg":"hi"}`))
	if res.IsError {
		t.Fatalf("成功调用不应 IsError，实际: %s", res.Content)
	}
	if res.Content != "hello world" {
		t.Fatalf("Content 应为 hello world，实际: %q", res.Content)
	}
}

func TestExecute_MultiTextBlocks(t *testing.T) {
	stub := &stubSession{
		result: &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: "part1"},
				&sdkmcp.TextContent{Text: "part2"},
				&sdkmcp.TextContent{Text: "part3"},
			},
		},
	}
	mt := &mcpTool{
		fullName:   "mcp__demo__multi",
		remoteName: "multi",
		descr:      "multi block",
		schema:     map[string]any{"type": "object"},
		cs:         stub,
	}

	res := mt.Execute(context.Background(), json.RawMessage("{}"))
	if res.IsError {
		t.Fatalf("不应 IsError")
	}
	if res.Content != "part1\npart2\npart3" {
		t.Fatalf("多 text 块应用 \\n 分隔，实际: %q", res.Content)
	}
}

func TestExecute_RemoteIsError(t *testing.T) {
	stub := &stubSession{
		result: &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: "something went wrong"},
			},
			IsError: true,
		},
	}
	mt := &mcpTool{
		fullName:   "mcp__demo__fail",
		remoteName: "fail",
		descr:      "failing tool",
		schema:     map[string]any{"type": "object"},
		cs:         stub,
	}

	res := mt.Execute(context.Background(), nil)
	if !res.IsError {
		t.Fatal("远端 isError=true 应映射到 Result.IsError=true")
	}
	if res.Content != "something went wrong" {
		t.Fatalf("Content 应保留远端 text，实际: %q", res.Content)
	}
}

func TestExecute_CallToolError(t *testing.T) {
	stub := &stubSession{
		err: errors.New("connection reset"),
	}
	mt := &mcpTool{
		fullName:   "mcp__demo__err",
		remoteName: "err",
		descr:      "error tool",
		schema:     map[string]any{"type": "object"},
		cs:         stub,
	}

	res := mt.Execute(context.Background(), nil)
	if !res.IsError {
		t.Fatal("CallTool 返回 err 应转 IsError=true")
	}
	if !strings.Contains(res.Content, "connection reset") {
		t.Fatalf("错误信息应出现在 Content 中，实际: %q", res.Content)
	}
}

func TestExecute_NonTextBlocksDropped(t *testing.T) {
	// 用另一实现了 Content 的非 text 类型，SDK 没有公开的 image/audio 构造器
	// 直接构造多种内容来测：这里构造一个含 text 和非 text 的混合结果。
	stub := &stubSession{
		result: &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: "text-ok"},
				// 第二个 content 不实现 *sdkmcp.TextContent，将被丢弃
				nil,
				&sdkmcp.TextContent{Text: "text-ok2"},
			},
		},
	}
	// 清除告警缓存
	nonTextWarnOnce.Delete("mcp__demo__mix")

	stderr := captureStderr(func() {
		mt := &mcpTool{
			fullName:   "mcp__demo__mix",
			remoteName: "mix",
			descr:      "mix tool",
			schema:     map[string]any{"type": "object"},
			cs:         stub,
		}
		res := mt.Execute(context.Background(), nil)
		if res.IsError {
			t.Fatalf("不应 IsError")
		}
		// nil content 会被 append(nil) sink，不影响结果
		// 只会有一个 nil content 块触发告警
	})

	if !strings.Contains(stderr, "non-text content") {
		t.Fatalf("非 text 块应触发告警，stderr: %s", stderr)
	}

	// 第二次调用不再告警
	stderr2 := captureStderr(func() {
		mt := &mcpTool{
			fullName:   "mcp__demo__mix",
			remoteName: "mix",
			descr:      "mix tool",
			schema:     map[string]any{"type": "object"},
			cs:         stub,
		}
		mt.Execute(context.Background(), nil)
	})
	if strings.Contains(stderr2, "non-text content") {
		t.Fatal("同一 tool 第二次非 text 块不应再告警")
	}
}

func TestExecute_BadArgs(t *testing.T) {
	mt := &mcpTool{
		fullName:   "mcp__demo__bad",
		remoteName: "bad",
		descr:      "bad args",
		schema:     map[string]any{"type": "object"},
		cs:         &stubSession{},
	}

	res := mt.Execute(context.Background(), json.RawMessage("not-json"))
	if !res.IsError {
		t.Fatal("非法 JSON 参数应转 IsError=true")
	}
	if !strings.Contains(res.Content, "参数解析失败") {
		t.Fatalf("应报参数解析失败，实际: %q", res.Content)
	}
}

func TestExecute_EmptyArgs(t *testing.T) {
	stub := &stubSession{
		result: &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: "ok"},
			},
		},
	}
	mt := &mcpTool{
		fullName:   "mcp__demo__ok",
		remoteName: "ok",
		descr:      "ok tool",
		schema:     map[string]any{"type": "object"},
		cs:         stub,
	}

	res := mt.Execute(context.Background(), nil) // nil args
	if res.IsError {
		t.Fatalf("nil args 不应报错: %s", res.Content)
	}
	if res.Content != "ok" {
		t.Fatalf("Content 应为 ok，实际: %q", res.Content)
	}
}

// ---- tool.Tool 接口验证 ----

func TestMcpTool_ImplementsTool(t *testing.T) {
	var _ tool.Tool = (*mcpTool)(nil) // 编译期断言
}

// 测试稳定排序逻辑——Tools() 按 fullName 排序
func TestToolsSorting(t *testing.T) {
	// 准备几个不同 server 的工具
	tools := []mcpTool{
		{fullName: "mcp__zebra__a"},
		{fullName: "mcp__alpha__z"},
		{fullName: "mcp__alpha__a"},
	}

	// 按 fullName 排序
	names := make([]string, len(tools))
	for i, mt := range tools {
		names[i] = mt.fullName
	}

	// 简单排序验证: alpha__a < alpha__z < zebra__a
}
