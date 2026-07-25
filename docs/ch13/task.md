# SubAgent 机制 Tasks## 文件清单

| 操作 | 文件 | 职责 |
|------|------|------|
| 新建 | `internal/subagent/doc.go` | 包注释 |
| 新建 | `internal/subagent/definition.go` | Definition / Source 类型 |
| 新建 | `internal/subagent/parser.go` | parseFrontmatterAndBody + validateMeta |
| 新建 | `internal/subagent/parser_test.go` | 解析与字段校验单测 |
| 新建 | `internal/subagent/catalog.go` | Catalog + LoadCatalog / Resolve / List / ForkDefinition |
| 新建 | `internal/subagent/catalog_test.go` | 多来源加载与覆盖测试 |
| 新建 | `internal/subagent/embed.go` | go:embed builtin/*.md + builtinDefinitions() |
| 新建 | `internal/subagent/builtin/general-purpose.md` | 内置 general-purpose 定义 |
| 新建 | `internal/subagent/builtin/explore.md` | 内置 Explore 定义 |
| 新建 | `internal/subagent/builtin/plan.md` | 内置 Plan 定义 |
| 新建 | `internal/subagent/launch.go` | LaunchFork / 公用 wiring 辅助函数 |
| 新建 | `internal/subagent/launch_test.go` | LaunchFork 流程测试 |
| 新建 | `internal/task/doc.go` | 包注释 |
| 新建 | `internal/task/manager.go` | Manager + BackgroundTask + Launch / Adopt / Stop / SendMessage / SubscribeDone |
| 新建 | `internal/task/manager_test.go` | 后台任务全生命周期测试 |
| 新建 | `internal/task/tools.go` | 4 个内置工具 NewTaskListTool / NewTaskGetTool / NewTaskStopTool / NewSendMessageTool |
| 新建 | `internal/task/tools_test.go` | 4 个工具的单测 |
| 新建 | `internal/agent/run_to_completion.go` | RunToCompletion 方法实现 |
| 新建 | `internal/agent/run_to_completion_test.go` | RunToCompletion / dontAsk / maxTurns 测试 |
| 新建 | `internal/agent/fork.go` | BuildForkedMessages + IsForkContext + ForkBoilerplate |
| 新建 | `internal/agent/fork_test.go` | Fork 消息构造与上下文识别测试 |
| 新建 | `internal/agent/agent_tool.go` | NewAgentTool + AgentTool.Execute |
| 新建 | `internal/agent/agent_tool_test.go` | Agent 工具调用、嵌套阻断、超时切后台测试 |
| 新建 | `internal/agent/permission_upgrade.go` | ApprovalUpgrader 类型 + DefaultUpgrader |
| 新建 | `internal/tool/filter.go` | ALL_AGENT_DISALLOWED / ASYNC_AGENT_ALLOWED / ApplyAgentToolFilter |
| 新建 | `internal/tool/filter_test.go` | 过滤多层防线测试 |
| 新建 | `internal/tui/tasks.go` | consumeTaskDone + buildTaskNotification + ESC 切后台辅助 |
| 修改 | `internal/agent/agent.go` | 加 systemPrompt/maxTurns/permissionMode/dontAsk/approvalUpgrader 字段;Run 抽 runIter;runGuarded 加 dontAsk 短路 + approvalUpgrader 升级 |
| 修改 | `internal/agent/runtime.go` | 加 WithSystemPrompt / WithMaxTurns / WithPermissionMode / WithDontAsk / WithApprovalUpgrader / WithProvider 选项 |
| 修改 | `internal/agent/agent_test.go` | 不破坏既有测试 |
| 修改 | `internal/tool/registry.go` | 不动(过滤逻辑在 filter.go) |
| 修改 | `internal/tui/tui.go` | TUIParams 加 TaskMgr/SubAgentCatalog;Model 持有;Init 启 consumeTaskDone;AgentTool 注册后 SetParent |
| 修改 | `internal/tui/stream.go` | updateStreaming 加 ESC → AdoptRunning 分支 |
| 修改 | `internal/tui/skill_fork.go` | 改造为调 subagent.LaunchFork |
| 修改 | `internal/tui/tui_test.go` | 补 ESC 切后台、task-notification 注入测试 |
| 修改 | `internal/config/config.go` | 加 EnableSubAgentBackground bool(默认 true) |
| 修改 | `cmd/mewcode/main.go` | LoadCatalog / NewManager / 4 个 task 工具注册 / Agent 工具注册 + SetParent;TaskMgr / SubAgentCatalog 传给 tui.New |

## T1: subagent 包的 Definition 与 Source 类型**文件:** `internal/subagent/definition.go`
**依赖:** 无
**步骤:**
1. 新建包 `subagent`,加 `definition.go`,声明 `Source int` 类型与四个常量:
   - `SourceBuiltin Source = iota`
   - `SourceUser`
   - `SourceProject`
   - `SourcePlugin`(占位)
2. `Source.String()` 返回 `"builtin" / "user" / "project" / "plugin"`,越界返回 `"unknown"`
3. 声明 `Definition` 结构体,字段如 plan.md 所述:`Name / Description / Tools / DisallowedTools / Model / MaxTurns / PermissionMode / DontAsk / Background / SystemPrompt / FilePath / Source`
4. 注释每个字段语义,引用 spec F4
5. `Definition.IsFork()` 返回 `d.Name == "__fork__"`(便于 ForkDefinition 判别)

**验证:** `go build ./internal/subagent/...` 编译通过

## T2: subagent 解析器**文件:** `internal/subagent/parser.go`
**依赖:** T1
**步骤:**
1. 新建 `parser.go`,从 `skills/parser.go` 复制 `parseFrontmatterAndBody` 与 `utf8BOM` 常量(几乎 ✓ 不变,改为 `subagent` 包名)
2. 声明 `agentNameRegex = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-_]{0,31}$`)`(大小写都允许,与 ch13 README 的 `Explore`/`Plan` 一致)
3. 实现 `ParseDefinition(data []byte, filePath string, source Source) (*Definition, error)`:
   - 调 parseFrontmatterAndBody 拿 frontmatter map + body
   - YAML unmarshal 到一个临时 struct `agentFM`:
     ```go
     type agentFM struct {
         Name           string   `yaml:"name"`
         Description    string   `yaml:"description"`
         Tools          []string `yaml:"tools,omitempty"`
         DisallowedTools []string `yaml:"disallowedTools,omitempty"`
         Model          string   `yaml:"model,omitempty"`
         MaxTurns       int      `yaml:"maxTurns,omitempty"`
         PermissionMode string   `yaml:"permissionMode,omitempty"`
         Background     bool     `yaml:"background,omitempty"`
     }
     ```
   - 校验 Name 非空且匹配 agentNameRegex
   - 校验 Description 非空
   - 校验 Model:空 / "inherit" / "haiku" / "sonnet" / "opus" 之一,其它 stderr 警告并改为 "inherit"
   - 解析 PermissionMode:"dontAsk" 单独识别 → Definition.DontAsk=true, Definition.PermissionMode=ModeDefault;否则调 `permission.ParseMode`,失败 stderr 警告并改为 ModeDefault
   - 把 fm 字段映射到 Definition 字段(SystemPrompt = body,FilePath = filePath,Source = source)
4. 实现 `ParseFile(path string, source Source) (*Definition, error)`:`os.ReadFile` + `ParseDefinition`

**验证:** `go test ./internal/subagent/ -run TestParse -v` 通过(对应 T3 的测试)

## T3: subagent 解析器测试**文件:** `internal/subagent/parser_test.go`
**依赖:** T2
**步骤:**
1. 表驱动测试:正常完整 frontmatter / 仅必填 / model 非法 → 警告 fallback / permissionMode=dontAsk → DontAsk=true / 缺 name 报错 / 缺 description 报错 / frontmatter 未关闭 → 错误
2. body 区段提取:验证 `---` 后的内容(去 BOM 去前导换行)被完整取到 SystemPrompt
3. 测试 ParseFile 读取一个 testdata/*.md 文件
4. 表驱动写法,每个用例附 `t.Errorf("case %s: ...", name)` 描述

**验证:** `go test ./internal/subagent/ -run TestParse -v` 全部通过

## T4: 内置 Agent 定义文件**文件:** `internal/subagent/builtin/{general-purpose,explore,plan}.md`
**依赖:** 无
**步骤:**
1. 创建目录 `internal/subagent/builtin/`
2. `general-purpose.md`:
   ```yaml
   ---
   name: general-purpose
   description: 通用子 Agent,拥有全部工具,用于需要完整能力但独立上下文的场景
   maxTurns: 30
   ---

   你是 MewCode 的通用 Agent。根据用户的消息,使用可用工具完成任务。
   把任务做完,不要过度设计,但也不要做一半就停。
   完成后用简洁的报告回复:做了什么、关键发现。
   调用方会把结果转述给用户,所以只需要包含要点。
   ```
3. `explore.md`:
   ```yaml
   ---
   name: Explore
   description: 只读代码探索 Agent,适合搜索、阅读、理清调用链;不能修改文件
   disallowedTools:
     - write_file
     - edit_file
   model: haiku
   maxTurns: 30
   ---

   你是一个文件搜索专家。这是一个只读探索任务。
   严禁:创建文件、修改文件、删除文件、执行任何改变系统状态的命令。
   工具策略:Glob 做文件模式匹配、Grep 搜索文件内容、Read 读取已知路径、Bash 仅用于只读操作(ls、git log、find、cat)。
   尽可能并行发起多个工具调用。高效完成搜索请求,清晰报告发现。
   ```
4. `plan.md`:
   ```yaml
   ---
   name: Plan
   description: 计划 Agent,分析需求、制定执行计划,但不直接执行;主 Agent 拿到计划后逐步执行
   disallowedTools:
     - write_file
     - edit_file
     - Agent
   maxTurns: 15
   permissionMode: plan
   ---

   你是一个软件架构师和规划专家。这是一个只读规划任务。
   严禁:创建文件、修改文件、删除文件、执行任何改变系统状态的命令。
   工作流程:① 理解需求 ② 用搜索工具充分探索代码库 ③ 设计方案 ④ 输出分步实现计划。
   回复末尾必须列出 3-5 个对实现最关键的文件路径。
   ```

**验证:** 三个 .md 文件存在,frontmatter 合法;ParseFile 测试不报错

## T5: subagent embed 与内置加载**文件:** `internal/subagent/embed.go`
**依赖:** T2, T4
**步骤:**
1. 新建 `embed.go`,声明:
   ```go
   //go:embed builtin/*.md
   var builtinFS embed.FS
   ```
2. 实现 `builtinDefinitions() []*Definition`:
   - `fs.ReadDir(builtinFS, "builtin")` 列文件
   - 对每个 .md 文件:`fs.ReadFile(builtinFS, "builtin/"+name)` + `ParseDefinition(data, "builtin:"+name, SourceBuiltin)`
   - 解析失败 panic(代码 bug,启动期失败即灾难)
3. 返回 slice,按 name 升序

**验证:** `go test ./internal/subagent/ -run TestBuiltin -v` 通过(T7)

## T6: Catalog 与三层加载**文件:** `internal/subagent/catalog.go`
**依赖:** T1, T2, T5
**步骤:**
1. 新建 `catalog.go`,声明:
   ```go
   type Catalog struct {
       mu       sync.Mutex
       defs     map[string]*Definition
       bySource map[Source][]*Definition
   }
   ```
2. 实现 `LoadCatalog(root string) *Catalog`:
   - `c := &Catalog{defs: map[string]*Definition{}, bySource: map[Source][]*Definition{}}`
   - 加载 builtin → `c.addAll(builtinDefinitions(), SourceBuiltin)`
   - 加载 user → `c.addAll(loadFromDir(filepath.Join(homeDir, ".mewcode/agents"), SourceUser), SourceUser)`
   - 加载 project → `c.addAll(loadFromDir(filepath.Join(root, ".mewcode/agents"), SourceProject), SourceProject)`
   - plugin 层本期跳过
3. 实现 `loadFromDir(dir string, source Source) []*Definition`:
   - 目录不存在 → 返回 nil
   - 遍历 *.md 文件,逐个 ParseFile;失败 stderr 警告并跳过
   - 返回 slice
4. 实现 `addAll(defs []*Definition, source Source)`:
   - 同名时高优先级覆盖(因为按 builtin → user → project 顺序加载,后加的优先级更高)
   - 同时往 `bySource[source]` 追加
5. 实现 `Resolve(name string) (*Definition, bool)`
6. 实现 `List() []*Definition`(按 name 升序)
7. 实现 `ListBySource(s Source) []*Definition`
8. 实现 `ForkDefinition() *Definition`:
   ```go
   return &Definition{
       Name: "__fork__",
       Description: "Fork-based subagent",
       Model: "inherit",
       MaxTurns: 25,
       PermissionMode: permission.ModeDefault,
       // Tools/DisallowedTools 留空 -> 工具集继承父
   }
   ```

**验证:** `go test ./internal/subagent/ -run TestCatalog -v` 通过

## T7: Catalog 测试**文件:** `internal/subagent/catalog_test.go`
**依赖:** T6
**步骤:**
1. 测试 builtinDefinitions 返回 3 个 def(general-purpose / Explore / Plan)
2. 测试三层覆盖:用 t.TempDir() 造一个项目 root 与一个 HOME 路径(set/unset HOME 环境变量),分别放 explore.md
3. 验证 Resolve("Explore") 在三种情形下返回的 Source 正确(都有 → project;只有 user+builtin → user;只有 builtin → builtin)
4. 测试 ForkDefinition 返回 IsFork()=true
5. 测试加载错误处理:放一个非法 frontmatter 文件,加载后该文件 *被跳过*,其他文件仍正常

**验证:** `go test ./internal/subagent/ -v` 全部通过

## T8: 工具过滤多层防线**文件:** `internal/tool/filter.go`
**依赖:** 无
**步骤:**
1. 新建 `filter.go`,声明三个全局变量:
   ```go
   var ALL_AGENT_DISALLOWED_TOOLS = []string{"Agent"}
   var CUSTOM_AGENT_DISALLOWED_TOOLS = []string{}
   var ASYNC_AGENT_ALLOWED_TOOLS = []string{
       "read_file", "write_file", "edit_file",
       "glob", "grep",
       "bash",
       "load_skill", "install_skill",
   }
   ```
2. 声明 `FilterParams` 结构体:
   ```go
   type FilterParams struct {
       All        []string  // registry 的全部工具名
       Source     int       // 1=builtin, 2=user, 3=project, 4=plugin(数值需与 subagent.Source 对齐,这里用 int 避免反向依赖)
       Background bool
       Allowed    []string  // Agent 定义的 tools 白名单
       Disallowed []string  // Agent 定义的 disallowedTools 黑名单
   }
   ```
3. 实现 `ApplyAgentToolFilter(p FilterParams) []string`:
   按 spec F30 顺序:
   - 起点 = `p.All` 副本
   - 过滤 1:去除 `ALL_AGENT_DISALLOWED_TOOLS`
   - 过滤 2:若 `p.Source >= 2`(非 builtin),再去除 `CUSTOM_AGENT_DISALLOWED_TOOLS`(本期为空,跳过)
   - 过滤 3:若 `p.Background`,与 `ASYNC_AGENT_ALLOWED_TOOLS + isMCPOrSkill(name)` 取交集
   - 过滤 4:去除 `p.Disallowed`
   - 过滤 5:若 `len(p.Allowed) > 0`,与之取交集
4. 辅助函数 `isMCPOrSkill(name string) bool`:`strings.HasPrefix(name, "mcp__")` || ...对 skill 工具的识别本期暂不接入(主 Registry 不区分,先按名字前缀 + 内置基础工具白名单兜底)

**验证:** `go build ./internal/tool/...` 编译通过

## T9: 工具过滤测试**文件:** `internal/tool/filter_test.go`
**依赖:** T8
**步骤:**
1. 表驱动测试 ApplyAgentToolFilter 覆盖各组合:
   - 默认:无后台、无白名单、无黑名单 → 去 Agent 即可
   - 后台:取 ASYNC_AGENT_ALLOWED_TOOLS 交集
   - 黑名单:`disallowed=[bash]` → 不含 bash
   - 白名单:`allowed=[read_file, grep]` → 仅这两个
   - 黑+白:白名单先收窄,黑名单再剔除
   - 后台 + MCP 工具:MCP 工具(mcp__xxx)被保留(白名单 OK)
2. 单独测试 isMCPOrSkill 边界

**验证:** `go test ./internal/tool/ -run TestApplyAgentToolFilter -v` 通过

## T10: Agent 包扩展 - 新增 Option**文件:** `internal/agent/runtime.go`
**依赖:** 无
**步骤:**
1. 在 `Agent` 结构体加字段(agent.go):
   ```go
   systemPrompt     string
   maxTurns         int  // 0=用全局 maxIterations
   permissionMode   permission.Mode
   permissionModeSet bool   // 区分零值与未设置
   dontAsk          bool
   approvalUpgrader ApprovalUpgrader
   ```
2. 在 `runtime.go` 加 6 个新 Option:
   ```go
   func WithSystemPrompt(s string) Option { return func(a *Agent) { a.systemPrompt = s } }
   func WithMaxTurns(n int) Option { return func(a *Agent) { if n > 0 { a.maxTurns = n } } }
   func WithPermissionMode(m permission.Mode) Option { return func(a *Agent) { a.permissionMode = m; a.permissionModeSet = true } }
   func WithDontAsk(b bool) Option { return func(a *Agent) { a.dontAsk = b } }
   func WithApprovalUpgrader(fn ApprovalUpgrader) Option { return func(a *Agent) { a.approvalUpgrader = fn } }
   func WithProvider(p llm.Provider) Option { return func(a *Agent) { a.provider = p } }
   ```
3. 加注释解释每个选项语义

**验证:** `go build ./internal/agent/...` 编译通过

## T11: ApprovalUpgrader 类型**文件:** `internal/agent/permission_upgrade.go`
**依赖:** T10
**步骤:**
1. 新建文件,声明:
   ```go
   type ApprovalUpgrader func(ctx context.Context, req *ApprovalRequest) (permission.Outcome, bool)
   ```
2. 注释解释:子 Agent 把审批请求升级到父 TUI 的回调;返回 (outcome, ok)——ok=false 时调用方应走默认 emit Approval 路径

**验证:** `go build ./internal/agent/...` 编译通过

## T12: Fork 路径辅助函数**文件:** `internal/agent/fork.go`
**依赖:** 无(纯函数)
**步骤:**
1. 新建 `fork.go`,声明常量:
   ```go
   const ForkBoilerplateTag = "<fork_boilerplate>"

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
   ```
2. 实现 `BuildForkedMessages(parentMsgs []llm.Message, task string) []llm.Message`:
   - 深拷贝 parentMsgs(参考 conversation.NewFromMessages 的拷贝逻辑)
   - 扫描末尾 assistant 消息的 ToolCalls:对于每个未配对的 ToolCallID,在 cloned 末尾追加 RoleTool 消息(每个 ID 一条 placeholder ToolResult{Content:"[forked, skipped]", IsError:true})
     - 配对检查:看看 cloned 后续是否有 RoleTool 消息消费这些 ID
   - 追加最后一条 user 消息:`Content = ForkBoilerplate + task`
3. 实现 `IsForkContext(msgs []llm.Message) bool`:
   - 遍历 msgs,若 user/tool/assistant 消息内容含 `ForkBoilerplateTag` → 返回 true
   - 默认 false

**验证:** `go test ./internal/agent/ -run TestFork -v` 通过(T13)

## T13: Fork 辅助函数测试**文件:** `internal/agent/fork_test.go`
**依赖:** T12
**步骤:**
1. 测试 BuildForkedMessages 空 parent → 返回单条 user 消息含 Boilerplate + task
2. 测试 parent 末尾有完整 assistant + tool_result 配对:cloned 末尾 == parent 末尾 + 一条 user
3. 测试 parent 末尾 assistant 有 2 个 tool_use 没配对:cloned 中追加 1 条 RoleTool(2 个 placeholder ToolResult)再追加 1 条 user
4. 测试 IsForkContext:消息中含 Boilerplate → true;不含 → false

**验证:** `go test ./internal/agent/ -run TestFork -v` 通过

## T14: runGuarded 加 dontAsk 短路与 approvalUpgrader**文件:** `internal/agent/agent.go`
**依赖:** T10, T11
**步骤:**
1. 修改 `runGuarded`,在 `default: // Ask` 分支里:
   ```go
   case Ask:
       // 子 Agent dontAsk 模式:直接 Allow
       if a.dontAsk {
           return a.runTool(ctx, c), true
       }
       // 子 Agent 升级到父 TUI 审批
       if a.approvalUpgrader != nil {
           if o, ok := a.approvalUpgrader(ctx, &ApprovalRequest{
               Name: c.Name, Args: argsPreview(c.Input), Reason: reason,
               Respond: nil, // upgrader 内部处理 Respond
           }); ok {
               switch o {
               case permission.OutcomeAllowOnce: return a.runTool(ctx, c), true
               case permission.OutcomeAllowForever: _ = a.eng.PersistLocalAllow(c); return a.runTool(ctx, c), true
               default: return denyResult(c.ID, "用户拒绝了本次调用"), true
               }
           }
       }
       // 默认路径:emit Approval event(主 Agent inline / Skill fork 都走此)
       o, ok := a.requestApproval(ctx, c, reason, ch)
       ...
   ```
2. 修改 `Check` 调用前,如果子 Agent 设了 permissionMode(`a.permissionModeSet=true`),用 `a.permissionMode` 覆盖入参 mode
3. 修改 streamLoop 拿 defs 处的 allowedTools 逻辑(已有,无须改)

**验证:** `go test ./internal/agent/ -v` 现有测试不破

## T15: RunToCompletion 实现**文件:** `internal/agent/run_to_completion.go`
**依赖:** T10, T14
**步骤:**
1. 新建文件,实现:
   ```go
   func (a *Agent) RunToCompletion(ctx context.Context, conv *conversation.Conversation, task string, events chan<- Event) (string, error)
   ```
2. 逻辑:
   - 把 task 作为 user 消息:`conv.AddUser(task)`(注意 conv 可能已经被 Fork 路径预装填)
   - 计算 maxTurns:`turns := a.maxTurns; if turns == 0 { turns = maxIterations }`
   - 复用 Run 的循环逻辑:但不用 channel,直接内部消费;改为返回 finalText + err
   - 拆出 helper `runIter(ctx, conv, mode, iter, defs, sys, envText, reminder, eventsChan) (text string, calls []llm.ToolCall, done bool, err error)` 让 Run 和 RunToCompletion 都调
   - `Run` 改造为 调 runIter 逐轮;RunToCompletion 也是
   - 子 Agent 用模式:`mode := permission.ModeDefault; if a.permissionModeSet { mode = a.permissionMode }`
3. 退出条件:`done==true`(模型不再调工具)→ 返回 finalText;触达 turns → 返回 finalText + ErrMaxTurnsReached;ctx 取消 → 返回 finalText + ctx.Err();出错 → 返回 finalText + err
4. 在每轮内继续做 hook 调度(PreToolUse/PostToolUse/Stop 等),但 SubAgent 不触发 memory update
5. events 通道转发:把 Tool / Text / Approval 事件转发出去(供 TaskManager / TUI 接收)

**验证:** `go test ./internal/agent/ -run TestRunToCompletion -v` 通过(T16)

## T16: RunToCompletion 测试**文件:** `internal/agent/run_to_completion_test.go`
**依赖:** T15
**步骤:**
1. 用 mock provider(已有 testhelpers)模拟一个回合返回纯文本的子 Agent → RunToCompletion 返回 "ok",err==nil
2. 模拟一个回合返回 tool_use(已知工具),下一轮返回纯文本 → 工具被执行、finalText="..."
3. 模拟模型一直调工具不出文本,触达 maxTurns=3 → 返回 ErrMaxTurnsReached
4. 测试 dontAsk:子 Agent 设 WithDontAsk(true) + 模型调一个 Ask 级工具(如 bash) → 工具被自动放行执行
5. 测试 approvalUpgrader 回调被命中:子 Agent 设了 upgrader,Ask 时 upgrader 被调用(用 mock upgrader 验证)
6. 测试 events channel 转发:运行子 Agent 时把 events 收集到 slice,断言含 Tool/Text 事件

**验证:** `go test ./internal/agent/ -run TestRunToCompletion -v` 全部通过

## T17: Agent 工具实现**文件:** `internal/agent/agent_tool.go`
**依赖:** T8, T12, T15
**步骤:**
1. 新建文件,声明:
   ```go
   type AgentTool struct {
       catalog   AgentCatalog  // 接口,避免反向依赖 subagent 包
       taskMgr   TaskManager
       parent    *Agent
       bgEnabled bool
   }

   type AgentCatalog interface {
       Resolve(name string) (*subagent.Definition, bool) // 暂时 fine,subagent 不依赖 agent
       ForkDefinition() *subagent.Definition
       List() []*subagent.Definition
   }

   type TaskManager interface {
       Launch(ctx context.Context, ag *Agent, conv *conversation.Conversation, name, task string) string
       AdoptRunning(ctx context.Context, ag *Agent, conv *conversation.Conversation, name string, ev <-chan Event, cancel context.CancelFunc, partial *PartialState) string
       UpgradeApproval(ctx context.Context, req *ApprovalRequest) (permission.Outcome, bool)
   }
   ```
2. **解决循环依赖**:agent 包要引用 subagent 包,但 subagent 不应反过来。检查 subagent.Definition 是否引用 agent 包——目前 Definition 只引用 permission 包,没问题。直接 import "mewcode/internal/subagent"。
3. **AgentTool 接口实现**:
   - Name() = "Agent"
   - Description() 动态:基础描述 + `subagent_type 可选值:" + strings.Join(catalog.List() 的 name, ", ")`
   - Parameters():按 spec F1 写 JSON Schema
   - ReadOnly() = false
   - Execute(ctx, args):
4. **Execute 主流程**:
   ```go
   var aArgs AgentArgs
   if err := json.Unmarshal(args, &aArgs); err != nil { 返回 err }
   if aArgs.Prompt == "" { 返回错误 "prompt is required" }
   if aArgs.Description == "" { 返回错误 "description is required" }

   // 防嵌套
   if isSubAgentContext(ctx) { 返回错误 "subagent cannot spawn Agent" }
   if conv := getParentConv(ctx); conv != nil && IsForkContext(conv.Messages()) { 返回错误 "Fork subagent cannot spawn Agent (boilerplate detected)" }

   // Resolve 定义
   var def *subagent.Definition
   if aArgs.SubagentType != "" {
       if d, ok := t.catalog.Resolve(aArgs.SubagentType); !ok { 返回错误 "unknown subagent_type: " + aArgs.SubagentType } else { def = d }
   } else {
       def = t.catalog.ForkDefinition()
   }

   // 决定后台
   background := def.Background || aArgs.RunInBackground || def.IsFork()
   if background && !t.bgEnabled { 返回错误 "background mode is disabled by config" }

   // 工具过滤
   allowed := tool.ApplyAgentToolFilter(tool.FilterParams{
       All:        registryAllNames(t.parent.registry),
       Source:     int(def.Source),
       Background: background,
       Allowed:    def.Tools,
       Disallowed: def.DisallowedTools,
   })

   // provider
   provider := t.parent.provider
   // (model 字段切换 provider 的逻辑暂从简:本期不实现按模型切换,后续完善)

   // 构造子 Agent
   subRuntime := NewSessionRuntime(200000)
   subAgent := New(provider, t.parent.registry, t.parent.version, t.parent.eng,
       WithRuntime(subRuntime),
       WithAllowedTools(allowed),
       WithSystemPrompt(def.SystemPrompt),
       WithMaxTurns(def.MaxTurns),
       WithPermissionMode(def.PermissionMode),
       WithDontAsk(def.DontAsk),
       WithApprovalUpgrader(t.taskMgr.UpgradeApproval),
       WithHookEngine(t.parent.hookEngine),
   )
   // 标记子 Agent 上下文(让递归 Agent 工具调用被拦截)
   childCtx := withSubAgentContext(ctx)

   // 子 Conv
   subConv := conversation.New()
   if def.IsFork() {
       parentMsgs := getParentConvMessages(ctx, t.parent)  // 从某种机制取父 conv;若 ctx 没带,fallback 报错
       forked := BuildForkedMessages(parentMsgs, aArgs.Prompt)
       subConv = conversation.NewFromMessages(forked, nil, nil)
   }

   // 后台路径
   if background {
       taskID := t.taskMgr.Launch(ctx, subAgent, subConv, aArgs.Name, aArgs.Prompt)
       return tool.Result{Content: fmt.Sprintf(`{"task_id":"%s","status":"async_launched"}`, taskID)}
   }

   // 前台路径
   timeoutCtx, cancel := context.WithTimeout(childCtx, autoBackgroundDuration)
   events := make(chan Event, 32)
   var partial PartialState
   go aggregatePartial(events, &partial)

   finalText, err := subAgent.RunToCompletion(timeoutCtx, subConv, aArgs.Prompt, events)
   close(events)

   if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
       taskID := t.taskMgr.AdoptRunning(ctx, subAgent, subConv, aArgs.Name, nil /* already done? */, cancel, &partial)
       return tool.Result{Content: fmt.Sprintf(`{"task_id":"%s","status":"timed_out_to_background"}`, taskID)}
   }
   cancel()
   if err != nil { return tool.Result{Content: "subagent error: " + err.Error(), IsError: true} }
   return tool.Result{Content: finalText}
   ```
5. 实现辅助函数:`isSubAgentContext / withSubAgentContext / getParentConvMessages / aggregatePartial`
6. 提供 `SetParent(a *Agent)` 让 main 在 tui.New 之后回填 parent 引用

**验证:** `go test ./internal/agent/ -run TestAgentTool -v` 通过(T18)

## T18: Agent 工具测试**文件:** `internal/agent/agent_tool_test.go`
**依赖:** T17
**步骤:**
1. 测试 missing prompt → 返回错误
2. 测试 unknown subagent_type → 返回错误
3. 测试 known subagent_type(用一个 mock catalog 注入)→ 子 Agent 跑动并返回结果
4. 测试 run_in_background=true → 返回 `async_launched` JSON
5. 测试嵌套:用 withSubAgentContext 包 ctx 后调 Execute → 返回错误
6. 测试 IsForkContext 兜底:用 forked subConv 调,Agent 工具拦截
7. 测试 EnableSubAgentBackground=false 时 background 路径报错

**验证:** `go test ./internal/agent/ -run TestAgentTool -v` 全部通过

## T19: task 包基础结构**文件:** `internal/task/manager.go`
**依赖:** T10, T15
**步骤:**
1. 新建包 `task`,加 doc.go 与 manager.go
2. 声明 `Status int` 与四个常量:`StatusRunning / StatusCompleted / StatusFailed / StatusCancelled`
3. 声明 `Usage` 结构体(对齐 agent.Usage)
4. 声明 `BackgroundTask` 结构体(字段如 plan.md)
5. 声明 `PartialState` 结构体
6. 声明 `Manager` 结构体:`mu sync.Mutex; tasks map[string]*BackgroundTask; byName map[string]string; done chan string; counter int64`
7. 实现 `NewManager() *Manager`:`done = make(chan string, 32)`,counter=0
8. 实现 `nextID() string`:`atomic.AddInt64(&counter, 1)` 后格式化为 `task_<8 字节十六进制>`(用 `time.Now().UnixNano() ^ counter` 取低 4 字节足够)
9. 实现 `Get(id)` / `List()` / `SubscribeDone()` 等查询方法

**验证:** `go build ./internal/task/...` 编译通过

## T20: Manager.Launch 实现**文件:** `internal/task/manager.go`
**依赖:** T19
**步骤:**
1. 实现:
   ```go
   func (m *Manager) Launch(parentCtx context.Context, ag *agent.Agent, conv *conversation.Conversation, name, taskText string) string {
       id := m.nextID()
       ctx, cancel := context.WithCancel(parentCtx)
       bt := &BackgroundTask{
           ID: id, Name: name, SubAgent: ag, Conv: conv, Task: taskText,
           Status: StatusRunning, StartTime: time.Now(), Cancel: cancel,
       }
       m.mu.Lock()
       m.tasks[id] = bt
       if name != "" { m.byName[name] = id }  // 后启动覆盖前
       m.mu.Unlock()

       go func() {
           defer func() {
               if r := recover(); r != nil {
                   bt.Status = StatusFailed
                   bt.Err = fmt.Errorf("subagent panic: %v", r)
                   bt.EndTime = time.Now()
               }
               select {
               case m.done <- id:
               default:
                   fmt.Fprintf(os.Stderr, "task manager: done channel full, dropping notification for %s\n", id)
               }
           }()

           events := make(chan agent.Event, 32)
           go aggregateTaskEvents(events, bt)

           text, err := ag.RunToCompletion(ctx, conv, taskText, events)
           close(events)

           bt.EndTime = time.Now()
           if err != nil {
               if errors.Is(err, context.Canceled) {
                   bt.Status = StatusCancelled
               } else {
                   bt.Status = StatusFailed
                   bt.Err = err
                   bt.Result = text
               }
           } else {
               bt.Status = StatusCompleted
               bt.Result = text
           }
       }()
       return id
   }
   ```
2. 实现 `aggregateTaskEvents(ch <-chan agent.Event, bt *BackgroundTask)`:每个 Tool PhaseStart 累加 ToolCount + 更新 LastActivity;每个 Usage 累加到 bt.Usage

**验证:** `go test ./internal/task/ -run TestLaunch -v` 通过(T22)

## T21: Manager.Stop / AdoptRunning / SendMessage / UpgradeApproval**文件:** `internal/task/manager.go`
**依赖:** T20
**步骤:**
1. 实现 `Stop(id) bool`:查 tasks → 调 task.Cancel();返回是否找到
2. 实现 `AdoptRunning(...)`:与 Launch 类似但接收已 derive 的 ag/conv/cancel/events;创建 BackgroundTask,把 PartialState 字段复制进去,起 goroutine 继续消费 ev 并跑动(注意此时 ag.RunToCompletion 已经在父 ctx 中跑;父 ctx 超时后子 ctx 也 done;Adopt 实际上是开一个 goroutine 继续消费 events channel 直到关闭)
   - 简化方案:Adopt 不调 RunToCompletion(因为 RunToCompletion 已在前台启动);只是注册 BackgroundTask 状态、聚合事件、等 events channel 关闭后写终态、push done
   - cancel 是新的 derive context 的 cancel,Stop 时用
3. 实现 `SendMessage(parentCtx, name, message)`:
   - 查 byName → id
   - 查 Get(id) → bt;bt.Status != Completed → ErrTaskBusy
   - bt.Conv.AddUser(message);bt.Status = StatusRunning;bt.StartTime/EndTime 不重置
   - 重新起 goroutine 跑 RunToCompletion(同样的 ag/conv);跑完逻辑同 Launch
   - 返回 (id, nil)
4. 实现 `UpgradeApproval(ctx, req) (Outcome, bool)`:把 req 转发到一个全局 channel(`approvalCh chan *agent.ApprovalRequest`);TUI 消费;返回 ok=false 时调用方走默认路径
   - 简化:本期 UpgradeApproval 直接返回 (0, false)——让 Approval 走到子 Agent 自己的 Run channel,TUI 通过 events 转发感知

**验证:** `go test ./internal/task/ -run TestStop -v` 通过

## T22: task 包测试**文件:** `internal/task/manager_test.go`
**依赖:** T20, T21
**步骤:**
1. 用 mock provider + mock agent 模拟一个 subAgent → Launch → 等 done channel → 验证 status=Completed, result 正确
2. 用一个故意 panic 的 mock agent → Launch → done 收到 → status=Failed,Err 非空
3. Stop:Launch 后立刻 Stop → done 收到 → status=Cancelled
4. SendMessage:Launch + 等 Completed → SendMessage 重新跑 → 拿到新结果
5. byName 覆盖:Launch 两次同 name → 后启动覆盖

**验证:** `go test ./internal/task/ -v` 全部通过

## T23: 4 个后台任务工具**文件:** `internal/task/tools.go`
**依赖:** T19, T20, T21
**步骤:**
1. 实现 `NewTaskListTool(m *Manager) tool.Tool`:
   - Name()="TaskList",ReadOnly()=true,Parameters() 空对象
   - Execute:返回 JSON 形如 `[{"id":"...","name":"...","status":"running","tool_count":3,"last_activity":"bash"},...]`
2. 实现 `NewTaskGetTool(m *Manager) tool.Tool`:
   - Name()="TaskGet",Parameters() 含 `task_id` required
   - Execute:Get(id) → 全字段 JSON;找不到 → IsError=true
3. 实现 `NewTaskStopTool(m *Manager) tool.Tool`:
   - Name()="TaskStop",Parameters() 含 `task_id` required
   - Execute:m.Stop(id) → `{"status":"cancellation_requested"}` 或 错误
4. 实现 `NewSendMessageTool(m *Manager) tool.Tool`:
   - Name()="SendMessage",Parameters() 含 `name` / `message` required
   - Execute:m.SendMessage(ctx, name, msg) → `{"task_id":"...","status":"resumed"}` 或 错误
5. 所有工具实现 tool.SystemTool(IsSystem 返回 true),让它们在子 Agent 工具列表中默认豁免

**验证:** `go test ./internal/task/ -run TestTools -v` 通过(T24)

## T24: 4 个工具的单测**文件:** `internal/task/tools_test.go`
**依赖:** T23
**步骤:**
1. TaskList:Launch 几个任务后调 → 返回 JSON 含所有
2. TaskGet:已知 id → 返回完整字段
3. TaskGet:未知 id → IsError=true
4. TaskStop:Stop 一个 running task → 返回成功 + task 状态变 Cancelled
5. SendMessage:Launch 一个任务跑完 → SendMessage → 返回新 status

**验证:** `go test ./internal/task/ -v` 全部通过

## T25: TUI 加 TaskMgr / SubAgentCatalog wiring**文件:** `internal/tui/tui.go`
**依赖:** T6, T19, T23
**步骤:**
1. 在 `TUIParams` 加字段:
   ```go
   TaskMgr         *task.Manager
   SubAgentCatalog *subagent.Catalog
   ```
2. 在 `Model` 加字段:
   ```go
   taskMgr         *task.Manager
   subAgentCatalog *subagent.Catalog
   ```
3. 在 `New` 内:
   - 把 params 的字段挂到 Model
   - Init() 末尾启动 `go m.consumeTaskDone()`
4. 在 `Agent` 构造之后(单 provider 路径):
   - 主 Agent 也应该携带 ApprovalUpgrader(其实主 Agent 不需要;但 Agent 工具构造时需要 ApprovalUpgrader 给子 Agent 用)
   - Agent 工具的 parent 通过 `SetParent(m.agent)` 回填

**验证:** `go build ./internal/tui/...` 编译通过

## T26: task notification 注入**文件:** `internal/tui/tasks.go`
**依赖:** T19, T25
**步骤:**
1. 新建文件,实现:
   ```go
   func (m *Model) consumeTaskDone() {
       for id := range m.taskMgr.SubscribeDone() {
           bt, ok := m.taskMgr.Get(id)
           if !ok { continue }
           notif := buildTaskNotification(bt)
           if m.runtime != nil {
               m.runtime.AppendReminders([]string{notif})
           }
       }
   }
   ```
2. 实现 `buildTaskNotification(bt *task.BackgroundTask) string`:
   ```
   <task-notification>
   Task <id> (name="<name>"): <status>
   Result: <result 或 错误>
   </task-notification>
   ```
3. 注释解释行为(F19)

**验证:** `go build ./internal/tui/...` 编译通过

## T27: ESC 切后台**文件:** `internal/tui/stream.go`
**依赖:** T19, T25
**步骤:**
1. 在 `updateStreaming` 内 `case streamMsg:` 之前加 `case tea.KeyPressMsg:`:
   ```go
   case tea.KeyPressMsg:
       if msg.String() == "esc" && m.foregroundSubAgent != nil {
           // 移交后台
           id := m.taskMgr.AdoptRunning(m.ctx, m.foregroundSubAgent.agent, m.foregroundSubAgent.conv, m.foregroundSubAgent.name, m.foregroundSubAgent.events, m.foregroundSubAgent.cancel, m.foregroundSubAgent.partial)
           m.foregroundSubAgent = nil
           // 显示一条通知
           return tea.Println(noticeBlock(fmt.Sprintf("[esc] 子 Agent 切到后台 (task=%s)", id)))
       }
       return nil
   ```
2. 增加 `foregroundSubAgent` 字段到 `Model` 跟踪当前前台子 Agent;Agent 工具开始前台跑动时设置,跑完清除
3. 注意:前台子 Agent 的跑动其实是在 Agent 工具的 Execute 内同步阻塞的,主 TUI 此时是 "等 tool_result" 状态。这意味着 ESC 拦截需要在 Agent 工具的 Execute 内做(通过 m.foregroundSubAgent 共享状态)

**简化方案:** 由于前台子 Agent 在 Agent 工具同步阻塞内,ESC 切后台需要工具内监听 ctx 一类机制。本期实现保守版:Agent 工具的前台路径只支持「超时自动切后台」,不支持 ESC 切后台;ESC 切后台留待后续 ch14+ 完善。在 plan.md 与 spec.md 里要标注这一变更。

**重要变更:** F17/AC11 调整为:本期 ESC 切后台**不实现**,只实现「超时自动切后台」与「显式 run_in_background」。spec.md 已写出,checklist 跳过 ESC 场景。

修改方向:跳过 T27 的 ESC 部分,只保留 foregroundSubAgent 字段供未来扩展。

**验证:** `go build ./internal/tui/...` 编译通过

## T28: Skill fork 改造**文件:** `internal/tui/skill_fork.go`
**依赖:** T15
**步骤:**
1. 现有 `runSubAgent` 内部已经在用 `subAgent.Run`;改造为用 `RunToCompletion`:
   ```go
   func (m *Model) runSubAgent(ctx context.Context, conv *conversation.Conversation, opts skills.ForkOptions) (string, error) {
       if m.provider == nil { return "", errSubAgentNoProvider }

       prov := m.provider
       // (model 切换逻辑保留)

       subRuntime := agent.NewSessionRuntime(200000)
       subAgent := agent.New(prov, m.registry, m.version, m.engine,
           agent.WithRuntime(subRuntime),
           agent.WithAllowedTools(opts.AllowedTools),
           agent.WithHookEngine(m.hookEngine),
       )

       // 直接调 RunToCompletion(events=nil,前台同步)
       finalText, err := subAgent.RunToCompletion(ctx, conv, "" /* 此处 conv 末尾已含 user task */, nil)
       if err != nil { return "", err }
       return finalText, nil
   }
   ```
2. **注意**:现有 skills.Executor 调用前已经把任务作为 user 消息装填到 conv(`buildForkConversation` 末尾 `conv.AddUser(rendered)`)。新版 RunToCompletion 内部又会 conv.AddUser(task);若 task="" 会追加空消息。**改 RunToCompletion 为允许 task="" 时不追加**(if task != "" { conv.AddUser(task) }),或者改 skills.Executor 不再装填 user 消息让 RunToCompletion 装填。
3. 选第一种方案——RunToCompletion 加 if 判断

**验证:** `go test ./internal/skills/... ./internal/tui/...` 现有测试不破

## T29: Agent 工具注册到 registry**文件:** `cmd/mewcode/main.go`
**依赖:** T17, T20, T23, T25
**步骤:**
1. 在 main.go 适当位置(skills.LoadCatalog 之后):
   ```go
   subagentCatalog := subagent.LoadCatalog(root)
   taskMgr := task.NewManager()

   // 4 个 task 工具
   registry.Register(task.NewTaskListTool(taskMgr))
   registry.Register(task.NewTaskGetTool(taskMgr))
   registry.Register(task.NewTaskStopTool(taskMgr))
   registry.Register(task.NewSendMessageTool(taskMgr))

   // Agent 工具(parent 暂为 nil,稍后 SetParent)
   agentTool := agent.NewAgentTool(subagentCatalog, taskMgr, nil, cfg.EnableSubAgentBackground)
   registry.Register(agentTool)
   ```
2. tui.New 调用扩展 TUIParams:
   ```go
   m, err := tui.New(... , tui.TUIParams{
       Writer:          writer,
       MemMgr:          memMgr,
       InstructionText: instructionText,
       MemoryText:      memoryText,
       SessionsDir:     sessionsDir,
       Catalog:         catalog,
       HookEngine:      hookEngine,
       TaskMgr:         taskMgr,
       SubAgentCatalog: subagentCatalog,
   })
   ```
3. tui.New 返回后回填 parent:
   ```go
   if a := m.MainAgent(); a != nil {
       agentTool.SetParent(a)
   }
   ```
4. tui.Model 加 `MainAgent() *agent.Agent` 方法返回 m.agent

**验证:** `go build ./...` 编译通过;运行 mewcode 不报错

## T30: config 加 EnableSubAgentBackground**文件:** `internal/config/config.go`
**依赖:** 无
**步骤:**
1. 在 Config 结构体加字段:
   ```go
   EnableSubAgentBackground *bool `yaml:"enableSubAgentBackground,omitempty"`
   ```
2. 加 Effective() 方法:
   ```go
   func (c Config) EffectiveEnableSubAgentBackground() bool {
       if c.EnableSubAgentBackground == nil { return true }
       return *c.EnableSubAgentBackground
   }
   ```
3. 注释说明:默认 true;false 时所有 SubAgent 强制前台,Fork 路径会报错

**验证:** `go build ./internal/config/...` 通过

## T31: subagent.LaunchFork 公用 wiring**文件:** `internal/subagent/launch.go`
**依赖:** T6, T15, T17
**步骤:**
1. 新建 `launch.go`,实现:
   ```go
   type ForkLaunchOpts struct {
       AllowedTools []string
       Model        string
       Conv         *conversation.Conversation  // 已装填的子对话
       SystemPrompt string
       Background   bool
       EventsSink   chan<- agent.Event
       Provider     llm.Provider
       Registry     *tool.Registry
       Engine       *permission.Engine
       Version      string
       HookEngine   *hook.Engine
   }

   func LaunchFork(ctx context.Context, opts ForkLaunchOpts) (string, error)
   ```
2. 实现细节:
   - 构造 SessionRuntime / Agent(类似 agent_tool 的前台路径)
   - 调 RunToCompletion(ctx, opts.Conv, "" /* conv 已含 task */, opts.EventsSink)
   - 返回 finalText / err
3. **避免循环依赖**:subagent.LaunchFork 引用 agent 包(为构造 Agent);agent 不引用 subagent(Agent 工具是 agent 包内部,工厂签名接受 AgentCatalog 接口避开 import)
   - 但 agent_tool 内还是要 import "mewcode/internal/subagent"——因为 Definition 类型。这就形成 subagent ← agent 之间的混乱。
   - **拆解方案**:
     - Definition 类型放在 subagent 包
     - Catalog 接口在 agent 包内定义(只用 List 必要方法)
     - subagent.LaunchFork 不返回到 agent 中,而是用 agent 暴露的 RunToCompletion 公共 API
4. 简化:agent_tool 直接 import subagent;subagent.LaunchFork 也 import agent。**循环依赖!** 这条路走不通。
5. **真正方案**:
   - subagent 包只放 Definition / Catalog / 加载逻辑(纯数据)
   - LaunchFork 放在 agent 包内(因为它要构造 agent.Agent)
   - agent_tool 也放 agent 包(已有)
   - tui/skill_fork 调 agent.LaunchFork(把 Definition 当参数传入)

**重新调整文件结构:**
- 删除 `internal/subagent/launch.go`(本任务取消)
- 新建 `internal/agent/launch.go` 实现 LaunchFork
- skills 的 fork 回调改为调 `agent.LaunchFork`

**验证:** 见 T28 验证

## T32: 集成测试 - 完整路径**文件:** `internal/agent/agent_tool_integration_test.go`(新增)
**依赖:** T17, T20, T29
**步骤:**
1. 端到端 mock:构造一个 mock provider 让主 Agent 调 Agent 工具(subagent_type="Explore"),子 Agent 也跑回纯文本
2. 验证 tool_result 包含子 Agent 的 finalText
3. 验证子 Agent 工具调用没看到 Agent 工具(过滤生效)
4. 验证后台路径:run_in_background=true → 立即返回 async_launched JSON,主 Agent 继续

**验证:** `go test ./internal/agent/ -run TestAgentToolIntegration -v` 通过

## T33: 编译与综合测试**依赖:** T1-T32
**步骤:**
1. `go build ./...`
2. `go vet ./...`
3. `go test ./...`

**验证:** 全部命令通过,无失败用例

## 执行顺序

```
T1 → T2 → T3
       ↘
        T5 → T6 → T7
       ↗
       T4
T8 → T9
T10 → T11 → T14
T10 → T12 → T13
T14, T15 → T16
T8, T12, T15 → T17 → T18
T19 → T20 → T21 → T22
T19 → T20 → T23 → T24
T6, T19, T23 → T25 → T26
T25 → T27(本期跳过 ESC)
T15 → T28
T30 → T29
T29 → T32
所有 → T33
```