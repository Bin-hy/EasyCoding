# Hook 生命周期挂钩系统 Tasks## 文件清单

| 操作 | 文件 | 职责 |
|------|------|------|
| 新建 | `internal/permission/matcher.go` | Matcher 接口、四种实现、CompileMatcher 工厂 |
| 新建 | `internal/permission/matcher_test.go` | 四种 type × 边界条件覆盖 |
| 修改 | `internal/permission/rule.go` | parseRule 识别前缀、Rule 持有 Matcher 替代 Pattern 字符串、hitAny/matchRule 改造 |
| 修改 | `internal/permission/rule_test.go` | 扩展用例覆盖新语法 |
| 修改 | `internal/permission/settings.go` | toRuleSet 改造：失败 rule 走 stderr |
| 修改 | `internal/permission/settings_test.go` | 验证 stderr 报错与跳过逻辑 |
| 新建 | `internal/hook/doc.go` | 包注释 |
| 新建 | `internal/hook/event.go` | 11 个 Event 常量 + 拦截类列表 + IsBlocking 判定 |
| 新建 | `internal/hook/rule.go` | Rule / Condition / Action / Payload 数据结构 |
| 新建 | `internal/hook/matcher.go` | EvalCondition / GetByPath |
| 新建 | `internal/hook/loader.go` | YAML 解析、双层合并、字段校验 |
| 新建 | `internal/hook/loader_test.go` | 字段校验、加载错误、合并测试 |
| 新建 | `internal/hook/engine.go` | Engine + Dispatch 主流程 + only_once |
| 新建 | `internal/hook/engine_test.go` | 各事件 dispatch、拦截、reminder、once 覆盖 |
| 新建 | `internal/hook/executor.go` | 四类 action 执行器 |
| 新建 | `internal/hook/executor_test.go` | shell exit2、http block、prompt、subagent stub |
| 修改 | `internal/agent/runtime.go` | SessionRuntime 加 PendingReminders + HookEngine 字段 + ResetForNewSession 清空 |
| 修改 | `internal/agent/runtime_test.go` | 验证 PendingReminders 行为 |
| 修改 | `internal/agent/agent.go` | WithHookEngine 选项、11 个 emit 点（部分由 tui 触发，agent 负责 PreUserMessage/PreToolUse/PostToolUse/PreCompact/PostCompact/Stop/Notification） |
| 修改 | `internal/agent/agent_test.go` | 拦截路径测试 |
| 新建 | `internal/tui/hooks.go` | /hooks 命令 handler、Model 的 hook 查询方法 |
| 修改 | `internal/tui/tui.go` | TUIParams 加 HookEngine、Model 持有；Init 触发 SessionStart |
| 修改 | `internal/tui/stream.go` | submit() 内 UserPromptSubmit dispatch + 拦截集成 |
| 修改 | `internal/tui/commands.go` | /clear、/resume 触发 SessionEnd + SessionStart/Resume |
| 修改 | `internal/command/builtins.go` | 加 /hooks 内置命令 |
| 修改 | `internal/command/ui.go` | UI 接口加 hook 查询方法 |
| 修改 | `cmd/mewcode/main.go` | 加 hook.Load(root) 与 wiring；SessionEnd defer |

## T1: 实现 permission.Matcher 接口与四种类型**文件：** `internal/permission/matcher.go`
**依赖：** 无
**步骤：**
1. 新建 `matcher.go`，声明接口 `type Matcher interface { Match(s string) bool; String() string }`
2. 实现 4 个类型：
   - `matcherExact{value string}`：`Match` 返回 `s == value`
   - `matcherGlob{pattern string, isCommand bool}`：command 模式用 wildcard，否则用 matchPath；通过 `String()` 返回 `pattern`
   - `matcherRegex{re *regexp.Regexp, src string}`：`Match` 返回 `re.MatchString(s)`
   - `matcherNot{inner Matcher}`：`Match` 返回 `!inner.Match(s)`
