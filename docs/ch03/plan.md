# 工具系统 Plan

> 基于已批准的 spec.md。本文档与语言相关（Go）。SDK 类型已对 anthropic-sdk-go v1.46.0、openai-go/v3 v3.37.0 实测核对。

## 架构概览

在 ch02「provider → conversation → tui」三件套之上，新增两个包并扩展三处：

- **internal/tool（新建）**：统一工具抽象 `Tool`、执行结果 `Result`、注册中心 `Registry`、6 个核心工具。零外部依赖，不感知 LLM 协议。
- **internal/agent（新建）**：承载「单轮闭环」编排——请求#1（带工具）→ 收集工具调用 → 注册中心执行 → 结果回灌进 `Conversation` → 请求#2（续答）→ 最终文本 → 停。对外吐出一条 `Event` 流供 TUI 渲染。只依赖 `llm`、`tool`、`conversation`，不 import anthropic/openai，保持协议无关。
- **internal/llm（扩展）**：`Message`/`StreamEvent` 增加工具字段；新增协议无关类型 `ToolCall`/`ToolResult`/`ToolDefinition` 与 `RoleTool` 常量；`Provider.Stream` 增加 `tools` 参数；两个适配器注入工具定义、解析流式工具调用、回灌工具结果。
- **internal/conversation（扩展）**：新增「assistant 工具调用回合」与「工具结果回合」的追加方法。
- **internal/prompt（扩展）**：`SystemPrompt` 增补 Agent 角色与工具使用约定。
- **internal/tui（扩展）**：`submit` 改走 `agent.Run`；事件泵处理工具事件；渲染 Claude Code 风格工具行与执行指示。
- **cmd/mewcode/main.go（扩展）**：构造 `tool.NewDefaultRegistry()` 并注入 `tui.New`。

依赖方向（无环）：`tool → llm`；`conversation → llm`；`agent → {llm, tool, conversation}`；`tui → {agent, tool, conversation, llm, prompt}`；`llm → {config, prompt}`。

## 核心数据结构### llm 包（provider.go 扩展）

```go
// 消息角色——新增 RoleTool。
const (
    RoleUser      = "user"
    RoleAssistant = "assistant"
    RoleTool      = "tool" // 携带工具执行结果的回合
)

// ToolCall 协议无关地承载模型发起的一次工具调用（流式拼接完成后）。
type ToolCall struct {
    ID    string          // provider 侧调用 id；回灌结果时配对
    Name  string          // 工具名（注册中心按名查找）
    Input json.RawMessage // 拼接完成的 JSON 参数
}

// ToolResult 协议无关地承载一次工具执行结果。
type ToolResult struct {
    ToolCallID string // 对应 ToolCall.ID
    Content    string // 执行产出（成功内容或结构化错误文本）
    IsError    bool   // 是否为错误结果（F9）
}

// ToolDefinition 注册中心导出的协议无关工具定义。
type ToolDefinition struct {
    Name        string
    Description  string
    InputSchema  map[string]any // 完整 JSON Schema 对象：type/properties/required
}

// Message 扩展：assistant 回合可带 ToolCalls；RoleTool 回合带 ToolResults。
type Message struct {
    Role        string
    Content     string
    ToolCalls   []ToolCall   // 仅 assistant：本回合请求的工具调用
    ToolResults []ToolResult // 仅 RoleTool：工具执行结果（一条消息可含多个）
}

// StreamEvent 扩展：在 Text/Done/Err 之外，turn 结束时一次性上抛 ToolCalls。
type StreamEvent struct {
    Text      string     // 正文文本增量
    ToolCalls []ToolCall // 非空：本轮模型请求执行这些工具（Done 之前发出）
    Done      bool
    Err       error
}
```

`Provider.Stream` 签名变更：

```go
Stream(ctx context.Context, msgs []Message, tools []ToolDefinition) <-chan StreamEvent
```

`tools` 为空表示本次请求不带工具。续答请求（请求#2）仍传入 `tools`（与真实协议一致），但编排层忽略其再次返回的工具调用（单轮）。

### tool 包（新建）

