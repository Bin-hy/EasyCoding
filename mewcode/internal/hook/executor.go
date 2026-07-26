package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"text/template"
	"time"
)

// Executor 四类动作执行器。
type Executor struct {
	httpClient *http.Client
}

// ExecutionResult 单次动作执行的结果。
type ExecutionResult struct {
	Blocked bool   // 是否表达拦截（仅拦截类事件下有语义）
	Reason  string // 拦截原因
	Prompt  string // 仅 prompt 动作非空
	Err     error  // hook 自身失败（不拦截）
}

// NewExecutor 构造执行器。
func NewExecutor() *Executor {
	return &Executor{
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Run 按 rule.Action.Type 分发到四类动作执行器。
// blocking 参数指示是否为拦截类事件（影响 shell exit code 2 与 HTTP block 判定）。
func (x *Executor) Run(ctx context.Context, rule Rule, payload Payload, blocking bool) ExecutionResult {
	switch rule.Action.Type {
	case ActionShell:
		if rule.Action.Shell == nil {
			return ExecutionResult{Err: fmt.Errorf("shell action with nil Shell")}
		}
		timeout := rule.Timeout
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		return x.runShell(ctx, rule.Action.Shell, payload, blocking, timeout)
	case ActionPrompt:
		if rule.Action.Prompt == nil {
			return ExecutionResult{Err: fmt.Errorf("prompt action with nil Prompt")}
		}
		return x.runPrompt(rule.Action.Prompt)
	case ActionHTTP:
		if rule.Action.HTTP == nil {
			return ExecutionResult{Err: fmt.Errorf("http action with nil HTTP")}
		}
		timeout := rule.Timeout
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		return x.runHTTP(ctx, rule.Action.HTTP, payload, blocking, timeout)
	case ActionSubagent:
		if rule.Action.Subagent == nil {
			return ExecutionResult{Err: fmt.Errorf("subagent action with nil Subagent")}
		}
		return x.runSubagent(rule.Action.Subagent)
	default:
		return ExecutionResult{Err: fmt.Errorf("unknown action type: %s", rule.Action.Type)}
	}
}

// ─── shell 动作 ──────────────────────────────────────────

func (x *Executor) runShell(ctx context.Context, sa *ShellAction, payload Payload, blocking bool, timeout time.Duration) ExecutionResult {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", sa.Command)

	// 将 payload 序列化为单行 JSON 经 stdin 传入
	payloadJSON := marshalSorted(payload)
	cmd.Stdin = bytes.NewReader(payloadJSON)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// 超时
	if ctx.Err() != nil {
		return ExecutionResult{Err: fmt.Errorf("timed out after %s", timeout)}
	}

	if err != nil {
		// 检查 exit code
		if exitErr, ok := err.(*exec.ExitError); ok {
			code := exitErr.ExitCode()

			if blocking && code == 2 {
				// 拦截信号：stderr || stdout 合并去尾换行作为拒绝原因
				reason := strings.TrimSpace(stderr.String() + stdout.String())
				if reason == "" {
					reason = "blocked by hook (exit code 2)"
				}
				return ExecutionResult{Blocked: true, Reason: reason}
			}

			if code == 0 {
				// 成功放行
				return ExecutionResult{}
			}

			// 其它非零退出 → hook 失败
			return ExecutionResult{Err: fmt.Errorf("exit %d: %s", code, strings.TrimSpace(stderr.String()))}
		}

		// 非 ExitError（如启动失败）
		return ExecutionResult{Err: err}
	}

	// 正常退出（exit 0）：放行
	return ExecutionResult{}
}

// ─── prompt 动作 ─────────────────────────────────────────

func (x *Executor) runPrompt(pa *PromptAction) ExecutionResult {
	return ExecutionResult{Prompt: pa.Text}
}

// ─── HTTP 动作 ───────────────────────────────────────────

func (x *Executor) runHTTP(ctx context.Context, ha *HTTPAction, payload Payload, blocking bool, timeout time.Duration) ExecutionResult {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 构造请求体
	var bodyBytes []byte
	if ha.Body == "" {
		// 缺省：JSON 序列化 payload
		bodyBytes = marshalSorted(payload)
	} else {
		// Go text/template 渲染
		rendered, err := renderTemplate(ha.Body, payload)
		if err != nil {
			return ExecutionResult{Err: fmt.Errorf("http template render failed: %w", err)}
		}
		bodyBytes = []byte(rendered)
	}

	method := ha.Method
	if method == "" {
		method = "POST"
	}

	req, err := http.NewRequestWithContext(ctx, method, ha.URL, bytes.NewReader(bodyBytes))
	if err != nil {
		return ExecutionResult{Err: fmt.Errorf("http request create failed: %w", err)}
	}

	for k, v := range ha.Headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return ExecutionResult{Err: fmt.Errorf("http request failed: %w", err)}
	}
	defer func() { _ = resp.Body.Close() }()

	// 拦截判定：2xx 且 body 含 {"decision":"block","reason":"..."}
	if blocking && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var bodyMap map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&bodyMap); err != nil {
			// JSON 解析失败 → hook 失败但不拦截
			return ExecutionResult{Err: fmt.Errorf("http response JSON parse failed: %w", err)}
		}
		if decision, _ := bodyMap["decision"].(string); decision == "block" {
			reason, _ := bodyMap["reason"].(string)
			if reason == "" {
				reason = "blocked by http hook"
			}
			return ExecutionResult{Blocked: true, Reason: reason}
		}
	}

	// 其它情况：放行
	return ExecutionResult{}
}

// ─── subagent 动作（占位）─────────────────────────────────

func (x *Executor) runSubagent(sa *SubagentAction) ExecutionResult {
	fmt.Fprintf(os.Stderr, "[hook subagent] not yet implemented, skipped: %s\n", sa.AgentName)
	return ExecutionResult{}
}

// ─── 辅助函数 ────────────────────────────────────────────

// marshalSorted 将 Payload 序列化为 JSON，保证 key 字典序（Go json.Marshal 默认行为）。
func marshalSorted(p Payload) []byte {
	b, err := json.Marshal(p)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// renderTemplate 使用 Go text/template 渲染模板字符串。
// 只支持最基本的字段访问 {{.field}}，不开放函数调用。
func renderTemplate(tmpl string, payload Payload) (string, error) {
	t, err := template.New("hook").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, payload); err != nil {
		return "", err
	}
	return buf.String(), nil
}