3. 实现工厂 `CompileMatcher(pattern string, isCommand bool) (Matcher, error)`：
   - 空串 → 错误 `"empty matcher pattern"`
   - `=value` → `matcherExact{value}`
   - `~regex` → `regexp.Compile`，错误透传
   - `!inner` → 递归 `CompileMatcher(inner, isCommand)` 包装
   - 其它 → `matcherGlob{pattern, isCommand}`
4. matcherGlob 的 Match 内部调 `matchCommand(pattern, s)`(isCommand=true) 或 `matchPath(pattern, s)`(false)
5. 写 doc comment 解释每个 Matcher 类型的语义

**验证：** `go build ./internal/permission/...` 编译通过

## T2: matcher 单元测试**文件：** `internal/permission/matcher_test.go`
**依赖：** T1
**步骤：**
1. 覆盖 4 种类型各自的命中/不命中用例
2. `=git status` 命中 `git status`、不命中 `git status -s`
3. `~^npm (install|test)$` 命中 `npm install`、不命中 `npm run dev`
4. `!=foo` 不命中 `foo`、命中 `bar`
5. `!~^rm` 命中 `ls -lh`、不命中 `rm -rf .`
6. `!git *` 命中 `npm install`、不命中 `git status`（嵌套 not + glob）
7. 编译失败：`~[invalid` 应返回 error
8. 空串：`""` 应返回 error
9. 表驱动写法，每个用例附 `t.Errorf` 描述

**验证：** `go test ./internal/permission/ -run TestMatcher -v` 通过

## T3: 升级 permission.Rule 与 parseRule**文件：** `internal/permission/rule.go`
**依赖：** T1
**步骤：**
1. Rule 结构体改：去 Pattern 字段、加 Matcher（Matcher 类型）和 raw（string，原始描述）
2. parseRule 签名改：`func parseRule(s string) (Rule, bool, error)`——返回 err 让 toRuleSet 写日志
3. parseRule 内部：剥出 tool 与 pattern 后调 `CompileMatcher(pattern, tool == "Bash")`；空 pattern 仍按 nil matcher 表示"全匹配"
4. 改造 `matchRule(r Rule, target string)`：r.Matcher == nil 返回 true（全匹配），否则 `r.Matcher.Match(target)`
5. `escapeGlob` 保留不变，仅供 ch08 自动生成的精确规则使用
6. doc comment 更新说明四种语法

**验证：** `go build ./internal/permission/...` 编译通过

## T4: 升级 toRuleSet 错误日志**文件：** `internal/permission/settings.go`
**依赖：** T3
**步骤：**
1. toRuleSet 改造：parseRule 失败时调 `fmt.Fprintf(os.Stderr, "rule %q parse failed: %s\n", str, err)`
2. 加 `import "fmt"` `import "os"`（os 可能已在）
3. 加注释说明：失败的 rule 不进入 RuleSet，但其它 rule 不受影响

**验证：** `go build ./internal/permission/...` 编译通过

## T5: 扩展 rule_test 与 settings_test**文件：** `internal/permission/rule_test.go`、`internal/permission/settings_test.go`
**依赖：** T3、T4
**步骤：**
1. rule_test：补充用例
   - `Bash(=git status)` 精确匹配
   - `Bash(~^npm.*)` 正则匹配
   - `Bash(!~^rm)` 反向正则
   - `Write(**/*.go)` glob 沿用（确认向后兼容）
2. settings_test：构造一份含非法 rule 的 yaml，验证 toRuleSet 返回的 RuleSet 不含该 rule（用 stderr 输出 capture 不重要，验证 RuleSet.allow/deny 长度即可）
3. 旧测试 TestMatchCommand / TestMatchPath 改成调用 matcher 的形式或保留底层函数测试

**验证：** `go test ./internal/permission/... -v` 全部通过