```go
// Result 工具执行结果——永远以值类型返回，从不返回 Go error。
type Result struct {
    Content string // 回灌给模型的文本（已截断/带行号等）
    IsError bool   // true 表示结构化错误，Content 即错误描述
}

// Tool 统一工具抽象（F1）。
type Tool interface {
    Name() string                // 模型看到的工具名，如 "read_file"
    Description() string         // 给模型的用途说明
    Parameters() map[string]any  // 手写 JSON Schema（type/properties/required/description）
    Execute(ctx context.Context, args json.RawMessage) Result
}

// Registry 集中登记、按名查找、导出定义、按名执行。
type Registry struct {
    order []string        // 保持注册顺序，导出稳定
    tools map[string]Tool
}

func (r *Registry) Register(t Tool)
func (r *Registry) Get(name string) (Tool, bool)
func (r *Registry) Definitions() []llm.ToolDefinition          // F3/AC1：按序导出
func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage) Result // F5/F9：未知工具兜底为 IsError

// NewDefaultRegistry 构造并注册 6 个工具，固化 bash 超时与各上限常量。
func NewDefaultRegistry() *Registry

// DefaultTimeout 单个工具执行的默认超时（N1，不可配）。
const DefaultTimeout = 30 * time.Second
```

每个工具私有入参 struct，`Execute` 内 `json.Unmarshal(args, &a)`，解析失败转为 `Result{IsError:true}`：

| 工具名 | 参数（JSON Schema） | 成功结果 | 错误结果 |
|--------|--------------------|---------|---------|
| `read_file` | `path`(必填) | 带行号文本（cat -n 风格，≤2000 行 / ≤256KB，超出截断标注 `[truncated]`） | 不存在/不可读/是目录 |
| `write_file` | `path`(必填)、`content`(必填) | `MkdirAll` 建父目录后覆盖写，返回路径与字节数 | 写入失败 |
| `edit_file` | `path`、`old_string`、`new_string`(均必填) | `strings.Count`==1 时唯一替换并写回 | 0 处→「未找到匹配」；>1 处→「匹配到 N 处，old_string 不唯一，请提供更长上下文」 |
| `bash` | `command`(必填) | 按平台选 shell（Unix `sh -c` / Windows `cmd /C`）执行，返回 stdout/stderr/exit_code（合并视图截断 ~30000 字符） | 超时（IsError）；命令非零退出按结果回灌 |
| `glob` | `pattern`(必填，如 `**/*.go`)、`path`(可选，默认 cwd) | 匹配路径列表（≤100，排序） | 无匹配返回空说明（非 IsError） |
| `grep` | `pattern`(必填，RE2 正则)、`path`(可选)、`glob`(可选文件名过滤) | `file:line:content` 列表（≤100，超出标注） | 正则非法（IsError）；无命中返回空说明 |

### agent 包（新建）

```go
// Agent 持有 provider 与注册中心，执行单轮闭环。
type Agent struct {
    provider llm.Provider
    registry *tool.Registry
}

func New(p llm.Provider, r *tool.Registry) *Agent

// Phase 工具事件阶段。
type Phase int
const (
    PhaseStart Phase = iota // 工具开始执行
    PhaseEnd                 // 工具执行完毕
)

// ToolEvent 一次工具调用的开始/结束（供 TUI 渲染工具行与结果摘要）。
type ToolEvent struct {
    Name    string
    Args    string // 参数预览（用于 ● name(args)）
    Phase   Phase
    Result  string // PhaseEnd：结果摘要
    IsError bool   // PhaseEnd：是否错误
}

// Event 单轮闭环对外事件流元素，TUI 据非零字段分派渲染。
type Event struct {
    Text string     // 文本增量（preamble 或最终答复）
    Tool *ToolEvent // 工具调用开始/结束
    Done bool        // 本轮结束
    Err  error       // 出错（不中断会话）
}

// Run 执行单轮闭环，返回事件 channel（调用方用 waitForEvent 泵消费）。
func (a *Agent) Run(ctx context.Context, conv *conversation.Conversation) <-chan Event
```

