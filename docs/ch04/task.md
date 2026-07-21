# Agent Loop Tasks

> 基于已批准的 spec.md + plan.md。任务有序，每步留绿编译。验证一律「先跑命令看输出，再下结论」。

## 文件清单

| 操作 | 文件 | 职责 |
|------|------|------|
| 修改 | `internal/llm/provider.go` | 新增 Usage 类型；StreamEvent 加 Usage；Stream 加 systemSuffix 形参 |
| 修改 | `internal/llm/anthropic.go` | effectiveSystem(suffix)；流结束上抛 acc.Usage |
| 修改 | `internal/llm/openai.go` | StreamOptions.IncludeUsage；toOpenAIMessages 拼 suffix；上抛 acc.Usage |
| 修改 | `internal/tool/tool.go` | Tool 接口加 ReadOnly() |
| 修改 | `internal/tool/registry.go` | ReadOnlyDefinitions、IsReadOnly |
| 修改 | `internal/tool/{read_file,write_file,edit_file,bash,glob,grep}.go` | 各加 ReadOnly() |
| 修改 | `internal/conversation/conversation.go` | LastRole() |
| 修改 | `internal/prompt/prompt.go` | PlanModeReminder、ExecuteDirective；SystemPrompt 增循环约定 |
| 重写 | `internal/agent/agent.go` | ReAct 循环、Mode、executeBatched、Usage/Iter/Notice 事件、历史收尾 |
| 重写 | `internal/agent/agent_test.go` | 多轮 fake provider、并发分批、停止条件、Plan 工具集 |
| 修改 | `internal/conversation/conversation_test.go` | LastRole 断言 |
| 修改 | `internal/tui/{tui,stream,view}.go` | mode、per-turn ctx、Esc/Ctrl+C、/plan /do、Usage/Iter/Notice/多工具、状态栏、动态区 |
| 修改 | `cmd/smoke/main.go` | agent.Run 调用处补 mode 实参（ModeNormal）|

## T1: llm 新增 Usage 类型（纯增量）**文件：** `internal/llm/provider.go`
**依赖：** 无
**步骤：**
1. 新增类型 `Usage{InputTokens, OutputTokens int64}`（带中文注释：本轮输入/输出 token 数）。
2. 给 `StreamEvent` 增字段 `Usage *Usage`（指针，非空即本轮用量），更新 `StreamEvent` 文档注释补「Usage 非空：本轮 token 用量，Done 之前一次性发出」。

**验证：** `go build ./...` 通过（纯增字段，向后兼容，不改签名）。

## T2: tool 只读分类**文件：** `internal/tool/tool.go`、`internal/tool/registry.go`、`internal/tool/{read_file,write_file,edit_file,bash,glob,grep}.go`
**依赖：** 无
**步骤：**
1. `tool.go`：`Tool` 接口加 `ReadOnly() bool`（注释：true=只读，可并发执行 & Plan Mode 放行）。
2. 6 个工具各加一行方法：`read_file`/`glob`/`grep` → `func (t xxxTool) ReadOnly() bool { return true }`；`write_file`/`edit_file`/`bash` → `return false`。
3. `registry.go`：
   - `ReadOnlyDefinitions() []llm.ToolDefinition`：仿 `Definitions()` 按 order 遍历，仅收 `r.tools[name].ReadOnly()==true` 的项。
   - `IsReadOnly(name string) bool`：`t, ok := r.Get(name); return ok && t.ReadOnly()`。

**验证：** `go build ./internal/tool/...` 通过；`go test ./internal/tool/...` 不回归（接口加方法后 6 工具均实现，编译即证明完整）。

## T3: conversation.LastRole**文件：** `internal/conversation/conversation.go`、`internal/conversation/conversation_test.go`
**依赖：** 无
**步骤：**
1. `conversation.go`：新增 `LastRole() string`——空历史返回 `""`，否则返回 `c.messages[len-1].Role`。
2. `conversation_test.go`：补一条断言——空会话 `LastRole()==""`；`AddUser` 后 `==RoleUser`；`AddToolResults` 后 `==RoleTool`；`AddAssistant` 后 `==RoleAssistant`。

**验证：** `go test ./internal/conversation/...` 通过。

