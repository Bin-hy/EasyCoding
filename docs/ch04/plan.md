# Agent Loop Plan

> 基于已批准的 spec.md。本文档与语言相关（Go）。SDK 类型已对 anthropic-sdk-go v1.46.0、openai-go/v3 v3.37.0 核对（grounding 实测）。

## 架构概览ch04 不新增包，在 ch03「tool / agent / llm / conversation / prompt / tui」之上**扩展**：

- **internal/agent（重写 Run）**：把 ch03 的「请求#1 → 执行 → 请求#2 → 停」改为真正的 ReAct 循环——`for` 迭代直到自然完成 / 上限 / 取消 / 连续未知工具 / 出错。新增保序分批并发执行、迭代进度与用量事件、终止时的历史一致性收尾、Plan/Normal 两种模式。
- **internal/llm（扩展）**：`StreamEvent` 增 `Usage` 字段；`Provider.Stream` 增 `systemSuffix string` 形参（Plan Mode 系统提示后缀）；两适配器在流结束后上抛本轮 token 用量、把 `systemSuffix` 拼到内置系统提示后；OpenAI 打开 `StreamOptions.IncludeUsage`。
- **internal/tool（扩展）**：`Tool` 接口增 `ReadOnly() bool`；6 个工具各实现；`Registry` 增 `ReadOnlyDefinitions()` 与 `IsReadOnly(name)`。
- **internal/conversation（扩展）**：增 `LastRole()`（终止收尾判断角色尾巴）。
- **internal/prompt（扩展）**：增 `PlanModeReminder`（计划态系统后缀）与 `ExecuteDirective`（`/do` 触发执行的用户消息）；`SystemPrompt` 增补「持续工作直到任务完成」的 Agent 循环约定。
- **internal/tui（扩展）**：`submit` 识别 `/plan`、`/do`；引入 per-turn 取消上下文；事件泵处理用量 / 进度 / 通知 / 多个并发工具；按键处理拆分 Esc / Ctrl+C；状态栏显示模式与累计用量、动态区显示迭代轮次。

依赖方向不变、无环：`tool → llm`；`conversation → llm`；`agent → {llm, tool, conversation}`；`tui → {agent, tool, conversation, llm, prompt}`；`llm → {config, prompt}`。

## 核心数据结构### llm 包（provider.go 扩展）

```go
// Usage 协议无关地承载一轮请求的 token 用量。
type Usage struct {
  InputTokens  int64 // 本轮请求输入（含完整历史）token 数
  OutputTokens int64 // 本轮响应输出 token 数
}

// StreamEvent 扩展：在 Text/ToolCalls/Done/Err 之外，turn 结束时一次性上抛 Usage。
type StreamEvent struct {
  Text      string
  ToolCalls []ToolCall
  Usage     *Usage // 非空：本轮 token 用量（Done 之前一次性发出）
  Done      bool
  Err       error
}
```

`Provider.Stream` 签名变更（新增第 4 形参）：

```go
// systemSuffix 非空时拼接到内置 system prompt 之后（Plan Mode 计划态约束）；为空即普通模式。
Stream(ctx context.Context, msgs []Message, tools []ToolDefinition, systemSuffix string) <-chan StreamEvent
```

`Message`/`ToolCall`/`ToolResult`/`ToolDefinition` 与 `RoleTool` 沿用 ch03，不变。

### tool 包（接口扩展）

```go
// Tool 接口新增 ReadOnly：true=只读工具（可并发执行 & Plan Mode 放行）。
type Tool interface {
  Name() string
  Description() string
  Parameters() map[string]any
  ReadOnly() bool // 新增
  Execute(ctx context.Context, args json.RawMessage) Result
}
```

只读分类（依据语义）：`read_file`/`glob`/`grep` → `true`；`write_file`/`edit_file`/`bash` → `false`（`bash` 可执行任意副作用命令，保守归为有副作用、串行执行）。

`Registry` 新增：

