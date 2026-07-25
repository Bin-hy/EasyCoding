# SubAgent 机制 Checklist

> 每一项通过运行代码或观察行为来验证,聚焦系统行为。

## 实现完整性### subagent 包

- [ ] internal/subagent 包存在且编译通过(验证:`go build ./internal/subagent/...`)
- [ ] Definition 结构体包含 Name/Description/Tools/DisallowedTools/Model/MaxTurns/PermissionMode/DontAsk/Background/SystemPrompt/FilePath/Source 全部字段(验证:`go test ./internal/subagent/ -run TestDefinition`)
- [ ] ParseDefinition 能正确解析合法 frontmatter + body,permissionMode=dontAsk 时 DontAsk=true(验证:`go test ./internal/subagent/ -run TestParse -v`)
- [ ] ParseDefinition 对 frontmatter 缺 name/description 报错,model 非法 fallback 到 inherit 并 stderr 警告(验证:对应测试通过)
- [ ] 内置 3 个文件(general-purpose/explore/plan)在 builtin/ 目录下,go:embed 加载成功(验证:`go test ./internal/subagent/ -run TestBuiltin`)
- [ ] LoadCatalog 按 builtin → user → project 顺序加载,同名高优先级覆盖(验证:`go test ./internal/subagent/ -run TestCatalog`)
- [ ] Catalog.Resolve("Explore") 在三层覆盖场景下返回正确 Source(验证:对应测试通过)
- [ ] Catalog.ForkDefinition() 返回 IsFork()=true 的临时 Definition(验证:对应测试通过)

### tool 过滤多层防线

- [ ] tool.ALL_AGENT_DISALLOWED_TOOLS / CUSTOM_AGENT_DISALLOWED_TOOLS / ASYNC_AGENT_ALLOWED_TOOLS 三个常量存在(验证:`go test ./internal/tool/ -run TestFilterConstants`)
- [ ] ApplyAgentToolFilter 按 spec F30 顺序应用五层过滤(验证:`go test ./internal/tool/ -run TestApplyAgentToolFilter -v`)
- [ ] 后台模式下,工具集与 ASYNC_AGENT_ALLOWED_TOOLS 取交集,Agent / TaskList / SendMessage 等元工具被剔除(验证:对应测试用例通过)
- [ ] MCP 工具(mcp__ 前缀)在后台模式下被保留(验证:对应测试用例通过)

### agent 包扩展

- [ ] WithSystemPrompt / WithMaxTurns / WithPermissionMode / WithDontAsk / WithApprovalUpgrader / WithProvider 6 个新 Option 存在且生效(验证:`go test ./internal/agent/ -run TestOptions`)
- [ ] BuildForkedMessages 正确克隆父消息 + 处理悬空 tool_use + 追加 Boilerplate(验证:`go test ./internal/agent/ -run TestFork`)
- [ ] IsForkContext 能识别消息中含 `<fork_boilerplate>` 标签(验证:对应测试通过)
- [ ] Agent.RunToCompletion 能跑完一轮非交互循环,返回最后一条 assistant 文本(验证:`go test ./internal/agent/ -run TestRunToCompletion -v`)
- [ ] RunToCompletion 触达 maxTurns 时返回错误(验证:对应测试通过)
- [ ] dontAsk 模式下,工具 Ask 决策被自动转 Allow(验证:对应测试通过)
- [ ] approvalUpgrader 回调在 Ask 决策时被命中(验证:对应测试通过)
- [ ] RunToCompletion 把 events 转发到外部 channel,Tool/Text/Approval 事件可被消费(验证:对应测试通过)

### Agent 工具

