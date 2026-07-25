# SubAgent 机制 Plan## 架构概览

本章实现拆为四个层次：

1. **subagent 包**（新增,核心数据层）——定义 Agent 角色的数据结构、Markdown+YAML 解析、Catalog 多来源加载、内置角色 embed
2. **task 包**（新增,后台运行层）——`task.Manager` 管理后台任务生命周期,4 个内置工具(TaskList/TaskGet/TaskStop/SendMessage)
3. **agent 包扩展**——新增 `RunToCompletion` 方法、5 个新 Option、Fork 路径辅助函数 `buildForkedMessages`、子 Agent 权限升级 callback
4. **工具与 TUI 集成层**——Agent 工具实现、工具过滤多层防线常量、TUI 接入 task notification、ESC 切后台、Skill fork 改造为复用 SubAgent 底座

模块构成：

- `subagent.Definition` / `subagent.Catalog` / `subagent.SourceXxx` — 数据结构与三层加载
- `subagent/builtin/*.md` — 内置 3 个角色文件,go:embed
- `task.Manager` / `task.BackgroundTask` — 后台任务管理与生命周期
- `task.*Tool` — 4 个内置工具,注册到 `tool.Registry`
- `agent.RunToCompletion` / `agent.WithSystemPrompt` / `agent.WithProvider` / `agent.WithMaxTurns` / `agent.WithPermissionMode` / `agent.WithApprovalUpgrader` — Agent 包扩展
- `agent/fork.go` — `BuildForkedMessages`、Fork Boilerplate 常量
- `agent/agent_tool.go` — Agent 工具实现
- `tool/filter.go` — `ALL_AGENT_DISALLOWED_TOOLS` / `ASYNC_AGENT_ALLOWED_TOOLS` 常量与过滤函数
- `tui` 改动 — TaskManager wiring、ESC 切后台、`<task-notification>` 注入、子 Agent 审批弹窗
- `tui/skill_fork.go` 改造 — 复用 `subagent.LaunchFork`

## 核心数据结构### subagent.Definition

```go
// Definition 是一个 Agent 角色的完整定义,从 Markdown+YAML frontmatter 解析。
type Definition struct {
    Name           string         // frontmatter.name (-> agentType)
    Description    string         // frontmatter.description (-> whenToUse)
    Tools          []string       // frontmatter.tools 白名单;空表示不收窄
    DisallowedTools []string      // frontmatter.disallowedTools 黑名单
    Model          string         // "haiku" / "sonnet" / "opus" / "inherit";缺省 "inherit"
    MaxTurns       int            // 0 表示沿用全局默认 (25)
    PermissionMode permission.Mode // permission.ParseMode 解析;"dontAsk" 单独处理(见 DontAsk 字段)
    DontAsk        bool           // 是否启用"绕过 Ask"的子 Agent 兜底模式
    Background     bool           // 强制后台
    SystemPrompt   string         // Markdown body(去 frontmatter 后的全文)
    FilePath       string         // 定义文件绝对路径(用于调试)
    Source         Source         // SourceProject / SourceUser / SourceBuiltin / SourcePlugin
}

type Source int

const (
    SourceBuiltin Source = iota
    SourceUser
    SourceProject
    SourcePlugin // 占位
)

func (s Source) String() string // "builtin" / "user" / "project" / "plugin"
```

### subagent.Catalog

```go
type Catalog struct {
    mu       sync.Mutex
    defs     map[string]*Definition // name -> 最高优先级定义
    bySource map[Source][]*Definition // 各层的副本(用于 /agents 命令展示与 debug)
}

func LoadCatalog(root string) *Catalog
// 顺序加载:builtin -> user -> project,优先级高的覆盖低的;
// 解析错误走 stderr 警告并跳过;返回非 nil Catalog 即使无任何定义。

func (c *Catalog) Resolve(name string) (*Definition, bool)
func (c *Catalog) List() []*Definition // 按 name 排序
func (c *Catalog) ListBySource(s Source) []*Definition

// LaunchFork 返回一个"Fork 路径"用的临时 Definition——name="__fork__",SystemPrompt="" (子 Agent 走继承的系统提示),
// 但 DisallowedTools 不应包含 Agent 工具(Fork 子 Agent 工具集保留 Agent,靠 QuerySource 阻断)。
func (c *Catalog) ForkDefinition() *Definition
```

### task.Manager 与 BackgroundTask

