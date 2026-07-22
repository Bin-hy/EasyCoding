# 系统提示工程化 Plan

> 技术栈:Go;anthropic-sdk-go v1.46.0、openai-go/v3 v3.37.0。所有 SDK 缓存 API 已对 vendored 源码核实(见技术决策表)。

## 架构概览ch05 在三层叠加,**不改 ch04 的 Agent Loop 控制流**:

- **prompt 包(重写)**:从「单个常量字符串」升级为「模块化装配 + 环境采集 + 补充消息构造」。对外产出三类文本——**稳定系统提示**(可缓存)、**环境信息段**(不缓存)、**system-reminder 包裹的补充指令**。prompt 包不依赖 llm 包(避免环依赖)。
- **llm 包(改造)**:`Provider.Stream` 入参从位置参数改为 `Request` 结构体,承载 `Messages / Tools / System{Stable,Environment} / Reminder`。Anthropic 把 Stable 块打缓存断点、Env 块不打;OpenAI 把 Stable 置于系统消息前缀。`Usage` 增加缓存写/读字段。两 provider 把 `Reminder` 按各自协议安全地织入消息通道(N3)。
- **agent 包(改造)**:每次 `Run` 开始采集环境、装配稳定系统提示;每轮迭代按 `mode + iter` 计算本轮 reminder(规划模式按轮次详略),组装 `Request` 发起请求;把缓存用量透传到 `Event.Usage`。
- **smoke(改造)**:打印每轮用量的缓存写/读字段,作为缓存策略生效的验证手段(TUI 状态栏不变)。

数据流:`agent.Run` → `prompt.BuildSystemPrompt()`(稳定) + `prompt.GatherEnvironment().Render()`(环境) + `prompt.PlanReminder(full)`(本轮补充) → 组装 `llm.Request` → `provider.Stream` → Anthropic/OpenAI 各自装配缓存通道与消息通道 → 流式事件回到 agent → `Event{Usage:{...CacheWrite,CacheRead}}` → smoke 打印。

## 核心数据结构### prompt.Module(新增)
```go
type Module struct {
    Name     string // 模块标识(身份、系统约束 …),仅用于可读性与测试断言
    Priority int    // 数值越小优先级越高、排越前;固定模块 10..70,可选模块 80..100
    Content  string // 模块正文;为空则装配时跳过(可选空槽)
}
```

### prompt.Environment(新增)
```go
type Environment struct {
    WorkingDir string // os.Getwd()
    Platform   string // runtime.GOOS
    Date       string // time.Now().Format("2006-01-02")
    GitStatus  string // `git status --porcelain` 摘要;非 git 目录/取不到则留空
    Version    string // 应用版本(从 agent 透传)
    Model      string // provider.Model()
}
```

### llm.System(新增)
```go
type System struct {
    Stable      string // 可缓存:装配好的稳定系统提示(工具定义随 tools 一并进缓存前缀)
    Environment string // 不缓存:环境信息段
}
```

### llm.Request(新增,替换 Stream 位置参数)
```go
type Request struct {
    Messages []Message        // 持久对话历史(不含本轮 reminder)
    Tools    []ToolDefinition // 本轮工具集(普通=全量 / 规划=只读)
    System   System           // 稳定系统提示 + 环境段
    Reminder string           // 本轮 system-reminder 内容(已含标签;空=不注入)
}
```

### llm.Usage(扩展)
```go
type Usage struct {
    InputTokens  int64
    OutputTokens int64
    CacheWrite   int64 // Anthropic: cache_creation_input_tokens;OpenAI: 恒 0(自动缓存无写计数)
    CacheRead    int64 // Anthropic: cache_read_input_tokens;OpenAI: prompt_tokens_details.cached_tokens
}
```

### agent.Usage(扩展,对外事件)
```go
type Usage struct {
    Input, Output       int64
    CacheWrite, CacheRead int64 // 透传自 llm.Usage,供 smoke 打印
}
```

## 核心接口### prompt 包
```go
func FixedModules() []Module                 // 七个固定模块(身份…文本输出),内容内置
func OptionalModules() []Module              // 三个可选空槽(自定义指令/已激活Skill/长期记忆),Content=""
func AssembleSystem(mods []Module) string    // 按 Priority 升序、跳过空 Content、以 "\n\n" 连接
func BuildSystemPrompt() string              // = AssembleSystem(append(FixedModules(), OptionalModules()...))
func GatherEnvironment(version, model string) Environment // 采集环境;git/date 失败降级留空
func (Environment) Render() string           // 渲染为「环境信息」第二段文本
func SystemReminder(body string) string      // 用 <system-reminder>…</system-reminder> 包裹 body
func PlanReminder(full bool) string          // 返回包好标签的规划模式提醒(full=完整 / 否=精简)
```

### llm.Provider(签名变更)
```go
type Provider interface {
    Name() string
    Model() string
    Stream(ctx context.Context, req Request) <-chan StreamEvent // 由 Request 承载全部入参
}
```