## 模块设计### internal/tool**职责：** 提供 6 个工具的统一抽象与执行；集中登记与导出；所有失败包成 `Result{IsError:true}` 而非 panic（F1/F2/F9/N4）。
**对外接口：** `Tool`、`Result`、`Registry`、`NewDefaultRegistry`。
**依赖：** 标准库（`os`、`os/exec`、`path/filepath`、`regexp`、`strings`、`context`）、`mewcode/internal/llm`（仅为 `Definitions()` 返回 `[]llm.ToolDefinition`）。
**关键实现点：**
- Schema 手写为 `map[string]any`：OpenAI 直接用整对象；Anthropic 由 llm 适配器取 `["properties"]`/`["required"]`。
- `read_file` 带行号、行/字节上限、`[truncated]` 标注（N5/AC2）。
- `edit_file` 唯一匹配语义 + 含计数的可区分错误（AC4）。
- `bash` 按 `runtime.GOOS` 选 shell：Windows 用 `exec.CommandContext(ctx,"cmd","/C",cmd)`，其余用 `exec.CommandContext(ctx,"sh","-c",cmd)`；超时由 agent 传入的子 ctx 控制；超时/非零退出均为结构化结果（AC5/N1）。
- `glob` 用 `filepath.WalkDir` 自实现 `**` 段匹配（`path.Match` 不支持 `**`）。`grep` 用 `WalkDir` + `regexp` 逐行扫，循环中检查 `ctx.Err()`。
- 空 `args`（OpenAI 可能给空串而非 `{}`）按 `{}` 处理，避免误报参数错误。

### internal/agent**职责：** 单轮闭环编排（F5/F6），保证 AC9 单轮上限；把 provider 的 `StreamEvent` 与工具执行翻译成统一 `Event` 流。
**对外接口：** `Agent`、`New`、`Run`、`Event`、`ToolEvent`、`Phase`。
**依赖：** `llm`、`tool`、`conversation`、`context`。
**Run 算法：**
1. `defs := registry.Definitions()`。
2. **请求#1**：`streamOnce(ctx, conv, defs, ch)` → 转发 `Text` 增量到 `ch`、累积完整 preamble 文本、收集 `ToolCalls`；出错则发 `Event{Err}` 后结束。
3. 若无 `ToolCalls`：`conv.AddAssistant(preamble)`，发 `Event{Done}`，结束（纯文本回合，与 ch02 等价）。
4. 有 `ToolCalls`：`conv.AddAssistantWithToolCalls(preamble, calls)`。
5. 顺序执行每个 call：发 `Event{Tool:{Name,Args,PhaseStart}}` → `tctx,cancel := context.WithTimeout(ctx, tool.DefaultTimeout)` → `r := registry.Execute(tctx, call.Name, call.Input)` → `cancel()` → 发 `Event{Tool:{Name,PhaseEnd,Result,IsError}}` → 收集 `llm.ToolResult{ToolCallID:call.ID, Content:r.Content, IsError:r.IsError}`。
6. `conv.AddToolResults(results)`。
7. **请求#2**：`streamOnce(...)` → 转发最终答复 `Text`、累积 final 文本；**忽略**其返回的任何 `ToolCalls`（单轮，AC9）。
8. `conv.AddAssistant(final)`，发 `Event{Done}`。
- `ctx` 取消（退出/Ctrl+C）时各阶段静默结束；工具执行经子 ctx 受 `DefaultTimeout` 约束（N1）。

### internal/llm（扩展）**职责：** 协议无关请求/响应抽象 + 两协议工具调用全流程（F3/F4/F6/F7）。
**anthropic.go 关键改动：**
- 请求构造加 `params.Tools = toAnthropicTools(tools)`：每项 `anthropic.ToolUnionParam{OfTool:&anthropic.ToolParam{Name, Description:anthropic.String(d.Description), InputSchema:anthropic.ToolInputSchemaParam{Properties:d.InputSchema["properties"], Required:toStrings(d.InputSchema["required"])}}}`。
- 流循环引入 `acc := anthropic.Message{}`，每次 `acc.Accumulate(stream.Current())`（检查返回 error）；文本增量仍上抛，`InputJSONDelta`/`ThinkingDelta` 不上抛（由 Accumulate 缓冲/丢弃）。
- 流结束后若 `acc.StopReason == anthropic.StopReasonToolUse`：遍历 `acc.Content`，对 `ToolUseBlock` 收集 `ToolCall{ID,Name,Input}`，经 `StreamEvent{ToolCalls}` 上抛。
- `toAnthropicMessages` 扩展：assistant 回合若有 `ToolCalls`，用 `anthropic.NewToolUseBlock(id,input,name)`（可与 `NewTextBlock` 文本块并存）；`RoleTool` 回合把每个 `ToolResult` 用 `anthropic.NewToolResultBlock(toolUseID,content,isError)` 拼进**一条 user 消息**。

