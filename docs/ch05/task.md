# 系统提示工程化 Tasks## 文件清单

| 操作 | 文件 | 职责 |
|------|------|------|
| 新建 | `internal/prompt/modules.go` | `Module` 类型;`FixedModules()` 七固定模块、`OptionalModules()` 三空槽的内容常量 |
| 改   | `internal/prompt/prompt.go` | `AssembleSystem`/`BuildSystemPrompt`;删旧 `SystemPrompt`/`PlanModeReminder`/`ExecuteDirective` 常量(迁移);保留 banner |
| 新建 | `internal/prompt/environment.go` | `Environment` 结构、`GatherEnvironment`、`Render` |
| 新建 | `internal/prompt/reminder.go` | `SystemReminder` 标签包裹、`PlanReminder(full)`、规划提醒完整/精简常量、`ExecuteDirective` |
| 新建 | `internal/prompt/prompt_test.go` | 装配顺序、跳空槽、N1 确定性、双重强化文本断言 |
| 改   | `internal/tool/edit_file.go` | `Description` 补「编辑前先 read_file」 |
| 改   | `internal/tool/bash.go` | `Description` 补「优先用专用工具而非 bash 拼凑」 |
| 改   | `internal/llm/provider.go` | `System`/`Request` 结构;`Usage` 加缓存字段;`Provider.Stream(ctx, Request)`;删 `effectiveSystem` 及 prompt import |
| 改   | `internal/llm/anthropic.go` | 两块 System(稳定块打断点 + env 块)、缓存用量解析、reminder 并入末条 user |
| 改   | `internal/llm/openai.go` | 单条 system(Stable+env 拼接)、`cached_tokens` 解析、reminder 追加尾部 user |
| 改   | `internal/agent/agent.go` | `New(+version)`;`Run` 采集环境/装配系统;按轮次 reminder;缓存用量透传 |
| 改   | `internal/agent/agent_test.go` | 断言 Request 装配(System 两段、规划按轮次 reminder)、缓存用量透传;修既有用例适配新签名 |
| 改   | `internal/tui/stream.go` | `agent.New` 传 `m.version` |
| 改   | `cmd/smoke/main.go` | 打印缓存用量;`agent.New` 传版本字面量 |

---

## T1: prompt 模块化装配**文件:** `internal/prompt/modules.go`、`internal/prompt/prompt.go`
**依赖:** 无
**步骤:**
1. 在 `modules.go` 定义 `Module struct { Name string; Priority int; Content string }`。
2. `FixedModules() []Module` 返回七个固定模块,内容内置(中英按现有 SystemPrompt 风格,英文为主):
   - 身份(10):MewCode 是终端编码 Agent。
   - 系统约束(20):操作边界——在工作目录约定内行事、不外泄密钥、对破坏性操作谨慎。
   - 任务模式(30):ReAct——多步推进、读后再改、完成才给终答。
   - 动作执行(40):何时调工具、连续只读可并发、有副作用谨慎。
   - 工具使用(50):**优先用 read_file/glob/grep 而非 bash 拼凑;编辑文件前必先 read_file**(F5)。
   - 语气风格(60):简洁、直接、不奉承。
   - 文本输出(70):必要时用 Markdown(代码块/列表),终答精炼。
3. `OptionalModules() []Module` 返回三个空槽:自定义指令(80)、已激活 Skill(90)、长期记忆(100),`Content` 均为 `""`。
4. 在 `prompt.go`:
   - `AssembleSystem(mods []Module) string`:按 `Priority` 升序稳定排序、**跳过 `Content==""`**、以 `"\n\n"` 连接。
   - `BuildSystemPrompt() string`:`AssembleSystem(append(FixedModules(), OptionalModules()...))`。
   - 删除旧 `SystemPrompt`、`PlanModeReminder` 常量(内容迁至模块/reminder);`ExecuteDirective` 迁至 `reminder.go`。保留 `CatBanner`/`ReadyHint`/`RenderBanner`。

**验证:** `go build ./internal/prompt/...` 通过;临时 `fmt.Println(BuildSystemPrompt())` 观察七模块按序、空槽不留空行。

## T2: 环境采集与渲染**文件:** `internal/prompt/environment.go`
**依赖:** 无
**步骤:**
1. 定义 `Environment struct { WorkingDir, Platform, Date, GitStatus, Version, Model string }`。
2. `GatherEnvironment(version, model string) Environment`:
   - `WorkingDir = os.Getwd()`(失败留空)、`Platform = runtime.GOOS`、`Date = time.Now().Format("2006-01-02")`。
   - `GitStatus`:用 `exec.CommandContext` 加 ~2s 超时跑 `git status --porcelain`;非零退出/非 git 目录/超时 → `""`;有输出则取摘要(如「N 个文件改动」或前几行)。
   - `Version=version`、`Model=model`。**不读任何环境变量**(N5)。
