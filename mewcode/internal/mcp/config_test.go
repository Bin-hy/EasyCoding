package mcp

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStderr 重定向 os.Stderr 到 buffer，执行 f，返回捕获的输出并恢复。
func captureStderr(f func()) string {
	// 保存原始 stderr
	orig := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	f()
	_ = w.Close()

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	return buf.String()
}

// writeTempYAML 在指定目录写入临时 YAML 文件，返回文件路径。
func writeTempYAML(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("写入临时文件失败: %v", err)
	}
	return path
}

// ---- loadFile 单测 ----

func TestLoadFile_NotExist(t *testing.T) {
	cfg, err := loadFile("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("文件不存在应返回 nil err，实际: %v", err)
	}
	if len(cfg.McpServers) != 0 {
		t.Fatalf("空文件应返回空 McpServers，实际: %v", cfg.McpServers)
	}
}

func TestLoadFile_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := writeTempYAML(t, dir, "invalid.yaml", "{{{bad: yaml: [[[")

	_, err := loadFile(path)
	if err == nil {
		t.Fatal("非法 YAML 应返回 error")
	}
}

func TestLoadFile_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := writeTempYAML(t, dir, "config.yaml", `
mcp_servers:
  test:
    type: stdio
    command: echo
`)

	cfg, err := loadFile(path)
	if err != nil {
		t.Fatalf("合法 YAML 不应返回 error: %v", err)
	}
	if _, ok := cfg.McpServers["test"]; !ok {
		t.Fatal("应解析出 test server")
	}
}

// ---- expandVars 单测 ----

func TestExpandVars_Defined(t *testing.T) {
	_ = os.Setenv("TEST_MCP_VAR", "hello_value")
	defer func() { _ = os.Unsetenv("TEST_MCP_VAR") }()

	out, undefined := expandVars("prefix_${TEST_MCP_VAR}_suffix")
	if out != "prefix_hello_value_suffix" {
		t.Fatalf("展开结果错误: %q", out)
	}
	if len(undefined) != 0 {
		t.Fatalf("应无未定义变量，实际: %v", undefined)
	}
}

func TestExpandVars_Undefined(t *testing.T) {
	out, undefined := expandVars("${UNDEFINED_VAR_XYZ}")
	if out != "" {
		t.Fatalf("未定义变量应展开为空串，实际: %q", out)
	}
	if len(undefined) == 0 || undefined[0] != "UNDEFINED_VAR_XYZ" {
		t.Fatalf("应报告未定义变量，实际: %v", undefined)
	}
}

func TestExpandVars_MultipleVars(t *testing.T) {
	_ = os.Setenv("A", "1")
	_ = os.Setenv("B", "2")
	defer func() { _ = os.Unsetenv("A"); _ = os.Unsetenv("B") }()

	out, undefined := expandVars("${A}_${B}_${C}")
	if out != "1_2_" {
		t.Fatalf("展开结果错误: %q", out)
	}
	if len(undefined) != 1 || undefined[0] != "C" {
		t.Fatalf("应仅报告 C 未定义，实际: %v", undefined)
	}
}

func TestExpandVars_NoVars(t *testing.T) {
	out, undefined := expandVars("plain text without variables")
	if out != "plain text without variables" {
		t.Fatalf("无变量应原样返回: %q", out)
	}
	if len(undefined) != 0 {
		t.Fatalf("应无未定义变量: %v", undefined)
	}
}

// ---- applyExpansion 告警 ----

func TestApplyExpansion_WarnsUndefined(t *testing.T) {
	srv := &rawServer{
		Type: "stdio",
		Env:  map[string]string{"TOKEN": "${NONEXISTENT_TOKEN_XYZ}"},
	}
	stderr := captureStderr(func() {
		applyExpansion("test-srv", srv)
	})

	if !strings.Contains(stderr, "NONEXISTENT_TOKEN_XYZ") {
		t.Fatalf("应告警未定义变量，stderr: %s", stderr)
	}
	if !strings.Contains(stderr, "test-srv") {
		t.Fatalf("告警应包含 server 名，stderr: %s", stderr)
	}
	if srv.Env["TOKEN"] != "" {
		t.Fatalf("未定义变量应展开为空串，实际: %q", srv.Env["TOKEN"])
	}
}

// ---- mergeServers 单测 ----

