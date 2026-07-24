package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================================
// Parser 测试
// ============================================================================

func TestSplitFrontmatter_Basic(t *testing.T) {
	content := `---
name: test-skill
description: 测试 Skill
mode: inline
---

# SOP 正文

这是测试内容。`

	meta, body, err := splitFrontmatter(content)
	if err != nil {
		t.Fatalf("splitFrontmatter failed: %v", err)
	}
	if meta.Name != "test-skill" {
		t.Errorf("expected name 'test-skill', got %q", meta.Name)
	}
	if meta.Description != "测试 Skill" {
		t.Errorf("expected description '测试 Skill', got %q", meta.Description)
	}
	if meta.Mode != "inline" {
		t.Errorf("expected mode 'inline', got %q", meta.Mode)
	}
	if !strings.Contains(body, "SOP 正文") {
		t.Errorf("body should contain 'SOP 正文', got: %s", body)
	}
}

func TestSplitFrontmatter_NoFrontmatter(t *testing.T) {
	content := "# 纯 Markdown 内容\n\n没有 frontmatter。"
	meta, body, err := splitFrontmatter(content)
	if err != nil {
		t.Fatalf("splitFrontmatter failed: %v", err)
	}
	if meta.Name != "" {
		t.Errorf("expected empty name, got %q", meta.Name)
	}
	if body != content {
		t.Errorf("body should equal content: got %q", body)
	}
}

func TestSplitFrontmatter_ForkMode(t *testing.T) {
	content := `---
name: review
description: 代码审查
mode: fork
fork_context: recent
allowed_tools:
  - read_file
  - grep
---

审查所有修改。`

	meta, body, err := splitFrontmatter(content)
	if err != nil {
		t.Fatalf("splitFrontmatter failed: %v", err)
	}
	if meta.Mode != "fork" {
		t.Errorf("expected mode 'fork', got %q", meta.Mode)
	}
	if meta.ForkContext != "recent" {
		t.Errorf("expected fork_context 'recent', got %q", meta.ForkContext)
	}
	if len(meta.AllowedTools) != 2 {
		t.Errorf("expected 2 allowed_tools, got %d", len(meta.AllowedTools))
	}
	if !strings.Contains(body, "审查所有修改") {
		t.Errorf("body mismatch: %s", body)
	}
}

func TestSplitFrontmatter_DefaultMode(t *testing.T) {
	content := `---
name: default-mode
description: 无 mode 字段
---

正文`

	meta, _, err := splitFrontmatter(content)
	if err != nil {
		t.Fatalf("splitFrontmatter failed: %v", err)
	}
	// Empty mode should remain empty (caller decides default)
	if meta.Mode != "" {
		t.Errorf("expected empty mode, got %q", meta.Mode)
	}
}

func TestParseSkillMD_MissingName(t *testing.T) {
	content := `---
description: 没名字
---
正文`

	tmpDir := t.TempDir()
	mdPath := filepath.Join(tmpDir, "SKILL.md")
	//nolint:errcheck
	os.WriteFile(mdPath, []byte(content), 0o644)

	_, _, err := parseSkillMD(mdPath)
	if err == nil {
		t.Error("expected error for missing name")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error should mention 'name': %v", err)
	}
}

// ============================================================================
// ActiveSkills 测试
// ============================================================================

func TestActiveSkills_Activate(t *testing.T) {
	as := NewActiveSkills()
	as.Activate("test", "test body")

	snapshot := as.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(snapshot))
	}
	if snapshot[0].Name != "test" {
		t.Errorf("expected name 'test', got %q", snapshot[0].Name)
	}
}

func TestActiveSkills_Clear(t *testing.T) {
	as := NewActiveSkills()
	as.Activate("a", "body a")
	as.Activate("b", "body b")
	as.Clear()

	snapshot := as.Snapshot()
	if len(snapshot) != 0 {
		t.Errorf("expected 0 entries after clear, got %d", len(snapshot))
	}
}

func TestActiveSkills_DuplicateActivate(t *testing.T) {
	as := NewActiveSkills()
	as.Activate("test", "v1")
	as.Activate("test", "v2")

	snapshot := as.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 entry after duplicate activate, got %d", len(snapshot))
	}
	if snapshot[0].Body != "v2" {
		t.Errorf("expected body 'v2', got %q", snapshot[0].Body)
	}
}

// ============================================================================
// RenderBody 测试
// ============================================================================