## T4: prompt 计划态提示与循环约定**文件：** `internal/prompt/prompt.go`
**依赖：** 无
**步骤：**
1. `SystemPrompt` 增补一句 Agent 循环约定：持续调用工具推进任务，直到任务完成后再给出最终简洁答复（不要每步都停下来等用户）。
2. 新增 `const PlanModeReminder`：计划模式系统后缀——当前为计划模式，只能用只读工具（读文件 / 按模式找文件 / 搜内容）调研并产出一份分步执行计划；不得写文件、改文件或执行命令；计划写完即停，等用户用 `/do` 批准后再执行。
3. 新增 `const ExecuteDirective = "请按上面的计划开始执行。"`。
4. （可选）`ReadyHint` 增提 `/plan`、`/do`。

**验证：** `go build ./internal/prompt/...`；`go test ./...` 不回归。

## T5: llm Stream 加 systemSuffix + 用量上抛**文件：** `internal/llm/provider.go`、`internal/llm/anthropic.go`、`internal/llm/openai.go`、`internal/agent/agent.go`（临时补参）
**依赖：** T1
**步骤：**
1. `provider.go`：`Provider.Stream` 签名改为 `Stream(ctx, msgs []Message, tools []ToolDefinition, systemSuffix string) <-chan StreamEvent`，更新接口注释说明 systemSuffix 语义（非空时拼到内置 SystemPrompt 之后）。
2. `anthropic.go`：
   - `Stream` 加 `systemSuffix` 形参；`params.System` 由硬编码改为 `effectiveSystem(systemSuffix)`——`suffix==""` 单块 `prompt.SystemPrompt`；非空时单块 `prompt.SystemPrompt+"\n\n"+suffix`。
   - 流正常结束（`stream.Err()==nil`）后、上抛 ToolCalls 与关闭 channel 前：`ch <- StreamEvent{Usage:&Usage{InputTokens:acc.Usage.InputTokens, OutputTokens:acc.Usage.OutputTokens}}`。
3. `openai.go`：
   - `Stream` 加 `systemSuffix`；`params.StreamOptions = openai.ChatCompletionStreamOptionsParam{IncludeUsage: openai.Bool(true)}`。
   - `toOpenAIMessages(msgs, systemSuffix)`：首条 system 消息文本 `prompt.SystemPrompt`，suffix 非空时 `+"\n\n"+suffix`（其调用处同步加实参）。
   - 流结束后：`ch <- StreamEvent{Usage:&Usage{InputTokens:acc.Usage.PromptTokens, OutputTokens:acc.Usage.CompletionTokens}}`。
4. `internal/agent/agent.go`：把现有 `streamOnce` 里唯一的 `provider.Stream(ctx, conv.Messages(), defs)` 调用补成 `..., defs, "")` 以匹配新签名——本步即让**非测试构建**保持绿（T6 会整体重写 agent.go）。

> 说明：`cmd/smoke/main.go` 走 `agent.Run`、不直接调 `Stream`，本步不动它（其 Run 调用在 T7 随 mode 形参一并更新）。`internal/agent/agent_test.go` 的 `fakeProvider.Stream` 也实现该接口，本步之后它会编译失败——这是预期的，T6 重写 agent_test 时一并补 `systemSuffix` 形参；因此本步**不要**跑 `go test ./internal/agent/...`。

**验证：** `go build ./...` 通过（不含测试文件，绿）；`go vet ./internal/llm/...` 无告警；`go run ./cmd/mewcode` 发一条纯文本回复正常（用量已随流上抛，旧 agent 暂未消费）。

