# 工具系统 Tasks

> 基于已批准的 spec.md + plan.md。任务有序，每步留绿编译。验证一律「先跑命令看输出，再下结论」。

## 文件清单

| 操作 | 文件 | 职责 |
|------|------|------|
| 修改 | `internal/llm/provider.go` | 新增 ToolCall/ToolResult/ToolDefinition/RoleTool；扩展 Message/StreamEvent；Stream 加 tools 参数 |
| 修改 | `internal/llm/anthropic.go` | 注入 Tools、acc.Accumulate 解析工具调用、tool_use/tool_result 回灌 |
| 修改 | `internal/llm/openai.go` | 注入 Tools、acc.AddChunk 解析、assistant.tool_calls/tool 消息回灌 |
| 新建 | `internal/tool/tool.go` | Tool 接口、Result、截断工具函数 |
| 新建 | `internal/tool/registry.go` | Registry、Register/Get/Definitions/Execute、NewDefaultRegistry、DefaultTimeout |
| 新建 | `internal/tool/{read_file,write_file,edit_file,bash,glob,grep}.go` | 6 个核心工具 |
| 新建 | `internal/tool/tool_test.go` | 注册中心 + 各工具单测 |
| 新建 | `internal/agent/agent.go` | Agent、Event、ToolEvent、Phase、Run（单轮闭环） |
| 新建 | `internal/agent/agent_test.go` | fake provider 驱动单轮闭环（AC8/AC9） |
| 修改 | `internal/conversation/conversation.go` | AddAssistantWithToolCalls、AddToolResults |
| 修改 | `internal/prompt/prompt.go` | SystemPrompt 增 Agent 角色与工具约定 |
| 修改 | `internal/tui/{tui,stream,view}.go` | 接入 agent.Run、工具事件渲染、工具行/执行指示 |
| 修改 | `cmd/mewcode/main.go` | 构造 NewDefaultRegistry 注入 tui.New |

## T1: 扩展 llm 协议无关类型**文件：** `internal/llm/provider.go`
**依赖：** 无
**步骤：**
1. 新增 `import "encoding/json"`。
2. 增加常量 `RoleTool = "tool"`。
3. 新增类型 `ToolCall{ID, Name string; Input json.RawMessage}`、`ToolResult{ToolCallID, Content string; IsError bool}`、`ToolDefinition{Name, Description string; InputSchema map[string]any}`（各带中文注释）。
4. 给 `Message` 增字段 `ToolCalls []ToolCall`、`ToolResults []ToolResult`（纯增量，不破坏现有构造）。
5. 给 `StreamEvent` 增字段 `ToolCalls []ToolCall`，更新其文档注释为四态语义说明。

**验证：** `go build ./internal/llm/...` 通过（此步只加字段/类型，不改 Stream 签名，向后兼容）。

## T2: tool 接口与注册中心骨架**文件：** `internal/tool/tool.go`、`internal/tool/registry.go`
**依赖：** T1
**步骤：**
1. `tool.go`：定义 `Result{Content string; IsError bool}`、`Tool` 接口（`Name/Description/Parameters/Execute`）、一个 `truncate(s string, maxLines, maxChars int) string` 工具函数（超出尾部加 `[truncated]`）。
2. `registry.go`：定义 `Registry{order []string; tools map[string]Tool}`、`Register`、`Get`、`Definitions() []llm.ToolDefinition`（按 order 把每工具 Name/Description/Parameters 组成 `llm.ToolDefinition`）、`Execute(ctx,name,args)`（`Get` 未命中返回 `Result{IsError:true, Content:"未知工具: "+name}`，命中则调 `t.Execute`）、常量 `DefaultTimeout = 30*time.Second`。**暂不写** `NewDefaultRegistry`。

**验证：** `go build ./internal/tool/...` 通过。

## T3: read_file 工具**文件：** `internal/tool/read_file.go`
**依赖：** T2
**步骤：**
1. 定义 `readFileArgs{Path string `json:"path"`}` 与 `readFileTool` 实现 `Tool`。
2. `Parameters()` 返回手写 schema：`type:object`、`properties.path{type:string, description:"要读取的文件路径"}`、`required:["path"]`。
3. `Execute`：空 args 当 `{}`；`os.ReadFile`；目录/不存在/不可读 → `Result{IsError:true}`；成功按行加行号（`%6d\t` 风格），经 `truncate` 限 2000 行 / 256KB。