## T6: hook 包基础数据结构**文件：** `internal/hook/doc.go`、`internal/hook/event.go`、`internal/hook/rule.go`
**依赖：** 无
**步骤：**
1. `doc.go`：包注释，描述本包职责
2. `event.go`：
   - `type Event string`
   - 11 个常量 EventSessionStart / EventSessionEnd / EventSessionResume / EventUserPromptSubmit / EventStop / EventPreUserMessage / EventPreToolUse / EventPostToolUse / EventPreCompact / EventPostCompact / EventNotification
   - `var allEvents = []Event{...}` 用于枚举校验
   - `var blockingEvents = map[Event]bool{EventPreToolUse: true, EventUserPromptSubmit: true}`
   - 函数 `IsBlocking(e Event) bool`、`ParseEvent(s string) (Event, bool)`
3. `rule.go`：
   - `Rule`、`Condition`、`AtomCondition`、`Action`、`ShellAction`、`PromptAction`、`HttpAction`、`SubagentAction`、`ActionType`、`CombineMode` 等结构与常量
   - `Payload` 类型别名 `type Payload map[string]any`

**验证：** `go build ./internal/hook/...` 编译通过

## T7: hook.Matcher 字段路径求值**文件：** `internal/hook/matcher.go`
**依赖：** T6、T1
**步骤：**
1. `GetByPath(p Payload, path string) string`：按 `.` 分隔；递归取值；中途遇 nil/非 map 返回空串
2. 字段值非字符串时：bool/数字转字符串（`fmt.Sprint`）；嵌套对象转 JSON（json.Marshal）
3. `EvalCondition(c *Condition, p Payload) bool`：
   - c == nil → true
   - 遍历 c.Atoms，每条用 GetByPath + AtomCondition.Matcher.Match
   - CombineAllOf 要求全部 true、CombineAnyOf 要求至少一个 true

**验证：** `go build ./internal/hook/...` 编译通过

## T8: hook.Loader YAML 解析**文件：** `internal/hook/loader.go`
**依赖：** T6、T7、T1
**步骤：**
1. 定义 YAML 中间结构 `fileSchema`：`Hooks []hookYAML`，hookYAML 含 Name/Event/If/Action/OnlyOnce/Async/Timeout 字符串/对象字段
2. `Load(projectRoot string) (*Engine, []string)` 主入口：
   - 计算两个候选路径：`<projectRoot>/.mewcode/hooks.yaml`、`<home>/.mewcode/hooks.yaml`
   - 文件不存在跳过；存在但解析失败 stderr 输出后跳过
   - 对每个 hookYAML 调 `compileRule(src, idx, raw) (Rule, error)`
   - 累积成功的 rule、stderr 输出失败的 rule
   - 跨文件 name 冲突时跳过后者
3. `compileRule` 内做字段校验：
   - name 非空
   - event 枚举（ParseEvent）
   - action.type 枚举与子字段必填（shell.command、prompt.text、http.url、subagent.agent_name+prompt）
   - if 顶层 all_of / any_of 互斥
   - 每个 AtomCondition 的 match.type ∈ {exact,glob,regex,not} 且 value/inner 字段完整
   - async + IsBlocking(event) → 报错跳过
   - timeout 解析为 time.Duration（go time.ParseDuration），缺省 30s
4. Matcher 编译用 permission.CompileMatcher；命令类匹配（即工具是 Bash）这里不区分——hook 上下文都是 payload 字段值，统一按非 command 形式（matchPath 的 glob 语义）；正则不变；exact 不变；not 不变。
   - **决策修正**：hook 的 matcher 在初始化时统一传 `isCommand=false`，使 glob 走 `matchPath`（段内 `*` 不跨 `/`，与文件 glob 一致；这对 tool_input.command 这种字段是有点限制——但用户可以改用 regex 表达 shell 字符串匹配，文档需说清）

**验证：** `go build ./internal/hook/...` 编译通过

## T9: hook.Loader 测试**文件：** `internal/hook/loader_test.go`
**依赖：** T8
**步骤：**
1. 临时目录场景：写一份合法 hooks.yaml（含 2 条 hook），Load 返回 Engine 含 2 条 rule
2. 字段缺失：name 空、event 不存在、action.type 无效 → 跳过该条但其它通过
3. all_of + any_of 同时存在 → 跳过该条
4. async + PreToolUse → 跳过该条且 stderr 含 `async not allowed for blocking events`
5. 跨文件同名冲突 → 项目级保留、用户级跳过
6. matcher 编译失败（非法正则） → 跳过该条