```go
// BackgroundTask 是一个后台子 Agent 的完整状态快照。
type BackgroundTask struct {
    ID           string                  // manager 生成,如 "task_<8 字节十六进制>"
    Name         string                  // F1 中 Agent 工具 name 参数,可空
    SubAgent     *agent.Agent
    Conv         *conversation.Conversation
    Task         string                  // 初始任务文本(SendMessage 不更新此字段)
    Status       Status                  // running/completed/failed/cancelled
    Result       string                  // 跑完的最终文本
    Err          error
    StartTime    time.Time
    EndTime      time.Time
    Cancel       context.CancelFunc
    Usage        Usage                   // 累计 token
    ToolCount    int                     // 工具调用累计
    LastActivity string                  // 最近一次工具名
}

type Status int

const (
    StatusRunning Status = iota
    StatusCompleted
    StatusFailed
    StatusCancelled
)

type Usage struct {
    Input, Output, CacheWrite, CacheRead int64
}

// Manager 管理后台任务。线程安全。
type Manager struct {
    mu     sync.Mutex
    tasks  map[string]*BackgroundTask
    byName map[string]string         // name -> id,弱引用,后启动的覆盖
    done   chan string               // 完成任务的 id push 进去,TUI 消费;缓冲 32
}

func NewManager() *Manager

// Launch 起一个后台 goroutine 跑 agent.RunToCompletion;Conv 应该是已经装填了消息的子对话。
// 返回 ID;goroutine 内部跑完后写 status/result + push 到 done。
func (m *Manager) Launch(parentCtx context.Context, ag *agent.Agent, conv *conversation.Conversation, name, task string) (id string)

// AdoptRunning 把一个正在前台跑的 agent 移交到后台。
// 调用方应已经把"用户的 ESC / 120 秒超时"对应的 cancel 准备好,并把已 partial 收集的事件吐到 partial 内。
// Manager 接管 ev 事件流继续消费,直到 Done 或 Err。
func (m *Manager) AdoptRunning(parentCtx context.Context, ag *agent.Agent, conv *conversation.Conversation, name string, ev <-chan agent.Event, cancel context.CancelFunc, partial *PartialState) (id string)

// PartialState 是前台→后台移交时已收集的中间状态。
type PartialState struct {
    LastAssistantText string
    ToolCount         int
    LastActivity      string
    Usage             Usage
}

func (m *Manager) Get(id string) (*BackgroundTask, bool)
func (m *Manager) List() []*BackgroundTask // 按 StartTime 升序
func (m *Manager) Stop(id string) bool
// SubscribeDone 返回 done channel;TUI 在主事件循环里 select 消费,
// 收到 id 后查 Get 拿状态,把 <task-notification> 拼到 runtime.PendingReminders。
func (m *Manager) SubscribeDone() <-chan string

// SendMessage 给一个仍存活的后台 Agent 续派任务。
// 找不到 name -> ErrTaskNotFound;status != Completed -> ErrTaskBusy。
// 成功时把 message 加到 Conv,重新 Launch 一个新轮(返回新的 id 还是同 id?——选择**同 id**,
// 状态从 Completed 重置回 Running)。
func (m *Manager) SendMessage(parentCtx context.Context, name, message string) (id string, err error)
```

### agent 包扩展

```go
// 新增方法 ---

// RunToCompletion 执行子 Agent 的"跑到底"循环。
// 复用主 Run 的几乎所有逻辑(streamOnce / executeBatched / 权限判定),区别:
//   - 不通过 channel 返回事件(内部消费),最终返回 finalText
//   - maxTurns 由 a.maxTurns 决定(若 0 则用 maxIterations)
//   - 不触发 memory update / 不触发 compact reminder 等主对话专属逻辑(子 Agent 上下文短,
//     不需要;但内部依然走 manageContextAuto 防止超长)
//   - 接受一个可选的 events 通道,把内部事件(text/tool/approval)转发出去——TaskManager 借此聚合 ToolCount/LastActivity,
//     TUI 借此渲染前台子 Agent 的进度
func (a *Agent) RunToCompletion(ctx context.Context, conv *conversation.Conversation, task string, events chan<- Event) (finalText string, err error)

// 新增 Option ---

func WithSystemPrompt(text string) Option // 子 Agent 角色 prompt
func WithProvider(p llm.Provider) Option
func WithMaxTurns(n int) Option
func WithPermissionMode(m permission.Mode) Option
func WithDontAsk(enabled bool) Option              // 子 Agent dontAsk 模式
func WithApprovalUpgrader(fn ApprovalUpgrader) Option // 升级到父 TUI 的 callback
func WithParentRegistry(r *tool.Registry) Option   // 暂时与 WithRegistry 等价,显式区分语义

// ApprovalUpgrader 是子 Agent 把审批请求升级到父 TUI 的回调。
// 实现方:TaskManager 把请求转发到主 TUI 的事件流;前台 inline 模式直接复用现有 Approval 路径。
type ApprovalUpgrader func(ctx context.Context, req *ApprovalRequest) (permission.Outcome, bool)
```