- [ ] NewAgentTool 构造的工具 Name()="Agent",Parameters() 含 prompt/description/subagent_type/model/run_in_background/name 字段(验证:`go test ./internal/agent/ -run TestAgentToolBasic`)
- [ ] AgentTool.Execute 缺少 prompt 时返回错误(验证:对应测试通过)
- [ ] AgentTool.Execute 未知 subagent_type 时返回错误(验证:对应测试通过)
- [ ] AgentTool.Execute 定义式 subagent 调用走前台 RunToCompletion,返回 finalText(验证:对应测试通过)
- [ ] AgentTool.Execute run_in_background=true 时返回 `{"task_id":"...","status":"async_launched"}` JSON(验证:对应测试通过)
- [ ] AgentTool.Execute 在子 Agent context 内被再次调用时拦截(验证:嵌套阻断测试通过)
- [ ] AgentTool.Execute 检测到 conv 含 fork boilerplate 时拦截(验证:对应测试通过)
- [ ] AgentTool.Execute EnableSubAgentBackground=false 时,run_in_background=true 与 fork 路径报错(验证:对应测试通过)

### task 包

- [ ] task.Manager 的 Launch 起 goroutine 跑 RunToCompletion,跑完写 status=Completed,push done(验证:`go test ./internal/task/ -run TestLaunch -v`)
- [ ] task.Manager.Launch goroutine 内 panic 时,status=Failed,Err 含 panic 信息,主程序不崩(验证:对应测试通过)
- [ ] task.Manager.Stop 触发 task.Cancel,goroutine 退出后 status=Cancelled(验证:对应测试通过)
- [ ] task.Manager.SendMessage 在已 completed 的任务上重新跑动,新 user 消息追加到 Conv(验证:对应测试通过)
- [ ] task.Manager.byName 后启动覆盖前,Get(byName[name]) 返回最新 task(验证:对应测试通过)
- [ ] SubscribeDone() 返回的 channel 在 task 完成时收到 id(验证:对应测试通过)

### 4 个 task 工具

- [ ] TaskList 工具返回当前所有任务的 JSON 列表(验证:`go test ./internal/task/ -run TestTaskListTool`)
- [ ] TaskGet 工具返回指定任务的完整字段;未知 id 返回 IsError=true(验证:对应测试通过)
- [ ] TaskStop 工具调用 Manager.Stop,返回成功 JSON(验证:对应测试通过)
- [ ] SendMessage 工具调用 Manager.SendMessage,返回 resumed JSON(验证:对应测试通过)
- [ ] 4 个工具都实现 SystemTool 接口,IsSystem()=true(验证:工具列表过滤时它们对子 Agent 仍可见 - 实际上 ASYNC 白名单优先,在后台子 Agent 中**仍然不可见**;对前台定义式子 Agent 通过 ALL_AGENT_DISALLOWED 不在其中)

### TUI 集成

- [ ] tui.Model 持有 taskMgr 与 subAgentCatalog 字段(验证:`go build ./internal/tui/...`)
- [ ] Init() 启动 consumeTaskDone goroutine,任务完成时把 `<task-notification>` 注入 runtime.PendingReminders(验证:`go test ./internal/tui/ -run TestConsumeTaskDone`)
- [ ] tui/skill_fork.go 改造为调 agent.RunToCompletion 而非自己拼装循环(验证:现有 skills 测试不破)
- [ ] main.go 注册 4 个 task 工具 + 1 个 Agent 工具,subagentCatalog 与 taskMgr 传给 tui.New(验证:`go build ./...`)
- [ ] config.Config 新增 EnableSubAgentBackground 字段(验证:`go build ./internal/config/...`)

## 集成

- [ ] subagent.Catalog 与 tool.ApplyAgentToolFilter 协同工作:Resolve 拿到 def,过滤函数按 def.Source/Background/Tools/DisallowedTools 收窄(验证:agent_tool_integration_test 通过)
- [ ] Agent 工具的前台调用与现有 ch11 Skill fork 路径不互相干扰(验证:skills 包测试通过 + 手动 tmux 验证一个 inline skill 与一个 Agent 工具调用)
- [ ] Hook 引擎在子 Agent 内仍生效(PreToolUse / PostToolUse 在 RunToCompletion 内被调用)(验证:hook 包测试 + 子 Agent 跑动手动断言 hook 触发)
- [ ] 主 Agent 工具列表里仍含 Agent + TaskList + TaskGet + TaskStop + SendMessage 共 5 个新工具,数量稳定(验证:工具数计数测试)

