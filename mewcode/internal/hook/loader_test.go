package hook

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoaderValidYAML(t *testing.T) {
	dir := t.TempDir()
	hookPath := filepath.Join(dir, ".mewcode", "hooks.yaml")
	os.MkdirAll(filepath.Dir(hookPath), 0o755)

	yamlContent := `
hooks:
  - name: test-shell
    event: PreToolUse
    if:
      all_of:
        - field: tool_name
          match: { type: exact, value: write_file }
    action:
      type: shell
      command: "echo blocked >&2; exit 2"
  - name: test-prompt
    event: SessionStart
    action:
      type: prompt
      text: "用 zh-CN 回复"
`
	if err := os.WriteFile(hookPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	engine, sources := Load(dir)
	if len(sources) != 1 {
		t.Errorf("expected 1 source, got %d", len(sources))
	}
	rules := engine.Rules()
	if len(rules) != 2 {
		t.Errorf("expected 2 rules, got %d", len(rules))
	}
}

func TestLoaderMissingFields(t *testing.T) {
	dir := t.TempDir()
	hookPath := filepath.Join(dir, ".mewcode", "hooks.yaml")
	os.MkdirAll(filepath.Dir(hookPath), 0o755)

	yamlContent := `
hooks:
  - name: ""
    event: PreToolUse
    action:
      type: shell
      command: "echo x"
  - name: bad-event
    event: UnknownEvent
    action:
      type: shell
      command: "echo x"
  - name: good-hook
    event: SessionStart
    action:
      type: shell
      command: "echo ok"
`
	if err := os.WriteFile(hookPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	engine, _ := Load(dir)
	rules := engine.Rules()
	if len(rules) != 1 {
		t.Errorf("expected 1 rule (only good-hook), got %d", len(rules))
	}
	if rules[0].Name != "good-hook" {
		t.Errorf("expected good-hook, got %s", rules[0].Name)
	}
}

func TestLoaderAllOfAndAnyOf(t *testing.T) {
	dir := t.TempDir()
	hookPath := filepath.Join(dir, ".mewcode", "hooks.yaml")
	os.MkdirAll(filepath.Dir(hookPath), 0o755)

	yamlContent := `
hooks:
  - name: both-condition
    event: PreToolUse
    if:
      all_of:
        - field: tool_name
          match: { type: exact, value: bash }
      any_of:
        - field: tool_name
          match: { type: exact, value: bash }
    action:
      type: shell
      command: "echo x"
  - name: good-hook
    event: SessionStart
    action:
      type: shell
      command: "echo ok"
`
	if err := os.WriteFile(hookPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	engine, _ := Load(dir)
	rules := engine.Rules()
	if len(rules) != 1 {
		t.Errorf("expected 1 rule (only good-hook), got %d: %v", len(rules), rules)
	}
}

func TestLoaderAsyncBlockingConflict(t *testing.T) {
	dir := t.TempDir()
	hookPath := filepath.Join(dir, ".mewcode", "hooks.yaml")
	os.MkdirAll(filepath.Dir(hookPath), 0o755)

	yamlContent := `
hooks:
  - name: bad-async
    event: PreToolUse
    async: true
    action:
      type: shell
      command: "echo x"
  - name: good-hook
    event: SessionStart
    action:
      type: shell
      command: "echo ok"
`
	if err := os.WriteFile(hookPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	engine, _ := Load(dir)
	rules := engine.Rules()
	if len(rules) != 1 {
		t.Errorf("expected 1 rule (only good-hook), got %d", len(rules))
	}
	if len(rules) > 0 && rules[0].Name != "good-hook" {
		t.Errorf("expected good-hook, got %s", rules[0].Name)
	}
}

func TestLoaderInvalidRegex(t *testing.T) {
	dir := t.TempDir()
	hookPath := filepath.Join(dir, ".mewcode", "hooks.yaml")
	os.MkdirAll(filepath.Dir(hookPath), 0o755)

	yamlContent := `
hooks:
  - name: bad-regex
    event: UserPromptSubmit
    if:
      all_of:
        - field: prompt
          match: { type: regex, value: "[invalid" }
    action:
      type: shell
      command: "echo x"
  - name: good-hook
    event: SessionStart
    action:
      type: shell
      command: "echo ok"
`
	if err := os.WriteFile(hookPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	engine, _ := Load(dir)
	rules := engine.Rules()
	if len(rules) != 1 {
		t.Errorf("expected 1 rule (only good-hook), got %d", len(rules))
	}
}