## 模块设计### prompt 包
**职责:** 模块化装配稳定系统提示;采集并渲染环境信息;构造 system-reminder 与规划模式提醒。
**对外接口:** 见上。
**关键点:**
- 七个固定模块按优先级排:**身份(10) → 系统约束(20) → 任务模式(30) → 动作执行(40) → 工具使用(50) → 语气风格(60) → 文本输出(70)**;可选空槽:**自定义指令(80) → 已激活 Skill(90) → 长期记忆(100)**(Content="" 跳过)。
- **F5 双重强化**写在「工具使用(50)」模块:明确「优先用专用工具(read_file/glob/grep)而非用 bash 拼凑」「编辑文件前必须先 read_file 读取」;同时同义强化到 `edit_file`、`bash` 工具描述(见 tool 包改动)。
- `AssembleSystem` 只用常量内容 → 跨轮逐字节一致(**N1**);环境与时间相关内容只进 `Environment`,绝不进稳定模块。
- `GatherEnvironment`:git 状态用一条 `git status --porcelain` 带短超时执行,失败/非 git 目录则 `GitStatus=""`;不读取任何环境变量(**N5**)。
**依赖:** 标准库(os/runtime/time/os-exec);不依赖 llm。

### llm 包(provider.go / anthropic.go / openai.go)
**职责:** 把 `Request` 装配为各协议请求,分离缓存通道与消息通道,解析缓存用量,安全织入 reminder。
**对外接口:** `Stream(ctx, Request)`。
**关键点:**
- 删除 `effectiveSystem` 与对 `prompt` 包的 import(系统提示改由 agent 传入)。
- **Anthropic**:`System = []TextBlockParam{}`;`Stable` 非空 → `{Text:Stable, CacheControl: anthropic.NewCacheControlEphemeralParam()}`(断点,默认 5m TTL；**必须用构造器**——空字面量 `CacheControlEphemeralParam{}` 因 `json:"cache_control,omitzero"` 被判零值丢弃、断点不会发出);`Environment` 非空 → `{Text:Environment}`(无 CacheControl)。请求顺序 tools→system→messages,断点打在稳定块 → **缓存前缀 = 全部工具 + 稳定块**;env 与历史在断点后不缓存,env 每轮变化不影响前缀命中。`Usage.CacheWrite=acc.Usage.CacheCreationInputTokens`、`CacheRead=acc.Usage.CacheReadInputTokens`。
  - reminder 织入:`req.Reminder!=""` 时,把 `NewTextBlock(reminder)` **追加到最后一条消息的 content 块**(循环中最后一条恒为 user 或 tool_result→user,追加文本块仍是合法 user 消息,保 N3 角色交替);极端情形(末尾为 assistant)则新起一条 user 消息。
- **OpenAI**:系统消息 = `Stable`(若 `Environment!=""` 则拼为 `Stable + "\n\n" + Environment` 单条 system 消息——兼容端点对多条 system 消息支持不一,统一单条);`Stable` 居前缀 → 端点前缀缓存命中稳定部分。`Usage.CacheRead=acc.Usage.PromptTokensDetails.CachedTokens`、`CacheWrite=0`。
  - reminder 织入:`req.Reminder!=""` 时**追加一条尾部 user 消息**(OpenAI 容忍连续 user / tool 后接 user)。
- **N6**:缓存字段缺失即零值,不额外校验、不报错。

### agent 包(agent.go)
**职责:** 采集环境、装配系统提示、按轮次构造 reminder、组装 Request、透传缓存用量。
**关键点:**
- `New(p, r, version)` 增加 `version` 字段(供环境段);`Model` 取 `p.Model()`。
- `Run` 起始:`env := prompt.GatherEnvironment(a.version, a.provider.Model())`、`sys := prompt.BuildSystemPrompt()`(稳定,普通/规划模式一致——规划提醒已移出系统通道)。
- 每轮迭代计算 reminder:`mode==ModePlan` → `prompt.PlanReminder(full)`,`full = (iter==1 || (iter-1)%planReminderInterval==0)`;否则 `""`。`planReminderInterval=4`(内置常量)。
- `streamOnce` 组装 `llm.Request{Messages:conv.Messages(), Tools:defs, System:llm.System{Stable:sys, Environment:env.Render()}, Reminder:reminder}` 调 `provider.Stream`。
- 删除 `suffix`/`ReadOnlyDefinitions` 的「系统后缀」用法;**只读工具集仍按 mode 选择**(规划=`ReadOnlyDefinitions`),`PlanModeReminder` 常量从系统后缀迁移为 `prompt.PlanReminder` 的内容。
- 缓存用量透传:`Event{Usage:&Usage{Input,Output,CacheWrite,CacheRead}}`。

### smoke(cmd/smoke/main.go)
**职责:** 端到端验证缓存生效。
**关键点:** 消费 `Event.Usage` 时打印 `input/output/cache_write/cache_read`;跑两轮(或多轮)观察次轮 `cache_read>0`。`agent.New` 传一个版本字面量(如 `"dev"`)。

