package permission

import (
	"testing"
)

func TestCompileMatcherExact(t *testing.T) {
	m, err := CompileMatcher("=git status", false)
	if err != nil {
		t.Fatalf("CompileMatcher(=git status) unexpected error: %v", err)
	}
	if !m.Match("git status") {
		t.Errorf("exact: expected Match true for %q", "git status")
	}
	if m.Match("git status -s") {
		t.Errorf("exact: expected Match false for %q", "git status -s")
	}
	if m.Match("git statu") {
		t.Errorf("exact: expected Match false for %q", "git statu")
	}
}

func TestCompileMatcherRegex(t *testing.T) {
	m, err := CompileMatcher("~^npm (install|test)$", false)
	if err != nil {
		t.Fatalf("CompileMatcher(~^npm (install|test)$) unexpected error: %v", err)
	}
	if !m.Match("npm install") {
		t.Errorf("regex: expected Match true for %q", "npm install")
	}
	if !m.Match("npm test") {
		t.Errorf("regex: expected Match true for %q", "npm test")
	}
	if m.Match("npm run dev") {
		t.Errorf("regex: expected Match false for %q", "npm run dev")
	}
	if m.Match("npm") {
		t.Errorf("regex: expected Match false for %q", "npm")
	}
}

func TestCompileMatcherNotExact(t *testing.T) {
	m, err := CompileMatcher("!=foo", false)
	if err != nil {
		t.Fatalf("CompileMatcher(!=foo) unexpected error: %v", err)
	}
	if m.Match("foo") {
		t.Errorf("not exact: expected Match false for %q", "foo")
	}
	if !m.Match("bar") {
		t.Errorf("not exact: expected Match true for %q", "bar")
	}
	if !m.Match("") {
		t.Errorf("not exact: expected Match true for empty string")
	}
}

func TestCompileMatcherNotRegex(t *testing.T) {
	m, err := CompileMatcher("!~^rm", false)
	if err != nil {
		t.Fatalf("CompileMatcher(!~^rm) unexpected error: %v", err)
	}
	if m.Match("rm -rf .") {
		t.Errorf("not regex: expected Match false for %q", "rm -rf .")
	}
	if !m.Match("ls -lh") {
		t.Errorf("not regex: expected Match true for %q", "ls -lh")
	}
	if m.Match("rmdir foo") {
		t.Errorf("not regex: expected Match false for %q (starts with rm)", "rmdir foo")
	}
}

func TestCompileMatcherNotGlob(t *testing.T) {
	m, err := CompileMatcher("!git *", true)
	if err != nil {
		t.Fatalf("CompileMatcher(!git *) unexpected error: %v", err)
	}
	if m.Match("git status") {
		t.Errorf("not glob: expected Match false for %q", "git status")
	}
	if !m.Match("npm install") {
		t.Errorf("not glob: expected Match true for %q", "npm install")
	}
}

func TestCompileMatcherGlobCommand(t *testing.T) {
	// isCommand=true → command glob（* 匹配任意含空格字符序列）
	m, err := CompileMatcher("git *", true)
	if err != nil {
		t.Fatalf("CompileMatcher(git *) unexpected error: %v", err)
	}
	if !m.Match("git status") {
		t.Errorf("command glob: expected Match true for %q", "git status")
	}
	if !m.Match("git log --oneline") {
		t.Errorf("command glob: expected Match true for %q", "git log --oneline")
	}
	if m.Match("echo hello") {
		t.Errorf("command glob: expected Match false for %q", "echo hello")
	}
}

func TestCompileMatcherGlobFile(t *testing.T) {
	// isCommand=false → file path glob（/ 分段匹配）
	m, err := CompileMatcher("**/*.go", false)
	if err != nil {
		t.Fatalf("CompileMatcher(**/*.go) unexpected error: %v", err)
	}
	if !m.Match("main.go") {
		t.Errorf("file glob: expected Match true for %q", "main.go")
	}
	if !m.Match("internal/permission/matcher.go") {
		t.Errorf("file glob: expected Match true for %q", "internal/permission/matcher.go")
	}
	if m.Match("README.md") {
		t.Errorf("file glob: expected Match false for %q", "README.md")
	}
}

func TestCompileMatcherErrors(t *testing.T) {
	// 空串
	_, err := CompileMatcher("", false)
	if err == nil {
		t.Error("expected error for empty pattern")
	}

	// 非法正则
	_, err = CompileMatcher("~[invalid", false)
	if err == nil {
		t.Error("expected error for invalid regex ~[invalid")
	}
}

func TestCompileMatcherNotNested(t *testing.T) {
	// !!git * → 双重取反 = 等效 glob
	m, err := CompileMatcher("!!git *", true)
	if err != nil {
		t.Fatalf("CompileMatcher(!!git *) unexpected error: %v", err)
	}
	// not(not(glob("git *"))) ≈ glob("git *")
	if !m.Match("git status") {
		t.Errorf("not not glob: expected Match true for %q", "git status")
	}
	if m.Match("npm test") {
		t.Errorf("not not glob: expected Match false for %q", "npm test")
	}
}

func TestMatcherString(t *testing.T) {
	tests := []struct {
		pattern string
		want    string
	}{
		{"=git status", "=git status"},
		{"~^npm.*", "~^npm.*"},
		{"git *", "git *"},
		{"!=foo", "!=foo"},
		{"!~^rm", "!~^rm"},
	}
	for _, tt := range tests {
		m, err := CompileMatcher(tt.pattern, true)
		if err != nil {
			t.Errorf("CompileMatcher(%q) unexpected error: %v", tt.pattern, err)
			continue
		}
		if got := m.String(); got != tt.want {
			t.Errorf("string: expected %q, got %q", tt.want, got)
		}
	}
}