**验证：** `go test ./internal/tool/ -run ReadFile`（在 T9 补测后跑）或临时 `go build`；手测读本文件出现行号、读不存在文件得 IsError。

## T4: write_file 工具**文件：** `internal/tool/write_file.go`
**依赖：** T2
**步骤：**
1. `writeFileArgs{Path, Content string}`、`writeFileTool`。
2. `Parameters()`：`path`、`content` 均必填。
3. `Execute`：`os.MkdirAll(filepath.Dir(path),0o755)` 后 `os.WriteFile`（覆盖）；成功返回 `已写入 <path>（N 字节）`；失败 IsError。

**验证：** `go build ./internal/tool/...`；T9 后单测写嵌套路径检查磁盘。

## T5: edit_file 工具**文件：** `internal/tool/edit_file.go`
**依赖：** T2
**步骤：**
1. `editFileArgs{Path, OldString, NewString string}`、`editFileTool`。
2. `Parameters()`：三字段必填，描述说明唯一匹配语义。
3. `Execute`：读文件失败 IsError；`n := strings.Count(content, old)`；`n==0`→`Result{IsError:true,Content:"未找到匹配的内容"}`；`n>1`→`Result{IsError:true,Content:fmt.Sprintf("匹配到 %d 处，old_string 不唯一，请提供更长上下文使其唯一", n)}`；`n==1`→`strings.Replace(content,old,new,1)` 写回，返回成功。

**验证：** `go build`；T9 后单测覆盖 0/1/多三情形。

## T6: bash 工具**文件：** `internal/tool/bash.go`
**依赖：** T2
**步骤：**
1. `bashArgs{Command string}`、`bashTool`。
2. `Parameters()`：`command` 必填。
3. `Execute`：按 `runtime.GOOS` 选 shell——Windows `exec.CommandContext(ctx,"cmd","/C",cmd)`，其余 `exec.CommandContext(ctx,"sh","-c",cmd)`；捕获合并 stdout/stderr 与 exit code；`ctx.Err()==context.DeadlineExceeded` → `Result{IsError:true,Content:"命令超时"}`；否则返回含 stdout/stderr/exit_code 的文本（经 `truncate` ~30000 字符），非零退出不设 IsError（按结果回灌让模型判断）。

**验证：** `go build`；T9 后单测 `echo hi` 与超时命令（用极短 DefaultTimeout 或子 ctx 注入）。

## T7: glob 工具**文件：** `internal/tool/glob.go`
**依赖：** T2
**步骤：**
1. `globArgs{Pattern string; Path string}`、`globTool`。
2. `Parameters()`：`pattern` 必填，`path` 可选（默认 `.`）。
3. `Execute`：`filepath.WalkDir(root)`，对每个文件相对路径做支持 `**` 的段匹配（自实现 `matchGlob(pattern, relPath)`；`**` 跨任意层级目录）；收集匹配（≤100，排序）；循环检查 `ctx.Err()`；无匹配返回「无匹配」（非 IsError）。

**验证：** `go build`；T9 后单测 `**/*.go` 能命中 `internal/...` 下文件。

## T8: grep 工具**文件：** `internal/tool/grep.go`
**依赖：** T2
**步骤：**
1. `grepArgs{Pattern string; Path string; Glob string}`、`grepTool`。
2. `Parameters()`：`pattern` 必填（RE2 正则，描述注明），`path`/`glob` 可选。
3. `Execute`：`regexp.Compile` 失败 IsError；`WalkDir` 遍历（`glob` 非空时按文件名过滤），逐行匹配，收集 `file:line:content`（≤100，超出尾部标注）；循环检查 `ctx.Err()`；无命中返回「无命中」（非 IsError）。

**验证：** `go build`；T9 后单测搜一个已知关键字命中。

## T9: NewDefaultRegistry 与 tool 单测**文件：** `internal/tool/registry.go`、`internal/tool/tool_test.go`
**依赖：** T3–T8
**步骤：**
1. `registry.go` 增 `NewDefaultRegistry()`：依次 `Register` 6 个工具，返回 `*Registry`。
2. `tool_test.go`：测 `Definitions()` 返回恰好 6 条且名称有序（AC1）；`read_file` 存在/不存在；`write_file` 新建 + 嵌套路径检查磁盘；`edit_file` 0/1/多三情形错误可区分；`bash` echo 与超时；`glob` `**/*.go`；`grep` 关键字（用 `t.TempDir()` 造数据）。