### tool 包(描述微调,F5)
- `edit_file.Description`:补「编辑前请先用 read_file 读取目标文件,确认 old_string 唯一」。
- `bash.Description`:补「读文件/找文件/搜内容请优先用 read_file/glob/grep,不要用 bash 拼凑」。
- 仅改描述文本,不改行为、不改 schema(N2)。

## 模块交互

```
TUI/smoke ─Run(ctx,conv,mode)→ agent
  agent.Run:
    env  = prompt.GatherEnvironment(version, provider.Model())
    sys  = prompt.BuildSystemPrompt()
    for iter:
      reminder = (mode==Plan) ? prompt.PlanReminder(full(iter)) : ""
      req = llm.Request{conv.Messages(), defs(mode), llm.System{sys, env.Render()}, reminder}
      provider.Stream(ctx, req) ──→ StreamEvent{Text/ToolCalls/Usage(+cache)/Done/Err}
    Event{Usage:{...CacheWrite,CacheRead}} ──→ smoke 打印 / TUI 状态栏(忽略 cache 字段)
```

依赖方向(无环):`agent → {prompt, llm, conversation, tool}`;`llm → config`(不再 import prompt);`prompt → 标准库`。

## 文件组织

```
mewcode/
├── internal/prompt/
│   ├── prompt.go        — 改:Module/装配/BuildSystemPrompt;保留 banner(CatBanner/RenderBanner/ReadyHint)
│   ├── modules.go       — 新:FixedModules()/OptionalModules() 七固定+三空槽的内容常量
│   ├── environment.go   — 新:Environment/GatherEnvironment/Render
│   ├── reminder.go      — 新:SystemReminder/PlanReminder(完整版/精简版常量)
│   └── prompt_test.go   — 新:装配顺序/跳空槽/N1 确定性/双重强化文本 断言
├── internal/llm/
│   ├── provider.go      — 改:Request/System 结构;Usage 加缓存字段;Provider.Stream 签名;删 effectiveSystem
│   ├── anthropic.go     — 改:两块 System(断点+env)、缓存用量解析、reminder 织入
│   └── openai.go        — 改:单条 system(Stable+env)、cached_tokens 解析、reminder 尾部注入
├── internal/agent/
│   ├── agent.go         — 改:New(+version)、Run 采集环境/装配系统、按轮次 reminder、缓存透传
│   └── agent_test.go    — 改/新:断言 Request 装配(System 两段、规划按轮次 reminder)、缓存用量透传
├── internal/tool/
│   ├── edit_file.go     — 改:Description 补强化
│   └── bash.go          — 改:Description 补强化
├── internal/tui/
│   └── stream.go        — 改:agent.New 传 version(m.version 已有)
└── cmd/smoke/main.go    — 改:打印缓存用量;agent.New 传版本字面量
```

## 技术决策

| 决策点 | 选择 | 理由 |
|--------|------|------|
| 系统提示组织 | 模块化(Module{Name,Priority,Content} + AssembleSystem) | 满足 F1「挂载即扩展」;优先级排序使顺序确定(N1) |
| 环境信息归属 | system 通道独立第二块(用户拍板) | 结构上接系统提示之后;物理上与稳定块分离,不进缓存 |
| Anthropic 缓存断点 | 仅在稳定 system 块打 `CacheControl`(默认 5m) | 请求序 tools→system→messages,断点在稳定块即缓存「工具+稳定块」整段前缀;env 在其后不缓存,env 变化不冲前缀命中(源码核实:TextBlockParam.CacheControl) |
| 工具是否单独打断点 | 否 | 稳定块断点的前缀已含全部工具,无需 ToolParam.CacheControl 再标 |
| OpenAI 环境信息 | 拼入单条 system 消息(Stable 在前) | 兼容端点对多条 system 支持不一;Stable 居前缀,端点前缀缓存自动命中稳定部分。代价:env 居 system 尾,OpenAI 工具可能不进缓存前缀——本章 OpenAI 缓存为尽力而为、不强制(F8) |
| 缓存用量字段 | Usage 加 CacheWrite/CacheRead | Anthropic 取 cache_creation/cache_read;OpenAI 取 prompt_tokens_details.cached_tokens(源码核实) |
| Stream 入参 | 改 `Request` 结构体 | 入参从 4 个增至含 System/Reminder,结构体更清晰、后续扩展不再改签名(N8) |
| reminder 注入位置 | Anthropic 并入末条 user 消息 content 块;OpenAI 追加尾部 user 消息 | Anthropic 严格角色交替——并入避免连续 user 触发 400(N3);OpenAI 容忍连续 user |
| reminder 持久化 | 不写入 conversation(用户拍板) | 每轮动态构造;不污染缓存、不破坏历史可恢复性 |
| 规划提醒节奏 | iter==1 或 (iter-1)%4==0 → 完整,否则精简(per Run 内 iter) | 实现 F7「首轮完整、间隔重复、其余精简」;复用已有 iter 计数 |
| 缓存验证呈现 | smoke/调试打印(用户拍板) | 不动 TUI 状态栏;Usage 携带字段供打印 |
| prompt↔llm 依赖 | 系统提示由 agent 传入,llm 不再 import prompt | 打破潜在环依赖;职责更清晰 |