`Agent` 结构体新增字段:
- `systemPrompt string` — 非空时 buildEnvText / BuildSystemPrompt 阶段用此覆盖默认
- `maxTurns int` — 0 表示用全局 maxIterations
- `permissionMode permission.Mode` — 子 Agent 启动模式(主 Agent 用 TUI 的运行时 mode)
- `dontAsk bool`
- `approvalUpgrader ApprovalUpgrader`

### fork.go 内容

```go
const ForkBoilerplateTag = "<fork_boilerplate>"

// ForkBoilerplate 是 Fork 子 Agent 首条 user 消息的前缀,约束其行为。
const ForkBoilerplate = `<fork_boilerplate>
你是一个 Fork 出来的工作进程。你不是主 Agent。
规则(不可协商):
1. 不能再 Fork(调用 Agent 工具会被拦截)。
2. 不要对话、不要提问、不要请求确认。
3. 直接使用工具:读文件、搜索代码、做修改。
4. 严格限制在你被分配的任务范围内。
5. 最终报告以 "Scope:" 开头,500 字以内。
</fork_boilerplate>

`

// BuildForkedMessages 把父对话克隆到 Fork 子对话,处理悬空 tool_use,追加 Boilerplate+task。
//
// 行为:
//   1. 深拷贝 parentMsgs(所有 Message + 内部 ToolCalls/ToolResults 切片)
//   2. 扫描末尾 assistant 消息的 ToolCalls,如果对应的 RoleTool 消息缺失,
//      生成一条 placeholder ToolResults(每个 ID 对一条"[forked, skipped]" 错误内容)
//   3. 追加 user 消息 = ForkBoilerplate + task
//
// 返回新消息列表,直接用 conversation.NewFromMessages 装载即可。
func BuildForkedMessages(parentMsgs []llm.Message, task string) []llm.Message

// IsForkContext 判定一个 conversation 的消息历史是否来自 Fork(用 ForkBoilerplateTag 扫描)。
// QuerySource 检测的兜底机制——caller 链丢失时靠这个。
func IsForkContext(msgs []llm.Message) bool
```

### Agent 工具

`internal/agent/agent_tool.go`：

```go
// AgentTool 是注册到 tool.Registry 的统一 Agent 工具。
type AgentTool struct {
    catalog       *subagent.Catalog
    taskMgr       *task.Manager
    parentAgent   *Agent                                     // 取 provider/registry/eng/runtime 等
    bgEnabled     bool                                       // N6 配置开关
}

func NewAgentTool(catalog *subagent.Catalog, mgr *task.Manager, parent *Agent, bgEnabled bool) tool.Tool

// Name 返回 "Agent"
// ReadOnly 返回 false(子 Agent 可能做任何事)
// Description 列出已知的 subagent_type 名,从 catalog.List() 渲染

// Execute 主流程:
//   1. 解析 args -> AgentArgs{prompt, description, subagent_type, model, run_in_background, name}
//   2. 校验:prompt 非空、description 非空;
//   3. 检测嵌套:从 ctx 取 ParentInfo,若 parent 已是子 Agent 或对话历史含 fork tag -> 返回错误
//   4. Resolve 定义:subagent_type 非空走 catalog.Resolve,空走 catalog.ForkDefinition
//   5. 决定 background:def.Background || args.RunInBackground || (是 fork)
//   6. 应用工具过滤多层防线 ApplyAgentToolFilter,得到 allowed []string
//   7. 选 provider:args.Model 非空 -> 切;否则 def.Model 非 inherit -> 切;否则用 parent
//   8. 构造子 Agent + 子 Conv(空白或 Fork 路径装填消息)
//   9. 前台路径:开 ctx,WithTimeout 120s,跑 RunToCompletion;
//      - 完成 → 返回 finalText
//      - 超时/ESC → AdoptRunning,返回 {task_id, status:"timed_out_to_background"}
//   10. 后台路径:Launch,返回 {task_id, status:"async_launched"}
```