```go
func (r *Registry) ReadOnlyDefinitions() []llm.ToolDefinition // Plan Mode：只导出 ReadOnly()==true 的工具定义
func (r *Registry) IsReadOnly(name string) bool               // 分批判定；未知工具返回 false（按串行处理）
```

### agent 包（事件模型扩展 + Run 重写）

```go
// Usage 一轮请求的 token 用量（透传 llm.Usage 的语义）。
type Usage struct {
  Input  int64
  Output int64
}

// Event 对外事件流元素，消费者据非零字段分派渲染。
type Event struct {
  Text   string     // 模型文本增量（preamble 或最终答复）
  Tool   *ToolEvent // 工具调用开始/结束（沿用 ch03）
  Usage  *Usage     // 本轮 token 用量（每轮 stream 结束后一次）
  Iter   int        // >0：进入第 Iter 轮迭代（进度提示）
  Notice string     // 系统提示（停止原因等），仅用于 UI 展示，不入对话历史
  Done   bool       // 本轮（整个 Loop）结束
  Err    error      // 出错（不中断会话）
}

// Mode 区分普通模式与计划模式。
type Mode int

const (
  ModeNormal Mode = iota
  ModePlan
)

// Run 执行 Agent Loop，返回事件 channel；mode 决定工具集与系统后缀。
func (a *Agent) Run(ctx context.Context, conv *conversation.Conversation, mode Mode) <-chan Event
```

`ToolEvent`、`Phase`(PhaseStart/PhaseEnd)、`Agent`、`New` 沿用 ch03。`Run` 签名新增 `mode` 形参。

`New` 沿用 ch03：`func New(p llm.Provider, r *tool.Registry) *Agent`。`mode` 为 `Run` 的每次调用入参，不写入 `Agent` 状态（同一 `Agent` 可被不同 mode 复用）。

迭代、停止常量与提示文案（内置，不可配）：

```go
const (
  maxIterations = 25 // 迭代上限兜底（F2）
  maxUnknownRun = 3  // 连续「整轮只产生未知工具调用」的迭代数上限（F2）
)

// 停止/收尾提示文案——既作为 Event{Notice} 推给 UI，也作为 ensureAssistantTail 写入历史的兜底文本。
const (
  noticeMaxIter      = "（已达最大迭代轮数 25，自动停止；可继续发消息推进。）"
  noticeUnknownTools = "（连续多轮只请求到未注册的工具，自动停止。）"
  noticeStreamErr    = "（请求出错，本轮已中断。）"
  noticeCancelled    = "（已取消。）"
)
```

## 模块设计### internal/agent（核心：Run 重写）**职责：** ReAct 循环编排（F1/F2）、保序分批并发执行（F5）、事件流（F3/F8/F9）、终止历史一致性（F6）、Plan/Normal 模式（F10）。
**对外接口：** `Agent`、`New`、`Run(ctx, conv, mode)`、`Event`、`ToolEvent`、`Phase`、`Mode`、`Usage`。
**依赖：** `llm`、`tool`、`conversation`、`context`、`sync`（并发批 WaitGroup）。

**Run 算法（goroutine 内，`defer close(ch)`）：**

1. 按 `mode` 取工具集与系统后缀：
   - `ModePlan` → `defs = registry.ReadOnlyDefinitions()`、`suffix = prompt.PlanModeReminder`。
   - `ModeNormal` → `defs = registry.Definitions()`、`suffix = ""`。
