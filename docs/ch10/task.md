# slash命令体系 Tasks## 文件清单

| 操作 | 文件 | 职责 |
|------|------|------|
| 新建 | `internal/command/command.go` | Kind 常量、Command 与 Handler 类型 |
| 新建 | `internal/command/registry.go` | Registry: New/Register/Lookup/Visible/PrefixMatch + 冲突检测 |
| 新建 | `internal/command/registry_test.go` | 注册、冲突 panic、前缀匹配、Visible 排序的单测 |
| 新建 | `internal/command/dispatch.go` | Parse(input) — 解析 `/<name>` 形态 |
| 新建 | `internal/command/dispatch_test.go` | Parse 各种输入的单测 |
| 新建 | `internal/command/ui.go` | UI 接口 + NopUI 测试桩 |
| 新建 | `internal/command/builtin_local.go` | 5 条纯本地命令(/help /status /memory /permission /session) |
| 新建 | `internal/command/builtin_ui.go` | 5 条影响界面命令(/exit /plan /compact /resume /clear) |
| 新建 | `internal/command/builtin_prompt.go` | 2 条提示词命令(/do /review) + reviewDirective 常量 |
| 新建 | `internal/command/builtins.go` | RegisterBuiltins(reg) 把 12 条命令一次性注入 |
| 新建 | `internal/command/builtins_test.go` | 12 条命令注册成功、NopUI 调用全部 handler 不报错 |
| 改造 | `internal/tui/commands.go` | 删旧 builtinCommands + handle* 函数;新增 Model 实现 UI 接口的全部方法 + dispatchSlash 入口 |
| 新建 | `internal/tui/complete.go` | completionMenu 类型 + handleCompletionKey + syncCompletionFromInput + Render |
| 改造 | `internal/tui/resume.go` | handleResume 改名/拆分为 Model.OpenResumeMenu(UI 接口实现) |
| 改造 | `internal/tui/tui.go` | Model 增 registry+completion 字段; New() 构造 registry; Update 在 stateIdle 接入补全键位拦截 |
| 改造 | `internal/tui/stream.go` | submit() 把 dispatchCommand 调用替换为 m.dispatchSlash |
| 改造 | `internal/tui/view.go` | View() 在 textarea 渲染后、status bar 前插入 m.completion.Render |
| 改造 | `internal/tui/tui_test.go` | 老的 TestTUISlashCompactRoutesToCommand / TestTUIUnknownSlashCommandFriendly 迁到新分发器 |
| 改造 | `internal/prompt/prompt.go` | ReadyHint 改为"建议输入 /help 查看命令"(不再列具体命令名) |
| 改造 | `internal/prompt/prompt_test.go` | 跟随 ReadyHint 变化 |
| 改造 | `internal/memory/manager.go` + `internal/memory/manager_test.go` | 新增 ListFiles |
| 改造 | `internal/session/writer.go` + `internal/session/writer_test.go` | 新增 Path() |
| 改造 | `internal/tool/registry.go` | 新增 Count() |
| 改造 | `internal/agent/runtime.go` + `internal/agent/runtime_test.go` | 新增 ResetForNewSession |

## 任务### T0a: memory.Manager.ListFiles**文件**：`internal/memory/manager.go`、`internal/memory/manager_test.go`
**依赖**:无
**步骤**：
1. 在 manager.go 新增 `func (m *Manager) ListFiles() (project []string, user []string)` — 列出项目层与用户层 memory 目录下的 .md 文件(含 MEMORY.md 自身);目录不存在视为空 slice 不报 error;其它 ReadDir 错误 `log.Printf` 记录后视为空 slice;返回值已按文件名字典序排序
2. 单测覆盖 4 个 case:目录不存在 / 仅含 MEMORY.md / 含多 .md / 含 .md 与非 .md 混合

**验证**：`go test ./internal/memory/ -run TestListFiles -v` 全绿

### T0b: session.Writer.Path**文件**：`internal/session/writer.go`、`internal/session/writer_test.go`
**依赖**:无
**步骤**：
1. Writer 结构体新增 `path string` 字段;`NewWriter` / `openWriter` 在打开成功后写 `w.path = <绝对路径>`
2. 新增 `func (w *Writer) Path() string { return w.path }`
3. 单测:创建 writer 后断言 `Path()` 非空且文件存在

