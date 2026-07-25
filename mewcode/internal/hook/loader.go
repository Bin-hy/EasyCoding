package hook

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"mewcode/internal/permission"

	"gopkg.in/yaml.v3"
)

// ─── YAML 中间结构 ───────────────────────────────────────

// fileSchema YAML 文件顶层结构。
type fileSchema struct {
	Hooks []hookYAML `yaml:"hooks"`
}

// hookYAML 单条 hook 的 YAML 中间表示（字段名与 YAML 对齐）。
type hookYAML struct {
	Name     string       `yaml:"name"`
	Event    string       `yaml:"event"`
	If       *ifYAML      `yaml:"if"`
	Action   actionYAML   `yaml:"action"`
	OnlyOnce bool         `yaml:"only_once"`
	Async    bool         `yaml:"async"`
	Timeout  string       `yaml:"timeout"` // 字符串形式如 "30s"
}

// ifYAML 条件表达式 YAML 中间层。
type ifYAML struct {
	AllOf []atomYAML `yaml:"all_of"`
	AnyOf []atomYAML `yaml:"any_of"`
}

// atomYAML 原子条件 YAML 中间层。
type atomYAML struct {
	Field string     `yaml:"field"`
	Match matchYAML  `yaml:"match"`
}

// matchYAML 匹配描述 YAML 中间层。
type matchYAML struct {
	Type  string     `yaml:"type"`
	Value string     `yaml:"value"`
	Inner *matchYAML `yaml:"inner"` // 仅 not 类型使用
}

// actionYAML 动作 YAML 中间层。
type actionYAML struct {
	Type     string            `yaml:"type"`
	Command  string            `yaml:"command"`
	Text     string            `yaml:"text"`
	URL      string            `yaml:"url"`
	Method   string            `yaml:"method"`
	Headers  map[string]string `yaml:"headers"`
	Body     string            `yaml:"body"`
	// subagent
	AgentName string `yaml:"agent_name"`
	Prompt    string `yaml:"prompt"`
}

// ─── Load ────────────────────────────────────────────────

// Load 扫描两层 YAML 配置文件，编译 Hook 规则并返回引擎。
//
// 两层优先级（就近匹配）：
//  1. 项目级：<projectRoot>/.mewcode/hooks.yaml
//  2. 用户级：~/.mewcode/hooks.yaml
//
// 两层规则叠加合并；同名 hook 存在冲突时跳过后者并 stderr 提示。
// 所有加载错误（字段缺失、枚举错、async+拦截冲突等）走 stderr 但不阻断返回。
//
// 返回 (Engine, sources)。Engine 用于后续 Dispatch；sources 为已加载文件列表供 /hooks 命令展示。
func Load(projectRoot string) (*Engine, []string) {
	var rules []Rule
	var sources []string

	home, _ := os.UserHomeDir()
	candidates := []string{}
	if projectRoot != "" {
		candidates = append(candidates, filepath.Join(projectRoot, ".mewcode", "hooks.yaml"))
	}
	if home != "" {
		candidates = append(candidates, filepath.Join(home, ".mewcode", "hooks.yaml"))
	}

	seenNames := make(map[string]bool)

	for _, path := range candidates {
		loaded := loadFile(path, &seenNames)
		if len(loaded) > 0 {
			rules = append(rules, loaded...)
			sources = append(sources, path)
		}
	}

	return NewEngine(rules, sources), sources
}

// loadFile 解析单个 YAML 文件并返回编译成功的 Rule 列表。
// 文件不存在返回 nil（静默跳过）；解析/校验失败 stderr 后继续。
func loadFile(path string, seenNames *map[string]bool) []Rule {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 文件不存在，静默跳过
		}
		fmt.Fprintf(os.Stderr, "hook: read %s failed: %v\n", path, err)
		return nil
	}

	var schema fileSchema
	if err := yaml.Unmarshal(data, &schema); err != nil {
		fmt.Fprintf(os.Stderr, "hook: parse %s failed: %v\n", path, err)
		return nil
	}

	var rules []Rule
	for i, hy := range schema.Hooks {
		r, err := compileRule(hy, i, path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "hook %q: %v, skipped\n", hy.Name, err)
			continue
		}

		// 跨文件同名冲突检测
		if (*seenNames)[r.Name] {
			fmt.Fprintf(os.Stderr, "hook %q: name conflict with previously loaded hook, skipped (source: %s)\n", r.Name, path)
			continue
		}
		(*seenNames)[r.Name] = true

		rules = append(rules, r)
	}

	return rules
}

// ─── compileRule ─────────────────────────────────────────