func TestMergeServers_ProjectOverridesUser(t *testing.T) {
	user := map[string]rawServer{
		"github": {Type: "stdio", Command: "user-cmd"},
		"shared": {Type: "stdio", Command: "user-shared", Args: []string{"--user"}},
	}
	project := map[string]rawServer{
		"shared": {Type: "http", URL: "https://project.example.com"},
		"local":  {Type: "stdio", Command: "local-cmd"},
	}

	merged := mergeServers(user, project)

	// user-only server
	if merged["github"].Command != "user-cmd" {
		t.Fatalf("user-only server 不应被覆盖: %+v", merged["github"])
	}

	// project-override: 应完整替换
	if merged["shared"].Type != "http" || merged["shared"].URL != "https://project.example.com" {
		t.Fatalf("project 同名 server 应完整覆盖: %+v", merged["shared"])
	}
	if len(merged["shared"].Args) != 0 {
		t.Fatalf("project 覆盖应不保留 user 字段，Args: %v", merged["shared"].Args)
	}

	// project-only server
	if merged["local"].Command != "local-cmd" {
		t.Fatalf("project-only server 应存在: %+v", merged["local"])
	}
}

// ---- validateServer 单测 ----

func TestValidateServer_MissingType(t *testing.T) {
	stderr := captureStderr(func() {
		_, ok := validateServer("s1", rawServer{Command: "echo"})
		if ok {
			t.Error("缺 type 应被跳过")
		}
	})
	if !strings.Contains(stderr, "missing type") {
		t.Fatalf("告警应说明 missing type，stderr: %s", stderr)
	}
}

func TestValidateServer_IllegalType(t *testing.T) {
	stderr := captureStderr(func() {
		_, ok := validateServer("s1", rawServer{Type: "grpc", Command: "x"})
		if ok {
			t.Error("非法 type 应被跳过")
		}
	})
	if !strings.Contains(stderr, "unknown type") {
		t.Fatalf("告警应说明 unknown type，stderr: %s", stderr)
	}
}

func TestValidateServer_StdioMissingCommand(t *testing.T) {
	stderr := captureStderr(func() {
		_, ok := validateServer("s1", rawServer{Type: "stdio"})
		if ok {
			t.Error("stdio 缺 command 应被跳过")
		}
	})
	if !strings.Contains(stderr, "command") {
		t.Fatalf("告警应说明缺 command，stderr: %s", stderr)
	}
}

func TestValidateServer_HttpMissingURL(t *testing.T) {
	stderr := captureStderr(func() {
		_, ok := validateServer("s1", rawServer{Type: "http"})
		if ok {
			t.Error("http 缺 url 应被跳过")
		}
	})
	if !strings.Contains(stderr, "url") {
		t.Fatalf("告警应说明缺 url，stderr: %s", stderr)
	}
}

func TestValidateServer_ValidStdio(t *testing.T) {
	sc, ok := validateServer("s1", rawServer{
		Type:    "stdio",
		Command: "echo",
		Args:    []string{"hello"},
		Env:     map[string]string{"FOO": "bar"},
	})
	if !ok {
		t.Fatal("合法 stdio server 应通过校验")
	}
	if sc.Command != "echo" {
		t.Fatalf("Command 应为 echo，实际: %q", sc.Command)
	}
}

func TestValidateServer_ValidHTTP(t *testing.T) {
	sc, ok := validateServer("s1", rawServer{
		Type:    "http",
		URL:     "https://example.com/mcp",
		Headers: map[string]string{"Authorization": "Bearer token"},
	})
	if !ok {
		t.Fatal("合法 http server 应通过校验")
	}
	if sc.URL != "https://example.com/mcp" {
		t.Fatalf("URL 应为原值，实际: %q", sc.URL)
	}
}

// ---- LoadConfig 集成单测 ----

func TestLoadConfig_NoFiles(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig 永不返回 error，实际: %v", err)
	}
	if len(cfg.Servers) != 0 {
		t.Fatalf("无配置文件时应返回空 Servers，实际: %d", len(cfg.Servers))
	}
}

func TestLoadConfig_UserOnly(t *testing.T) {
	// 用临时 HOME 覆盖
	homeDir := t.TempDir()
	mewcodeDir := filepath.Join(homeDir, ".mewcode")
	_ = os.MkdirAll(mewcodeDir, 0755)
	writeTempYAML(t, mewcodeDir, "config.yaml", `
mcp_servers:
  gh:
    type: stdio
    command: npx
`)

	t.Setenv("HOME", homeDir)

	dir := t.TempDir()
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig 不应返回 error: %v", err)
	}
	if srv, ok := cfg.Servers["gh"]; !ok || srv.Command != "npx" {
		t.Fatalf("用户级 server 应被加载: %+v", cfg.Servers)
	}
}

func TestLoadConfig_ProjectOnly(t *testing.T) {
	dir := t.TempDir()
	writeTempYAML(t, dir, ".mewcode.yaml", `
mcp_servers:
  local:
    type: http
    url: https://local.example.com
`)

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig 不应返回 error: %v", err)
	}
	if srv, ok := cfg.Servers["local"]; !ok || srv.URL != "https://local.example.com" {
		t.Fatalf("项目级 server 应被加载: %+v", cfg.Servers)
	}
}

