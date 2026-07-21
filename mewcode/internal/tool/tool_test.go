package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ============================================================
// 注册中心测试 (AC1)
// ============================================================

func TestRegistry_Definitions(t *testing.T) {
	r := NewDefaultRegistry()
	defs := r.Definitions()

	if len(defs) != 6 {
		t.Errorf("期望 6 条工具定义，实际 %d", len(defs))
	}

	// 验证名称有序
	expectedNames := []string{"read_file", "write_file", "edit_file", "bash", "glob", "grep"}
	for i, name := range expectedNames {
		if defs[i].Name != name {
			t.Errorf("第 %d 条工具名期望 %s，实际 %s", i, name, defs[i].Name)
		}
	}

	// 验证包含 InputSchema
	for _, d := range defs {
		if d.InputSchema == nil {
			t.Errorf("工具 %s 的 InputSchema 为空", d.Name)
		}
		if d.Description == "" {
			t.Errorf("工具 %s 的 Description 为空", d.Name)
		}
	}
}

func TestRegistry_Get(t *testing.T) {
	r := NewDefaultRegistry()

	tool, ok := r.Get("read_file")
	if !ok {
		t.Fatal("应能找到 read_file")
	}
	if tool.Name() != "read_file" {
		t.Errorf("期望 read_file，实际 %s", tool.Name())
	}

	_, ok = r.Get("non_existent")
	if ok {
		t.Error("不应找到 non_existent")
	}
}

func TestRegistry_Execute_UnknownTool(t *testing.T) {
	r := NewDefaultRegistry()
	result := r.Execute(context.Background(), "no_such_tool", nil)
	if !result.IsError {
		t.Error("未知工具应返回 IsError")
	}
	if !strings.Contains(result.Content, "未知工具") {
		t.Errorf("错误信息应包含'未知工具'，实际: %s", result.Content)
	}
}

// ============================================================
// read_file 测试 (AC2)
// ============================================================

func TestReadFile_Exists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "line1\nline2\nline3"
	os.WriteFile(path, []byte(content), 0o644)

	tool := &readFileTool{}
	args, _ := json.Marshal(readFileArgs{Path: path})
	result := tool.Execute(context.Background(), args)

	if result.IsError {
		t.Fatalf("read_file 应成功，但返回错误: %s", result.Content)
	}
	if !strings.Contains(result.Content, "1\tline1") {
		t.Errorf("应包含行号，实际: %s", result.Content)
	}
	if !strings.Contains(result.Content, "3\tline3") {
		t.Errorf("应包含第 3 行，实际: %s", result.Content)
	}
}

func TestReadFile_NotExists(t *testing.T) {
	tool := &readFileTool{}
	args, _ := json.Marshal(readFileArgs{Path: "/no/such/file.txt"})
	result := tool.Execute(context.Background(), args)

	if !result.IsError {
		t.Fatal("不存在的文件应返回 IsError")
	}
	if !strings.Contains(result.Content, "文件不存在") {
		t.Errorf("错误应提及文件不存在: %s", result.Content)
	}
}

func TestReadFile_Directory(t *testing.T) {
	dir := t.TempDir()
	tool := &readFileTool{}
	args, _ := json.Marshal(readFileArgs{Path: dir})
	result := tool.Execute(context.Background(), args)

	if !result.IsError {
		t.Fatal("目录应返回 IsError")
	}
	if !strings.Contains(result.Content, "目录") {
		t.Errorf("错误应提及是目录: %s", result.Content)
	}
}

func TestReadFile_EmptyArgs(t *testing.T) {
	tool := &readFileTool{}
	result := tool.Execute(context.Background(), nil)
	if !result.IsError {
		t.Fatal("空参数应返回 IsError（path 为空）")
	}
}

// ============================================================
// write_file 测试 (AC3)
// ============================================================

func TestWriteFile_Create(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")
	tool := &writeFileTool{}
	args, _ := json.Marshal(writeFileArgs{Path: path, Content: "hello world"})
	result := tool.Execute(context.Background(), args)

	if result.IsError {
		t.Fatalf("write_file 应成功: %s", result.Content)
	}

	// 验证文件落地
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("应能读回文件: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("文件内容不匹配: %s", string(data))
	}
}

func TestWriteFile_NestedPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c.txt")
	tool := &writeFileTool{}
	args, _ := json.Marshal(writeFileArgs{Path: path, Content: "nested"})
	result := tool.Execute(context.Background(), args)

	if result.IsError {
		t.Fatalf("嵌套路径写文件应成功: %s", result.Content)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "nested" {
		t.Error("嵌套路径文件内容不匹配")
	}
}

func TestWriteFile_Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overwrite.txt")
	os.WriteFile(path, []byte("old"), 0o644)

	tool := &writeFileTool{}
	args, _ := json.Marshal(writeFileArgs{Path: path, Content: "new content"})
	result := tool.Execute(context.Background(), args)

	if result.IsError {
		t.Fatalf("覆盖写入应成功: %s", result.Content)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "new content" {
		t.Error("覆盖后内容应更新")
	}
}

// ============================================================
// edit_file 测试 (AC4)
// ============================================================

func TestEditFile_UniqueMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	os.WriteFile(path, []byte("hello world"), 0o644)

	tool := &editFileTool{}
	args, _ := json.Marshal(editFileArgs{Path: path, OldString: "hello", NewString: "hi"})
	result := tool.Execute(context.Background(), args)

	if result.IsError {
		t.Fatalf("唯一匹配替换应成功: %s", result.Content)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "hi world" {
		t.Errorf("替换后内容应为 'hi world'，实际: %s", string(data))
	}
}