## T6: agent ReAct 循环重写**文件：** `internal/agent/agent.go`、`internal/agent/agent_test.go`
**依赖：** T1, T2, T3, T4, T5
**步骤：**
1. `agent.go`：
   - 包注释改为「ReAct 循环编排」。
   - 类型：保留 `Phase`/`ToolEvent`/`Agent`/`New`；新增 `Usage{Input,Output int64}`、`Mode`(`const (ModeNormal Mode = iota; ModePlan)`）；`Event` 增字段 `Usage *Usage`、`Iter int`、`Notice string`。
   - 常量：按 plan「迭代、停止常量与提示文案」原样落 `maxIterations`/`maxUnknownRun` 与 `noticeMaxIter`/`noticeUnknownTools`/`noticeStreamErr`/`noticeCancelled`（文案以 plan 为准，T8 端到端按这些文案核对）。
   - `Run(ctx, conv, mode)`：按 plan「Run 算法」实现 `for iter` 循环——按 mode 取 `defs`(`Definitions`/`ReadOnlyDefinitions`) 与 `suffix`(`""`/`prompt.PlanModeReminder`)；emit Iter → streamOnce → emit Usage → 无工具自然完成 / 有工具 `AddAssistantWithToolCalls` → 统计 `unknownRun` → `executeBatched` → `AddToolResults`（无条件）→ **取消（!completed）最高优先级收尾** → 未知工具上限收尾 → 循环走完触达迭代上限收尾。
   - `streamOnce(ctx, conv, defs, suffix, ch) → (text, calls, usage, ok)`：`suffix` 为 ch04 新增形参，透传给 `provider.Stream`；转发 Text、收集 calls、记录 `ev.Usage`、Err 即发 `Event{Err}` 返回 `ok=false`。
   - `executeBatched(ctx, calls, ch) → (results, completed)`：保序分批——从 `i=0` 扫描，`IsReadOnly(calls[i])` 为真则吃最长连续只读区间 `[i,j)` 用 `sync.WaitGroup` **并发**（每 goroutine 内 `context.WithTimeout(ctx, tool.DefaultTimeout)` 后 `Execute`，只写自己下标 `results[k]`），否则**串行**单个；每段执行前判 `ctx.Err()` 取消则填 `noticeCancelled` 结果返 `completed=false`；事件「Start 按序、End 按序」（见 plan）。
   - 辅助：`allUnknown(calls)`（每个 call 用 `registry.Get` 判，全未注册才 true）、`ensureFinal`（沿用 ch03）、`ensureAssistantTail(conv, fallback)`、`finishCancelled(conv)`、`emit`/`argsPreview`（沿用 ch03）。
2. `agent_test.go`（**替换** ch03 的 `TestSingleRoundReadAndAnswer`/`TestSingleRoundLimit`——后者断言单轮已与 ch04 多轮矛盾）。`fakeProvider.Stream` 签名补 `systemSuffix string`（并在某用例里记录收到的 `tools`/`suffix` 供断言）；多轮靠 `scripts [][]StreamEvent` 逐次返回：
   - 场景 A（多轮链路 AC1）：脚本①返回 1 个 read_file 工具调用、脚本②返回纯文本 → 断言事件序列含 Iter=1、ToolStart/End、Iter=2、最终 Text、Done；`conv` 末尾为 assistant 文本，中间含 tool_use 回合 + RoleTool 回合。
   - 场景 B（迭代上限 AC3）：用「每次 Stream 都返回一个工具调用」的 fake（忽略脚本耗尽，恒返工具）→ 断言恰好 `maxIterations` 次请求后停（`fp.calls==maxIterations`）、收到 `Notice`(noticeMaxIter)、`conv.LastRole()==RoleAssistant`。
   - 场景 C（连续未知工具 AC4）：脚本连续返回未注册工具名 → 断言 `maxUnknownRun` 轮后停并 Notice(noticeUnknownTools)；另一用例在其间混入一个 read_file，断言计数重置、不提前停。
   - 场景 D（保序分批 AC8）：构造**自定义 registry**注册两个插桩工具——一个只读工具（`ReadOnly()==true`，Execute 内 `atomic` 记录「同时在跑的并发数」峰值、并 sleep 制造重叠）与一个有副作用工具（`ReadOnly()==false`，记录开始时刻）。脚本一轮返回 `[ro, ro, rw]` → 断言：两只读的并发峰值 ≥2（确实并发）、rw 的开始时刻晚于两只读完成、`AddToolResults` 写入历史的结果顺序与调用序一致（按结果内容/ID 比对，不依赖具体方法名）。
   - 场景 E（取消历史一致 AC9）：插桩工具在 Execute 中阻塞，测试侧在执行期间 `cancel()` per-turn ctx → 断言 `conv` 末尾配对合法（含 tool_results、最后是 assistant 文本 noticeCancelled），无悬空 tool_use；随后再追加一轮纯文本脚本能正常跑（角色交替未坏）。
   - 场景 F（Plan 工具集 AC13）：`Run(ctx, conv, ModePlan)` → 断言 fake 收到的 `tools` 仅含只读工具定义、`suffix==prompt.PlanModeReminder`。

**验证：** `go test ./internal/agent/...` 全通过；`go test -race ./internal/agent/...` 无竞争告警（覆盖并发分批，N6）。

## T7: tui 接入 Agent Loop + 收尾 Run 调用方**文件：** `internal/tui/tui.go`、`internal/tui/stream.go`、`internal/tui/view.go`、`cmd/smoke/main.go`
**依赖：** T4, T6
**说明：** T6 改了 `Agent.Run` 签名（加 `mode`），其调用方 `tui/stream.go` 与 `cmd/smoke/main.go` 在此步同步更新——本步完成后 `go build ./...` 才在**仓库级**重新转绿（T6 后只保证 agent 包及其测试绿）。
**步骤：**
1. `tui.go`：
   - `Model` 新增字段：`mode agent.Mode`、`iter int`、`usageIn int64`、`usageOut int64`、`curTools []toolDisplay`（移除单个 `curTool`）、`turnCancel context.CancelFunc`。
   - `Update` 按键拆分：`ctrl+c` → `stateStreaming` 时 `m.turnCancel()`（不退出，重挂泵等结束）/ 否则 `m.cancel(); tea.Quit`；新增 `esc` → `stateStreaming` 时 `m.turnCancel()`。
2. `stream.go`：
   - `submit`：识别 `/exit`（退出）、`/plan`（`mode=ModePlan`、提示块、回 idle）、`/do`（`mode=ModeNormal`、`conv.AddUser(prompt.ExecuteDirective)`、走启动流程）、普通文本（`conv.AddUser`）。启动处：`turnCtx, m.turnCancel = context.WithCancel(m.ctx)`；`m.events = agent.New(m.provider,m.registry).Run(turnCtx, m.conv, m.mode)`；`m.iter=0`；`m.state=stateStreaming`。
   - `updateStreaming` 按 plan 分派顺序处理 `Err`/`Tool`/`Usage`(累加 usageIn/usageOut)/`Notice`(灰提示块)/`Iter`(set m.iter)/`Done`/`Text`；`Tool.PhaseStart` 追加 `curTools`（首个工具前先提交 preamble）、`PhaseEnd` 从 `curTools` 移除队首匹配并 `tea.Sequence` 提交工具行+结果摘要。
   - `finishTurn`：清 `curReply`/`curTools`/`events`/`iter`/`turnCancel`，回 `stateIdle`（保留 `mode`、`usageIn/usageOut`）。
3. `view.go`：
   - `statusBar`：左侧 provider 名后在 `ModePlan` 时附「PLAN」徽标；右侧 model 名旁附 `↑{in} ↓{out} tok`（紧凑数字，如 `1.2k`）。
   - 流式动态区：`curTools` 非空逐行渲染 `● name(args)` Running…；否则「Imagining… (Ns · 第 N 轮)」（`m.iter>0` 附轮次）。
4. `cmd/smoke/main.go`：`a.Run(ctx, conv)` 调用补 mode 实参 → `a.Run(ctx, conv, agent.ModeNormal)`（保持其调试用途，不需感知 plan/取消）。

**验证：** `go build ./...`（仓库级转绿）；`go vet ./...` 无告警；`gofmt -l internal/tui cmd/smoke` 无输出。

## T8: 全量验证与端到端冒烟**文件：** 无（验证）
**依赖：** T1–T7
**步骤：**
1. `gofmt -l .`（goimports 分组正确）；`go vet ./...`；`go test ./...`；`go test -race ./internal/agent/... ./internal/tool/...`。
2. 端到端（openai 兼容端点，用 `.mewcode/config.yaml`）：
   - 多轮（AC1）：问「读 `docs/ch03/spec.md`，再据其内容新建 `docs/ch03/summary.txt` 写一句话摘要」→ 观察 read_file → write_file 跨多轮自动连环、状态栏用量增长、动态区轮次递增、最终答复。
   - 取消（AC10）：发一个会跑多步的任务，中途按 Esc / Ctrl+C → 回空闲态不退出 → 再正常发一条继续对话（验证历史未坏）。
   - 流出错（AC5）：临时改坏 base_url 或断网发一条 → 错误提示、程序不退出、改回后继续。
   - Plan Mode（AC13）：`/plan` → 问「给登录功能加单测的方案」→ 观察只出现 read/glob/grep 类工具与计划文本、无写/执行 → `/do` → 切回全工具按计划执行。
3. （可选）若有 anthropic 配置，重复多轮场景验证跨协议一致（AC14）。

**验证：** 全部命令通过、端到端各场景符合预期；密钥不回显（通读输出，AC/N7）。

## 执行顺序

```
T1 ─┬─ T5 ─┐
T2 ─┤      │
T3 ─┼──────┼─ T6 ─┬─ T7 ─┐
T4 ─┘      │      │      │
           └──────┘      └─ T8
```
（T1–T4 互相独立可并行；T5 依赖 T1；T6 依赖 T1/T2/T3/T4/T5；T7 依赖 T4/T6；T8 收尾全部。）