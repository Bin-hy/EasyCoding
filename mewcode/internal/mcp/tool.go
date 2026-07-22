package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"mewcode/internal/tool"
)

// callerSession 是 mcpTool 依赖的最小会话能力（生产实现是 *sdkmcp.ClientSession）。
type callerSession interface {
	CallTool(ctx context.Context, params *sdkmcp.CallToolParams) (*sdkmcp.CallToolResult, error)
}

// 工具名禁用字符校验正则：仅允许 [A-Za-z0-9_-]。
var validToolName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// nonTextWarnOnce 记录每个工具名是否已告警过非 text 块。
var nonTextWarnOnce sync.Map

// mcpTool 实现 tool.Tool，将远端 MCP 工具适配为内置工具抽象。
type mcpTool struct {
	fullName   string // "mcp__<server>__<tool>"
	remoteName string // server 上的原始工具名
	descr      string
	schema     map[string]any // JSON Schema 透传
	readOnly   bool           // 仅来自远端 annotations.readOnlyHint==true
	cs         callerSession
}

// Name 返回 MCP 命名空间前缀的全名。
func (t *mcpTool) Name() string { return t.fullName }

// Description 返回工具描述，空时使用兜底文案。
func (t *mcpTool) Description() string { return t.descr }

// Parameters 返回工具的 JSON Schema 参数定义。
func (t *mcpTool) Parameters() map[string]any { return t.schema }

// ReadOnly 返回是否为只读工具。
func (t *mcpTool) ReadOnly() bool { return t.readOnly }

// Execute 调用远端 MCP 工具并聚合结果。
//
// 参数 args 是 JSON 编码的工具参数。远端返回的 text content 块按序拼接为
// Result.Content；非 text 块静默丢弃（首次告警）；远端 isError 映射到
// Result.IsError；协议错误（含 30s 超时）也转为 IsError==true 的结构化错误。
func (t *mcpTool) Execute(ctx context.Context, args json.RawMessage) tool.Result {
	ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// 解析参数，空参数视为 nil
	var argMap map[string]any
	if len(args) > 0 {
		if err := json.Unmarshal(args, &argMap); err != nil {
			return tool.Result{
				Content: fmt.Sprintf("参数解析失败: %v", err),
				IsError: true,
			}
		}
	}

	res, err := t.cs.CallTool(ctx2, &sdkmcp.CallToolParams{
		Name:      t.remoteName,
		Arguments: argMap,
	})

	if err != nil {
		return tool.Result{
			Content: fmt.Sprintf("MCP 工具调用失败: %v", err),
			IsError: true,
		}
	}

	// 遍历 content，拼接 text 块
	var sb strings.Builder
	nonTextCount := 0
	for _, c := range res.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(tc.Text)
		} else {
			nonTextCount++
			if _, warned := nonTextWarnOnce.LoadOrStore(t.fullName, true); !warned {
				fmt.Fprintf(os.Stderr, "[mcp] warn: tool %s returned non-text content blocks (dropped)\n", t.fullName)
			}
		}
	}

	return tool.Result{
		Content: sb.String(),
		IsError: res.IsError,
	}
}

// adaptTool 把一个远端 MCP Tool 包装为 mcpTool。
//
// 返回 (nil, false) 表示该工具被跳过（命名非法）。
// 命名校验（fullName 仅允许 [A-Za-z0-9_-]）+ 描述兜底 + schema 透传 + readOnly 判定。
func adaptTool(serverName string, t *sdkmcp.Tool, cs callerSession) (*mcpTool, bool) {
	fullName := "mcp__" + serverName + "__" + t.Name

	// 禁用字符校验
	if !validToolName.MatchString(fullName) {
		fmt.Fprintf(os.Stderr, "[mcp] warn: skip tool %s: name contains illegal characters\n", fullName)
		return nil, false
	}

	// 描述：空则兜底
	descr := t.Description
	if descr == "" {
		descr = "来自 MCP server " + serverName + " 的工具 " + t.Name
	}

	// schema 透传
	var schema map[string]any
	if t.InputSchema != nil {
		b, _ := json.Marshal(t.InputSchema)
		json.Unmarshal(b, &schema)
	}
	if schema == nil || len(schema) == 0 {
		schema = map[string]any{"type": "object"}
	}

	// readOnly：严格只信 annotations.readOnlyHint==true
	readOnly := t.Annotations != nil && t.Annotations.ReadOnlyHint

	return &mcpTool{
		fullName:   fullName,
		remoteName: t.Name,
		descr:      descr,
		schema:     schema,
		readOnly:   readOnly,
		cs:         cs,
	}, true
}