2. `unknownRun := 0`。
3. `for iter := 1; iter <= maxIterations; iter++`：
   1. `emit(Event{Iter: iter})`（进度，F9）；emit 返回 false（ctx 取消）→ `finishCancelled(conv)`、return。
   2. `text, calls, usage, ok := streamOnce(ctx, conv, defs, suffix, ch)`。
      - `!ok` 且 `ctx.Err()!=nil`（取消）→ `finishCancelled(conv)`、return。
      - `!ok` 且 `ctx.Err()==nil`（流出错，Err 已在 streamOnce 内发出）→ `ensureAssistantTail(conv, noticeStreamErr)`、return。
   3. `if usage != nil { emit(Event{Usage:&Usage{usage.InputTokens, usage.OutputTokens}}) }`（F8）。
   4. **无工具** `len(calls)==0`：`conv.AddAssistant(ensureFinal(ch, text))`；`emit(Event{Done:true})`；return（自然完成，F2-1）。
   5. **有工具**：`conv.AddAssistantWithToolCalls(text, calls)`。
   6. 统计未知工具：`if allUnknown(calls) { unknownRun++ } else { unknownRun = 0 }`。
   7. `results, completed := executeBatched(ctx, calls, ch)`（保序分批并发，F5）。
   8. `conv.AddToolResults(results)`（无论是否取消都回灌，含已取消占位，F6）。
   9. `if !completed`（执行中被取消）→ `ensureAssistantTail(conv, "（已取消）")`、return。
   10. `if unknownRun >= maxUnknownRun` → `emit(Event{Notice: noticeUnknownTools})`；`ensureAssistantTail(conv, noticeUnknownTools)`；`emit(Event{Done:true})`；return（F2-4）。
4. 循环正常走完（触达上限）：`emit(Event{Notice: noticeMaxIter})`；`ensureAssistantTail(conv, noticeMaxIter)`；`emit(Event{Done:true})`（F2-2）。

**streamOnce(ctx, conv, defs, suffix, ch) → (text string, calls []llm.ToolCall, usage *llm.Usage, ok bool)：**
遍历 `provider.Stream(ctx, conv.Messages(), defs, suffix)`：
- `ev.Err != nil` → `emit(Event{Err: ev.Err})`、`return "", nil, nil, false`。
- `ev.Usage != nil` → 记录 `usage = ev.Usage`（不立即 emit，由 Run 在拿到后统一 emit）。
- `len(ev.ToolCalls) > 0` → `calls = append(calls, ev.ToolCalls...)`。
- `ev.Text != ""` → 累积 `text` 并 `emit(Event{Text: ev.Text})`；emit 失败→`return ...,false`。
循环后 `if ctx.Err()!=nil { return "",nil,nil,false }`；否则 `return text, calls, usage, true`。

**executeBatched(ctx, calls, ch) → (results []llm.ToolResult, completed bool)：**
保序分批（F5）。`results := make([]llm.ToolResult, len(calls))`；从 `i=0` 逐段扫描：
- 当前 `calls[i]` 只读 → 向前吃连续只读得最长区间 `[i,j)`（`j` 为首个非只读或末尾），**并发**执行该批：每个调用一个 goroutine，goroutine 内 `tctx, cancel := context.WithTimeout(ctx, tool.DefaultTimeout)` 后 `registry.Execute(tctx, ...)`，结果写入**自己下标** `results[k]`（互不重叠，无锁）；`sync.WaitGroup` 汇合。`i = j`。
- 当前 `calls[i]` 非只读 → **串行**执行单个 `calls[i]`（同样 `context.WithTimeout(ctx, tool.DefaultTimeout)`），写 `results[i]`。`i++`。
- 每段开始执行前先判 `ctx.Err()!=nil`（取消）：给区间内尚未执行的 call 填「已取消」结果（`Result{IsError:true, Content:noticeCancelled}`），其余沿用已得结果，`return results, false`。
- 全部完成 `return results, true`。

> 超时口径：每个工具各拿一个 `DefaultTimeout`（30s）子 ctx，互不相加——并发批的整体上限仍是单个 30s（N1）。子 ctx 都派生自 per-turn `ctx`，用户取消时一并 Done，工具尽快返回。