func TestEditFile_NoMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	os.WriteFile(path, []byte("hello world"), 0o644)

	tool := &editFileTool{}
	args, _ := json.Marshal(editFileArgs{Path: path, OldString: "nonexistent", NewString: "x"})
	result := tool.Execute(context.Background(), args)

	if !result.IsError {
		t.Fatal("无匹配应返回 IsError")
	}
	if !strings.Contains(result.Content, "未找到匹配") {
		t.Errorf("错误应提及'未找到匹配': %s", result.Content)
	}
}

func TestEditFile_MultipleMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	os.WriteFile(path, []byte("hello hello world"), 0o644)

	tool := &editFileTool{}
	args, _ := json.Marshal(editFileArgs{Path: path, OldString: "hello", NewString: "hi"})
	result := tool.Execute(context.Background(), args)

	if !result.IsError {
		t.Fatal("多处匹配应返回 IsError")
	}
	if !strings.Contains(result.Content, "不唯一") {
		t.Errorf("错误应提及不唯一: %s", result.Content)
	}
	if !strings.Contains(result.Content, "2") {
		t.Errorf("错误应包含匹配数 2: %s", result.Content)
	}
}

// ============================================================
// bash 测试 (AC5)
// ============================================================

func TestBash_Echo(t *testing.T) {
	tool := &bashTool{}
	args, _ := json.Marshal(bashArgs{Command: "echo hello"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := tool.Execute(ctx, args)
	if result.IsError {
		t.Fatalf("echo 应成功: %s", result.Content)
	}
	if !strings.Contains(result.Content, "hello") {
		t.Errorf("输出应包含 'hello': %s", result.Content)
	}
	if !strings.Contains(result.Content, "exit_code: 0") {
		t.Errorf("应包含 exit_code: 0: %s", result.Content)
	}
}

func TestBash_Timeout(t *testing.T) {
	tool := &bashTool{}
	args, _ := json.Marshal(bashArgs{Command: "sleep 10"})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result := tool.Execute(ctx, args)
	if !result.IsError {
		t.Fatal("超时命令应返回 IsError")
	}
	if !strings.Contains(result.Content, "超时") {
		t.Errorf("错误应提及超时: %s", result.Content)
	}
}

func TestBash_NonZeroExit(t *testing.T) {
	tool := &bashTool{}
	args, _ := json.Marshal(bashArgs{Command: "exit 1"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := tool.Execute(ctx, args)
	// 非零退出不设 IsError，结果回灌
	if result.IsError {
		t.Logf("非零退出返回 IsError（也合理）: %s", result.Content)
	}
	// 但应包含 exit_code
	if !strings.Contains(result.Content, "exit_code") {
		t.Errorf("应包含 exit_code: %s", result.Content)
	}
}

// ============================================================
// glob 测试 (AC6)
// ============================================================

func TestGlob_GoFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte("package main"), 0o644)
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("text"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "d.go"), []byte("package sub"), 0o644)

	tool := &globTool{}
	args, _ := json.Marshal(globArgs{Pattern: "**/*.go", Path: dir})
	result := tool.Execute(context.Background(), args)

	if result.IsError {
		t.Fatalf("glob 应成功: %s", result.Content)
	}
	if !strings.Contains(result.Content, "a.go") {
		t.Error("应包含 a.go")
	}
	if !strings.Contains(result.Content, "b.go") {
		t.Error("应包含 b.go")
	}
	if !strings.Contains(result.Content, filepath.Join("sub", "d.go")) {
		t.Error("应包含 sub/d.go")
	}
	if strings.Contains(result.Content, "c.txt") {
		t.Error("不应包含 c.txt")
	}
}

func TestGlob_NoMatch(t *testing.T) {
	dir := t.TempDir()

	tool := &globTool{}
	args, _ := json.Marshal(globArgs{Pattern: "*.xyz", Path: dir})
	result := tool.Execute(context.Background(), args)

	if result.IsError {
		t.Fatalf("无匹配不应返回 IsError: %s", result.Content)
	}
	if !strings.Contains(result.Content, "无匹配") {
		t.Errorf("应包含'无匹配': %s", result.Content)
	}
}

// ============================================================
// grep 测试 (AC6)
// ============================================================

func TestGrep_Match(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\nimport \"fmt\"\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("nothing here"), 0o644)

	tool := &grepTool{}
	args, _ := json.Marshal(grepArgs{Pattern: "Println", Path: dir})
	result := tool.Execute(context.Background(), args)

	if result.IsError {
		t.Fatalf("grep 应成功: %s", result.Content)
	}
	if !strings.Contains(result.Content, "a.go") {
		t.Error("应包含 a.go")
	}
	if !strings.Contains(result.Content, "Println") {
		t.Error("应包含 Println")
	}
}

func TestGrep_NoMatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello"), 0o644)

	tool := &grepTool{}
	args, _ := json.Marshal(grepArgs{Pattern: "zzzzNOTFOUNDzzzz", Path: dir})
	result := tool.Execute(context.Background(), args)

	if result.IsError {
		t.Fatalf("无命中不应返回 IsError: %s", result.Content)
	}
	if !strings.Contains(result.Content, "未找到匹配") {
		t.Error("应包含'未找到匹配'")
	}
}

func TestGrep_InvalidRegex(t *testing.T) {
	tool := &grepTool{}
	args, _ := json.Marshal(grepArgs{Pattern: "[invalid", Path: "."})
	result := tool.Execute(context.Background(), args)

	if !result.IsError {
		t.Fatal("非法正则应返回 IsError")
	}
	if !strings.Contains(result.Content, "正则") {
		t.Errorf("错误应提及正则: %s", result.Content)
	}
}