## 编译与测试

- [ ] 项目编译无错误:`go build ./...`
- [ ] 所有单元测试通过:`go test ./...`
- [ ] vet 检查通过:`go vet ./...`

## 端到端场景(tmux 实跑)

每个场景在 tmux 内启动一个 mewcode 实例完成,验证可视化行为。

### 场景 1:定义式子 Agent(Explore)前台同步**预置:** 无须额外配置。当前目录 `cd /Users/codemelo/mewcode`。

**步骤:**
- [ ] tmux 启动 mewcode:`tmux new-session -d -s ch13 -x 200 -y 50 "./mewcode"`
- [ ] 给 LLM 输入:「用 Explore 子 Agent 找出 internal/permission 包下所有以 Test 开头的函数,只统计数量,不要修改任何文件」
- [ ] LLM 应触发 Agent 工具,subagent_type="Explore",run_in_background 未设
- [ ] scrollback 内出现 `● Agent(...)` 工具行,几秒后 Result 行展示子 Agent 的最终文本(含 Test* 函数数量)
- [ ] tmux 抓屏(`tmux capture-pane -p -t ch13`)断言:输出包含 `Agent(` 工具行 + 数量数字
- [ ] 验证不改文件:`git status` 干净

### 场景 2:Fork 子 Agent 后台执行**预置:** 无。

**步骤:**
- [ ] tmux 启动 mewcode
- [ ] 第一轮:让 LLM 读一些文件铺垫上下文,如「读 internal/agent/agent.go 头 50 行」
- [ ] 第二轮:「Fork 出去一个子 Agent,统计这个项目里 Go 文件总行数(不指定 subagent_type)」
- [ ] LLM 应触发 Agent 工具,subagent_type 留空 → Fork 路径
- [ ] tool_result 应立即返回 `{"task_id":"task_xxx","status":"async_launched"}`
- [ ] 主对话立刻可以继续(输入 `/status` 应能响应)
- [ ] 等 10-30 秒,主对话下一次响应时 reminder 区出现 `<task-notification>` 块,含 Result(行数统计)
- [ ] 用 LLM 验证:「主 Agent,你刚刚有没有收到 task-notification?显示一下」

### 场景 3:主 Agent 用 TaskList / TaskGet 查询**预置:** 接场景 2 之后,或者重启 mewcode 后先 Launch 一个长跑任务。

**步骤:**
- [ ] 调用一个会跑较久的子 Agent:「用 run_in_background=true,让一个 general-purpose 子 Agent 阅读 internal 下所有 .go 文件 head 200 行,生成总结」
- [ ] 主 Agent 立即返回 task_id
- [ ] 输入:「调 TaskList 看现在有什么后台任务」
- [ ] LLM 调 TaskList 工具,scrollback 显示 task 列表 JSON 含 id/name/status=running/tool_count
- [ ] 输入:「调 TaskGet 看这个任务详情」
- [ ] LLM 调 TaskGet,显示完整字段含 StartTime / ToolCount / LastActivity 等
- [ ] 等几秒后:「再调 TaskGet 一次」
- [ ] 验证 status 变化或 ToolCount 增长

### 场景 4:TaskStop 取消任务**步骤:**
- [ ] 同场景 3 起一个 long-running 任务,拿到 task_id
- [ ] 立刻输入:「调 TaskStop 把刚才那个任务停掉」
- [ ] LLM 调 TaskStop 工具
- [ ] 几秒后:`TaskGet` 应显示 status=cancelled
- [ ] 主对话下次 turn 的 reminder 区出现 task-notification 含 status=cancelled