事件与顺序（满足 N3 顺序、N2 不阻塞、N6 无竞争）：
- 单个串行工具：`emit(Tool{Start})` → 执行 → `emit(Tool{End})`（沿用 ch03 时序，动态区显示该工具 Running）。
- 并发批：**先**按序 `emit(Tool{Start})` 区间内每个工具（动态区列出多个在执行的工具行）→ 并发执行 → **再**按原始顺序 `emit(Tool{End})` 每个工具（逐个把工具行 + 结果摘要提交 scrollback）。即「开始事件按序、结束事件按序」，并发只发生在执行环节，事件顺序始终是调用序，scrollback 不交错。
- 并发安全：每个 goroutine 只写自己下标的 `results[k]`（不同下标互不重叠），不触碰 `conv`；`conv.AddToolResults` 由 Run 主流程在 WaitGroup 汇合后串行调用。Token 用量累计在 TUI 侧串行处理。

**辅助函数：**
- `emit(ctx, ch, e) bool`：沿用 ch03——`select { case ch<-e: return true; case <-ctx.Done(): return false }`。即**返回 false 当且仅当 per-turn ctx 被取消**（channel 由 Run 自己持有且 `defer close`，不会在发送中被关）。调用方据 false 提前收尾。
- `allUnknown(calls)`：对每个 call 用 `registry.Get(call.Name)` 判断，**全部** `ok==false` 才返回 true；任一已注册即 false（混入已知工具视为有进展，计数重置）。不能用 `IsReadOnly`（未知工具它也返回 false，会与有副作用工具混淆）。
- `ensureFinal(ch, text)`：沿用 ch03——`text` 非空原样返回；为空则 emit 占位提示并返回占位文本（避免空 assistant 回合破坏下一轮请求）。
- `ensureAssistantTail(conv, fallback)`：若 `conv.LastRole() != llm.RoleAssistant`（含空历史、末尾为 user 或 RoleTool），`conv.AddAssistant(fallback)`，保证历史以 assistant 文本回合收尾（F6：取消/出错/上限后角色仍交替，下一轮请求不报 400）。
- `finishCancelled(conv)`：取消路径统一收尾——`ensureAssistantTail(conv, noticeCancelled)`、return（**不 emit**，因 ctx 已取消 emit 必失败；channel 经 `defer close` 关闭，TUI 由 `waitForEvent` 收到关闭即视为结束）。

> 终止优先级：执行中取消（`completed==false`）是**最高优先级**终止——立即 `ensureAssistantTail` 并 return，**跳过**未知工具计数与迭代上限检查。

### internal/llm（扩展）**职责：** 协议无关请求/响应 + 两协议工具调用全流程（沿用 ch03）+ 本轮用量上抛（F8）+ 系统后缀（F10）。

**provider.go：** 新增 `Usage` 类型；`StreamEvent` 增 `Usage *Usage`；`Provider.Stream` 增 `systemSuffix string` 形参（更新接口文档）。

**anthropic.go：**
- 系统提示：`params.System` 由硬编码 `prompt.SystemPrompt` 改为 `effectiveSystem(suffix)`——`suffix==""` 时单块 `prompt.SystemPrompt`；非空时拼成 `prompt.SystemPrompt + "\n\n" + suffix`（单 `TextBlockParam`，避免多块边界差异）。
- 用量：流正常结束（`stream.Err()==nil`、`acc.Accumulate` 完成）后，在上抛 `ToolCalls` / `Done` 之前 `ch <- StreamEvent{Usage: &Usage{InputTokens: acc.Usage.InputTokens, OutputTokens: acc.Usage.OutputTokens}}`（`acc.Usage` 仅在流结束后完整）。
- 历史含工具交互时 thinking 已自动关闭（ch03 既有逻辑，line 52），多轮续答沿用，无需改动。

**openai.go：**
- 请求构造增 `params.StreamOptions = openai.ChatCompletionStreamOptionsParam{IncludeUsage: openai.Bool(true)}`（不开则流式 Usage 为空）。
- 系统提示：`toOpenAIMessages` 接收 `suffix`，把首条 system 消息文本由 `prompt.SystemPrompt` 改为拼接 `suffix`（非空时 `+"\n\n"+suffix`）。
- 用量：流结束后读 `acc.Usage`（`CompletionUsage`），`ch <- StreamEvent{Usage: &Usage{InputTokens: acc.Usage.PromptTokens, OutputTokens: acc.Usage.CompletionTokens}}`。