**验证**：`go test ./internal/session/ -run TestWriterPath -v` 全绿
**注**:SessionID 不由 writer 提供,数据源是 `m.runtime.Session.SessionID`

### T0c: SessionRuntime.ResetForNewSession + tool.Registry.Count**文件**：`internal/agent/runtime.go`、`internal/agent/runtime_test.go`、`internal/tool/registry.go`
**依赖**:无
**步骤**：
1. runtime.go 新增 `func (r *SessionRuntime) ResetForNewSession(sesCtx *compact.SessionContext)` — 原子重置 Replacement / Recovery / AutoTracking / Session / UsageAnchor / AnchorMsgLen / TurnCount,把 r.Session 指向 sesCtx
2. runtime_test.go 单测:调用后所有字段回到 New 时的零值,Session 字段被替换
3. tool/registry.go 新增 `func (r *Registry) Count() int` — 返回当前已注册工具数量(O(1) 实现,基于现有内部 slice 或 map 长度)

**验证**：`go test ./internal/agent/ -run TestResetForNewSession -v` 与 `go build ./internal/tool/...` 全过

### T1: 定义 Command 类型与 Kind 常量**文件**：`internal/command/command.go`
**依赖**：无
**步骤**：
1. 新建 package command
2. 定义 `type Kind int` 与 3 个枚举常量 `KindLocal`、`KindUI`、`KindPrompt`(按这个顺序)
3. 定义 `type Handler func(ctx context.Context, ui UI) error` (UI 接口在 T4 声明,但 Go 允许跨文件前向引用)
4. 定义 `type Command struct { Name, Description string; Aliases []string; Kind Kind; Hidden bool; Handler Handler }`

**验证**：`go build ./internal/command/...` 不报错(若 UI 未声明,先在 ui.go 占位)

### T2: 实现 Registry + 冲突检测 + 前缀匹配**文件**：`internal/command/registry.go`、`internal/command/registry_test.go`
**依赖**：T1
**步骤**：
1. `Registry` 结构体含 `byName map[string]*Command`、`visible []*Command`
2. `New() *Registry`:初始化空 map + 空 slice
3. `Register(c *Command)`:校验 Name 非空且全小写、Aliases 全部非空且全小写;遍历 (Name + Aliases) 每个键,如已存在于 byName 则 `panic` 含具体冲突键;通过后把每个键塞进 byName 都指向同一 `*c`;若 Hidden=false 则 append 到 visible,然后对 visible 按 Name 字典序排序
4. `Lookup(name string) (*Command, bool)`:小写化 name 后查 byName
5. `Visible() []*Command`:返回 visible 的拷贝(防外部改动)
6. `PrefixMatch(prefix string) []*Command`:把 prefix 去掉前导 `/`、小写化;遍历 visible,Name 以 prefix 开头的入选,保持字典序返回;prefix 为空时返回全部 visible
7. 在 registry_test.go 写 5 个测试:`TestRegister_OK`(注册一条,Lookup 命中)、`TestRegister_DuplicateNamePanics`、`TestRegister_DuplicateAliasPanics`、`TestVisibleSorted`、`TestPrefixMatch`

**验证**：`go test ./internal/command/ -run TestRegister -v` + `go test ./internal/command/ -run TestVisible -v` + `go test ./internal/command/ -run TestPrefixMatch -v` 全绿

### T3: Parse 输入解析**文件**：`internal/command/dispatch.go`、`internal/command/dispatch_test.go`
**依赖**：无
**步骤**：
1. `Parse(input string) (name string, isSlash bool)`:对 input 调 `strings.TrimSpace`;若不以 `/` 开头返回 `("", false)`;若仅为 `/` 返回 `("", true)`;否则取掉前导 `/`、把首个空白前的部分小写化;若 name 之后还有非空白尾随字符,则返回 `name="", isSlash=true`(Lookup 必然 miss);否则返回 `(name, true)`
2. 在 dispatch_test.go 用表驱动测试样本:`""`/`"   "`/`"hello"`/`"/"`/`"/help"`/`"  /HELP  "`/`"/help xx"`(→ name="", isSlash=true)/`"/help  "`(→ name="help", isSlash=true)/`"//double"`/`"/ /help"`(→ name="", isSlash=true),确认每个返回值