// compileRule 将 YAML 中间层编译为 Rule 结构体。
// 做全部字段校验：必填字段、枚举合法性、Matcher 编译、async+拦截冲突等。
func compileRule(hy hookYAML, idx int, src string) (Rule, error) {
	// name 必填
	if hy.Name == "" {
		return Rule{}, fmt.Errorf("hook #%d: name is required", idx+1)
	}

	// event 枚举校验
	event, ok := ParseEvent(hy.Event)
	if !ok {
		return Rule{}, fmt.Errorf("hook %q: unknown event %q, skipped", hy.Name, hy.Event)
	}

	// action 校验
	action, err := compileAction(hy.Action, hy.Name)
	if err != nil {
		return Rule{}, err
	}

	// if 条件编译
	var cond *Condition
	if hy.If != nil {
		cond, err = compileIf(hy.If, hy.Name)
		if err != nil {
			return Rule{}, err
		}
	}

	// async + 拦截事件冲突
	timeout := 30 * time.Second
	if hy.Timeout != "" {
		timeout, err = time.ParseDuration(hy.Timeout)
		if err != nil {
			return Rule{}, fmt.Errorf("hook %q: invalid timeout %q: %w", hy.Name, hy.Timeout, err)
		}
	}

	if hy.Async && IsBlocking(event) {
		return Rule{}, fmt.Errorf("hook %q: async not allowed for blocking events, skipped", hy.Name)
	}

	return Rule{
		Name:     hy.Name,
		Event:    event,
		If:       cond,
		Action:   action,
		OnlyOnce: hy.OnlyOnce,
		Async:    hy.Async,
		Timeout:  timeout,
		source:   src,
	}, nil
}

// compileAction 校验 action.type 与子字段完整性。
func compileAction(ay actionYAML, name string) (Action, error) {
	switch ActionType(ay.Type) {
	case ActionShell:
		if ay.Command == "" {
			return Action{}, fmt.Errorf("hook %q: shell action requires command", name)
		}
		return Action{
			Type:  ActionShell,
			Shell: &ShellAction{Command: ay.Command},
		}, nil

	case ActionPrompt:
		if ay.Text == "" {
			return Action{}, fmt.Errorf("hook %q: prompt action requires text", name)
		}
		return Action{
			Type:   ActionPrompt,
			Prompt: &PromptAction{Text: ay.Text},
		}, nil

	case ActionHTTP:
		if ay.URL == "" {
			return Action{}, fmt.Errorf("hook %q: http action requires url", name)
		}
		method := ay.Method
		if method == "" {
			method = "POST"
		}
		return Action{
			Type: ActionHTTP,
			HTTP: &HTTPAction{
				URL:     ay.URL,
				Method:  method,
				Headers: ay.Headers,
				Body:    ay.Body,
			},
		}, nil

	case ActionSubagent:
		if ay.AgentName == "" {
			return Action{}, fmt.Errorf("hook %q: subagent action requires agent_name", name)
		}
		if ay.Prompt == "" {
			return Action{}, fmt.Errorf("hook %q: subagent action requires prompt", name)
		}
		return Action{
			Type: ActionSubagent,
			Subagent: &SubagentAction{
				AgentName: ay.AgentName,
				Prompt:    ay.Prompt,
			},
		}, nil

	default:
		return Action{}, fmt.Errorf("hook %q: unknown action type %q", name, ay.Type)
	}
}

// compileIf 编译条件表达式，校验 all_of/any_of 互斥与 Matcher 编译。
func compileIf(iff *ifYAML, name string) (*Condition, error) {
	hasAll := len(iff.AllOf) > 0
	hasAny := len(iff.AnyOf) > 0

	if hasAll && hasAny {
		return nil, fmt.Errorf("hook %q: if must have either all_of or any_of, not both", name)
	}
	if !hasAll && !hasAny {
		return nil, fmt.Errorf("hook %q: if block must contain all_of or any_of", name)
	}

	var mode CombineMode
	var rawAtoms []atomYAML
	if hasAll {
		mode = CombineAllOf
		rawAtoms = iff.AllOf
	} else {
		mode = CombineAnyOf
		rawAtoms = iff.AnyOf
	}

	atoms := make([]AtomCondition, 0, len(rawAtoms))
	for i, ra := range rawAtoms {
		if ra.Field == "" {
			return nil, fmt.Errorf("hook %q: if atom #%d: field is required", name, i+1)
		}

		m, err := compileMatch(ra.Match, name, i)
		if err != nil {
			return nil, err
		}
		atoms = append(atoms, AtomCondition{Field: ra.Field, Matcher: m})
	}

	return &Condition{Mode: mode, Atoms: atoms}, nil
}

// compileMatch 将 matchYAML 编译为 permission.Matcher。
//
// Hook 场景下 Matcher 初始化传 isCommand=false，使 glob 走文件路径语义。
// 工具调用（Bash/命令串）的精确匹配请使用 exact 或 regex 类型。
func compileMatch(my matchYAML, name string, atomIdx int) (permission.Matcher, error) {
	switch my.Type {
	case "exact":
		if my.Value == "" {
			return nil, fmt.Errorf("hook %q: if atom #%d: exact match requires value", name, atomIdx+1)
		}
		return permission.CompileMatcher("="+my.Value, false)

	case "regex":
		if my.Value == "" {
			return nil, fmt.Errorf("hook %q: if atom #%d: regex match requires value", name, atomIdx+1)
		}
		return permission.CompileMatcher("~"+my.Value, false)

	case "glob":
		if my.Value == "" {
			return nil, fmt.Errorf("hook %q: if atom #%d: glob match requires value", name, atomIdx+1)
		}
		return permission.CompileMatcher(my.Value, false)

	case "not":
		if my.Inner == nil {
			return nil, fmt.Errorf("hook %q: if atom #%d: not match requires inner", name, atomIdx+1)
		}
		inner, err := compileMatch(*my.Inner, name, atomIdx)
		if err != nil {
			return nil, err
		}
		return permission.CompileMatcher("!"+inner.String(), false)

	default:
		return nil, fmt.Errorf("hook %q: if atom #%d: unknown match type %q (expected exact/regex/glob/not)", name, atomIdx+1, my.Type)
	}
}