### 工具过滤多层防线

`internal/tool/filter.go`:

```go
// ALL_AGENT_DISALLOWED_TOOLS 是任何子 Agent 永远不能用的工具名列表。
// 本期最小列表:Agent。后续可扩展 AskUserQuestion / TaskStop / 系统级敏感工具。
var ALL_AGENT_DISALLOWED_TOOLS = []string{"Agent"}

// CUSTOM_AGENT_DISALLOWED_TOOLS 是自定义(user / project / plugin 来源)Agent 比内置 Agent 多禁用的工具。
// 本期为空。
var CUSTOM_AGENT_DISALLOWED_TOOLS = []string{}

// ASYNC_AGENT_ALLOWED_TOOLS 是后台 Agent 工具白名单。
// 不含 Agent / TaskStop / SendMessage / TaskList / TaskGet 等任何元工具。
var ASYNC_AGENT_ALLOWED_TOOLS = []string{
    "read_file", "write_file", "edit_file",
    "glob", "grep",
    "bash",
    "load_skill", "install_skill",
}
// MCP 工具与 Skill 工具按工具命名约定动态识别(以 "mcp__" 起头 / 来自 RegisterSkillTool),
// 通过 IsAllowedInBackground 函数走另一条分支判定。

// FilterParams 是过滤一个 Agent 的工具列表的参数。
type FilterParams struct {
    All        []string // registry 的全部工具名(按注册顺序)
    Source     subagent.Source
    Background bool
    Allowed    []string // Agent 定义 frontmatter.tools 白名单
    Disallowed []string // Agent 定义 frontmatter.disallowedTools 黑名单
}

// ApplyAgentToolFilter 按 spec F30 顺序过滤。
// 返回最终 allowed 列表(传给 agent.WithAllowedTools)。
func ApplyAgentToolFilter(p FilterParams) []string
```

### TUI 集成层

`internal/tui/tui.go` 改动：
- `TUIParams` 加 `TaskManager *task.Manager`(由 main 注入)
- `Model` 持有 `taskMgr *task.Manager`
- `Init()` 末尾启动一个 go-routine 消费 `taskMgr.SubscribeDone()`,把 `<task-notification>` 拼成 reminder 推到 `m.runtime.AppendReminders`
- 主对话 Agent 通过 `agent.WithApprovalUpgrader(m.taskMgr.UpgradeApproval)` 让子 Agent 审批升级回主 TUI

`internal/tui/stream.go` 改动：
- `updateStreaming` 监听 ESC 键(`tea.KeyPressMsg` "esc"):若 m.state==stateStreaming 且当前有运行中的 SubAgent → 调 `m.taskMgr.AdoptRunning`,切回 idle 态
- 监听 SubAgent ApprovalRequest 转发——TaskManager 通过 events channel 转回主 TUI 走现有 Approval 路径

`internal/tui/skill_fork.go` 改造：
- 删除现有 `runSubAgent` 内的零散逻辑
- 改为调 `subagent.LaunchFork(ctx, host, opts, conv)`,host 持有 m.taskMgr / m.runtime / m.engine 等

## 模块设计### 模块 A:subagent 包**职责:**
- 数据结构 Definition
- Markdown+YAML 解析(复用 skills/parser.go 的 parseFrontmatterAndBody——抽到 internal/util/markdown 让两方共用 OR skills 与 subagent 都各自有一份)
- 三层 + 内置 embed 加载

**对外接口:**
- `LoadCatalog(root string) *Catalog`
- `Catalog.Resolve(name) (*Definition, bool)` / `List()` / `ForkDefinition()`

**依赖:**
- `internal/permission`(解析 PermissionMode 字段)
- `gopkg.in/yaml.v3`
- 标准库 path/filepath、embed

**关键设计:**
- Markdown 解析复用 skills/parser.go 的 `parseFrontmatterAndBody`——抽到 subagent/parser.go 独立实现一份(避免互相依赖),内容几乎一致
- 内置文件 `subagent/builtin/general-purpose.md` / `explore.md` / `plan.md` 用 `//go:embed builtin/*.md` 加载
- 加载错误统一 stderr `fmt.Fprintf(os.Stderr, "subagent %s: ... skipped\n", ...)`