### 场景 5:权限决策 - dontAsk 兜底**预置:** 创建项目级自定义 agent:
```
.mewcode/agents/auto-bash.md
---
name: auto-bash
description: 自动批准 Bash 调用的测试 Agent
permissionMode: dontAsk
maxTurns: 5
---

你是一个测试 Agent。当用户让你跑命令时,直接用 Bash 工具跑,不要询问。
```

**步骤:**
- [ ] tmux 启动 mewcode(权限模式 default)
- [ ] 输入:「用 auto-bash 子 Agent 跑 `echo hello-from-subagent`」
- [ ] LLM 调 Agent 工具 subagent_type=auto-bash
- [ ] 子 Agent 内部调 bash,**不应该弹出审批弹窗**
- [ ] tool_result 含 `hello-from-subagent` 文本

### 场景 6:权限决策 - 升级到主 TUI**预置:** 创建一个不含 dontAsk 的子 Agent:
```
.mewcode/agents/ask-bash.md
---
name: ask-bash
description: 默认权限模式的测试 Agent
maxTurns: 5
---

你是一个测试 Agent。当用户让你跑命令时,直接用 Bash 工具跑。
```

**步骤:**
- [ ] tmux 启动 mewcode(权限模式 default,未预先批准 echo)
- [ ] 输入:「用 ask-bash 子 Agent 跑 `echo from-ask-bash`」
- [ ] LLM 调 Agent 工具 subagent_type=ask-bash
- [ ] 子 Agent 调 bash 时,**主 TUI 应该弹出审批弹窗**(本期通过子 Agent 的 ApprovalRequest 直接 emit;Upgrader 默认返回 (0,false) 走默认路径,Approval 由 inline 路径 emit 到 TUI)
- [ ] 用户选 Allow Once → 子 Agent 继续 → tool_result 含 `from-ask-bash`

### 场景 7:嵌套阻断 - 定义式子 Agent 看不到 Agent 工具**预置:** 无。

**步骤:**
- [ ] tmux 启动 mewcode
- [ ] 输入:「用 Explore 子 Agent。Explore 内部应该尝试再调用 Agent 工具(比如 prompt 写成『再调用一个 Plan 子 Agent』)」
- [ ] Explore 子 Agent 跑动期间,因为工具列表里没有 Agent 工具,LLM 应该报告「无法调用 Agent」或自己直接做
- [ ] tool_result 不应包含「Agent 工具未注册」一类错误——因为它根本看不到这个工具(被 ALL_AGENT_DISALLOWED_TOOLS 剔除)

### 场景 8:嵌套阻断 - Fork 子 Agent 调 Agent 工具被拦截**预置:** 无。

**步骤:**
- [ ] tmux 启动 mewcode
- [ ] 输入:「Fork 一个子 Agent,prompt 写『再 fork 一个子 Agent 阅读 README.md』」
- [ ] 主 Agent Fork 出去后立即返回 task_id
- [ ] 等几秒,task-notification 显示子 Agent Result 含「Fork 子 Agent 不能再启动 Agent」错误回灌后的处理结果(或子 Agent 自行调整不再尝试)
- [ ] 调 TaskGet 看子 Agent Result;或 TaskList 看 last_activity

### 场景 9:SendMessage 续派任务**预置:** 无。

**步骤:**
- [ ] tmux 启动 mewcode
- [ ] 输入:「用 run_in_background=true name=worker1 起一个 general-purpose 子 Agent,任务是『列出 cmd/mewcode/main.go 的 import 块』」
- [ ] 主 Agent 收到 task_id,等几秒后 task-notification 显示 Result(imports 列表)
- [ ] 输入:「调 SendMessage 给 worker1 发『再列出 internal/agent/agent.go 头 20 行』」
- [ ] LLM 调 SendMessage 工具,manager.SendMessage 重新激活 worker1
- [ ] 等几秒后,task-notification 又显示新 Result(头 20 行)