**验证**：`go test ./internal/command/ -run TestParse -v` 全绿

### T4: UI 接口 + NopUI 测试桩**文件**：`internal/command/ui.go`
**依赖**：无(声明 UI 接口让 T1 的 Handler 类型签名合法)
**步骤**：
1. import `internal/permission`
2. 声明 UI 接口,方法集完整列出(见 plan.md "core.UI" 一节):Println/Error/Mode/SetMode/InjectAndSend/UsageIn/UsageOut/ModelName/Cwd/ToolCount/MemoryFiles/SessionPath/SessionID/Quit/ForceCompact/OpenResumeMenu/ClearAndNewSession/Idle
3. 提供 `NopUI() UI`:返回一个空实现 struct,所有写入方法 no-op、所有查询返回零值(Mode 返回 ModeDefault、MemoryFiles 返回 nil 等)
4. 在 dispatchSlash 出现需要的 helper 之前,先保证 NopUI 可用

**验证**：`go build ./internal/command/...` 编译通过

### T5: 实现 5 条纯本地命令**文件**：`internal/command/builtin_local.go`
**依赖**：T1、T2、T4
**步骤**：
1. handleHelp(reg *Registry) Handler:返回闭包,handler 内调 `reg.Visible()`,计算最长 Name 长度做对齐填充,逐条拼 `"/<name>  <desc>"`,用 `\n` 连接后 `ui.Println(...)` 一次输出
2. handleStatus:6 行 key:value,key 列宽固定 (`Mode:` `Tokens:` `Tools:` `Memories:` `Model:` `Directory:` 中最长那个);值依次取 `ui.Mode().String()`、`fmt.Sprintf("%d in / %d out", ui.UsageIn(), ui.UsageOut())`、`fmt.Sprintf("%d enabled", ui.ToolCount())`、`fmt.Sprintf("%d files", len(ui.MemoryFiles()))`、`ui.ModelName()`、`ui.Cwd()`;首行加标题 "MewCode Status"(空行隔开)
3. handleMemory:files = ui.MemoryFiles();len(files)==0 时 ui.Println("无已加载的记忆文件");否则按行打印 files
4. handlePermission:ui.Println(ui.Mode().String())
5. handleSession:ui.Println(fmt.Sprintf("Session: %s\nPath: %s", ui.SessionID(), ui.SessionPath()))

**验证**：`go build ./internal/command/...` 通过 + 后续 T8 的 builtins_test 会覆盖

### T6: 实现 5 条影响界面命令**文件**：`internal/command/builtin_ui.go`
**依赖**：T1、T4
**步骤**：
1. handleExit:`ui.Quit(); return nil`
2. handlePlan:`ui.SetMode(permission.ModePlan); ui.Println("已切换到 PLAN 模式"); return nil`
3. handleCompact:`if !ui.Idle() { ui.Error("请等待当前任务完成"); return nil }; ui.ForceCompact(); return nil`
4. handleResume:`if !ui.Idle() { ui.Error("请等待当前任务完成"); return nil }; ui.OpenResumeMenu(); return nil`
5. handleClear:`ui.ClearAndNewSession(); ui.Println("已清空当前会话,开启新 session"); return nil`

**验证**：`go build ./internal/command/...` 通过

### T7: 实现 2 条提示词命令**文件**：`internal/command/builtin_prompt.go`
**依赖**：T1、T4
**步骤**：
1. 包内 `const reviewDirective = "请审查当前上下文中的代码变更/已读取的文件,指出潜在 bug、可读性问题和可简化处。"`
2. handleDo:`ui.SetMode(permission.ModeDefault); ui.InjectAndSend("/do", prompt.ExecuteDirective); return nil` (import internal/prompt)
3. handleReview:`ui.InjectAndSend("/review", reviewDirective); return nil`

**验证**：`go build ./internal/command/...` 通过