### 模块 B:task 包**职责:**
- 后台任务生命周期管理
- 4 个内置工具(TaskList/TaskGet/TaskStop/SendMessage)

**对外接口:**
- `NewManager() *Manager`
- `Launch / AdoptRunning / Get / List / Stop / SendMessage / SubscribeDone`
- `NewTaskListTool(m *Manager) tool.Tool` 等四个工厂

**依赖:**
- `internal/agent`(*agent.Agent)
- `internal/conversation`
- `internal/tool`
- `internal/llm`

**关键设计:**
- `done` channel 缓冲 32 够大,正常场景不可能填满;真满了 push 走 select default 丢弃 +
  stderr 警告(主 TUI 漏一条通知不致命)
- `Launch` goroutine 包 `defer recover()`,panic 转 status=failed
- `Stop` 调 `task.Cancel`,Cancel 是 Launch 时 derive 的 context.WithCancel
- `SendMessage`:仅当 status==Completed 时允许;否则 ErrTaskBusy。重新 Launch 时用 *同 id*,status 从 Completed 重置回 Running

### 模块 C:agent 包扩展**职责:**
- 新增 `RunToCompletion` 方法
- 新增 5 个 Option
- Fork 路径辅助

**对外新增接口:**
- `Agent.RunToCompletion(ctx, conv, task, events) (string, error)`
- `WithSystemPrompt / WithProvider / WithMaxTurns / WithPermissionMode / WithDontAsk / WithApprovalUpgrader`
- `BuildForkedMessages`
- `IsForkContext`

**关键设计:**
- `RunToCompletion` 与 `Run` 共用 `streamOnce` / `executeBatched` / `manageContextAuto` /
  `recordReadFileIfApplicable`,通过抽公共 helper 实现共享(把 Run 的循环体抽到
  `runIter(ctx, conv, mode, iter, ...)`,Run 与 RunToCompletion 都调它)
- 子 Agent 的 `permissionMode` + `dontAsk` 决策点在 `executeBatched` 的 `runGuarded` 内多一层短路:
  ```go
  if a.dontAsk {
      // 角色定义 dontAsk:走 sandbox/黑名单/规则后,默认 Allow 而非 Ask
      if d == permission.Ask { d = permission.Allow }
  }
  ```
- 升级到父 TUI 的 callback 在 `requestApproval` 里调:
  ```go
  if a.approvalUpgrader != nil {
      outcome, ok := a.approvalUpgrader(ctx, &req)
      if ok { return outcome, true }
  }
  // 否则走默认 emit Approval event 路径(主 Agent inline 子 Agent 路径)
  ```

**Fork Boilerplate 注入策略:**
- `BuildForkedMessages` 把 Boilerplate 写在 user 消息开头(与 ch13 README 一致)
- `IsForkContext` 用 strings.Contains 扫描 *所有* 历史 user 消息内容寻找 `<fork_boilerplate>`(QuerySource 兜底)

### 模块 D:Agent 工具与 TUI 集成**职责:**
- 把 Agent 工具注册到 registry
- TUI 接入 task notification
- 改造 Skill fork

**对外接口:**
- `agent.NewAgentTool(catalog, taskMgr, parentAgent, bgEnabled) tool.Tool`
- `subagent.LaunchFork(ctx, host, opts) (...)` 公共 Fork 启动函数(Skill fork 与 Agent 工具都调)

**关键设计:**
- `AgentTool.Execute` 在前台 inline 路径返回结果时要小心:
  - 前台跑完返回 finalText 作为 tool_result content
  - 中途超时切后台 → 返回 JSON `{"task_id":"...","status":"timed_out_to_background"}`
- 嵌套阻断:`AgentTool.Execute` 入口检查 ctx 是否携带 `parentAgentCtxKey`(子 Agent 启动时塞入);若有 → 返回结构化错误
  - 不依赖 ctx 单值:也扫 conv 历史是否含 Fork tag(IsForkContext)
- TUI 的 task notification 注入:
  - `Init()` 开 `go m.consumeTaskDone()`
  - `consumeTaskDone` 接 `done` channel,Get 拿状态,渲染成 `<task-notification>` 块,调 `m.runtime.AppendReminders` 推入
  - 主对话下一次 Run 自动拿到(已有机制)

## 模块交互### 启动期 wiring