3. `(e Environment) Render() string`:渲染为「环境信息」段——逐行 `Key: Value`,空值项省略。

**验证:** 单测在临时非 git 目录 `GatherEnvironment` 得 `GitStatus==""` 且不 panic;`Render()` 含 cwd/platform/date。

## T3: 补充消息与规划提醒构造**文件:** `internal/prompt/reminder.go`
**依赖:** 无
**步骤:**
1. `SystemReminder(body string) string`:返回 `"<system-reminder>\n" + body + "\n</system-reminder>"`。
2. 规划提醒常量:`planReminderFull`(完整版,含「仅可用只读工具调研、产出分步计划、等 /do 批准」)、`planReminderConcise`(精简版,一两句)。
3. `PlanReminder(full bool) string`:`SystemReminder(full?planReminderFull:planReminderConcise)`。
4. `ExecuteDirective`(从 prompt.go 迁来):`/do` 注入的用户消息文案。

**验证:** 单测断言 `PlanReminder(true)` 含 `<system-reminder>` 与完整文案;`PlanReminder(false)` 用精简文案。

## T4: prompt 单测**文件:** `internal/prompt/prompt_test.go`
**依赖:** T1, T2, T3
**步骤:**
1. 装配顺序:断言 `BuildSystemPrompt()` 中身份段出现在工具使用段之前;模块以空行分隔。
2. 跳空槽:在 `AssembleSystem` 传入含空 `Content` 的模块,断言其不出现、不产生连续多空行。
3. **N1 确定性**:连续两次 `BuildSystemPrompt()` 结果 `==`。
4. **F5 双重强化**:`BuildSystemPrompt()` 文本含「编辑」与「先读」之意、含「优先」与专用工具名。
5. 环境与 reminder:见 T2/T3 验证。

**验证:** `go test ./internal/prompt/...` 通过。

## T5: 工具描述双重强化**文件:** `internal/tool/edit_file.go`、`internal/tool/bash.go`
**依赖:** 无
**步骤:**
1. `edit_file.Description` 末补:「编辑前请先用 read_file 读取目标文件,确认 old_string 唯一。」
2. `bash.Description` 末补:「读文件、找文件、搜内容请优先用 read_file/glob/grep,不要用 bash 拼凑。」
3. 不改 schema、不改 Execute 行为。

**验证:** `go build ./internal/tool/...`;`go test ./internal/tool/...` 仍通过。

## T6: llm 接口改造**文件:** `internal/llm/provider.go`
**依赖:** 无(但 T7/T8/T9 依赖本任务)
**步骤:**
1. 新增 `System struct { Stable, Environment string }`、`Request struct { Messages []Message; Tools []ToolDefinition; System System; Reminder string }`。
2. `Usage` 加 `CacheWrite int64`、`CacheRead int64`。
3. `Provider.Stream` 改为 `Stream(ctx context.Context, req Request) <-chan StreamEvent`;更新接口文档注释。
4. 删除 `effectiveSystem` 函数与对 `mewcode/internal/prompt` 的 import。
5. `trySend`/`New` 保持。

**验证:** `go build ./internal/llm/...` 报 anthropic.go/openai.go 未适配的编译错(预期),T7/T8 修复。

## T7: Anthropic 适配缓存通道 + reminder**文件:** `internal/llm/anthropic.go`
**依赖:** T6
**步骤:**
1. `Stream(ctx, req Request)`:
   - 构造 `System []anthropic.TextBlockParam`:`req.System.Stable!=""` → `{Text:Stable, CacheControl: anthropic.NewCacheControlEphemeralParam()}`（**必须用构造器**，空字面量会被 omitzero 丢弃）;`req.System.Environment!=""` → `{Text:Environment}`(无 CacheControl)。
   - `Messages = toAnthropicMessages(req.Messages)`;`req.Reminder!=""` → 调 `appendReminderAnthropic(msgs, req.Reminder)`:把 `anthropic.NewTextBlock(reminder)` 追加到**最后一条消息**的 content;末条非 user 时新起一条 user 消息。
   - `Tools = toAnthropicTools(req.Tools)`(不另打断点)。
   - thinking 逻辑沿用(`assistantUsedTools(req.Messages)`)。
2. Usage 解析:`StreamEvent{Usage:&Usage{InputTokens:acc.Usage.InputTokens, OutputTokens:acc.Usage.OutputTokens, CacheWrite:acc.Usage.CacheCreationInputTokens, CacheRead:acc.Usage.CacheReadInputTokens}}`。

**验证:** `go build ./internal/llm/...` 通过(配合 T8);单测/烟囱 anthropic 跑两轮见 `CacheRead>0`(次轮)。