**验证：** `go test ./internal/tool/...` 全通过；输出确认 6 条定义、edit 三情形文案不同。

## T10: Provider.Stream 加 tools 参数（注入定义，暂不解析）**文件：** `internal/llm/provider.go`、`internal/llm/anthropic.go`、`internal/llm/openai.go`、`internal/tui/stream.go`
**依赖：** T1
**步骤：**
1. `provider.go`：`Provider.Stream` 签名改为 `Stream(ctx, msgs []Message, tools []ToolDefinition) <-chan StreamEvent`，更新接口注释。
2. `anthropic.go`：`Stream` 加 `tools` 形参；新增 `toAnthropicTools(tools)` 并设 `params.Tools`；流解析暂不变。
3. `openai.go`：同理，新增 `toOpenAITools(tools)` 设 `params.Tools`。
4. `tui/stream.go`：`submit` 中 `provider.Stream(m.ctx, m.conv.Messages())` 暂改为传 `nil` 第三参数（T16 会替换为 agent.Run）。

**验证：** `go build ./...` 通过；`go run ./cmd/mewcode` 发一条纯文本仍正常（工具定义已随请求发送，模型未必调用）。

## T11: anthropic 适配器解析工具调用 + 回灌**文件：** `internal/llm/anthropic.go`
**依赖：** T10
**步骤：**
1. 流循环前 `acc := anthropic.Message{}`；循环内 `if err := acc.Accumulate(stream.Current()); err != nil { ch<-StreamEvent{Err:err}; return }`；保留文本增量上抛（对 `cbd.Delta.AsAny()` 做类型 switch：`TextDelta` 上抛，`ThinkingDelta`/`InputJSONDelta` 不上抛）。
2. 流正常结束后：若 `acc.StopReason == anthropic.StopReasonToolUse`，遍历 `acc.Content`，对 `ToolUseBlock` 收集 `ToolCall{ID,Name,Input}`，`ch<-StreamEvent{ToolCalls:calls}`；随后照常 `StreamEvent{Done}`。
3. `toAnthropicMessages` 扩展：assistant 有 `ToolCalls` 时除文本块外 append `NewToolUseBlock(id,input,name)`；`RoleTool` 消息把每个 `ToolResult` 用 `NewToolResultBlock(toolUseID,content,isError)` 拼成一条 `NewUserMessage`。

**验证：** `go build ./internal/llm/...`；`go vet ./internal/llm/...` 无告警（类型断言/字段名正确）。

## T12: openai 适配器解析工具调用 + 回灌**文件：** `internal/llm/openai.go`
**依赖：** T10
**步骤：**
1. 流循环用 `acc := openai.ChatCompletionAccumulator{}`，循环内 `acc.AddChunk(evt)`；`Delta.Content` 仍上抛。
2. 流结束后读 `acc.Choices[0].Message.ToolCalls`（非空即组 `ToolCall{ID,Name,Input:json.RawMessage(fn.Arguments)}`），`ch<-StreamEvent{ToolCalls:calls}`；再 `StreamEvent{Done}`。判定可结合 `FinishReason=="tool_calls"` 与 acc 是否含工具调用兜底。
3. `toOpenAIMessages` 扩展：assistant 有 `ToolCalls` 时手工构造 `ChatCompletionAssistantMessageParam{Content, ToolCalls:[]...{OfFunction:&{ID,Function:{Name,Arguments:string(call.Input)}}}}`（经 `ChatCompletionMessageParamUnion.OfAssistant`）；`RoleTool` 消息每个 `ToolResult` 发 `openai.ToolMessage(content, toolCallID)`。

**验证：** `go build ./internal/llm/...`；`go vet ./internal/llm/...` 无告警。

## T13: conversation 扩展**文件：** `internal/conversation/conversation.go`
**依赖：** T1
**步骤：**
1. 新增 `AddAssistantWithToolCalls(text string, calls []llm.ToolCall)`：append `Message{Role:RoleAssistant, Content:text, ToolCalls:calls}`。
2. 新增 `AddToolResults(results []llm.ToolResult)`：append `Message{Role:RoleTool, ToolResults:results}`。
3. 保留现有方法不变。

**验证：** `go test ./internal/conversation/...` 通过（补一条断言新方法落库的小测）。