### internal/tool（扩展）

- `Tool` 接口加 `ReadOnly() bool`；6 个工具各加一行实现（read/glob/grep 返回 true，write/edit/bash 返回 false）。
- `Registry.ReadOnlyDefinitions()`：仿 `Definitions()`，仅收 `tools[name].ReadOnly()==true` 的项，保持注册顺序。
- `Registry.IsReadOnly(name)`：`t, ok := Get(name); return ok && t.ReadOnly()`（未知工具 false）。
- `Execute`、`DefaultTimeout`、6 工具的执行逻辑均不变。

### internal/conversation（扩展）

```go
// LastRole 返回最后一条消息的角色；空历史返回 ""。
func (c *Conversation) LastRole() string
```
其余沿用 ch03。

### internal/prompt（扩展）

```go
// PlanModeReminder：Plan Mode 系统提示后缀，拼接到 SystemPrompt 之后。
const PlanModeReminder = "You are currently in PLAN MODE. You may use ONLY the read-only tools " +
  "(read_file, glob, grep) to investigate the codebase. You must NOT write files, edit files, " +
  "or run shell commands. Produce a clear, step-by-step plan for the task, then stop and wait for " +
  "the user to approve it with /do before doing any work."

// ExecuteDirective：/do 注入的用户消息——指示模型按上文已确认的计划开始执行，可使用全部工具。
const ExecuteDirective = "请按上面的计划开始执行。"
```
`SystemPrompt` 增补一句 Agent 循环约定（追加到现有文案）：`"Keep using tools across multiple steps to make progress, and only give your final concise answer once the task is complete."`（中文项目里保持英文 system prompt 风格，与 ch03 现有 `SystemPrompt` 一致）。

### internal/tui（扩展）**Model 新增字段（tui.go）：**
- `mode agent.Mode`——当前模式（默认 `ModeNormal`），`/plan`、`/do` 切换，跨轮保持。
- `iter int`——当前迭代轮次（进度显示），每轮 `Iter` 事件更新，`finishTurn` 归零。
- `usageIn, usageOut int64`——会话累计 token 用量，每个 `Usage` 事件累加。
- `curTools []toolDisplay`——替换 ch03 的单个 `curTool *toolDisplay`，支持并发批多个在执行的工具行。
- `turnCancel context.CancelFunc`——本轮取消函数（派生自 `m.ctx`），Esc / Ctrl+C 触发；`m.ctx`/`m.cancel` 仍为程序级。

**submit（stream.go）：**
1. `/exit` → 退出（沿用）。
2. `/plan` → `m.mode = agent.ModePlan`；提交一行提示块到 scrollback（如「已进入计划模式（只读工具）」）；回空闲态。
3. `/do` → `m.mode = agent.ModeNormal`；`m.conv.AddUser(prompt.ExecuteDirective)`；走与普通提交相同的启动流程（不把 `/do` 本身入历史）。
4. 普通文本 → `m.conv.AddUser(text)`。
5. 启动：`turnCtx, m.turnCancel = context.WithCancel(m.ctx)`；`m.events = agent.New(m.provider, m.registry).Run(turnCtx, m.conv, m.mode)`；`m.state = stateStreaming`；`m.iter = 0`。用户输入块先 `tea.Println` 再泵事件（沿用 ch03 `tea.Sequence`）。