**openai.go 关键改动：**
- 请求构造加 `params.Tools`：每项 `openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{Name, Description:openai.String(d.Description), Parameters:shared.FunctionParameters(d.InputSchema)})`。
- 流循环用 `acc := openai.ChatCompletionAccumulator{}`，每次 `acc.AddChunk(evt)`；`Delta.Content` 仍上抛。
- 流结束后读 `acc.Choices[0].Message.ToolCalls`（不依赖 `JustFinishedToolCall`，因其在多工具下不可靠）；非空则按 index 组 `ToolCall{ID,Name,Input:json.RawMessage(arguments)}`，经 `StreamEvent{ToolCalls}` 上抛。判定可同时参考 `FinishReason=="tool_calls"` 与 `acc` 是否含工具调用（兼容端点兜底）。
- `toOpenAIMessages` 扩展：assistant 回合若有 `ToolCalls`，**手工**构造 `ChatCompletionAssistantMessageParam{Content, ToolCalls:[]ChatCompletionMessageToolCallUnionParam{{OfFunction:&ChatCompletionMessageFunctionToolCallParam{ID, Function:{Name, Arguments:string(call.Input)}}}}}`（`openai.AssistantMessage(string)` 助手不携带工具调用，不能用）；`RoleTool` 回合每个 `ToolResult` 发一条 `openai.ToolMessage(content, toolCallID)`。

### internal/conversation（扩展）

```go
func (c *Conversation) AddAssistantWithToolCalls(text string, calls []llm.ToolCall) // assistant 工具调用回合
func (c *Conversation) AddToolResults(results []llm.ToolResult)                       // RoleTool 结果回合
```
保留 `AddUser`/`AddAssistant`/`Messages`/`Len` 不变。

### internal/tui（扩展）**职责：** 渲染 `agent.Event`（文本/工具行/结果摘要/错误/结束），保持非阻塞（N2）。
- `New(providers, version string, registry *tool.Registry)`：存 `registry`。
- `Model` 新增：`events <-chan agent.Event`（替换原 `<-chan llm.StreamEvent`）、`curTool *toolDisplay`（执行中指示：name/args，非空即渲染执行行）。
- `submit`：`conv.AddUser(text)` 后 `m.events = agent.New(m.provider, m.registry).Run(m.ctx, m.conv)`（不再直接调 `provider.Stream`）。
- `waitForEvent` 改泵 `agent.Event`；`updateStreaming` 分派：
  - `Text != ""`：追加 `curReply`，重挂泵。
  - `Tool.Phase==PhaseStart`：若 `curReply` 非空，先把 preamble 作为 assistant 块提交 scrollback 并清空 `curReply`；置 `curTool`；重挂泵。
  - `Tool.Phase==PhaseEnd`：`tea.Sequence(tea.Println(toolLine(name,args)), tea.Println(toolResultSummary(result,isError)))` 顺序提交；清 `curTool`；重挂泵。
  - `Done`：把 `curReply`（最终答复）经 `renderMarkdown` 提交 scrollback；`finishTurn`。
  - `Err`：`tea.Println(errorBlock(err))`；`finishTurn`。
- `view.go` 新增：`toolLine(name,args string) string`（青/绿 `●` + `name(args)`）、`toolResultSummary(result string,isError bool) string`（缩进 `  ⎿ `、灰/红、UI 截断 ~8 行）。`View()` 在 `curTool != nil` 时渲染「`● name(args)` + spinner Running…」，否则沿用「Imagining… (Ns)」。
- 多个 `tea.Println` 同一 Update 必须用 `tea.Sequence`（`tea.Batch` 不保证顺序）。

## 模块交互