**验证：** `go test ./internal/hook/ -run TestLoader -v` 通过

## T10: hook.Engine 与 Dispatch 主流程**文件：** `internal/hook/engine.go`
**依赖：** T6、T7
**步骤：**
1. Engine 结构体：rules、sources、mu、onceFired
2. `NewEngine(rules []Rule, sources []string) *Engine`
3. `Dispatch(ctx, event, payload) DispatchResult`：
   - 遍历 rules，跳过非本事件
   - 加锁查 onceFired，命中跳过；ResetForNewSession 清空
   - EvalCondition；不通过跳过
   - 命中后：
     - async=true 起 goroutine 调 executor.Run，立即继续（不等结果、不进入 InjectedPrompts 与 Blocked 判定）
     - 同步：调 executor.Run，blocking 参数 = IsBlocking(event)
   - 同步结果处理：
     - result.Err 非 nil → stderr 日志 `[hook <name>] <event> failed: <reason>`，继续下一个 rule（不拦截）
     - result.Prompt 非空 → 加入 InjectedPrompts
     - result.Blocked 且 IsBlocking(event) → 设置 DispatchResult.Blocked + Reason + BlockingHookName，break 退出循环
   - 命中且执行无 fatal err 的 rule，若 OnlyOnce → 加入 onceFired
4. `ResetForNewSession()`：加锁清空 onceFired
5. `Sources() []string`、`Rules() []Rule`

**验证：** `go build ./internal/hook/...` 编译通过

## T11: hook.Executor 四类动作执行**文件：** `internal/hook/executor.go`
**依赖：** T6
**步骤：**
1. `Executor` 结构体（无字段或仅 httpClient）
2. `NewExecutor() *Executor`
3. `Run(ctx, rule, payload, blocking) ExecutionResult` 分发到下面四个内部方法
4. `runShell(ctx, sa *ShellAction, payload, blocking, timeout) ExecutionResult`：
   - 调 `exec.CommandContext(ctx, "sh", "-c", sa.Command)`
   - stdin 写入 `json.Marshal(payload)` 单行
   - 等 cmd 完成；超时按失败处理
   - blocking && exit code 2 → Blocked=true、Reason=stderr/stdout 合并去尾
   - exit code 0 → 不拦截不报错
   - 其它非 0 exit → Err=fmt.Errorf("exit %d: %s", code, stderr)
5. `runPrompt(pa *PromptAction)` → ExecutionResult{Prompt: pa.Text}
6. `runHttp(ctx, ha *HttpAction, payload, blocking, timeout)`：
   - 默认 method=POST
   - body：缺省时 `json.Marshal(payload)`；否则 Go text/template 渲染 payload
   - 用 http.Client{Timeout: timeout} POST
   - status 2xx 且 body 含 `{"decision":"block","reason":"..."}` → Blocked=true
   - 网络错/超时/JSON 解析失败 → Err
7. `runSubagent(sa *SubagentAction)`：仅 `fmt.Fprintf(os.Stderr, "[hook subagent] not yet implemented, skipped: %s\n", sa.AgentName)`，返回空 ExecutionResult
8. payload JSON 序列化用一个共享辅助 `marshalSorted(p Payload) []byte`，保证 key 字典序

**验证：** `go build ./internal/hook/...` 编译通过

## T12: executor 单元测试**文件：** `internal/hook/executor_test.go`
**依赖：** T11
**步骤：**
1. shell exit 2 with stderr → Blocked + Reason 含 stderr
2. shell exit 0 → 放行不报错
3. shell exit 1 → Err 非 nil 不拦截
4. shell stdin JSON 解析：脚本读 stdin 后 echo 出来，验证 key 字典序
5. shell timeout：sleep 2s + timeout 100ms → Err 含 "timed out" 或 context.DeadlineExceeded
6. prompt → Prompt 字段非空
7. http with httptest.Server 返回 `{"decision":"block","reason":"x"}` → Blocked=true
8. http with 5xx → Err 非 nil
9. http 模板 body 含 `{{.event}}` → server 收到正确字段
10. subagent → stderr 含占位文本