### T8: RegisterBuiltins + 12 条命令一次性注册**文件**：`internal/command/builtins.go`、`internal/command/builtins_test.go`
**依赖**：T5、T6、T7
**步骤**：
1. `RegisterBuiltins(reg *Registry)`:按字典序注册 12 条 Command 字面量(name 全部小写,Description 一句中文,Kind 按设计,Aliases 留空数组,Hidden=false);/help 的 Handler 通过 `handleHelp(reg)` 闭包注入
2. 在 builtins_test.go 写:`TestRegisterBuiltins_AllRegistered`(注册后 `reg.Visible()` 长度=12、含所有 12 个名字)、`TestRegisterBuiltins_NoCollision`(直接调 RegisterBuiltins 不 panic)、`TestRegisterBuiltins_HandlersRunOnNopUI`(把 NopUI 传给每个命令的 Handler,断言全部返回 nil error)
3. 升级为可观测桩:在 builtins_test.go 新增 `RecordingUI` struct 嵌入 NopUI,记录 Println/Error/SetMode/InjectAndSend 调用;至少 3 个行为断言:
   - `TestHandleStatus_PrintsAllKeys` — handleStatus 调 Println 一次且文本含 6 个 key(Mode/Tokens/Tools/Memories/Model/Directory)
   - `TestHandleCompact_BlocksWhenBusy` — handleCompact 在 `Idle()==false` 时调 Error 不调 ForceCompact
   - `TestHandleDo_SetsModeAndInjects` — handleDo 调 `SetMode(ModeDefault)` + `InjectAndSend("/do", ...)`

**验证**：`go test ./internal/command/ -v` 全绿;`go vet ./internal/command/...` 无 warning

### T8.5: Model 字段铺垫**文件**：`internal/tui/tui.go`
**依赖**：T8
**步骤**：
1. Model 结构体在原 `registry *tool.Registry` 字段之后增 `cmdRegistry *command.Registry`、`completion completionMenu`、`pendingPrintln []string`、`pendingCmd tea.Cmd` 四个字段
2. 在 `New()` 中初始化(零值即可,registry 通过 T9c 注册)

**验证**：`go build ./internal/tui/...` 零退出码

### T9a: TUI Model 实现 UI 只读查询方法**文件**：`internal/tui/commands.go`(在该文件已被清空旧 handler 后重写)
**依赖**：T8.5
**步骤**：
1. 删除旧文件 `internal/tui/commands.go` 全部内容(`builtinCommands` map、`commandHandler` 类型、`dispatchCommand`、`handleExit`/`handlePlan`/`handleDo`/`handleCompact`/`handleUnknown`、`formatCompactNotice` 全部移除)
2. 给 `*Model` 实现 UI 接口的所有只读方法:
   - `Mode() permission.Mode` → return m.mode
   - `UsageIn() int64` / `UsageOut() int64` → 返回 m.usageIn / m.usageOut
   - `ModelName() string` → m.provider != nil 返回 m.provider.Model();否则空串
   - `Cwd() string` → 返回 m.cwd(若 Model 上没有此字段,从 TUIParams 拷一份)
   - `ToolCount() int` → return `m.registry.Count()` (tool.Registry)
   - `MemoryFiles() []string` → 调 `m.memMgr.ListFiles()` 拼接 project + user
   - `SessionPath() string` → `m.writer.Path()`(nil → "")
   - `SessionID() string` → `m.runtime.Session.SessionID`(nil 链路任一为 nil → "")
   - `Idle() bool` → m.state == stateIdle

**验证**：`go build ./internal/tui/...` 零退出码