```
用户提交
  └─ tui.submit: conv.AddUser(text); a := agent.New(provider, registry); events = a.Run(ctx, conv)
       └─ agent.Run (goroutine):
            ├─ 请求#1: provider.Stream(ctx, conv.Messages(), registry.Definitions())
            │     └─ 适配器: 注入 Tools → 流式拼接 → StreamEvent{Text…} / StreamEvent{ToolCalls}
            │     → agent 转发 Event{Text}（preamble），收集 calls
            ├─ 无 calls → conv.AddAssistant(preamble); Event{Done}
            └─ 有 calls:
                 ├─ conv.AddAssistantWithToolCalls(preamble, calls)
                 ├─ for call: Event{Tool:Start} → registry.Execute(子ctx超时) → Event{Tool:End}
                 ├─ conv.AddToolResults(results)
                 ├─ 请求#2: provider.Stream(ctx, conv.Messages(), defs) → Event{Text}（最终答复）
                 │     （适配器把 conv 里的 tool_use/tool_result 回合映射为各自线格式）
                 └─ conv.AddAssistant(final); Event{Done}
  └─ tui.updateStreaming: 按 Event 类型渲染（curReply 动态区 / tea.Println 进 scrollback）
```

并发：`conv` 在每个时刻只被一个 goroutine 触碰——`submit` 在交给 `Run` 前 `AddUser`，之后不再触碰；`Run` 的 goroutine 独占后续所有 `conv` 变更。`Messages()` 返回副本。TUI 仅按事件渲染，`curReply` 为自身显示缓冲，与 `conv` 互不干扰（N2）。

## 文件组织

```
mewcode/
├── cmd/mewcode/main.go              — 修改：tool.NewDefaultRegistry() 注入 tui.New
├── internal/
│   ├── llm/
│   │   ├── provider.go              — 修改：新增 ToolCall/ToolResult/ToolDefinition/RoleTool；扩展 Message/StreamEvent；Stream 加 tools 参数
│   │   ├── anthropic.go             — 修改：toAnthropicTools；acc.Accumulate 解析；StopReason 上抛 ToolCalls；toAnthropicMessages 支持 tool_use/tool_result
│   │   └── openai.go                — 修改：toOpenAITools；acc.AddChunk 解析；finish_reason 上抛 ToolCalls；toOpenAIMessages 支持 assistant.tool_calls/tool 消息
│   ├── tool/                        — 新建
│   │   ├── tool.go                  — Tool 接口、Result、截断工具函数
│   │   ├── registry.go              — Registry、Register/Get/Definitions/Execute、NewDefaultRegistry、DefaultTimeout
│   │   ├── read_file.go / write_file.go / edit_file.go / bash.go / glob.go / grep.go
│   │   └── tool_test.go             — 注册中心 + 各工具单测
│   ├── agent/                       — 新建
│   │   ├── agent.go                 — Agent、Event、ToolEvent、Phase、New、Run、streamOnce
│   │   └── agent_test.go            — 单轮闭环（fake provider）：AC8 链路、AC9 单轮
│   ├── conversation/
│   │   └── conversation.go          — 修改：AddAssistantWithToolCalls、AddToolResults
│   ├── prompt/
│   │   └── prompt.go                — 修改：SystemPrompt 增 Agent 角色与工具约定
│   └── tui/
│       ├── tui.go                   — 修改：New 接 registry；Model 增 events(agent.Event)/curTool 字段
│       ├── stream.go                — 修改：submit 走 agent.Run；waitForEvent 泵 agent.Event；updateStreaming 分派工具事件
│       └── view.go                  — 修改：toolLine/toolResultSummary；View 执行指示
```

## 技术决策

