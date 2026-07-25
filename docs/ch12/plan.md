# Hook 生命周期挂钩系统 Plan## 架构概览

本章拆为两个层次实现：

1. **权限匹配器升级层（permission 包内改造）**——把 Pattern 形态从字符串升级到结构化 Matcher 接口；新增 exact/regex/not 三种实现，glob 保留作为缺省类型。改造对外仅暴露语法升级和 stderr 错误回退,运行时 Allow/Deny 语义不变。

2. **Hook 主体层（新建 `internal/hook` 包）**——加载 YAML 规则、提供事件分派引擎、四类动作执行器；通过 11 个事件 emit 点接入 agent / tui。

模块构成：

- `permission.Matcher`(新)：匹配接口 + 四种实现的工厂
- `hook.Loader`(新)：YAML 解析 / 字段校验 / matcher 编译 / 双层文件合并
- `hook.Engine`(新)：事件分派、only_once 集合、动作执行器协调
- `hook.Executor`(新)：四类动作的执行入口（shell / prompt / http / subagent stub）
- `hook.Matcher`(薄包装)：复用 permission.Matcher，做字段路径取值与匹配组合
- `agent`/`tui` 改动：在生命周期 11 个时刻调 Engine.Dispatch
- `command`：新增 `/hooks` 内置命令

## 核心数据结构### permission.Matcher

```go
// Matcher 是规则匹配的统一接口；四种实现：matcherExact / matcherGlob / matcherRegex / matcherNot
type Matcher interface {
    Match(s string) bool
    String() string // 调试 / /hooks 输出用
}

// CompileMatcher 解析单条匹配描述串，返回 Matcher 或 error。
// 描述串规则：
//   "=value"  -> exact
//   "~regex"  -> regex
//   "!inner"  -> not(CompileMatcher("inner"))
//   "value"   -> glob（缺省，沿用现有 wildcard / matchPath 语义）
//
// Bash 工具沿用整串通配（matchCommand），其它沿用 matchPath。
// 调用方在 RuleSet 那侧通过 friendly 名分流到对应底层匹配函数；matcher 这边只关心模式串。
func CompileMatcher(pattern string) (Matcher, error)
```

### permission.Rule(改造)

```go
type Rule struct {
    Tool    string  // 不变
    Matcher Matcher // 替换原 Pattern 字符串；nil 表示「该工具全匹配」
    Allow   bool
    raw     string  // 原始模式串，仅供错误日志与调试
}
```

`parseRule` 升级：识别前缀，调用 `CompileMatcher` 构造 matcher。失败时返回 `(Rule{}, false, err)`；调用方（`toRuleSet`）记录 err 到 stderr 后跳过。

### hook.Rule

```go
type Rule struct {
    Name     string
    Event    Event           // 枚举 11 个事件
    If       *Condition      // nil 表示无条件
    Action   Action
    OnlyOnce bool
    Async    bool
    Timeout  time.Duration   // 0 用默认 30s

    source   string          // 来源文件路径，供 /hooks 显示
}

type Event string // const Event = "SessionStart" / "PreToolUse" / ...
```

### hook.Condition

```go
type Condition struct {
    Mode    CombineMode      // CombineAllOf 或 CombineAnyOf；二选一不混用
    Atoms   []AtomCondition
}

type AtomCondition struct {
    Field   string             // 形如 "tool_input.path"
    Matcher permission.Matcher // 复用四种匹配类型
}
```

### hook.Action

```go
type Action struct {
    Type     ActionType // "shell" | "prompt" | "http" | "subagent"
    Shell    *ShellAction
    Prompt   *PromptAction
    Http     *HttpAction
    Subagent *SubagentAction
}

type ShellAction    struct { Command string }
type PromptAction   struct { Text string }
type HttpAction     struct { URL, Method string; Headers map[string]string; Body string /* template */ }
type SubagentAction struct { AgentName, Prompt string }
```

### hook.Payload

```go
// Payload 是事件分派时携带的上下文数据；条件求值与动作输入都用它。
// 序列化为 JSON 时保证 key 字典序（N6）。
type Payload map[string]any
```

通用字段约定：`event`、`session_id`、`cwd`、`mode`，加上各事件特化字段。`GetByPath(payload, "tool_input.command")` 函数支持嵌套字段访问。

### hook.Engine