## T14: agent 单轮闭环**文件：** `internal/agent/agent.go`、`internal/agent/agent_test.go`
**依赖：** T9, T11, T12, T13
**步骤：**
1. `agent.go`：定义 `Agent`、`New`、`Phase`(PhaseStart/PhaseEnd)、`ToolEvent`、`Event`、`Run`（按 plan 的 Run 算法实现 streamOnce 转发文本+收集 ToolCalls；无工具→直接 Done；有工具→记录回合、顺序执行带 `context.WithTimeout(ctx, tool.DefaultTimeout)`、回灌、请求#2、忽略二轮工具、Done）。`Args` 预览取 `Input` 简短串。
2. `agent_test.go`：用实现 `llm.Provider` 的 fake，编排两种脚本——(a) 请求#1 返回 1 个工具调用、请求#2 返回文本 → 断言 Event 序列含 Tool Start/End 与最终 Text、conv 末尾为 assistant 文本（AC8）；(b) 请求#1 返回工具、请求#2 仍返回工具 → 断言只执行一轮、不再触发执行（AC9）。

**验证：** `go test ./internal/agent/...` 全通过；输出确认单轮上限生效。

## T15: prompt 系统提示词扩展**文件：** `internal/prompt/prompt.go`
**依赖：** 无
**步骤：**
1. 扩写 `SystemPrompt`：说明 MewCode 是能使用工具的 Agent，可读写改文件、执行命令、查找/搜索代码；需要信息或操作时调用相应工具，拿到结果后给出简洁答复。

**验证：** `go build ./internal/prompt/...`；`go test ./...` 不回归。

## T16: tui 接入 agent + 工具行渲染**文件：** `internal/tui/tui.go`、`internal/tui/stream.go`、`internal/tui/view.go`
**依赖：** T14, T15
**步骤：**
1. `tui.go`：`New(providers, version, registry)` 存 `registry *tool.Registry`；`Model` 把 `events` 改为 `<-chan agent.Event`，新增 `curTool *toolDisplay{name,args string}`。
2. `stream.go`：`streamMsg` 改包 `agent.Event`；`waitForEvent(<-chan agent.Event)`；`submit` 用 `agent.New(m.provider,m.registry).Run(m.ctx,m.conv)`（移除 T10 的临时 nil 调用）；`updateStreaming` 分派 Text/Tool(Start/End)/Done/Err（按 plan：Start 提交 preamble+置 curTool；End 用 `tea.Sequence` 提交工具行+结果摘要+清 curTool；Done 渲染最终 markdown）。
3. `view.go`：新增 `toolLine(name,args)`、`toolResultSummary(result,isError)`（缩进、灰/红、UI 截断 ~8 行）、工具头样式；`View()` 在 `curTool!=nil` 时渲染执行行（`● name(args)` + spinner Running…），否则沿用 Imagining…。

**验证：** `go build ./...`；`go vet ./...` 无告警。

## T17: main 接线**文件：** `cmd/mewcode/main.go`
**依赖：** T16
**步骤：**
1. 构造 `reg := tool.NewDefaultRegistry()`；`tui.New(cfg.Providers, version, reg)`。

**验证：** `go build ./...` 通过；`go run ./cmd/mewcode` 启动正常进入对话。

## T18: 全量验证与端到端冒烟**文件：** 无（验证）
**依赖：** T1–T17
**步骤：**
1. `gofmt -l .` / `goimports`；`go vet ./...`；`go test ./...`。
2. 用当前 `.mewcode/config.yaml`（openai 兼容端点）跑：问「读 docs/ch03/spec.md 并用一句话总结」→ 观察工具行 `● read_file(...)` + 结果摘要 + 最终答复（AC8/AC11）。
3. 触发各错误：读不存在文件、edit 匹配不到、bash 非零退出 → 错误结构化回灌、程序不退出（AC12）。
4. （可选）若有 anthropic 配置，重复步骤 2 验证跨协议一致（AC10）。

**验证：** 全部命令通过、端到端链路与错误恢复符合预期。

## 执行顺序

```
T1 ─┬─ T2 ─┬─ T3 ─┐
    │       ├─ T4 ─┤
    │       ├─ T5 ─┼─ T9 ─┐
    │       ├─ T6 ─┤      │
    │       ├─ T7 ─┤      │
    │       └─ T8 ─┘      │
    ├─ T10 ─┬─ T11 ──────┤
    │        └─ T12 ─────┤
    ├─ T13 ──────────────┤
    └─ T15               │
                T9,T11,T12,T13 ─→ T14 ─→ T16 ─→ T17 ─→ T18
                                   T15 ──┘
```