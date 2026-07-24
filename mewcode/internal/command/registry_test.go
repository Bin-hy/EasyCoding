package command

import (
	"testing"
)

func TestRegister_OK(t *testing.T) {
	r := New()
	r.Register(&Command{Name: "help", Description: "显示帮助", Kind: KindLocal, Handler: nil})
	cmd, ok := r.Lookup("help")
	if !ok {
		t.Fatal("Lookup 应命中已注册命令")
	}
	if cmd.Name != "help" {
		t.Fatalf("命令名应为 help，实际: %s", cmd.Name)
	}
}

func TestRegister_DuplicateNamePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("重复注册应 panic")
		}
	}()
	r := New()
	r.Register(&Command{Name: "help", Description: "a", Kind: KindLocal})
	r.Register(&Command{Name: "help", Description: "b", Kind: KindLocal})
}

func TestRegister_DuplicateAliasPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("别名冲突应 panic")
		}
	}()
	r := New()
	r.Register(&Command{Name: "help", Description: "a", Kind: KindLocal, Aliases: []string{"h"}})
	r.Register(&Command{Name: "status", Description: "b", Kind: KindLocal, Aliases: []string{"h"}})
}

func TestRegister_AliasLookup(t *testing.T) {
	r := New()
	r.Register(&Command{Name: "help", Description: "帮助", Kind: KindLocal, Aliases: []string{"h", "?"}})

	cmd, ok := r.Lookup("h")
	if !ok || cmd.Name != "help" {
		t.Fatal("别名 lookup 失败")
	}
	cmd, ok = r.Lookup("H") // 大小写不敏感
	if !ok || cmd.Name != "help" {
		t.Fatal("别名大小写不敏感 lookup 失败")
	}
}

func TestVisibleSorted(t *testing.T) {
	r := New()
	r.Register(&Command{Name: "status", Description: "s", Kind: KindLocal})
	r.Register(&Command{Name: "help", Description: "h", Kind: KindLocal})
	r.Register(&Command{Name: "exit", Description: "e", Kind: KindUI})

	visible := r.Visible()
	if len(visible) != 3 {
		t.Fatalf("visible 应包含 3 条，实际: %d", len(visible))
	}
	if visible[0].Name != "exit" || visible[1].Name != "help" || visible[2].Name != "status" {
		t.Fatalf("visible 应按 Name 字典序排列: %v,%v,%v", visible[0].Name, visible[1].Name, visible[2].Name)
	}
}

func TestVisible_ExcludesHidden(t *testing.T) {
	r := New()
	r.Register(&Command{Name: "help", Description: "h", Kind: KindLocal, Hidden: false})
	r.Register(&Command{Name: "debug", Description: "d", Kind: KindLocal, Hidden: true})

	visible := r.Visible()
	if len(visible) != 1 {
		t.Fatalf("visible 应排除 hidden 命令，实际: %d", len(visible))
	}
	_, ok := r.Lookup("debug")
	if !ok {
		t.Fatal("dispatcher 应仍能命中 hidden 命令")
	}
}

func TestPrefixMatch(t *testing.T) {
	r := New()
	r.Register(&Command{Name: "help", Description: "h", Kind: KindLocal})
	r.Register(&Command{Name: "hello", Description: "s", Kind: KindLocal})
	r.Register(&Command{Name: "status", Description: "s", Kind: KindLocal})

	// 精确前缀 "he" 匹配 help, hello
	matches := r.PrefixMatch("/he")
	if len(matches) != 2 {
		t.Fatalf("/he 应匹配 2 条，实际: %d", len(matches))
	}

	// 空前缀返回全部
	matches = r.PrefixMatch("")
	if len(matches) != 3 {
		t.Fatalf("空前缀应返回全部，实际: %d", len(matches))
	}

	// 无匹配
	matches = r.PrefixMatch("/xyz")
	if len(matches) != 0 {
		t.Fatalf("/xyz 不应有匹配，实际: %d", len(matches))
	}
}