**updateStreaming（stream.go）分派顺序：**
`Err` → `Tool` → `Usage`（累加 `usageIn/usageOut`，重挂泵）→ `Notice`（`tea.Println` 一行灰色系统提示块，重挂泵）→ `Iter>0`（`m.iter = Iter`，重挂泵）→ `Done` → `Text`（累积 `curReply`，重挂泵）。
- `Tool.PhaseStart`：若 `curReply` 非空先把 preamble 提交 scrollback 并清空；`m.curTools = append(m.curTools, toolDisplay{name,args})`；重挂泵。
- `Tool.PhaseEnd`：**FIFO 弹出队首** `curTools[0]`（因 agent 保证 PhaseStart 与 PhaseEnd 都按调用序发出，结束序 == 入队序，弹首即对应工具，无需按 name 匹配，重名工具也不会错位）；用其 args 定型工具行，`tea.Sequence(tea.Println(toolLine), tea.Println(toolResultSummary), waitForEvent)`。

**按键（tui.go Update，全局优先）：**
- `ctrl+c`：`stateStreaming` → `m.turnCancel()`（取消本轮，不退出），重挂泵等 Done；否则 `m.cancel(); tea.Quit`（退出）。
- `esc`：`stateStreaming` → `m.turnCancel()`；其余忽略。

**view.go：**
- `statusBar`：左侧在 provider 名后附模式标记（`ModePlan` 显示「PLAN」徽标）；右侧在 model 名旁附累计用量 `↑{in} ↓{out} tok`（数值用紧凑格式，如 `1.2k`）。保持单行。
- 流式动态区：`curTools` 非空时逐行渲染 `● name(args)` + Running…（多个并发工具多行）；否则渲染「Imagining… (Ns · 第 N 轮)」（`m.iter>0` 时附轮次）。
- `toolLine` / `toolResultSummary` 沿用 ch03。

**finishTurn（stream.go）：** 清 `curReply`、`curTools=nil`、`events=nil`、`iter=0`、`turnCancel=nil`，回 `stateIdle`（`mode`、`usageIn/usageOut` 不清——跨轮保持）。

## 模块交互

```
用户提交 /do 或普通文本
  └─ tui.submit:
       ├─ /plan → mode=Plan，回 idle
       ├─ /do   → mode=Normal; conv.AddUser(ExecuteDirective)
       ├─ 文本  → conv.AddUser(text)
       └─ turnCtx,turnCancel = WithCancel(ctx); events = agent.New(...).Run(turnCtx, conv, mode)
            └─ agent.Run (goroutine, ReAct 循环):
                 for iter:
                   ├─ emit Iter
                   ├─ 请求: provider.Stream(turnCtx, conv.Messages(), defs(mode), suffix(mode))
                   │     └─ 适配器: 注入 tools + (SystemPrompt+suffix) → 流式拼接
                   │          → StreamEvent{Text…}/{ToolCalls}/{Usage}/{Done|Err}
                   │     → agent 转发 Text(preamble)、收集 calls、记录 usage
                   ├─ emit Usage
                   ├─ 无 calls → conv.AddAssistant(final); emit Done; 停
                   └─ 有 calls:
                        ├─ conv.AddAssistantWithToolCalls(preamble, calls)
                        ├─ executeBatched: 连续只读并发 / 有副作用串行
                        │     （Start 事件按序 → 执行 → End 事件按序）
                        ├─ conv.AddToolResults(results)
                        └─ 下一轮 iter
  └─ tui.updateStreaming: Text→curReply；Tool→curTools/scrollback；Usage→累加；
       Iter→m.iter；Notice→灰提示；Done→提交最终答复+finishTurn
  └─ Ctrl+C / Esc（streaming）→ turnCancel() → Run 收尾历史 → 关 channel → finishTurn → idle
```

并发模型：`conv` 任一时刻只被 `Run` 的主 goroutine 触碰（`submit` 在交给 `Run` 前 `AddUser`，之后不再触碰；执行批的工作 goroutine 只写各自 `results[k]`，不碰 `conv`）。`Messages()` 返回副本。TUI 仅按事件渲染。满足 N2/N6。

## 文件组织