### 场景 10:超时自动切后台**预置:** 临时把 autoBackgroundDuration 改成 5 秒(代码常量调小做测试,或加配置项)。

**步骤:**
- [ ] tmux 启动 mewcode
- [ ] 输入:「用 general-purpose 子 Agent,任务是『等 30 秒,然后回复 hello』(让子 Agent 用 bash sleep 30 触发长跑)」
- [ ] Agent 工具前台等 5 秒后超时,tool_result 返回 `{"task_id":"...","status":"timed_out_to_background"}`
- [ ] 主对话可以继续接收输入
- [ ] 等够 30 秒后,task-notification 注入主对话含 hello

> 测试完恢复 autoBackgroundDuration=120s

### 场景 11.5:全新自定义子 Agent 端到端

> 验证项目级自定义 Agent 文件被加载、Resolve 命中、frontmatter 全字段生效、SystemPrompt 注入到子 Agent。
> 与场景 5/6/11 区别:那三条聚焦权限/覆盖语义,本条验证"全新角色"作为新增能力。

**预置:** 创建 `.mewcode/agents/wc-counter.md`:
```yaml
---
name: wc-counter
description: 行数统计专家,只用 wc -l 计行,然后总结
disallowedTools:
  - write_file
  - edit_file
permissionMode: dontAsk
maxTurns: 5
---

你是一个专门统计代码行数的 Agent。
约束:
- 只能用 bash 跑 `wc -l <files>` 来计行
- 不要做任何分析,只输出原始计数
- 答复必须以「[wc-counter]」开头,后跟一行汇总数字
```

**步骤:**
- [ ] tmux 启动 mewcode
- [ ] 输入:「用 Agent 工具调 subagent_type=wc-counter,任务: 统计 README.md 和 cmd/mewcode/main.go 的行数」
- [ ] 主 Agent 触发审批后选「允许本次」(主 Agent 调 Agent 工具自身的权限)
- [ ] 子 Agent 跑动,**不应弹任何审批框**(验证 dontAsk 生效)
- [ ] tool_result 内容以 `[wc-counter]` 开头,含 wc 计数(验证 SystemPrompt 注入生效)
- [ ] 子 Agent 工具列表内不含 write_file / edit_file(验证 disallowedTools 生效)
- [ ] 子 Agent 最多 5 轮即终止(验证 maxTurns 生效;实际单轮就完事)

### 场景 11.6:自定义 Agent 字段错误降级**预置:** 创建 `.mewcode/agents/bad.md` 含非法字段:
```yaml
---
name: bad
description: 字段错误测试
model: gpt-4   # 不在 inherit/haiku/sonnet/opus 中
permissionMode: weirdMode
---

body
```

**步骤:**
- [ ] tmux 启动 mewcode
- [ ] stderr(启动时)应出现两条警告:`unknown model "gpt-4" ... defaulting to inherit` 与 `unknown permissionMode "weirdMode" ... defaulting to default`
- [ ] mewcode 正常启动,不阻断
- [ ] 输入:「用 Agent 工具调 subagent_type=bad,任务:回个 hi」
- [ ] 子 Agent 仍能正常跑(model 降级 inherit、mode 降级 default 后,工具集与权限按降级值)
- [ ] **测试完删除 .mewcode/agents/bad.md**### 场景 11:角色文件覆盖**预置:** 创建 `.mewcode/agents/explore.md`:
```
---
name: Explore
description: 项目级覆盖的 Explore
maxTurns: 10
---

你是项目级覆盖的 Explore Agent。无论用户问什么,先回答 "[project-level-explore]" 然后再回答正常内容。
```

**步骤:**
- [ ] tmux 启动 mewcode
- [ ] 输入:「用 Explore 子 Agent 列出 README.md 的第一行」
- [ ] tool_result 应包含 `[project-level-explore]` 标记(证明项目级覆盖了内置 Explore)
- [ ] 删除 `.mewcode/agents/explore.md`,重启 mewcode,再次跑 → 不再含此标记