### T9b: TUI Model 实现 UI 写入方法 + 缓冲机制**文件**：`internal/tui/commands.go`
**依赖**：T9a
**步骤**：
1. `Println(msg)` → `m.pendingPrintln = append(m.pendingPrintln, msg)`(原始字符串,Render 时再用 noticeBlock 包)
2. `Error(msg)` → `m.pendingPrintln = append(m.pendingPrintln, "ERROR\x00"+msg)`(用前缀编码区分 notice/error,Render 时按前缀分流)
3. `SetMode(m permission.Mode)` → m.mode = m
4. `Quit()` → m.pendingCmd = tea.Quit
5. `ForceCompact()` → 复用原 handleCompact 内构造的 tea.Cmd,push 到 m.pendingCmd
6. `OpenResumeMenu()` → 直接调 T10 提供的方法体(本步骤仅声明,实现在 T10 在 resume.go 提供,本步骤不在 commands.go 中重新定义)
7. `ClearAndNewSession()` — 步骤:
   a. 关闭 m.writer
   b. `newSesCtx, err := compact.NewSessionContext(m.cwd)`;`err != nil` 时 `ui.Error(err.Error())` 直接返回(不动现状)。注意签名:`compact.NewSessionContext(workspace string) (*SessionContext, error)`,内部在 `<workspace>/.mewcode/sessions/<id>/tool-results` 下建好目录
   c. `newWriter, err := session.OpenWriter(newSesCtx.SessionDir)`;`err != nil` 时 `ui.Error` 后返回(沿用 ch09 既有的 OpenWriter 入口,不要新写一个 NewWriter)
   d. m.writer = newWriter
   e. 重新构造 m.conv:`m.conv = conversation.NewWithHooks(onAppend, onReplace)`,onAppend/onReplace 闭包捕获 newWriter 与新的 `isFirst:=true`
   f. `m.runtime.ResetForNewSession(newSesCtx)`
   g. `m.iter=0; m.usageIn=0; m.usageOut=0`
   h. 把"重绘"tea.Cmd push 到 m.pendingCmd(可以是 tea.ClearScreen 或仅返回 nil)
8. `InjectAndSend(label, preset)` — `m.conv.AddUser(preset)`;把 `m.beginTurn(userBlock(label))` push 到 m.pendingCmd

建议:把 conv 闭包构造抽成 `(m *Model) bindConversation(writer *session.Writer)` 让 New() 和 ClearAndNewSession 共用,避免漂移

**验证**：`go build ./internal/tui/...` 零退出码

### T9c: dispatchSlash 入口 + 注册中心构造**文件**：`internal/tui/commands.go`、`internal/tui/tui.go`
**依赖**：T9b
**步骤**：
1. commands.go 新增 `(m *Model) dispatchSlash(text string) (tea.Cmd, bool)`:
   a. `name, isSlash := command.Parse(text)`;若 `!isSlash` 返回 `(nil, false)`
   b. 清 m.pendingPrintln 与 m.pendingCmd
   c. `cmd, ok := m.cmdRegistry.Lookup(name)`
   d. `!ok` → `m.pendingPrintln = append(..., noticeBlock("未知命令: 输入 /help 查看可用命令"))`(注:Parse 返回 `("", true)` 即退化输入(如纯 `"/"` 或 `"/<空白>"`)时,提示文案不要拼 `"/+name"` 避免出现 `"未知命令: /, ..."` 这种悬空斜杠)
   e. ok 且 (`cmd.Kind == KindUI` 或 `cmd.Kind == KindPrompt`) 且 `m.state != stateIdle` → 推 `errorBlock("请等待当前任务完成")` 到 pendingPrintln
   f. 否则 `err := cmd.Handler(m.ctx, m)`;`err != nil` → push `errorBlock(err.Error())`
   g. 把 pendingPrintln 拼成 `tea.Sequence(tea.Println(...)...)` + pendingCmd 合并成最终 tea.Cmd 返回 `(final, true)`
2. tui.go `New()` 中加:`reg := command.New(); command.RegisterBuiltins(reg); m.cmdRegistry = reg`

**验证**：`go build ./...` 零退出码;`go vet ./...` 无 warning

### T10: OpenResumeMenu — handleResume 重构进 UI 接口**文件**：`internal/tui/resume.go`
**依赖**：T9
**步骤**：
1. 把现有 `handleResume(ctx, m *Model) tea.Cmd` 函数体迁移到 `(m *Model) OpenResumeMenu()`:把"state guard"那一段挪到 builtin_ui 的 handleResume(已经做了);剩下"构造 sessionItem 列表、设置 m.resumeList、切换 m.state=stateResuming"放进 OpenResumeMenu;没有 tea.Cmd 返回则用 nop pendingCmd。同时移除 OpenResumeMenu 内部对 `m.state != stateIdle` 的判断和提示(guard 已在 dispatchSlash 按 Kind 统一处理,handler 内不重复 ui.Idle 检查)
2. 如果 handleResume 老函数还被引用,删除引用;否则直接整段移除
3. updateResuming、doResumeSession、resumeSessionMsg 保持不变(它们由 Update 调度,不属于命令系统)