```
main.go
  ├── NewDefaultRegistry()  → registry
  ├── NewEngine(root)       → engine
  ├── NewSessionRuntime     → runtime
  ├── skills.LoadCatalog    → skillCatalog
  ├── hook.Load             → hookEngine
  ├── subagent.LoadCatalog  → subagentCatalog       ← 新增
  ├── task.NewManager()     → taskMgr               ← 新增
  ├── registry.Register(task.NewTaskListTool(taskMgr))    ← 新增
  ├── registry.Register(task.NewTaskGetTool(taskMgr))     ← 新增
  ├── registry.Register(task.NewTaskStopTool(taskMgr))    ← 新增
  ├── registry.Register(task.NewSendMessageTool(taskMgr)) ← 新增
  ├── tui.New(..., TUIParams{TaskMgr: taskMgr, SubAgentCatalog: subagentCatalog, ...})
  │     │
  │     └── 在 tui.New 内:Agent 工具的注册被推迟到主 Agent 构造后(因为要把 parentAgent 注入),
  │         或者 Agent 工具 lazy 拿:把 catalog/taskMgr 写死,parentAgent 通过函数 / 持有 *Model 拿
```

**简化方案:** Agent 工具在 main.go 注册,parentAgent 字段在 tui.New 后回填:
```go
agentTool := agent.NewAgentTool(subagentCatalog, taskMgr, nil, cfg.EnableSubAgentBackground)
registry.Register(agentTool)
// 再 tui.New(...)
// 再 agentTool.SetParent(m.agent)
```

### 运行时:主 Agent 调 Agent 工具(前台,定义式)

```
LLM 流式产出 tool_use:{name:"Agent",input:{prompt:"...",subagent_type:"Explore"}}
    ↓
agent.executeBatched → 路由到 AgentTool.Execute(ctx, args)
    ↓
AgentTool.Execute:
    1. 解析参数 -> AgentArgs
    2. 防嵌套:检测 ctx / Conv 是否来自 Fork → 否
    3. Resolve("Explore") → def
    4. background = def.Background || args.RunInBackground → false
    5. ApplyAgentToolFilter -> allowed
    6. provider = (def.Model=="haiku") ? llm.New(haiku) : parent.Provider
    7. subRuntime := NewSessionRuntime(200000)
    8. subAgent := agent.New(provider, registry, version, engine,
           WithRuntime(subRuntime),
           WithAllowedTools(allowed),
           WithSystemPrompt(def.SystemPrompt),  ← 新
           WithMaxTurns(def.MaxTurns),
           WithPermissionMode(def.PermissionMode),
           WithDontAsk(def.DontAsk),
           WithApprovalUpgrader(parent.taskMgr.UpgradeApproval),
           WithHookEngine(parent.hookEngine))
    9. subConv := conversation.New()
    10. timeoutCtx, cancel := context.WithTimeout(ctx, 120s)
        events := make(chan agent.Event, 32)
        go func() {  // 前台路径:把 events 转发到主 TUI(可选,本期暂不渲染前台子进度,只在状态行显示一条 "● subAgent 跑中")
            for ev := range events { ... }
        }()
        finalText, err := subAgent.RunToCompletion(timeoutCtx, subConv, args.Prompt, events)
    11. timeoutCtx.Err() == DeadlineExceeded?
         - 是 → AdoptRunning(ctx, subAgent, subConv, args.Name, events, cancel, partial)
              → 返回 JSON {"task_id": "task_xxx", "status": "timed_out_to_background"}
         - 否 → 返回 finalText 作为 tool_result content
```

### 运行时:主 Agent 调 Agent 工具(后台,显式)

```
AgentTool.Execute:
    ...
    10. taskID := taskMgr.Launch(ctx, subAgent, subConv, args.Name, args.Prompt)
    11. 返回 JSON {"task_id": "task_xxx", "status": "async_launched"}
```

### 后台任务完成通知

```
taskMgr.Launch goroutine:
    finalText, err := subAgent.RunToCompletion(ctx, conv, task, nil)
    task.Result = finalText
    task.Err = err
    task.Status = StatusCompleted (or Failed/Cancelled)
    select {
    case m.done <- taskID:
    default: // 缓冲满,丢弃 + stderr 警告
    }
    ↓
tui.consumeTaskDone (goroutine):
    for taskID := range taskMgr.SubscribeDone() {
        t := taskMgr.Get(taskID)
        notification := buildTaskNotification(t)  // <task-notification>...</task-notification>
        m.runtime.AppendReminders([]string{notification})
        // 不主动唤醒主对话:等主 Agent 下次 Run 自然 take reminder
    }
    ↓
下一次 m.beginTurn → m.agent.Run → buildReminder takes pendingReminders → 注入 reminder 区
```

