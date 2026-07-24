package command

import (
	"context"
	"strings"
	"testing"

	"mewcode/internal/permission"
)

func TestRegisterBuiltins_AllRegistered(t *testing.T) {
	r := New()
	RegisterBuiltins(r)

	visible := r.Visible()
	if len(visible) != 12 {
		t.Fatalf("RegisterBuiltins 应注册 12 条可见命令，实际: %d", len(visible))
	}

	expectedNames := []string{
		"clear", "compact", "do", "exit", "help", "memory",
		"permission", "plan", "resume", "review", "session", "status",
	}
	for i, name := range expectedNames {
		if visible[i].Name != name {
			t.Errorf("visible[%d] 应为 %q，实际 %q", i, name, visible[i].Name)
		}
	}
}

func TestRegisterBuiltins_NoCollision(t *testing.T) {
	// 不应 panic
	r := New()
	RegisterBuiltins(r)
	if len(r.Visible()) != 12 {
		t.Fatal("注册后应恰好 12 条命令")
	}
}

func TestRegisterBuiltins_HandlersRunOnNopUI(t *testing.T) {
	r := New()
	RegisterBuiltins(r)

	for _, cmd := range r.Visible() {
		if cmd.Handler == nil {
			t.Errorf("%q 的 Handler 为 nil", cmd.Name)
			continue
		}
		if err := cmd.Handler(nil, NopUI()); err != nil {
			t.Errorf("%q handler 返回 error: %v", cmd.Name, err)
		}
	}
}

// recordingUI 嵌入 NopUI，记录关键方法调用。
type recordingUI struct {
	UI
	printlnCalls    []string
	errorCalls      []string
	setModeCalls    []permission.Mode
	injectCalls     []struct{ label, preset string }
	forceCompactCnt int
	quitCnt         int
}

func newRecordingUI() *recordingUI {
	return &recordingUI{
		UI: NopUI(),
	}
}

func (r *recordingUI) Println(msg string) {
	r.printlnCalls = append(r.printlnCalls, msg)
}

func (r *recordingUI) Error(msg string) {
	r.errorCalls = append(r.errorCalls, msg)
}

func (r *recordingUI) SetMode(m permission.Mode) {
	r.setModeCalls = append(r.setModeCalls, m)
}

func (r *recordingUI) InjectAndSend(label, preset string) {
	r.injectCalls = append(r.injectCalls, struct{ label, preset string }{label, preset})
}

func (r *recordingUI) ForceCompact() {
	r.forceCompactCnt++
}

func (r *recordingUI) Quit() {
	r.quitCnt++
}

func TestHandleStatus_PrintsAllKeys(t *testing.T) {
	rec := newRecordingUI()
	_ = handleStatus(context.TODO(), rec)

	if len(rec.printlnCalls) == 0 {
		t.Fatal("handleStatus 应调用 Println")
	}
	text := rec.printlnCalls[0]
	keys := []string{"Mode:", "Tokens:", "Tools:", "Memories:", "Model:", "Directory:"}
	for _, k := range keys {
		if !strings.Contains(text, k) {
			t.Errorf("handleStatus 输出应包含 %q", k)
		}
	}
}

func TestHandleDo_SetsModeAndInjects(t *testing.T) {
	rec := newRecordingUI()
	_ = handleDo(context.TODO(), rec)

	if len(rec.setModeCalls) != 1 || rec.setModeCalls[0] != permission.ModeDefault {
		t.Error("handleDo 应调用 SetMode(ModeDefault)")
	}
	if len(rec.injectCalls) != 1 {
		t.Fatal("handleDo 应调用 InjectAndSend")
	}
}

func TestHandleHelp_IncludesAllBuiltins(t *testing.T) {
	r := New()
	RegisterBuiltins(r)
	rec := newRecordingUI()

	helpHandler := handleHelp(r)
	if err := helpHandler(nil, rec); err != nil {
		t.Fatal(err)
	}

	if len(rec.printlnCalls) == 0 {
		t.Fatal("handleHelp 应调用 Println")
	}
	text := rec.printlnCalls[0]
	allNames := []string{
		"/clear", "/compact", "/do", "/exit", "/help", "/memory",
		"/permission", "/plan", "/resume", "/review", "/session", "/status",
	}
	for _, name := range allNames {
		if !strings.Contains(text, name) {
			t.Errorf("handleHelp 输出应包含 %q", name)
		}
	}
}