**验证**：`go build ./internal/tui/...` 待 T12 完成

### T11: completionMenu 状态机 + 渲染**文件**：`internal/tui/complete.go`(新)
**依赖**：T2
**步骤**：
1. 定义 `completionMenu` 结构体: items []*command.Command、cursor int、offset int、active bool
2. 定义常量 `completionMaxRows = 8`
3. `(c *completionMenu) Update(input string, reg *command.Registry)`:input 去前后空白;若不以 `/` 开头则 c.active=false 并 return;否则 c.items = reg.PrefixMatch(input);若 len(items)==0 仍 active=true(显示"无匹配");cursor/offset 在 items 长度变化时夹紧
4. `(c *completionMenu) MoveUp/MoveDown()`:夹在 [0, len(items)-1];offset 跟随 cursor,使 cursor 始终在可见窗口内
5. `(c *completionMenu) Selected() *command.Command`:items 非空时返回 items[cursor],否则 nil
6. `(c *completionMenu) Hide()`:active=false、items=nil、cursor=0、offset=0
7. `(c *completionMenu) Render(width int) string`:active=false 返回 "";否则用 lipgloss 渲染一个左对齐的多行块:每行 `/<name>  <description>`,Name 列做对齐填充;高亮 cursor 行(背景色或反色);上下溢出时显示 "↑ N more" / "↓ N more" 提示行;整块宽度不超 width
8. `(m *Model) handleCompletionKey(msg tea.KeyPressMsg) (tea.Cmd, bool)`:if !m.completion.active return (nil, false);switch key { "up": MoveUp, "down": MoveDown, "esc": Hide, "enter"/"tab": sel := Selected(); if sel != nil { ExecuteSelected } else { Hide };default: return (nil, false) // 透传 textarea };被消费的分支返回 (nil, true)。ExecuteSelected:`textarea.SetValue("/"+sel.Name); cmd := m.submit(); m.completion.Hide();` 由 handleCompletionKey 把 cmd 透传返回 `(cmd, true)`,不丢弃 tea.Quit / spinner.Tick / beginTurn 等
9. `(m *Model) syncCompletionFromInput()`:取 textarea.Value(),调 `m.completion.Update(value, m.cmdRegistry)`(注意是 cmdRegistry 不是 m.registry,后者是 tool.Registry)

**验证**：`go build ./internal/tui/...` 待 T12;先用 `go vet ./internal/tui/...` 看类型错误

### T12: TUI Update 集成补全键位**文件**：`internal/tui/tui.go`
**依赖**：T9c、T10、T11
**步骤**：
1. Model 字段已在 T8.5 加好(`cmdRegistry`、`completion`、`pendingPrintln`、`pendingCmd`);本任务不重新声明
2. `New(...)`:cmdRegistry 的构造与注入已在 T9c step 2 完成;本任务不重复
3. `Update` 在 stateIdle 分支处理 tea.KeyPressMsg 时:
   - 先调 `cmd, consumed := m.handleCompletionKey(msg)`;若 consumed 直接 `return m, cmd`
   - 否则继续原 textarea.Update 路径
   - textarea.Update 之后(在 update 函数尾部 return 之前)调 `m.syncCompletionFromInput()`
4. updateIdle 中对 Enter 键的处理保持现状(由 submit() 处理);submit() 内的命令分发改动放 T13

**验证**：`go build ./internal/tui/...` 通过

### T13: stream.submit 接入 dispatchSlash + view.View 渲染补全菜单**文件**：`internal/tui/stream.go`、`internal/tui/view.go`
**依赖**：T12
**步骤**：
1. stream.go: `submit()` 中把现有的 `if handler, isCmd := dispatchCommand(text); isCmd { return handler(m.ctx, m) }` 整段替换为 `if cmd, handled := m.dispatchSlash(text); handled { m.textarea.Reset(); m.syncInputHeight(); m.completion.Hide(); return cmd }`
2. view.go: 在 `View()` 函数中,定位到 textarea 渲染块之后、status bar 渲染块之前;插入 `if m.completion.active { sb.WriteString(m.completion.Render(m.width) + "\n") }` (sb 是 strings.Builder 或当前用的渲染累加器,按现状)
3. view.go: 不要动 statusBar / modeLabel / modeStatusStyle 函数

**验证**：`go build ./... && go vet ./...` 通过;`go test ./internal/tui/ -run TestTUISlash -v` 期待红(下一任务迁移测试)

### T14: 迁移测试 + ReadyHint 调整**文件**：`internal/tui/tui_test.go`、`internal/prompt/prompt.go`、`internal/prompt/prompt_test.go`
**依赖**：T13
**步骤**：
1. tui_test.go: 把 `TestTUISlashCompactRoutesToCommand` 改为构造 Model + 注册 builtins 后,调 `m.dispatchSlash("/compact")`、断言返回 handled=true、断言未调 conv.AddUser
2. tui_test.go: `TestTUIUnknownSlashCommandFriendly` 改为调 `m.dispatchSlash("/foobar")`、断言 handled=true、断言 m.pendingPrintln 含"未知命令"
3. tui_test.go: 新增 `TestTUIDispatch_CaseInsensitive` 测 `/Help` 与 `/help` 同效;`TestTUIDispatch_HelpListsAllBuiltins` 测 /help 输出含 12 个命令名
4. prompt.go: `ReadyHint` 字符串由现有"/plan, /do, /exit"列表改为类似 `"已就绪,输入 /help 查看可用命令。"`(具体文本含 /help 引导即可)
5. prompt_test.go: 跟随调整断言

**验证**：`go test ./... -count=1 -v` 全绿;`go vet ./...` 无 warning;`gofmt -l . | head` 输出为空

### T15: 端到端验证(tmux 实跑)**文件**：无(运行可执行文件)
**依赖**：T14
**步骤**：
1. `cd /Users/codemelo/mewcode && go build -o /tmp/mewcode ./cmd/mewcode`
2. `tmux new-session -d -s mewspec '/tmp/mewcode'` 启动会话
3. 按 checklist.md 的"端到端场景(tmux 实跑)"逐项发送按键并截屏:
   - 启动后键入 `/` 看补全菜单是否弹出且含 12 条
   - 键入 `/s` 看是否过滤为 /session、/status
   - 选中 /status 回车验证 6 字段输出
   - 依次跑 /help、/memory、/permission、/session、/review、/clear,逐一观测输出
   - 验证 /plan 切到 plan 模式后状态栏徽章变化
   - 验证 /do 切回 default + 触发 AI 回复
   - 验证 /resume 列表能看到 /clear 之前的旧会话
   - 验证未知命令 /foobar 提示
   - 验证启动期冲突检测:临时给某条命令多注册一遍同名,启动应 panic 退出,看错误信息
4. 全部通过后 `tmux kill-session -t mewspec`

**验证**：按 checklist.md 全部勾选;期间出错则修复后从失败点重跑

## 执行顺序

```
T0a, T0b, T0c (并行) → T1 → (T2, T3, T4 并行) → (T5, T6, T7 并行) → T8 → T8.5 → T9a → T9b → T9c → T10 → T11 → T12 → T13 → T14 → T15
```

- T0a/T0b/T0c 是底层 helper 铺垫(memory.ListFiles、session.Writer.Path、runtime.ResetForNewSession、tool.Registry.Count),互不依赖可并行
- T1/T2/T3/T4 是 command 包基础,T1→T2 是结构依赖,T2/T3/T4 互不依赖可并行
- T5/T6/T7 三组命令实现互不依赖,可并行
- T8 必须在 T5+T6+T7 后
- T8.5 给 Model 加字段,作为 T9a 前置
- T9a→T9b→T9c 拆分原 T9:只读方法 → 写入方法+缓冲 → dispatchSlash 与注册中心,严格串行
- T10 替换 OpenResumeMenu;T11(completionMenu)仅依赖 T2,放在 T10 后接入 UI 也可
- T12 把 Update 接入补全键位;T13 把 stream/view 接入;T14 是测试与 ReadyHint 调整,T15 是端到端验证