func TestLoadConfig_MergeOverride(t *testing.T) {
	// 用户级: shared 用 user-cmd
	homeDir := t.TempDir()
	mewcodeDir := filepath.Join(homeDir, ".mewcode")
	_ = os.MkdirAll(mewcodeDir, 0755)
	writeTempYAML(t, mewcodeDir, "config.yaml", `
mcp_servers:
  shared:
    type: stdio
    command: user-cmd
  user-only:
    type: stdio
    command: user-only-cmd
`)
	t.Setenv("HOME", homeDir)

	// 项目级: shared 用 project-url, 加 project-only
	dir := t.TempDir()
	writeTempYAML(t, dir, ".mewcode.yaml", `
mcp_servers:
  shared:
    type: http
    url: https://project.example.com
  project-only:
    type: http
    url: https://project-only.example.com
`)

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig 不应返回 error: %v", err)
	}

	// shared 应以项目级为准
	if srv, ok := cfg.Servers["shared"]; !ok {
		t.Fatal("shared server 应存在")
	} else if srv.Type != "http" || srv.URL != "https://project.example.com" {
		t.Fatalf("shared 应以项目级为准: %+v", srv)
	}

	// user-only 应保留
	if _, ok := cfg.Servers["user-only"]; !ok {
		t.Fatal("user-only server 应被保留")
	}

	// project-only 应存在
	if _, ok := cfg.Servers["project-only"]; !ok {
		t.Fatal("project-only server 应存在")
	}
}

func TestLoadConfig_InvalidFileDegrade(t *testing.T) {
	dir := t.TempDir()
	// 写一个非法 YAML
	writeTempYAML(t, dir, ".mewcode.yaml", "{{{not: valid: yaml: [[[")

	stderr := captureStderr(func() {
		cfg, err := LoadConfig(dir)
		if err != nil {
			t.Errorf("降级不应返回 error: %v", err)
		}
		if len(cfg.Servers) != 0 {
			t.Errorf("非法 YAML 降级后应无 server，实际: %d", len(cfg.Servers))
		}
	})

	if !strings.Contains(stderr, "warn") {
		t.Fatalf("非法 YAML 应产生告警，stderr: %s", stderr)
	}
}

func TestLoadConfig_EnvVarExpansion(t *testing.T) {
	_ = os.Setenv("MY_TOKEN", "secret123")
	_ = os.Setenv("MY_KEY", "key456")
	defer func() { _ = os.Unsetenv("MY_TOKEN"); _ = os.Unsetenv("MY_KEY") }()

	dir := t.TempDir()
	writeTempYAML(t, dir, ".mewcode.yaml", `
mcp_servers:
  srv:
    type: http
    url: https://example.com
    headers:
      Authorization: "Bearer ${MY_TOKEN}"
      X-Key: "${MY_KEY}"
`)

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig 不应返回 error: %v", err)
	}

	srv := cfg.Servers["srv"]
	if srv.Headers["Authorization"] != "Bearer secret123" {
		t.Fatalf("Authorization 应展开: %q", srv.Headers["Authorization"])
	}
	if srv.Headers["X-Key"] != "key456" {
		t.Fatalf("X-Key 应展开: %q", srv.Headers["X-Key"])
	}
}

func TestLoadConfig_CommandNotExpanded(t *testing.T) {
	_ = os.Setenv("CMD", "malicious_cmd")
	defer func() { _ = os.Unsetenv("CMD") }()

	dir := t.TempDir()
	writeTempYAML(t, dir, ".mewcode.yaml", `
mcp_servers:
  srv:
    type: stdio
    command: ${CMD}
`)

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig 不应返回 error: %v", err)
	}

	srv := cfg.Servers["srv"]
	// command 不展开，应保留字面量 ${CMD}
	if srv.Command != "${CMD}" {
		t.Fatalf("command 不应被展开，实际: %q", srv.Command)
	}
}

func TestLoadConfig_SkipInvalidServerKeepOthers(t *testing.T) {
	dir := t.TempDir()
	writeTempYAML(t, dir, ".mewcode.yaml", `
mcp_servers:
  bad:
    type: stdio
  good:
    type: http
    url: https://good.example.com
`)

	stderr := captureStderr(func() {
		cfg, err := LoadConfig(dir)
		if err != nil {
			t.Fatalf("LoadConfig 不应返回 error: %v", err)
		}
		if len(cfg.Servers) != 1 {
			t.Fatalf("应仅有 1 个合法 server，实际: %d", len(cfg.Servers))
		}
		if _, ok := cfg.Servers["good"]; !ok {
			t.Fatal("合法 server 应被保留")
		}
		if _, ok := cfg.Servers["bad"]; ok {
			t.Fatal("非法 server 应被跳过")
		}
	})

	if !strings.Contains(stderr, "bad") {
		t.Fatalf("告警应包含非法 server 名，stderr: %s", stderr)
	}
}