```go
type Engine struct {
    rules   []Rule                  // 按加载顺序
    sources []string                // 加载来源文件，供 /hooks 显示

    mu       sync.Mutex
    onceFired map[string]bool        // only_once 已触发的 hook name；ResetForNewSession 时清空
}

type DispatchResult struct {
    Blocked          bool
    Reason           string
    BlockingHookName string
    InjectedPrompts  []string // prompt 动作产生的文本，按声明序
}

func (e *Engine) Dispatch(ctx context.Context, event Event, payload Payload) DispatchResult
func (e *Engine) ResetForNewSession()
func (e *Engine) Sources() []string
func (e *Engine) Rules() []Rule
```

Dispatch 内部流程：
1. 过滤匹配 event 的 rule
2. 跳过 onceFired 中已触发的 only_once rule
3. 串行求值 if 条件
4. 命中条件后按 action.type 分发到 Executor
5. Async rule 起 goroutine、立即往下走
6. 同步 rule 等结果，拦截类事件下若 result 表达 block，累加到 DispatchResult，跳过后续同事件 rule
7. prompt 类 rule 把 text 累加到 InjectedPrompts

### Executor

```go
type Executor struct {
    httpClient *http.Client // 默认 timeout=30s，可被 rule 的 Timeout 覆盖
}

type ExecutionResult struct {
    Blocked bool
    Reason  string
    Prompt  string // 仅 prompt 动作非空
    Err     error  // hook 自身失败（不拦截）
}

func (x *Executor) Run(ctx context.Context, rule Rule, payload Payload, blocking bool) ExecutionResult
```

Run 内按 rule.Action.Type 调对应的私有 runShell / runPrompt / runHttp / runSubagent。

## 模块设计### 模块 A：permission.Matcher**职责：** 提供四种匹配类型的统一接口；CompileMatcher 解析前缀。
**对外接口：** `Matcher` 接口、`CompileMatcher(pattern string) (Matcher, error)`。
**依赖：** Go 标准库 `regexp`。
**改动文件：** `internal/permission/rule.go`(扩展 parseRule / matchRule)、新增 `internal/permission/matcher.go`。

### 模块 B：permission 错误日志**职责：** parseRule 失败时 stderr 打印失败规则与原因，原本静默跳过改为有声跳过。
**对外接口：** toRuleSet 内部行为变化，外部 API 不变。
**依赖：** 模块 A。

### 模块 C：hook.Loader**职责：** 扫描两层 YAML 文件、解析顶层 `hooks:` 数组、字段校验、Matcher 编译、合并去重。
**对外接口：** `Load(projectRoot string) (*Engine, []string)`——返回引擎与加载来源文件列表；所有错误走 stderr 不返回 error。
**依赖：** 模块 A、`gopkg.in/yaml.v3`、`hook.Engine`。
**校验项：** name 必填 + 跨文件冲突、event 枚举、if 顶层 all_of/any_of 互斥、action.type 枚举与子字段、async + 拦截事件冲突、Matcher 编译失败。

### 模块 D：hook.Engine**职责：** Dispatch 流程编排、only_once 集合管理、ResetForNewSession。
**对外接口：** 见上一节 Engine 结构体。
**依赖：** 模块 E。

### 模块 E：hook.Executor**职责：** 四类动作的执行——shell（sh -c + stdin JSON + exit code 2 拦截）、prompt（直接返回 InjectedPrompt）、http（POST JSON + decision=block 解析）、subagent（stub 占位日志）。
**对外接口：** `Run(ctx, rule, payload, blocking) ExecutionResult`。
**依赖：** Go 标准库 `os/exec`、`net/http`、`text/template`。

### 模块 F：hook.Matcher 包装**职责：** 把 permission.Matcher 应用到 payload 的字段路径上。
**对外接口：** `EvalCondition(cond *Condition, payload Payload) bool`、`GetByPath(payload Payload, path string) string`。
**依赖：** 模块 A。

### 模块 G：agent 接入**职责：** 在 agent.Run 等关键路径调 Engine.Dispatch；处理 PreToolUse 拦截、注入 reminder。
**对外接口：** `agent.WithHookEngine(*hook.Engine) Option`；agent 私有方法 `dispatchHook(ctx, event, payload) hook.DispatchResult`。
**依赖：** 模块 D。
**改动文件：** `internal/agent/agent.go`、`internal/agent/runtime.go`(SessionRuntime 加 `PendingReminders []string`、ResetForNewSession 清空)。

