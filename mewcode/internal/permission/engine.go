package permission

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"mewcode/internal/llm"
)

// Engine 权限判定引擎，承载前四层防御与配置。
type Engine struct {
	root      string           // 项目根（绝对、已解析符号链接）
	blacklist []*regexp.Regexp // 内置危险命令正则（不可配，N1）
	user      RuleSet          // 用户级规则（~/.mewcode/settings.yaml）
	project   RuleSet          // 项目级规则（<root>/.mewcode/settings.yaml）
	local     RuleSet          // 本地级规则（<root>/.mewcode/settings.local.yaml）
	localPath string           // 永久放行写入目标（本地层文件路径）
	startMode Mode             // 启动默认模式（取自配置）
}

// NewEngine 构造权限引擎：解析项目根、加载三层配置、编译黑名单、确定启动模式。
//
// 仅当 resolveRoot 失败时返回非 nil err，此时仍返回非 nil 的"空规则安全引擎"
// （root 退化为传入值、四层规则空、startMode=ModeDefault）。
// 配置文件缺失/格式错误绝不致错，只降级跳过该文件（N5）。
//
//goland:noinspection ALL
func NewEngine(root string) (*Engine, error) {
	e := &Engine{
		blacklist: blacklist, // 引用包级常量
	}

	// 解析项目根
	resolvedRoot, err := resolveRoot(root)
	if err != nil {
		e.root = root
		e.startMode = ModeDefault
		return e, err
	}
	e.root = resolvedRoot

	// 加载三层配置
	localDir := settingsDir(e.root)
	localPath := filepath.Join(localDir, "settings.local.yaml")

	e.localPath = localPath

	// 用户级
	userPath := userSettingsPath()
	if userPath != "" {
		s, _ := loadSettings(userPath)
		e.user = toRuleSet(s)
	}

	// 项目级
	projPath := filepath.Join(e.root, ".mewcode", "settings.yaml")
	projSettings, _ := loadSettings(projPath)
	e.project = toRuleSet(projSettings)

	// 本地级
	localSettings, _ := loadSettings(localPath)
	e.local = toRuleSet(localSettings)

	// 确定启动模式：本地 > 项目 > 用户 > ModeDefault
	e.startMode = resolveStartMode(localSettings, projSettings, e.userFromSettings(e.user, userPath))

	return e, nil
}

// userFromSettings 从已加载的 Settings 和路径中提取用户级配置用于 startMode 解析。
func (e *Engine) userFromSettings(rs RuleSet, path string) Settings {
	if path == "" {
		return Settings{}
	}
	s, _ := loadSettings(path)
	return s
}

// resolveStartMode 按优先级解析启动默认模式。
func resolveStartMode(local, project, user Settings) Mode {
	for _, s := range []Settings{local, project, user} {
		if m, ok := ParseMode(s.DefaultMode); ok {
			return m
		}
	}
	return ModeDefault
}

// StartMode 返回启动默认模式。
func (e *Engine) StartMode() Mode { return e.startMode }

// Check 前四层权限判定流水线。
//
// readOnly 由调用方根据工具注册信息给定。
// 流水线顺序：① 黑名单（仅 Exec）→ ② 沙箱（仅文件类）→ ③ 规则引擎（三级）→ ④ 模式兜底。
// 任一层给出 Allow/Deny 即短路返回；全部未拦且模式判 Ask 时返回 Ask。
//
//goland:noinspection ALL
func (e *Engine) Check(mode Mode, call llm.ToolCall, readOnly bool) (Decision, string) {
	cat := categorize(call.Name, readOnly)
	friendly := friendlyName(call.Name)
	target, isFile, ok := extractTarget(call)

	// ① 黑名单：仅对命令执行类生效（N1 最高优先级，bypass 也拦）
	if cat == CategoryExec && target != "" && hitsBlacklist(target) {
		return Deny, "命中危险命令黑名单：" + summarize(target, 60)
	}

	// ② 沙箱：仅对文件类工具生效（N2）
	if isFile {
		if !ok {
			return Deny, "无法解析文件路径参数，安全拒绝"
		}
		if !e.sandboxOK(target) {
			return Deny, "路径在项目目录之外：" + target
		}
	}

	// ③ 规则引擎：本地 > 项目 > 用户，就近命中即返回
	for _, layer := range []struct {
		rs   RuleSet
		name string
	}{
		{e.local, "本地"},
		{e.project, "项目"},
		{e.user, "用户"},
	} {
		if d, hit := layer.rs.match(friendly, target, isFile); hit {
			if d == Deny {
				return Deny, fmt.Sprintf("匹配 %s deny 规则：%s(%s)", layer.name, friendly, target)
			}
			return Allow, "" // allow 规则命中，直接放行
		}
	}

	// ④ 模式兜底矩阵：只产 Allow 或 Ask
	return modeFallback(mode, cat)
}

// modeFallback F5 模式兜底矩阵：只产 Allow/Ask，绝不产 Deny。
func modeFallback(mode Mode, cat Category) (Decision, string) {
	// 只读 / bypass 全 Allow
	if cat == CategoryRead || mode == ModeBypass {
		return Allow, ""
	}

	// acceptEdits：文件写 Allow、命令执行 Ask
	if mode == ModeAcceptEdits && cat == CategoryWrite {
		return Allow, ""
	}

	// 其余（default/plan 的 Write/Exec、acceptEdits 的 Exec）→ Ask
	reason := fmt.Sprintf("%s 模式下 %s 类操作需确认", mode.String(), catName(cat))
	return Ask, reason
}

// catName 返回类别的中文名（供 reason 文案使用）。
func catName(cat Category) string {
	switch cat {
	case CategoryRead:
		return "只读"
	case CategoryWrite:
		return "文件写入"
	case CategoryExec:
		return "命令执行"
	default:
		return "未知"
	}
}

// summarize 截断字符串（用于 reason 文案）。
func summarize(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return strings.TrimSpace(s[:maxLen]) + "…"
}