**验证：** `go test ./internal/hook/ -run TestExecutor -v` 通过

## T13: hook.Engine 测试**文件：** `internal/hook/engine_test.go`
**依赖：** T10、T11
**步骤：**
1. 多 rule 同事件按声明序执行
2. 拦截类事件下首个 Blocked 的 rule 中断后续
3. 非拦截类事件下 Blocked 字段不传递（fake exit code 2 但 IsBlocking=false 也不 set Blocked）
4. prompt rule 的 Prompt 累加到 InjectedPrompts
5. only_once 在首次执行后被加入 onceFired，第二次 Dispatch 跳过
6. ResetForNewSession 后 only_once 重置
7. async rule 不进入 Blocked 判定（用 wait group 验证 goroutine 已起）

**验证：** `go test ./internal/hook/ -run TestEngine -v` 通过

## T14: agent SessionRuntime 扩展**文件：** `internal/agent/runtime.go`、`internal/agent/runtime_test.go`
**依赖：** T6
**步骤：**
1. SessionRuntime 加字段：`PendingReminders []string`、`HookEngine *hook.Engine`
2. `NewSessionRuntime` 初始化空 slice
3. `ResetForNewSession` 清空 PendingReminders、若 HookEngine 非 nil 调 HookEngine.ResetForNewSession()
4. 新增 `AppendReminders(prompts []string)` 加锁追加
5. 新增 `TakeReminders() []string` 加锁取出并清空
6. 测试覆盖：AppendReminders + TakeReminders 单线程行为；ResetForNewSession 清空

**验证：** `go test ./internal/agent/ -run TestSessionRuntime -v` 通过

## T15: agent.WithHookEngine 选项与 emit 框架**文件：** `internal/agent/runtime.go`、`internal/agent/agent.go`
**依赖：** T14
**步骤：**
1. runtime.go：加 `WithHookEngine(e *hook.Engine) Option`，赋值到 `a.hookEngine`
2. agent.go：Agent 结构体加字段 `hookEngine *hook.Engine`
3. 私有方法 `(a *Agent) dispatchHook(ctx, event, payload) hook.DispatchResult`：
   - hookEngine == nil → 返回空 DispatchResult
   - 调 hookEngine.Dispatch
   - 把 InjectedPrompts 调 runtime.AppendReminders
   - 返回结果（保留 Blocked + Reason 供 PreToolUse 用）
4. 私有方法 `(a *Agent) buildReminder(mode permission.Mode, iter int) string`：
   - 原 planReminder + runtime.TakeReminders() join("\n\n")

**验证：** `go build ./internal/agent/...` 编译通过

## T16: agent 各事件 emit 接入**文件：** `internal/agent/agent.go`
**依赖：** T15
**步骤：**
1. Run 开始处补 `dispatchHook(ctx, EventStop, ...)` 入口准备——实际 Stop 在 `Done: true` emit 前调用
2. 每轮 iter 顶部、manageContextAuto 之前调 `dispatchHook(ctx, EventPreCompact, payload{trigger:"auto"})`；ManageContext 返回后 emit `EventPostCompact` 带 before/after tokens
3. emergencyCompactAndDecide：同样 PreCompact/PostCompact，trigger="emergency"
4. streamOnce 调 provider.Stream 之前 emit `EventPreUserMessage`，payload 含 conversation 末尾 user 消息
5. 把 reminder 串改造：取 `a.buildReminder(mode, iter)` 替代原裸的 `prompt.PlanReminder(full)`
6. executeBatched 改造：
   - 单工具循环开始处 emit PreToolUse，payload 含 tool_name、tool_input；Blocked=true 时构造 hookBlockedResult、emit PhaseStart/PhaseEnd（IsError=true），continue
   - tool 拿到 result 后、emit PhaseEnd 之前 emit PostToolUse，payload 含 tool_name、tool_input、tool_result、is_error