## T8: OpenAI 适配缓存通道 + reminder**文件:** `internal/llm/openai.go`
**依赖:** T6
**步骤:**
1. `toOpenAIMessages(req)`:首条 system 消息 = `req.System.Stable`(若 `Environment!=""` 拼为 `Stable+"\n\n"+Environment`);随后映射历史;`req.Reminder!=""` → 追加一条尾部 `openai.UserMessage(req.Reminder)`。
2. `Stream(ctx, req Request)` 改用 `req`;`params.Tools = toOpenAITools(req.Tools)`。
3. Usage 解析:`CacheRead = acc.Usage.PromptTokensDetails.CachedTokens`、`CacheWrite = 0`。

**验证:** `go build ./internal/llm/...` 通过;烟囱 openai 兼容端点跑两轮,`cached_tokens` 字段被打印(端点支持则 >0)。

## T9: agent 改造**文件:** `internal/agent/agent.go`
**依赖:** T1, T2, T3, T6
**步骤:**
1. `Agent` 加 `version string` 字段;`New(p llm.Provider, r *tool.Registry, version string)`。
2. 加常量 `planReminderInterval = 4`。
3. `Run` 起始:`env := prompt.GatherEnvironment(a.version, a.provider.Model())`;`sys := prompt.BuildSystemPrompt()`;`defs` 按 mode 选择(规划=`ReadOnlyDefinitions`,普通=`Definitions`)——**移除 suffix 变量**。
4. 每轮迭代算 reminder:`reminder := ""`;`if mode==ModePlan { full := iter==1 || (iter-1)%planReminderInterval==0; reminder = prompt.PlanReminder(full) }`。
5. `streamOnce` 签名改为接收 `sys`、`envText`、`defs`、`reminder`,内部组装 `llm.Request{Messages:conv.Messages(), Tools:defs, System:llm.System{Stable:sys, Environment:envText}, Reminder:reminder}` 调 `a.provider.Stream(ctx, req)`。
6. `agent.Usage` 加 `CacheWrite/CacheRead`;`Run` 透传 `Event{Usage:&Usage{Input,Output,CacheWrite,CacheRead}}`。

**验证:** `go build ./internal/agent/...` 通过(配合 T10/T11)。

## T10: TUI 与 smoke 接线**文件:** `internal/tui/stream.go`、`cmd/smoke/main.go`
**依赖:** T9
**步骤:**
1. `stream.go:95` 改 `agent.New(m.provider, m.registry, m.version)`;`/do` 注入仍用 `prompt.ExecuteDirective`(已迁至 reminder.go,import 路径不变)。
2. `smoke/main.go`:`agent.New(p, tool.NewDefaultRegistry(), "dev")`;消费 `Event.Usage` 时打印 `input/output/cache_write/cache_read`;可改为连发两条消息观察次轮 `cache_read`。

**验证:** `go build ./...` 全绿。

## T11: agent 单测适配**文件:** `internal/agent/agent_test.go`
**依赖:** T9
**步骤:**
1. 修 fake provider:`Stream` 实现新签名 `Stream(ctx, req llm.Request)`;记录收到的 `req`(System.Stable/Environment、Tools、Reminder)。
2. 既有 ch04 场景(A 自然完成、B 上限、C 未知工具、D 并发、E 取消、F 规划只读工具)适配新签名;`New(...)` 传 version。
3. 新增断言:
   - 规划模式下 `req.System.Stable` 非空且**普通/规划一致**;`req.System.Environment` 非空。
   - 规划模式 iter1 的 `req.Reminder` 含完整提醒、含 `<system-reminder>`;iter2 为精简版(构造一个让循环多轮的脚本)。
   - 规划模式 `req.Tools` 仅只读;普通模式全量。
   - reminder **不写入 conv 持久历史**(`conv.Messages()` 不含 reminder 文本)。
   - 缓存用量透传:fake 发 `Usage{CacheWrite:X,CacheRead:Y}` → 收到的 `Event.Usage` 携带 X/Y。

**验证:** `go test ./internal/agent/...` 通过;`go test -race ./internal/agent/...` 无竞争。

## T12: 全量编译测试与规范**文件:** —
**依赖:** T1–T11
**步骤:**
1. `gofmt -l .`(无输出)、`goimports` 分组检查。
2. `go vet ./...`(无告警)。
3. `go build ./...`、`go test ./...`、`go test -race ./internal/agent/... ./internal/tool/...`。

**验证:** 全部通过;检索输出无 api_key 明文。

## 执行顺序

```
T1 ─┐
T2 ─┼─→ T4(prompt 单测)
T3 ─┘
T5(工具描述，独立)

T6(接口) ─┬─→ T7(anthropic) ─┐
          └─→ T8(openai)    ─┤
T1,T2,T3,T6 ─→ T9(agent) ────┼─→ T10(tui/smoke)
                              └─→ T11(agent 单测)

全部 ─→ T12(编译/测试/race/gofmt/vet)
```