func TestRenderBody_ArgumentsReplacement(t *testing.T) {
	skill := &Skill{
		Meta: SkillMeta{Name: "test"},
		PromptBody: "请处理以下内容:\n\n$ARGUMENTS",
	}

	result := RenderBody(skill, "hello world")
	if !strings.Contains(result, "hello world") {
		t.Errorf("expected 'hello world' in result: %s", result)
	}
	if strings.Contains(result, "$ARGUMENTS") {
		t.Errorf("$ARGUMENTS should be replaced")
	}
}

func TestRenderBody_NoPlaceholder(t *testing.T) {
	skill := &Skill{
		Meta: SkillMeta{Name: "test"},
		PromptBody: "固定 SOP",
	}

	result := RenderBody(skill, "extra args")
	if !strings.Contains(result, "## User Request") {
		t.Errorf("expected '## User Request' appended: %s", result)
	}
	if !strings.Contains(result, "extra args") {
		t.Errorf("expected args in result: %s", result)
	}
}

func TestRenderBody_AllowedToolsHint(t *testing.T) {
	skill := &Skill{
		Meta: SkillMeta{
			Name:         "test",
			AllowedTools: []string{"read_file", "grep"},
		},
		PromptBody: "SOP 正文",
	}

	result := RenderBody(skill, "")
	if !strings.Contains(result, "read_file, grep") {
		t.Errorf("expected allowed tools hint: %s", result)
	}
	if !strings.Contains(result, "SOP 正文") {
		t.Errorf("expected body preserved: %s", result)
	}
}

// ============================================================================
// Catalog 测试
// ============================================================================

func TestCatalog_LoadFromDir(t *testing.T) {
	tmpDir := t.TempDir()

	// 创建 SKILL.md
	skillDir := filepath.Join(tmpDir, "test-skill")
	//nolint:errcheck
	os.MkdirAll(skillDir, 0o755)
	content := `---
name: test-skill
description: 测试用
---
测试 body`
	//nolint:errcheck
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644)

	cat := &Catalog{byName: make(map[string]*Skill)}
	cat.scanDir(tmpDir, SourceProject)
	cat.reorder()

	sk, ok := cat.Get("test-skill")
	if !ok {
		t.Fatal("expected skill to be found")
	}
	if sk.Meta.Name != "test-skill" {
		t.Errorf("expected name 'test-skill', got %q", sk.Meta.Name)
	}
	if sk.Source != SourceProject {
		t.Errorf("expected source project, got %v", sk.Source)
	}
}

func TestCatalog_SkipInvalid(t *testing.T) {
	tmpDir := t.TempDir()

	// 创建无 name 的 SKILL.md
	badDir := filepath.Join(tmpDir, "bad-skill")
	//nolint:errcheck
	os.MkdirAll(badDir, 0o755)
	//nolint:errcheck
	os.WriteFile(filepath.Join(badDir, "SKILL.md"), []byte("just body"), 0o644)

	// 创建正常的
	goodDir := filepath.Join(tmpDir, "good-skill")
	//nolint:errcheck
	os.MkdirAll(goodDir, 0o755)
	//nolint:errcheck
	os.WriteFile(filepath.Join(goodDir, "SKILL.md"), []byte(`---
name: good
description: good skill
---
ok`), 0o644)

	cat := &Catalog{byName: make(map[string]*Skill)}
	cat.scanDir(tmpDir, SourceProject)
	cat.reorder()

	if _, ok := cat.Get("bad-skill"); ok {
		t.Error("bad skill should have been skipped")
	}
	if _, ok := cat.Get("good"); !ok {
		t.Error("good skill should exist")
	}
}

func TestCatalog_NamesOrder(t *testing.T) {
	cat := &Catalog{
		byName: map[string]*Skill{
			"beta":  {Meta: SkillMeta{Name: "beta"}},
			"alpha": {Meta: SkillMeta{Name: "alpha"}},
		},
		order: []string{"alpha", "beta"},
	}

	names := cat.Names()
	if len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Errorf("unexpected order: %v", names)
	}
}

// ============================================================================
// Types 测试
// ============================================================================

func TestSkillSource_String(t *testing.T) {
	if SourceUser.String() != "user" {
		t.Errorf("expected 'user', got %q", SourceUser.String())
	}
	if SourceProject.String() != "project" {
		t.Errorf("expected 'project', got %q", SourceProject.String())
	}
}

func TestSkillMeta_ModeDefaults(t *testing.T) {
	tests := []struct {
		meta     SkillMeta
		expected string
	}{
		{SkillMeta{Mode: ""}, ""},
		{SkillMeta{Mode: "inline"}, "inline"},
		{SkillMeta{Mode: "fork"}, "fork"},
	}

	for _, tt := range tests {
		if tt.meta.Mode != tt.expected {
			t.Errorf("expected mode %q, got %q", tt.expected, tt.meta.Mode)
		}
	}
}