7. emit Done 之前调 `EventStop`，payload{iter}
8. emit Approval 之前调 `EventNotification`，payload{kind:"approval", detail: tool_name}
9. emit Err 之前调 `EventNotification`，payload{kind:"stream_error", detail: err.Error()}
10. 拦截结果整合：定义 `hookBlockedResult(callID, hookName, reason) llm.ToolResult`：Content=`[hook <name>] <reason>`、IsError=true

**验证：** `go build ./internal/agent/...` 编译通过

## T17: agent_test 拦截路径与 emit 覆盖**文件：** `internal/agent/agent_test.go`、`internal/agent/runtime_test.go`
**依赖：** T16
**步骤：**
1. 构造一个 fake provider + 注入 fake hook.Engine（mockEngine 实现相同接口）
2. 测试：PreToolUse 拦截时工具结果是 hookBlockedResult 形式、PhaseStart/PhaseEnd 仍 emit
3. 测试：PreUserMessage 注入的 prompt 在下一次 streamOnce 的 reminder 串中可见
4. 测试：Stop 事件在 Done 前一刻被 emit
5. 由于 Engine 类型不是接口，可能需要重构成接口或用 nil Engine 路径（更简单：在测试里直接 New 真实 Engine，注入合成 rules）

**验证：** `go test ./internal/agent/ -run TestHook -v` 通过

## T18: tui Model 持有 HookEngine**文件：** `internal/tui/tui.go`
**依赖：** T15
**步骤：**
1. TUIParams 加 `HookEngine *hook.Engine`
2. Model 加字段 `hookEngine *hook.Engine`
3. New 内：
   - 把 params.HookEngine 赋给 m.hookEngine 与 runtime.HookEngine
   - 构造 agent 时加 `agent.WithHookEngine(params.HookEngine)`
4. Init 末尾添加 `cmd := dispatchSessionStart(m)` 拼到 batch

**验证：** `go build ./internal/tui/...` 编译通过

## T19: tui UserPromptSubmit 拦截集成**文件：** `internal/tui/stream.go`
**依赖：** T18
**步骤：**
1. submit() 重写：
   - 现有的 trim 与 slash 分发保留
   - 非 slash 路径进入 Hook 拦截判定前
   - 构造 payload：`hook.Payload{"event": "UserPromptSubmit", "session_id": ..., "cwd": m.cwd, "mode": m.mode.String(), "prompt": text}`
   - 调 m.hookEngine.Dispatch(m.ctx, EventUserPromptSubmit, payload)
   - Blocked=true：返回 tea.Println(errorBlock(fmt.Errorf("[hook %s] %s", result.BlockingHookName, result.Reason))) 不消费 textarea
   - 否则：把 InjectedPrompts 经 runtime.AppendReminders；conv.AddUser(text)；beginTurn
2. 提供辅助函数 `(m *Model) basePayload(event hook.Event) hook.Payload` 构造通用字段

**验证：** `go build ./internal/tui/...` 编译通过

## T20: tui SessionStart / End / Resume**文件：** `internal/tui/tui.go`、`internal/tui/commands.go`、`internal/tui/stream.go`
**依赖：** T18、T19
**步骤：**
1. 新增 `dispatchSessionStart(m *Model) tea.Cmd`：构造 payload + 调 Engine.Dispatch + InjectedPrompts 写入 runtime + 返回 nil cmd
2. 新增 `dispatchSessionEnd(m *Model)`：仅同步调 Dispatch，不返回 cmd
3. 新增 `dispatchSessionResume(m *Model) tea.Cmd`：同 SessionStart 流程，event 改为 SessionResume
4. Init 末尾 batch 中调 dispatchSessionStart
5. /clear handler 内：先 dispatchSessionEnd，再 ResetForNewSession，最后 dispatchSessionStart
6. /resume handler 选中会话恢复完毕后：先 dispatchSessionEnd（旧），切到新会话后 dispatchSessionResume
7. handleExit 内：dispatchSessionEnd 后再退出
8. tui.Model.Run 退出前：在 main.go 的 defer 中 dispatchSessionEnd？或者 handleExit 即可
   - 简化：仅 /clear、/resume、/exit、ctrl+c 退出几条路径调；main.go defer 兜底（确保 ctrl+c 一退出也 emit）
   - 实际：在 tui.Run() 返回后由 main 调一次 hookEngine.Dispatch(EventSessionEnd)；tui 内的 /clear、/resume 自己控制