| 决策点 | 选择 | 理由 |
|--------|------|------|
| 工具调用循环放哪 | 新建 `internal/agent` 包，TUI 退化为渲染器 | 循环（请求#1→执行→请求#2）无法塞进 ch02 的一次性 `updateStreaming`；独立包可无 UI 单测（AC8/AC9），只依赖 llm+tool+conversation，不泄漏 SDK 类型。命名 `agent` 而非 `runner`：概念即 Agent，本章恰为单轮。 |
| 是否用 SDK 的 Beta tool-runner | 不用，坚持 stable streaming + 手动单轮 | `NewToolRunner.RunToCompletion` 自动连环到完成，违反 F6/AC9；且引入 Beta* 类型与 ch02 stable 代码不一致。 |
| 工具定义传入哪一层 | `Provider.Stream` 第三参数 `[]ToolDefinition` | 两 SDK 都把 tools 放 per-request params；续答仍需带；保持 Provider 无状态。 |
| 工具参数 Schema 生成 | 每工具手写 `map[string]any` | OpenAI `FunctionParameters=map[string]any` 直接用；Anthropic 取 `["properties"]`/`["required"]`。6 个固定工具手写最直白，描述对模型可读性最关键；不引入 invopop 反射（带 $id/$defs 还要剥离）。 |
| 流式工具参数拼接 | Anthropic 用 `acc.Accumulate`；OpenAI 用 `acc.AddChunk` 后读 `Message.ToolCalls` | SDK 自带累加器处理分片，避免手写 PartialJSON/按-index 拼接边界；OpenAI 不依赖 `JustFinishedToolCall`（多工具下不可靠）。 |
| Glob/Grep 实现 | 纯 Go（`WalkDir`+自实现 `**`/`regexp`） | 零依赖、跨平台（Windows 无 grep/rg）；spec 要求保持简单、不引入配置。 |
| Bash 实现与超时 | 按 `runtime.GOOS` 选 shell（Unix `sh -c` / Windows `cmd /C`）+ 30s 子 ctx 超时 | `sh -c`/`cmd /C` 各自支持管道/重定向；CommandContext 超时杀进程。30s 内置不可配（spec：超时不配置化）。跨平台兼容。 |
| 工具失败的表达 | `Execute` 返回 `Result{Content,IsError}`，从不返回 error | F9/N4：所有失败包成结构化结果回灌，程序不崩，上层无需区分 error 路径。 |
| 工具结果在 Message 的形态 | 平铺 slice（assistant 加 `ToolCalls`，`RoleTool` 加 `ToolResults`） | 两 SDK 工具语义本就是 id 关联的 tool_use/tool_result 列表；通用 content-block 联合属过度设计（本章结果均文本）。适配器吸收差异（Anthropic 结果进 user 消息、OpenAI 用 tool 角色）。 |
| UI 截断 vs 回灌截断 | 两者分离：UI 摘要 ~8 行；回灌为工具级上限（read 2000 行 / bash 30000 字符 等） | AC11/N5 要界面截断，但模型需较完整内容；尾部统一加 `[truncated]` 标注。 |
| 续答请求是否带 tools | 带，但忽略其返回的工具调用 | 与真实协议一致（OpenAI assistant+tool 后不带 tools 也可，但带更稳）；F6/AC9 由 agent 不再触发执行来保证单轮。 |
| thinking 与工具组合 | 历史含工具交互的请求（续答）不启用 thinking | Anthropic 在 thinking 启用时要求回灌带 tool_use 的 assistant 回合附原 thinking 块（含 signature），而本章按 spec 丢弃 thinking 增量、不留签名；故对这类请求关闭 thinking 以避免 400。 |
| 空最终答复 | 续答为空（仅请求工具被丢弃 / 空完成）时用单轮提示占位并推给 UI | 空 assistant 回合会破坏下一轮请求（Anthropic 要求非空内容 + 角色交替）；占位提示同时满足 AC9 的"单轮上限提示"。 |
| 空参数归一 | OpenAI 侧空 arguments 归一为 "{}" | 无参工具的 arguments 可能为空串，回灌时须是合法 JSON，否则严格兼容端点对 "arguments":"" 返回 400。 |
| grep 超长行 | 显式标注未完整搜索 | bufio.Scanner 遇 >1MB 行会静默中止并丢后续行；标注避免假"无命中"误导模型。 |
| scrollback 顺序提交 | 多个 `tea.Println` 用 `tea.Sequence` | `tea.Batch` 并发无序，会打乱工具行/结果/最终答复的顺序。 |
| 工具命名 | `read_file`/`write_file`/`edit_file`/`bash`/`glob`/`grep` | 符合 OpenAI 函数名规则（`a-zA-Z0-9_-`）与 Claude Code 习惯；TUI 工具行显示 `● name(关键参数)`。 |