### Fork 路径

```
AgentTool.Execute (subagent_type 空):
    1. def = catalog.ForkDefinition()  // name="__fork__"
    2. background = true (Fork 强制)
    3. allowed = ApplyAgentToolFilter(...)
       注意:这里 def.DisallowedTools 不含 "Agent" → Fork 子 Agent 工具集保留 Agent
    4. forkedMsgs := BuildForkedMessages(parentConv.Messages(), args.Prompt)
    5. subConv := conversation.NewFromMessages(forkedMsgs, ...)
    6. subAgent := agent.New(..., WithAllowedTools(allowed), WithSystemPrompt("")) // 继承主系统提示
    7. taskID := taskMgr.Launch(ctx, subAgent, subConv, args.Name, args.Prompt)
    8. 返回 {"task_id": "...", "status": "async_launched"}
```

### Fork 子 Agent 调 Agent 工具被阻断

```
Fork 子 Agent 跑动中,LLM 又产 tool_use:{name:"Agent", input:{...}}
    ↓
subAgent.executeBatched → AgentTool.Execute(subCtx, args)
    ↓
AgentTool.Execute:
    检测:IsForkContext(subConv.Messages()) → true(消息中含 <fork_boilerplate>)
    → 返回 ToolResult{IsError:true, Content:"Fork 子 Agent 不能再启动 Agent(检测到 fork boilerplate)"}
```

注:由于 `ALL_AGENT_DISALLOWED_TOOLS=[Agent]` 已经把 Agent 工具从子 Agent 工具列表里剔除,理论上 Fork 子 Agent 的 LLM 看不到 Agent 工具。但 Fork 路径**故意保留**(为了 prompt cache 一致性),靠 QuerySource + Boilerplate 兜底拦截。

**结论:** Fork 子 Agent 工具列表 = 父工具列表 - DisallowedTools - 后台白名单交集 - 但不去除 Agent 工具。

### Skill fork 改造

```
tui.Model.Execute("/foo") → skills.Executor.Execute → fork closure m.runSubAgent
    ↓ (改造后)
m.runSubAgent(ctx, conv, opts):
    return subagent.LaunchFork(ctx, subagent.FromTUI(m), subagent.ForkLaunchOpts{
        AllowedTools: opts.AllowedTools,
        Model:        opts.Model,
        Conv:         conv,              // skills 已构造好的 forkConv
        SystemPrompt: "",                // 走继承
        Background:   false,             // skills 仍走前台同步(返回 finalText 给 host)
        EventsSink:   nil,
    })
```

`subagent.LaunchFork` 内部:做与 `AgentTool.Execute` 前台路径相同的 wiring,只是不读 catalog Definition。

## 文件组织