### 模块 H：tui 接入**职责：** SessionStart / SessionEnd / SessionResume / UserPromptSubmit / Notification 五个事件在 TUI 侧 emit；UserPromptSubmit 拦截集成到 submit() 流程。
**对外接口：** `*Model` 上私有方法 `dispatchSessionStart` / `dispatchSessionEnd` 等。
**依赖：** 模块 D。
**改动文件：** `internal/tui/tui.go`、`internal/tui/stream.go`、`internal/tui/commands.go`(/clear、/resume 触发 SessionEnd + SessionStart/Resume)。

### 模块 I：/hooks 命令**职责：** 输出已加载 hook 列表 + 加载来源文件。
**对外接口：** 注册到 `command.RegisterBuiltins`。
**依赖：** Model 实现 UI 接口暴露 `HookSources()` / `HookRules()` 查询方法。

### 模块 J：main wiring**职责：** 在 main.go 中调 `hook.Load(projectRoot)`，把 Engine 注入 agent 与 Model。
**改动文件：** `cmd/mewcode/main.go`、`internal/tui/tui.go`(TUIParams 加 HookEngine 字段)。

## 模块交互**启动期数据流：**

```
main.go
  ├─ permission.NewEngine(root)             # 用升级后的 parseRule（stderr 报错）
  ├─ hook.Load(root)                        # 扫描两层 YAML、构造 Engine
  └─ tui.New(..., HookEngine=engine)
        ├─ agent.New(..., WithHookEngine(engine))
        └─ Model.hookEngine = engine
```

**SessionStart emit 时机：**

```
main.go 完成 wiring → tui.New 返回 Model → m.Run() → Init() 渲染 banner
                                                         │
                                                         └─ 首条 user 输入到达前
                                                            Init() 末尾派发 SessionStart 事件（cmd 队列）
```

实际接入：`Model.Init()` 返回的 tea.Cmd 中追加一个 `dispatchSessionStartCmd()`，该 cmd 同步调 Engine.Dispatch、收集 InjectedPrompts 注入到 runtime.PendingReminders、然后返回 nil。

**UserPromptSubmit 路径：**

```
submit() {
    text := trim(textarea.Value())
    if isSlash(text) { 走 dispatchSlash }
    result := hookEngine.Dispatch(ctx, "UserPromptSubmit", {prompt: text, ...})
    if result.Blocked {
        // 输入框下方显示 [hook <name>] reason，不消费输入
        return tea.Println(errorBlock(reason))
    }
    runtime.PendingReminders = append(runtime.PendingReminders, result.InjectedPrompts...)
    conv.AddUser(text)
    return beginTurn(...)
}
```

**PreToolUse 拦截路径：**

```
executeBatched(calls, mode, ch) {
    for each call {
        result := hookEngine.Dispatch(ctx, "PreToolUse", {tool_name, tool_input, ...})
        if result.Blocked {
            emit PhaseStart  // 用户仍能看到工具被尝试
            results[k] = hookBlockedResult(call.ID, result.BlockingHookName, result.Reason)
            emit PhaseEnd(IsError=true)
            continue
        }
        runtime.PendingReminders = append(runtime.PendingReminders, result.InjectedPrompts...)
        // ... 原有的权限 Check + 执行流程
        runtime.PendingReminders = append after PostToolUse Dispatch
    }
}
```

**Reminder 注入路径：**

```
Agent.Run() 第 iter 轮 streamOnce 之前：
    reminder := planReminder
    reminder += joinPendingReminders(runtime)  // 取出并清空 runtime.PendingReminders
    streamOnce(..., reminder, ...)
```

## 文件组织

