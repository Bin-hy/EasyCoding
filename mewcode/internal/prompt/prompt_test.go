package prompt

import (
	"strings"
	"testing"
)

func TestAssembleSystem_OrderByPriority(t *testing.T) {
	mods := []Module{
		{Name: "last", Priority: 100, Content: "Z"},
		{Name: "middle", Priority: 50, Content: "M"},
		{Name: "first", Priority: 10, Content: "A"},
	}
	result := AssembleSystem(mods)
	if !strings.HasPrefix(result, "A") {
		t.Errorf("expected first module (A) at start, got: %s", result)
	}
	if !strings.Contains(result, "M") || !strings.Contains(result, "Z") {
		t.Errorf("expected all modules present, got: %s", result)
	}
	// 验证中间有空行分隔
	if !strings.Contains(result, "\n\n") {
		t.Errorf("expected modules separated by double newline, got: %s", result)
	}
}

func TestAssembleSystem_SkipEmpty(t *testing.T) {
	mods := []Module{
		{Name: "hasContent", Priority: 10, Content: "Hello"},
		{Name: "empty", Priority: 20, Content: ""},
		{Name: "alsoHasContent", Priority: 30, Content: "World"},
	}
	result := AssembleSystem(mods)
	if strings.Contains(result, "empty") {
		t.Errorf("empty module should be skipped, got: %s", result)
	}
	// 不应出现连续多空行
	if strings.Contains(result, "\n\n\n") {
		t.Errorf("should not have consecutive empty lines, got: %s", result)
	}
}

func TestBuildSystemPrompt_NotEmpty(t *testing.T) {
	result := BuildSystemPrompt()
	if result == "" {
		t.Fatal("BuildSystemPrompt returned empty string")
	}
	// 七个固定模块都在
	expectedNames := []string{"MewCode", "终端", "文件操作", "ReAct", "工具调用", "优先", "Markdown"}
	for _, name := range expectedNames {
		if !strings.Contains(result, name) {
			t.Errorf("expected BuildSystemPrompt to contain %q", name)
		}
	}
}

// N1 缓存确定性：连续两次 BuildSystemPrompt 逐字节相等。
func TestBuildSystemPrompt_Deterministic(t *testing.T) {
	a := BuildSystemPrompt()
	b := BuildSystemPrompt()
	if a != b {
		t.Fatal("BuildSystemPrompt must be deterministic across calls (N1 cache stability)")
	}
}

// 验证可选空槽：BuildSystemPrompt 中三个可选模块内容为空，不出现对应占位。
func TestBuildSystemPrompt_OptionalSlotsEmpty(t *testing.T) {
	result := BuildSystemPrompt()
	// 可选槽为空，不应出现槽位名称或标记
	for _, slot := range []string{"自定义指令", "已激活 Skill", "长期记忆"} {
		if strings.Contains(result, slot) {
			t.Errorf("empty optional slot %q should be skipped", slot)
		}
	}
}

// F5 双重强化：系统提示中包含关键约定——优先用专用工具、编辑前必先读。
func TestBuildSystemPrompt_DoubleReinforcement(t *testing.T) {
	result := BuildSystemPrompt()
	// 专用工具优先
	if !strings.Contains(result, "read_file") || !strings.Contains(result, "glob") || !strings.Contains(result, "grep") {
		t.Error("BuildSystemPrompt should mention read_file/glob/grep for tool priority")
	}
	if !strings.Contains(result, "优先") && !strings.Contains(result, "bash") {
		t.Error("BuildSystemPrompt should mention tool priority over bash")
	}
	// 编辑前必先读
	if !strings.Contains(result, "编辑") || !strings.Contains(result, "读取") {
		t.Error("BuildSystemPrompt should mention read-before-edit convention")
	}
}

// 挂载即扩展：新增一个模块只需加入列表、不改 AssembleSystem。
func TestAssembleSystem_Extensible(t *testing.T) {
	base := FixedModules()
	extra := Module{Name: "extra", Priority: 5, Content: "EXTRA_EARLY"}
	extended := append(base, extra)
	result := AssembleSystem(extended)
	// extra 优先级 5 < 10（身份），应排在最前面
	if !strings.HasPrefix(result, "EXTRA_EARLY") {
		t.Errorf("extra module with priority 5 should appear first, got: %s", result)
	}
}

func TestEnvironmentRender(t *testing.T) {
	env := Environment{
		WorkingDir: "/home/user/project",
		Platform:   "darwin",
		Date:       "2026-07-22",
		GitStatus:  "clean",
		Version:    "dev",
		Model:      "claude-sonnet-5",
	}
	result := env.Render()
	checks := []string{"工作目录", "平台", "日期", "Git 状态", "应用版本", "模型"}
	for _, c := range checks {
		if !strings.Contains(result, c) {
			t.Errorf("Render should contain %q, got: %s", c, result)
		}
	}
}

func TestEnvironmentRender_EmptyFieldsOmitted(t *testing.T) {
	env := Environment{
		WorkingDir: "/tmp",
		// 其他字段留空
	}
	result := env.Render()
	if strings.Contains(result, "Git 状态:") || strings.Contains(result, "日期:") {
		t.Errorf("empty fields should be omitted from Render, got: %s", result)
	}
}

func TestSystemReminder_WrapsBody(t *testing.T) {
	body := "test body"
	result := SystemReminder(body)
	if !strings.Contains(result, "<system-reminder>") {
		t.Error("SystemReminder should contain opening tag")
	}
	if !strings.Contains(result, "</system-reminder>") {
		t.Error("SystemReminder should contain closing tag")
	}
	if !strings.Contains(result, body) {
		t.Error("SystemReminder should contain body text")
	}
}

func TestPlanReminder_FullVsConcise(t *testing.T) {
	full := PlanReminder(true)
	concise := PlanReminder(false)

	if !strings.Contains(full, "<system-reminder>") {
		t.Error("full PlanReminder should contain system-reminder tag")
	}
	if !strings.Contains(concise, "<system-reminder>") {
		t.Error("concise PlanReminder should contain system-reminder tag")
	}
	if !strings.Contains(full, "计划模式") || !strings.Contains(full, "只读工具") {
		t.Error("full PlanReminder should contain plan mode explanation")
	}
	if !strings.Contains(concise, "计划模式") {
		t.Error("concise PlanReminder should mention plan mode")
	}
	// 完整版应比精简版长
	if len(full) <= len(concise) {
		t.Error("full PlanReminder should be longer than concise")
	}
}

func TestPlanReminder_FullContainsToolNames(t *testing.T) {
	full := PlanReminder(true)
	for _, name := range []string{"read_file", "glob", "grep"} {
		if !strings.Contains(full, name) {
			t.Errorf("full PlanReminder should mention %s", name)
		}
	}
}

func TestGatherEnvironment_NoPanic(t *testing.T) {
	// 在任何当前目录下调用都应不 panic
	env := GatherEnvironment("dev", "test-model")
	if env.Date == "" {
		t.Error("Date should be populated")
	}
	if env.Platform == "" {
		t.Error("Platform should be populated")
	}
	// GitStatus 可能为空（非 git 目录），这是合法的降级行为
}