```
mewcode/
├── internal/
│   ├── subagent/                       ← 新增包
│   │   ├── doc.go                      包注释
│   │   ├── definition.go               Definition / Source 类型
│   │   ├── parser.go                   parseFrontmatterAndBody + validateMeta
│   │   ├── parser_test.go
│   │   ├── catalog.go                  Catalog + LoadCatalog / Resolve / List / ForkDefinition
│   │   ├── catalog_test.go
│   │   ├── embed.go                    go:embed builtin/*.md + builtinDefs() loader
│   │   ├── launch.go                   LaunchFork / Definition 公用 wiring 辅助
│   │   ├── launch_test.go
│   │   └── builtin/
│   │       ├── general-purpose.md
│   │       ├── explore.md
│   │       └── plan.md
│   │
│   ├── task/                           ← 新增包
│   │   ├── doc.go
│   │   ├── manager.go                  Manager + BackgroundTask + Launch / Adopt / Stop / SendMessage
│   │   ├── manager_test.go
│   │   └── tools.go                    NewTaskListTool / NewTaskGetTool / NewTaskStopTool / NewSendMessageTool
│   │   └── tools_test.go
│   │
│   ├── agent/                          ← 现有包扩展
│   │   ├── agent.go                    现有,加 systemPrompt/maxTurns/permissionMode/dontAsk/approvalUpgrader 字段;Run 抽出 runIter
│   │   ├── runtime.go                  现有,加 WithSystemPrompt/WithMaxTurns/WithPermissionMode/WithDontAsk/WithApprovalUpgrader/WithProvider 选项
│   │   ├── run_to_completion.go        ← 新增 RunToCompletion 实现
│   │   ├── fork.go                     ← 新增 BuildForkedMessages / IsForkContext / ForkBoilerplate
│   │   ├── fork_test.go
│   │   ├── agent_tool.go               ← 新增 NewAgentTool + Execute 逻辑
│   │   ├── agent_tool_test.go
│   │   ├── permission_upgrade.go       ← 新增 ApprovalUpgrader 类型 + DefaultUpgrader
│   │   ├── agent_test.go               补 RunToCompletion / dontAsk / approvalUpgrader 测试
│   │   └── ...其他不动
│   │
│   ├── tool/                           ← 现有包扩展
│   │   ├── filter.go                   ← 新增 ALL_AGENT_DISALLOWED / ASYNC_AGENT_ALLOWED / ApplyAgentToolFilter
│   │   ├── filter_test.go
│   │   └── ...其他不动
│   │
│   ├── tui/                            ← 现有包改动
│   │   ├── tui.go                      加 TaskMgr / SubAgentCatalog 字段 + consumeTaskDone goroutine + AgentTool 注册
│   │   ├── stream.go                   updateStreaming 加 ESC → AdoptRunning 分支;子 Agent ApprovalRequest 转发
│   │   ├── tasks.go                    ← 新增 consumeTaskDone + buildTaskNotification + ESC 切后台辅助
│   │   ├── skill_fork.go               ← 改造为复用 subagent.LaunchFork
│   │   └── ...
│   │
│   └── config/                         ← 现有,加配置项
│       └── config.go                   加 EnableSubAgentBackground bool(默认 true)
│
└── cmd/mewcode/main.go                 ← 加 subagent.LoadCatalog / task.NewManager / 4 个工具注册 / Agent 工具注册
```

## 技术决策

| 决策点 | 选择 | 理由 |
|--------|------|------|
| RunToCompletion 与 Run 关系 | 共用底层 helper(`runIter`/`streamOnce`),不重新写一遍循环 | 避免两套循环逻辑漂移;主对话与子 Agent 在 ReAct 层面行为应一致 |
| 子 Agent 是否独立 PermissionEngine | 暂共享同一 Engine,但增加 WithApprovalUpgrader 让审批升级回主 TUI | 本期权限规则全局一致;独立 Engine 是为隔离规则集准备的预留扩展点 |
| Fork 强制后台 | 是 | ch13 README 设计;Fork 上下文长,前台同步会阻塞用户;并行 Fork 才有意义 |
| 后台通知形式 | system reminder 注入(`<task-notification>`),不直接 push 到 LLM | 与 ch12 PendingReminders 一致;不打断用户当前操作;主 Agent 下次 turn 自然消费 |
| 嵌套阻断三道闸 | `ALL_AGENT_DISALLOWED_TOOLS` 全局 + Fork 路径 QuerySource + Boilerplate 标记扫描 | 单一闸门失效(对话压缩、工具列表漂移)仍能兜底;定义式靠工具过滤,Fork 靠双闸 |
| 后台白名单粒度 | 列具体工具名 + MCP/Skill 工具按命名约定动态识别 | ch13 README 同款做法;不需要为每个 MCP 工具列在白名单里 |
| done channel 缓冲 32 | 够大 | 正常场景一会儿不会有 32 个任务同时跑完;真满则丢弃 + stderr |
| SendMessage 同 id 复用 | 是 | 状态语义上是"该任务继续",而非"新任务";UI/查询体验更连贯 |
| 配置开关 EnableSubAgentBackground | 默认 true | 后台是核心能力,默认开启;关闭后所有子 Agent 强制前台,主要供 CI / 调试用 |
| Markdown 解析器复用 | 不共享,subagent 包独立实现一份(几乎与 skills/parser.go 一致) | 避免抽公共包导致循环依赖;两个包字段不一样,复用收益有限 |
| Agent 工具的 parent 注入时机 | main.go 注册时为 nil,tui.New 后 SetParent 回填 | tool.Registry 在 tui.New 之前已构造,Agent 工具的 parent 依赖 m.agent 反推 |
| ESC 切后台 vs Ctrl+C | ESC 切后台,Ctrl+C 仍是取消(沿用现有) | ESC 在 TUI 已经做"取消选择"用途,但流式态下 ESC 转为切后台是 ch13 README 设计 |