```
mewcode/
├── internal/
│   ├── permission/
│   │   ├── matcher.go        # 新增：Matcher 接口与四种实现
│   │   ├── matcher_test.go   # 新增：四种 type 覆盖
│   │   ├── rule.go           # 改造：parseRule 识别前缀、Rule 持有 Matcher
│   │   ├── rule_test.go      # 扩展：覆盖新语法
│   │   └── settings.go       # 改造：toRuleSet 报 stderr
│   ├── hook/                 # 全新包
│   │   ├── event.go          # 11 个 Event 常量 + 拦截类列表
│   │   ├── rule.go           # Rule / Condition / Action / Payload 数据结构
│   │   ├── matcher.go        # EvalCondition / GetByPath（复用 permission.Matcher）
│   │   ├── loader.go         # YAML 解析、字段校验、双层合并
│   │   ├── loader_test.go    # 校验项覆盖
│   │   ├── engine.go         # Engine + Dispatch 主流程 + only_once 集合
│   │   ├── engine_test.go    # 各事件 dispatch + 拦截 + reminder + once 覆盖
│   │   ├── executor.go       # 四类 action 执行器
│   │   ├── executor_test.go  # shell exit2 / http block / prompt / subagent stub 覆盖
│   │   └── doc.go            # 包注释
│   ├── agent/
│   │   ├── agent.go          # 增 dispatchHook 与 PreToolUse/PostToolUse/Stop/PreCompact 等 emit
│   │   ├── runtime.go        # SessionRuntime 加 PendingReminders、HookEngine 字段
│   │   └── runtime_test.go   # PendingReminders 覆盖
│   ├── command/
│   │   └── builtins.go       # 加 /hooks 命令
│   ├── tui/
│   │   ├── tui.go            # TUIParams 加 HookEngine、Model 持有
│   │   ├── stream.go         # submit() 内拦截 + SessionStart emit
│   │   ├── commands.go       # /clear / /resume 触发 SessionEnd + SessionStart/Resume
│   │   └── hooks.go          # 新增：/hooks handler、Model 的 hook 查询方法
│   └── ...
├── cmd/mewcode/
│   └── main.go               # 加 hook.Load(root) 与 wiring
└── docs/ch12/
    ├── spec.md
    ├── plan.md
    ├── task.md
    └── checklist.md
```

## 技术决策

| 决策点 | 选择 | 理由 |
|--------|------|------|
| 匹配前缀语法 | `=` 精确、`!` 反向、`~` 正则、无前缀=glob | 单字符前缀让既有 `Bash(git *)` 这种写法继续 work；用户写新形式时直观（=foo 一眼就是精确） |
| 反向类型嵌套 | `!=value`、`!~regex`、`!glob` 都合法 | 反向是一元运算，对内层 matcher 取反；嵌套写法直接，不需要 `not()` 函数语法 |
| Matcher 接口而非 enum | Go interface | enum + switch 在 Match 时每次 type-assert；interface 更符合 Go 习惯，便于扩展（后续可加 `is_dir` 之类） |
| Hook 包独立 | `internal/hook/` | 与 `permission` 平级；hook 依赖 permission.Matcher，但 permission 不依赖 hook，无循环 |
| Event 用 string 常量而非 int | `type Event string` | YAML 直接对应、调试日志可读、加新事件不破坏已有 yaml 配置 |
| Payload 用 map[string]any | 而非结构体 | 11 个事件字段差异大；map + GetByPath 灵活；JSON 序列化时排序简单（json.Marshal 默认按 key 字母序） |
| Reminder 注入用 SessionRuntime 而非 Engine 状态 | `runtime.PendingReminders` | 与现有 plan reminder 同一注入点；下一轮自动清空；不污染 Engine |
| PreToolUse 拦截位置 | 权限 Check 之前 | 让用户能用 hook 早于权限引擎做安全策略；hook 拦截后甚至不调权限 Check |
| shell 用 sh -c | 而非 exec.Command 数组 | 用户写 hook 时常用 `\|`、`>` 这种 shell 语法；与 ch08 bash 工具一致 |
| HTTP 默认 POST + JSON body | 而非 GET | hook 多是「事件通知」语义，POST 更合理；用户需要 GET 时显式声明 method |
| HTTP body 用 Go text/template | 不开放函数 | template 已经够覆盖字段插值；开放函数容易出注入风险 |
| subagent 占位仅打日志 | 不报错也不阻塞 | spec 明确本期不实现，但配置应能加载——避免用户写早期配置后续章节直接生效 |
| only_once 用内存 map | 不写盘 | spec N5 明确本期不持久化；map 在 runtime 里，与 ActiveSkills 同生命周期 |
| 事件分派同步串行 | 多 hook 不并发 | 拦截语义需要顺序；同步 stderr 日志顺序也确定；async hook 单独起 goroutine 但 dispatch 不等 |
| 拦截类 sync timeout 不全局上限 | 单条 hook timeout 累加 | 用户配的 timeout 自己负责；全局上限会引入复杂语义 |
| /hooks 命令风格 | 与 /skill 对齐 | 已加载条目按事件分组、每条一行；末尾标加载来源 |
| 加载来源记录 | engine.sources []string | YAML 文件路径列表，/hooks 命令展示 |