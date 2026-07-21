package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_ValidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := `
providers:
  - name: "claude"
    protocol: "anthropic"
    api_key: "sk-ant-test"
    model: "claude-opus-4-8"
    thinking: true
  - name: "gpt"
    protocol: "openai"
    api_key: "sk-test"
    model: "gpt-5"
`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if len(cfg.Providers) != 2 {
		t.Errorf("期望 2 个 providers，实际 %d", len(cfg.Providers))
	}
	if cfg.Providers[0].Name != "claude" {
		t.Errorf("期望 Name=claude，实际 %s", cfg.Providers[0].Name)
	}
	if cfg.Providers[0].Thinking != true {
		t.Errorf("期望 Thinking=true")
	}
	if cfg.Providers[1].Name != "gpt" {
		t.Errorf("期望 Name=gpt，实际 %s", cfg.Providers[1].Name)
	}
}

func TestLoad_MissingKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := `
providers:
  - name: "claude"
    protocol: "anthropic"
    model: "claude-opus-4-8"
`
	// 缺少 api_key
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Error("期望返回错误，但成功")
	}
}

func TestLoad_InvalidProtocol(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := `
providers:
  - name: "test"
    protocol: "invalid"
    api_key: "sk-test"
    model: "gpt-5"
`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Error("期望返回错误，但成功")
	}
}

func TestLoad_FileMissing(t *testing.T) {
	_, err := Load("/nonexistent/file.yaml")
	if err == nil {
		t.Error("期望返回错误，但成功")
	}
}