**验证：** `go build ./internal/tui/...` 编译通过

## T21: /hooks 命令**文件：** `internal/tui/hooks.go`、`internal/command/builtins.go`、`internal/command/ui.go`
**依赖：** T6、T10、T18
**步骤：**
1. UI 接口加方法 `HookSources() []string`、`HookRules() []hook.Rule`
2. Model 实现这两个方法（读 m.hookEngine 字段）
3. 新增 `internal/tui/hooks.go`，实现 handleHooks(ctx, ui)：
   - 取 rules 与 sources
   - 空时 println `No hooks loaded.`
   - 否则按 event 分组（保留 yaml 声明顺序）、每条一行 `  <name>  <event>  <action.type>  [once] [async]`
   - 末尾 `Loaded from: file1, file2`
4. builtins.go 注册新命令 `/hooks`，KindLocal，描述「列出已加载的 hook 列表」

**验证：** `go build ./...` 编译通过

## T22: main.go wiring**文件：** `cmd/mewcode/main.go`
**依赖：** T8、T18
**步骤：**
1. 在 permission.NewEngine 之后调 `hookEngine := hook.Load(root)`
2. tui.New 传 HookEngine
3. m.Run() 之后调 `if hookEngine != nil { _ = hookEngine.Dispatch(context.Background(), hook.EventSessionEnd, basePayload) }` 兜底 SessionEnd
4. import 加 `mewcode/internal/hook`

**验证：** `go build ./cmd/mewcode/...` 编译通过

## T23: 整体编译与测试**文件：** —
**依赖：** T1-T22 全部
**步骤：**
1. `go build ./...` 通过
2. `go test ./...` 通过——hooks 相关测试 + 既有测试都得过

**验证：** 上述两条命令本地通过

## T24: 修复回归**文件：** 根据测试输出决定
**依赖：** T23
**步骤：**
1. 修复 ch08 / ch11 等老测试因 Matcher 改造而失败的用例
2. 修复 ch10 / ch11 测试因 /hooks 命令加入而影响排序或数量的用例
3. 重新跑全套测试

**验证：** `go test ./...` 通过

## T25: tmux 端到端实跑（验收 AC17 与 checklist 端到端场景）**文件：** `.mewcode/hooks.yaml` 临时测试配置
**依赖：** T23、T24
**步骤：**
1. 写测试 hooks.yaml：包含 AC4-AC15 各典型场景的 hook
2. tmux 新建 session 启动 mewcode
3. 依次触发：write_file 工具调用、含 delete 关键字的用户输入、git 命令、Stop 事件
4. 观察 stderr 日志、tool_result 内容、reminder 注入是否符合预期
5. 全程不 panic、不卡顿

**验证：** 见 checklist.md

## 执行顺序

```
T1 → T2 → T3 → T4 → T5            # permission Matcher 扩展
T6 → T7 → T8 → T9                 # hook 基础结构 + Loader
T10 → T13                         # Engine
T11 → T12                         # Executor（与 Engine 并行）
T14 → T15 → T16 → T17             # agent 接入
T18 → T19 → T20                   # tui 接入
T21                               # /hooks 命令
T22                               # main wiring
T23 → T24                         # 整体编译测试
T25                               # tmux 实跑验收
```

并行机会：
- T11/T12 与 T10/T13 互不依赖,可并行
- T11 与 T8 在 T6 完成后可并行
- T17 必须在 T16 之后
- T19 之前 T18 必须先完成