```
mewcode/
├── internal/
│   ├── llm/
│   │   ├── provider.go     — 修改：新增 Usage；StreamEvent 加 Usage；Stream 加 systemSuffix 形参
│   │   ├── anthropic.go    — 修改：effectiveSystem(suffix)；流结束上抛 acc.Usage
│   │   └── openai.go       — 修改：StreamOptions.IncludeUsage；toOpenAIMessages 拼 suffix；上抛 acc.Usage
│   ├── tool/
│   │   ├── tool.go         — 修改：Tool 接口加 ReadOnly()
│   │   ├── registry.go     — 修改：ReadOnlyDefinitions、IsReadOnly
│   │   └── {read_file,write_file,edit_file,bash,glob,grep}.go — 修改：各加 ReadOnly()
│   ├── agent/
│   │   ├── agent.go        — 重写：ReAct 循环、Mode、executeBatched、Usage/Iter/Notice 事件、历史收尾
│   │   └── agent_test.go   — 扩展：多轮 fake provider（[][]StreamEvent 多次 Stream）、并发分批、停止条件、Plan 工具集
│   ├── conversation/
│   │   ├── conversation.go — 修改：LastRole()
│   │   └── conversation_test.go — 扩展：LastRole 断言
│   ├── prompt/
│   │   └── prompt.go       — 修改：PlanModeReminder、ExecuteDirective；SystemPrompt 增循环约定
│   └── tui/
│       ├── tui.go          — 修改：Model 增 mode/iter/usage/curTools/turnCancel；按键拆分 Esc/Ctrl+C
│       ├── stream.go       — 修改：submit 识别 /plan /do + per-turn ctx；updateStreaming 处理 Usage/Iter/Notice/多工具
│       └── view.go         — 修改：状态栏模式徽标+累计用量；动态区迭代轮次+多并发工具行
└── cmd/smoke/main.go       — 修改：调用 agent.Run 处补 mode 实参（agent.ModeNormal）
```

> 注：`cmd/mewcode/main.go` 已在 ch03 注入 registry，ch04 无需改动；`mode` 状态存于 TUI，不经 main。

### 签名变更的调用方清单（实测核对，确保编译不漏）

ch04 改了两个签名，必须同步所有调用方/实现方，否则编译断：

- **`Provider.Stream` 增 `systemSuffix string`（第 4 形参）**：
  - 实现方：`internal/llm/anthropic.go`、`internal/llm/openai.go`。
  - 调用方：`internal/agent/agent.go` 的 `streamOnce`（唯一直接调用方）。
  - 测试实现方：`internal/agent/agent_test.go` 的 `fakeProvider.Stream`（也实现该接口，签名须同步）。
  - **`cmd/smoke/main.go` 不直接调 `Stream`**（它走 `agent.Run`），无需为 systemSuffix 改动。
- **`Agent.Run` 增 `mode Mode`（第 3 形参）**：
  - 调用方：`internal/tui/stream.go`（`submit` 内）、`cmd/smoke/main.go`（line 25 `a.Run(ctx, conv)`）、`internal/agent/agent_test.go`（各用例）。三者都要补 `mode` 实参（smoke / 旧用例传 `agent.ModeNormal`）。

## 技术决策

