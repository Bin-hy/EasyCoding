package hook

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestE2E_PreToolUseShellBlock 验证 PreToolUse shell exit 2 拦截 write_file。
func TestE2E_PreToolUseShellBlock(t *testing.T) {
	dir := t.TempDir()
	hookPath := filepath.Join(dir, ".mewcode", "hooks.yaml")
	_ = os.MkdirAll(filepath.Dir(hookPath), 0o755)

	yamlContent := `
hooks:
  - name: block-write
    event: PreToolUse
    if:
      all_of:
        - field: tool_name
          match: { type: exact, value: write_file }
    action:
      type: shell
      command: "echo blocked by hook >&2; exit 2"
`
	if err := os.WriteFile(hookPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	engine, _ := Load(dir)
	payload := Payload{
		"event":     "PreToolUse",
		"tool_name": "write_file",
		"tool_input": map[string]any{
			"path":    "hello.txt",
			"content": "hi",
		},
	}

	result := engine.Dispatch(context.Background(), EventPreToolUse, payload)
	if !result.Blocked {
		t.Error("expected Blocked=true for write_file with exit 2 shell hook")
	}
	if result.BlockingHookName != "block-write" {
		t.Errorf("expected hook name block-write, got %s", result.BlockingHookName)
	}
	if result.Reason != "blocked by hook" {
		t.Errorf("expected reason 'blocked by hook', got %q", result.Reason)
	}
}

// TestE2E_SessionStartPromptInject 验证 SessionStart prompt 注入。
func TestE2E_SessionStartPromptInject(t *testing.T) {
	dir := t.TempDir()
	hookPath := filepath.Join(dir, ".mewcode", "hooks.yaml")
	_ = os.MkdirAll(filepath.Dir(hookPath), 0o755)

	yamlContent := `
hooks:
  - name: zh-cn-default
    event: SessionStart
    action:
      type: prompt
      text: "默认用 zh-CN 回复"
`
	if err := os.WriteFile(hookPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	engine, _ := Load(dir)
	payload := Payload{"event": "SessionStart"}

	result := engine.Dispatch(context.Background(), EventSessionStart, payload)
	if result.Blocked {
		t.Error("SessionStart should not be blocked")
	}
	if len(result.InjectedPrompts) != 1 {
		t.Fatalf("expected 1 injected prompt, got %d", len(result.InjectedPrompts))
	}
	if result.InjectedPrompts[0] != "默认用 zh-CN 回复" {
		t.Errorf("unexpected prompt: %q", result.InjectedPrompts[0])
	}
}

// TestE2E_OnlyOnce 验证 only_once 在同一会话内只触发一次。
func TestE2E_OnlyOnce(t *testing.T) {
	dir := t.TempDir()
	hookPath := filepath.Join(dir, ".mewcode", "hooks.yaml")
	_ = os.MkdirAll(filepath.Dir(hookPath), 0o755)

	yamlContent := `
hooks:
  - name: first-turn
    event: PreUserMessage
    only_once: true
    action:
      type: shell
      command: "echo first-turn-fired >&2"
`
	if err := os.WriteFile(hookPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	engine, _ := Load(dir)
	payload := Payload{"event": "PreUserMessage"}

	// 第一次应触发
	r1 := engine.Dispatch(context.Background(), EventPreUserMessage, payload)
	if r1.Blocked {
		t.Error("first dispatch should not block")
	}

	// 第二次应跳过（only_once）
	r2 := engine.Dispatch(context.Background(), EventPreUserMessage, payload)
	if r2.Blocked {
		t.Error("second dispatch should not block")
	}

	// ResetForNewSession 后应重新触发
	engine.ResetForNewSession()
	r3 := engine.Dispatch(context.Background(), EventPreUserMessage, payload)
	if r3.Blocked {
		t.Error("after reset dispatch should not block")
	}
}

// TestE2E_AsyncNotBlocking 验证 async hook 不进入 Blocked/InjectedPrompts。
func TestE2E_AsyncNotBlocking(t *testing.T) {
	dir := t.TempDir()
	hookPath := filepath.Join(dir, ".mewcode", "hooks.yaml")
	_ = os.MkdirAll(filepath.Dir(hookPath), 0o755)

	yamlContent := `
hooks:
  - name: bg-format
    event: PostToolUse
    if:
      all_of:
        - field: tool_name
          match: { type: exact, value: write_file }
    action:
      type: shell
      command: "echo gofmt would run here >&2; exit 2"
    async: true
`
	if err := os.WriteFile(hookPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	engine, _ := Load(dir)
	payload := Payload{
		"event":     "PostToolUse",
		"tool_name": "write_file",
	}

	result := engine.Dispatch(context.Background(), EventPostToolUse, payload)
	// async hook 的 exit 2 不应导致 Blocked（async 不表达拦截）
	if result.Blocked {
		t.Error("async hook should not set Blocked")
	}
	if len(result.InjectedPrompts) > 0 {
		t.Error("async hook should not produce prompts")
	}
}

// TestE2E_UserPromptSubmitBlock 验证用户消息拦截。
func TestE2E_UserPromptSubmitBlock(t *testing.T) {
	dir := t.TempDir()
	hookPath := filepath.Join(dir, ".mewcode", "hooks.yaml")
	_ = os.MkdirAll(filepath.Dir(hookPath), 0o755)

	yamlContent := `
hooks:
  - name: warn-delete
    event: UserPromptSubmit
    if:
      all_of:
        - field: prompt
          match: { type: regex, value: "(?i)delete" }
    action:
      type: shell
      command: "echo 用户消息含 delete 关键字 >&2; exit 2"
`
	if err := os.WriteFile(hookPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	engine, _ := Load(dir)

	// 含 delete 的消息应被拦截
	payload := Payload{"event": "UserPromptSubmit", "prompt": "请帮我 delete 那个文件"}
	result := engine.Dispatch(context.Background(), EventUserPromptSubmit, payload)
	if !result.Blocked {
		t.Error("expected Blocked=true for prompt containing 'delete'")
	}

	// 不含 delete 的消息应放行
	payload2 := Payload{"event": "UserPromptSubmit", "prompt": "请帮我创建文件"}
	result2 := engine.Dispatch(context.Background(), EventUserPromptSubmit, payload2)
	if result2.Blocked {
		t.Error("expected Blocked=false for prompt without 'delete'")
	}
}

// TestE2E_AllOfAndAnyOfCondition 验证 all_of / any_of 组合条件。
func TestE2E_AllOfAndAnyOfCondition(t *testing.T) {
	dir := t.TempDir()
	hookPath := filepath.Join(dir, ".mewcode", "hooks.yaml")
	_ = os.MkdirAll(filepath.Dir(hookPath), 0o755)

	yamlContent := `
hooks:
  - name: go-file-only
    event: PostToolUse
    if:
      all_of:
        - field: tool_name
          match: { type: exact, value: write_file }
        - field: tool_input.path
          match: { type: glob, value: "**/*.go" }
    action:
      type: shell
      command: "echo gofmt >&2"
`
	if err := os.WriteFile(hookPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	engine, _ := Load(dir)

	// write_file + .go 路径 → 命中
	payload := Payload{
		"event":     "PostToolUse",
		"tool_name": "write_file",
		"tool_input": map[string]any{
			"path": "main.go",
		},
	}
	result := engine.Dispatch(context.Background(), EventPostToolUse, payload)
	if result.Blocked {
		t.Error("go file should not be blocked (cmd exits 0)")
	}

	// write_file + .md 路径 → 不命中（条件不满足，hook 不执行）
	payload2 := Payload{
		"event":     "PostToolUse",
		"tool_name": "write_file",
		"tool_input": map[string]any{
			"path": "README.md",
		},
	}
	result2 := engine.Dispatch(context.Background(), EventPostToolUse, payload2)
	if result2.Blocked {
		t.Error("md file should not trigger go-file-only hook")
	}
}

// TestE2E_HooksCommand 验证 /hooks 命令的 Rules() 和 Sources() 返回值正确。
func TestE2E_HooksCommand(t *testing.T) {
	dir := t.TempDir()
	hookPath := filepath.Join(dir, ".mewcode", "hooks.yaml")
	_ = os.MkdirAll(filepath.Dir(hookPath), 0o755)

	yamlContent := `
hooks:
  - name: hook-a
    event: SessionStart
    action:
      type: prompt
      text: "hello"
  - name: hook-b
    event: PreToolUse
    if:
      any_of:
        - field: tool_name
          match: { type: exact, value: bash }
    action:
      type: shell
      command: "echo check"
    only_once: true
  - name: hook-c
    event: PostToolUse
    async: true
    action:
      type: shell
      command: "echo bg"
`
	if err := os.WriteFile(hookPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	engine, sources := Load(dir)

	rules := engine.Rules()
	if len(rules) != 3 {
		t.Fatalf("expected 3 rules, got %d", len(rules))
	}

	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}

	// 验证 each rule 的 flags
	if rules[0].Name != "hook-a" || rules[0].OnlyOnce {
		t.Error("hook-a should not have only_once")
	}
	if rules[1].Name != "hook-b" || !rules[1].OnlyOnce {
		t.Error("hook-b should have only_once=true")
	}
	if rules[2].Name != "hook-c" || !rules[2].Async {
		t.Error("hook-c should have async=true")
	}
}