| 决策点 | 选择 | 理由 |
|--------|------|------|
| Loop 放哪 | 重写 `agent.Run` 为循环，签名加 `mode` | 循环编排天然属 agent 包；TUI 维持纯渲染器。Run 已返回事件 channel，循环只是把单轮的两次 `streamOnce` 推广为 `for`，改动收敛在一个包。 |
| 不用 SDK 内置 tool-runner | 坚持手写循环 + stable streaming | 沿用 ch03 决策；自写循环才能精确控制停止条件、保序分批、取消与历史收尾，SDK 的自动 runner 把这些黑盒化。 |
| 停止条件之「连续未知工具」 | 连续 `maxUnknownRun=3` 轮「整轮只产生未知工具调用」即停 | 单次未知工具靠 registry 的「未知工具」结构化错误回灌即可让模型纠偏；只有连续多轮全错才说明在对幻觉工具空转，需兜底。混入任一已注册工具即重置计数（视为有进展）。 |
| 迭代上限值 | `maxIterations=25`，内置常量 | 兜底安全网，避免失控烧 token；25 足够覆盖正常多步任务。spec 明确不配置化，与 ch03 超时不配化一致。 |
| 并发分批粒度 | 「连续只读」合批并发，有副作用单个串行，保持调用序 | 用户选定的「保序分批」：read 之后的 write 不会被提前；相邻只读才并发加速。`bash` 保守归有副作用（可含任意写操作）。 |
| 并发的事件顺序 | 开始事件按序、结束事件按序，并发只在执行环节 | 满足 N3（scrollback 不交错）：UI 看到的工具行顺序始终是模型调用序；并发对用户透明，只体现为更快。每个 worker 只写自己下标的 `results[k]`，无竞争（N6）。 |
| 取消机制 | per-turn `context.WithCancel(m.ctx)`；Esc / Ctrl+C(streaming) 取消，Ctrl+C(idle) 退出 | 程序级 `m.ctx` 不动，新增每轮子 ctx 才能「取消本轮但不退程序」。取消即触发 streamOnce/工具 ctx 的 Done，自然停。 |
| 取消后历史一致 | 已发起工具补「已取消」结果 + `ensureAssistantTail` 收尾 | F6：取消可能停在「assistant 含 tool_use 但缺 tool_result」或「user 之后无 assistant」处；补齐工具结果 + 保证 assistant 文本尾巴，下一轮请求才不会因悬空 tool_use / 连续同角色被 API 拒（400）。 |
| 用量提取位置 | 适配器在流结束后从累加器读 `acc.Usage` 并经 `StreamEvent{Usage}` 上抛 | 两 SDK 的流式 usage 都只在流结束的累加器里完整（Anthropic `acc.Usage`、OpenAI 需 `IncludeUsage` 后读 `acc.Usage`）；逐 delta 不含。统一在 Done 前发一次。 |
| 累计用量口径 | 状态栏显示「会话累计计费 token」= 每轮 input+output 之和 | 多轮 Loop 每轮都重发完整历史，各轮 input 重复计费；按轮累加正是实际消耗/成本口径，对用户最有意义。 |
| Plan Mode 系统提示注入 | `Provider.Stream` 加 `systemSuffix string` 形参 | 系统提示在适配器内注入，要让计划态约束生效必须穿过 Stream。加一个字符串形参最小且显式；备选「请求 options struct」更可扩展但改动面更大，YAGNI 下不引入。 |
| Plan Mode 工具集 | 计划态只注入 `ReadOnlyDefinitions()` | 物理上不给模型写/执行工具，即便提示被忽略也无法改动；只读分类靠 `Tool.ReadOnly()`。 |
| `/do` 语义 | 切回 Normal + 注入 `ExecuteDirective` 用户消息 + 立即启动 Loop | 用户选定「切回全工具并立即执行」；复用已在历史里的计划，`/do` 不入历史，只把执行指令作为用户消息驱动模型开干。 |
| 模式状态存放 | 存于 TUI `Model`，不进 `Conversation` | `Conversation` 是历史、`Messages()` 返回副本，放不住可变模式；模式是会话级 UI 状态，跨轮保持，归 TUI 最自然。 |
| 多并发工具的 UI | `curTools []toolDisplay` 取代单个 `curTool` | 并发批同时有多个工具在跑，动态区需多行展示；结束事件按序逐个落 scrollback。 |
| 进度事件 | 每轮起始 emit `Event{Iter:n}`，UI 显示「第 N 轮」 | F9 让用户感知多轮推进；用非零 `Iter` 字段分派，与 ch03 的零值分派惯例一致。 |
| 通知 vs 历史 | 上限/未知工具的提示同时 emit `Notice`（UI 灰字）并写入 assistant 历史 | UI 要让用户看到为何停；写入历史是为满足 `ensureAssistantTail`（角色交替），二者用同一文案，避免历史里留空 assistant 回合。 |