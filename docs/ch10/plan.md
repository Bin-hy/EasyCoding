# slash命令体系 Plan## 架构概览

```
┌──────────────────────────────────────────────────────────┐
│                      internal/tui                        │
│                                                          │
│  Model.submit() ─┬─► command.Parse → command.Registry    │
│                  │       (lookup by name/alias)          │
│                  │             │                         │
│                  │             ▼                         │
│                  │      command.Handler(ctx, ui)         │
│                  │             │                         │
│                  │             ▼ via UI interface        │
│                  │      Model 字段桥接 (impl UI)         │
│                  │                                       │
│                  └─► (非 / 开头) conv.AddUser + beginTurn│
│                                                          │
│  updateIdle ─► completionMenu 状态机 ─► view.go 渲染     │
└──────────────────────────────────────────────────────────┘
                  ▲
                  │ 依赖
                  │
┌──────────────────────────────────────────────────────────┐
│                   internal/command (新包)                │
│                                                          │
│  Command/Kind/Handler 类型定义                           │
│  Registry: 注册 + 冲突检测 + 前缀匹配 + 字典序排序       │
│  UI 接口: handler 操作 TUI 的唯一通道                    │
│  Dispatch: Parse + Lookup                                │
│  12 条内置命令的 handler 实现 (按 Kind 分文件)            │
└──────────────────────────────────────────────────────────┘
```

- 新建包 `internal/command/`：纯领域逻辑,不依赖 bubbletea
- 既有包 `internal/tui/`：删掉 `commands.go` 里的旧注册表与 5 个旧 handler;改为构造 Registry、实现 UI 接口、把分发结果桥接回 Model
- 自动补全菜单是 TUI 层独有的 UX 元素,完全在 `internal/tui/complete.go` 中实现,只读 Registry 拿候选列表

## 核心数据结构### `command.Kind`

```go
type Kind int

const (
    KindLocal  Kind = iota // 纯本地: 只打印,不改 Model,不进 history
    KindUI                 // 影响界面: 改 Model 状态,不进 history
    KindPrompt             // 提示词: 注入 user 消息 + 触发回合,进 history
)
```

### `command.Command`

```go
type Command struct {
    Name        string   // 不带 "/" 前缀, 全小写, 唯一
    Aliases     []string // 不带 "/" 前缀, 全小写, 全局唯一(含 Name)
    Description string   // 一句话, 用于 /help 与补全菜单
    Kind        Kind
    Hidden      bool     // /help 与补全菜单都不显示, 但 dispatcher 仍可命中
    Handler     Handler
}

type Handler func(ctx context.Context, ui UI) error
```

### `command.Registry`

```go
type Registry struct {
    byName  map[string]*Command // 主名 + 别名都映射到同一 *Command, key 已转小写
    visible []*Command          // 按 Name 字典序排序, 排除 Hidden, 给 /help 与补全菜单使用
}

func New() *Registry
func (r *Registry) Register(c *Command)        // 名/别名冲突即 panic
func (r *Registry) Lookup(name string) (*Command, bool)  // name 已是小写
func (r *Registry) Visible() []*Command         // 返回已排序的可见命令副本
func (r *Registry) PrefixMatch(prefix string) []*Command // prefix 含 "/", 内部 trim 并小写; 前缀匹配 Name; 不匹配别名/描述
```

### `command.UI` 接口

handler 通过该接口操作 TUI;`*tui.Model` 实现此接口。

```go
type UI interface {
    // 输出 (通过 tea.Println 推 scrollback)
    Println(msg string)
    Error(msg string)

    // 模式
    Mode() permission.Mode
    SetMode(m permission.Mode)

    // 对话注入 (KindPrompt 命令使用)
    // displayLabel 在 scrollback 中显示, presetPrompt 是实际写入 conversation/JSONL 的文本
    InjectAndSend(displayLabel, presetPrompt string)

    // /status 与 /memory 等只读查询
    UsageIn() int64
    UsageOut() int64
    ModelName() string
    Cwd() string
    ToolCount() int
    MemoryFiles() []string
    SessionPath() string
    SessionID() string

    // 影响界面动作
    Quit()
    ForceCompact()
    OpenResumeMenu()
    ClearAndNewSession()

    // 状态机查询
    Idle() bool
}
```

### UI 接口降级合约

- dispatchSlash 仅在 stateIdle 下被调用
- 即便如此,UI 实现需对 nil 做防御:
  - `m.provider == nil` → `ModelName()` 返回 `""`
  - `m.agent == nil` → `ForceCompact` 用 `Error("agent 未就绪")` 兜底
  - `m.writer == nil` → `SessionPath()` / `SessionID()` 返回空串
  - `m.memMgr == nil` → `MemoryFiles()` 返回 `nil`

### `tui.completionMenu` (新)

```go
type completionMenu struct {
    items   []*command.Command // 当前候选,已按 Name 字典序
    cursor  int                // 当前高亮索引
    offset  int                // 滚动偏移(候选数 > maxRows 时)
    active  bool               // 是否激活
}

const completionMaxRows = 8

func (c *completionMenu) Update(input string, reg *command.Registry)  // 根据当前输入刷新候选;无 "/" 前缀则 deactivate
func (c *completionMenu) MoveUp()
func (c *completionMenu) MoveDown()
func (c *completionMenu) Selected() *command.Command
func (c *completionMenu) Hide()
func (c *completionMenu) Render(width int) string                     // 多行字符串, 已按 maxRows 截断 + 滚动
```

**边界规则**:
- `handleCompletionKey` 在 textarea 内容含 `\n` 时强制 `active=false`(避免多行粘贴误激活)
- `Selected() == nil` 时(零匹配),回车走未命中提示分支、Tab/ESC 仅关闭菜单

## 模块设计### `internal/command/command.go`
**职责**：定义 `Command`、`Kind`、`Handler`。
**对外接口**：上面列出的类型。
**依赖**：仅 `context` 与 `internal/permission`(Kind 的语义说明引用)。

### `internal/command/registry.go`
**职责**：注册中心。维护 `byName` 索引、`visible` 排序列表；`Register` 时做冲突检测;`PrefixMatch` 提供补全数据源。
**对外接口**：上面 `Registry` 的方法集。
**依赖**：仅自包 + `sort`、`strings`。

### `internal/command/dispatch.go`
**职责**：`Parse(input string) (name string, isSlash bool)` —— 空白/空串/非 `/` 开头返回 `("", false)`;只含 `/` 返回 `("", true)`;取掉前导 `/`、第一个空白前的部分小写化作为 name;若 name 之后还有非空白尾随字符(用户传了参数),返回 `name="" + isSlash=true` 让 `Lookup` 必然 miss 走未命中分支。纯粹字符串操作,无副作用。Registry 上的 `Lookup` 已能完成查找,Dispatch 不必另外封装。
**对外接口**：`Parse`。

### `internal/command/ui.go`
**职责**：定义 `UI` 接口；同时提供一个 `nopUI` 测试桩,供 registry/handler 单元测试用。
**对外接口**：`UI` 接口、`NopUI()` 工厂(返回吞掉所有调用、查询返回零值的桩)。

### `internal/command/builtin_local.go`
**职责**：5 条纯本地命令的 handler——`/help`、`/status`、`/memory`、`/permission`、`/session`。
- `/help`：调用 `reg.Visible()`,按"<name>  <description>" 两列对齐输出,通过 `ui.Println` 打印。其中 reg 由命令构造时闭包捕获(`RegisterBuiltins` 把 reg 自身传入 help handler)。
- `/status`：按固定顺序输出 6 行——`Mode/Tokens/Tools/Memories/Model/Directory`,值来自 `ui.Mode().String()` / `ui.UsageIn|Out()` / `ui.ToolCount()` / `len(ui.MemoryFiles())` / `ui.ModelName()` / `ui.Cwd()`。
- `/memory`：调用 `ui.MemoryFiles()`,逐行打印文件名;为空时打印"无已加载的记忆文件"。
- `/permission`：打印 `ui.Mode().String()` 一行。
- `/session`：打印 "Session: <id>" + "Path: <path>" 两行(值来自 `ui.SessionID()`、`ui.SessionPath()`)。

### `internal/command/builtin_ui.go`
**职责**：5 条影响界面命令——`/exit`、`/plan`、`/compact`、`/resume`、`/clear`。
- `/exit`：调用 `ui.Quit()`。
- `/plan`：调用 `ui.SetMode(permission.ModePlan)` + `ui.Println("已切换到 PLAN 模式")`。
- `/compact`：调用 `ui.ForceCompact()`(idle 守护由 dispatchSlash 在 handler 调用前完成,handler 自身不再检查)。
- `/resume`：调用 `ui.OpenResumeMenu()`(idle 守护由 handler 通过 dispatchSlash 做唯一一次,OpenResumeMenu 自身不再检查)。
- `/clear`：调用 `ui.ClearAndNewSession()`。

### `internal/command/builtin_prompt.go`
**职责**：2 条提示词命令——`/do`、`/review`。
- `/do`：`ui.SetMode(permission.ModeDefault)` + `ui.InjectAndSend("/do", prompt.ExecuteDirective)`。
- `/review`：`ui.InjectAndSend("/review", reviewDirective)`(reviewDirective 是包内 const,文案如 "请审查上下文中的代码变更,指出潜在 bug、可读性问题、可简化处")。

### `internal/command/builtins.go`
**职责**：`RegisterBuiltins(reg *Registry)`——按一致顺序在 reg 上注册 12 条命令,把对应 handler 写进 Command 字面量;`/help` 的 handler 需要闭包捕获 reg。
**对外接口**：`RegisterBuiltins(*Registry)`。

### `internal/tui/commands.go` (改造)
**职责**：变成 thin glue:
1. 给 `Model` 实现 `command.UI` 接口的所有方法(每个方法 1~5 行,字段桥接 + tea.Println)
2. 提供 `Model.dispatchSlash(text string) tea.Cmd`:调 `command.Parse` → `m.cmdRegistry.Lookup` → 找到则 `cmd.Handler(m.ctx, m)`、未找到则 `tea.Println(noticeBlock(未知命令提示))`
3. 删掉 `builtinCommands` map、`handleExit/handlePlan/handleDo/handleCompact/handleUnknown` 等 5 个老 handler;保留 `handleResume` 中和 `OpenResumeMenu` UI 方法整合的部分(ch09 写的 list/state 启动逻辑搬到 `Model.OpenResumeMenu`)
**依赖**：`internal/command` 包、`internal/permission`、`internal/prompt`。

### `internal/agent/runtime.go` (改动)
- `SessionRuntime` 新增 `ResetForNewSession(sesCtx *compact.SessionContext)` 方法:原子重置 Replacement/Recovery/AutoTracking 三个 compact 子状态,UsageAnchor/AnchorMsgLen/TurnCount 清零,Session 字段指向新的 sesCtx;ContextWindow 保留;writer 与 conv 重建由 `ClearAndNewSession` 自身负责,不进 runtime 接口

### `internal/tui/stream.go` (改动)
- `submit()` 函数:把 `dispatchCommand(text)` 这一行替换为 `m.dispatchSlash(text)`,其余流程不变(空输入早返回、非命令走 `conv.AddUser + beginTurn`)

### `internal/tui/complete.go` (新)
**职责**：自动补全菜单状态机 + 渲染。
- `completionMenu` 结构体与方法见数据结构小节
- 提供 `(m *Model) handleCompletionKey(msg tea.KeyPressMsg) (tea.Cmd, bool)`:当菜单激活时,返回 `(cmd, true)` 表示该键已被菜单消费;否则返回 `(nil, false)` 让上层透传给 textarea
- 提供 `(m *Model) syncCompletionFromInput()`:每次 textarea 内容变化后调用,根据当前内容刷新菜单 active/items

### `internal/tui/tui.go` (改动)
- `Model` 增字段:`cmdRegistry *command.Registry`、`completion completionMenu`(注意:不要与已有的 `m.registry *tool.Registry` 混淆,后者保持原名)
- `New(...)`:构造 `command.New()` → `command.RegisterBuiltins(reg)` → 赋给 `m.cmdRegistry`
- `Update` 在 stateIdle 分支:
  - tea.KeyPressMsg 进来时,先调 `m.handleCompletionKey(msg)`,被消费则直接返回
  - 否则继续走原 textarea.Update + Enter 触发 submit 的流程
  - textarea.Update 之后立刻调 `m.syncCompletionFromInput()` 让菜单跟随输入实时刷新

### `internal/tui/view.go` (改动)
- `View()` 函数:在 textarea 渲染块之后、status bar 渲染块之前,如果 `m.completion.active`,插入 `m.completion.Render(m.width)`
- 不动 statusBar、modeLabel、modeStatusStyle

## 模块交互### 命令分发流(用户回车)

```
keystroke (Enter) ─► tui.updateIdle
                       │
                       ▼
                 tui.submit()
                  trim 输入
                  空输入 → 早返回
                       │
                       ▼
                m.dispatchSlash(text)
                  │
                  ├─ command.Parse(text)
                  │   isSlash=false → 返回 nil (上层走 AddUser + beginTurn)
                  │   isSlash=true 拿到 name
                  │
                  ├─ m.cmdRegistry.Lookup(name)
                  │   未找到 → tea.Println(noticeBlock(unknown msg)), 清输入
                  │
                  └─ cmd.Handler(ctx, m)
                       │
                       ├─ err != nil → push errorBlock(err.Error()) 到 pendingPrintln
                       │
                       ▼
                     通过 UI 接口操作 m
                       ├─ Println    → tea.Println(noticeBlock(...))
                       ├─ SetMode    → m.mode = newMode
                       ├─ InjectAndSend → m.conv.AddUser(preset) + m.beginTurn(userBlock(label))
                       ├─ Quit       → 通过返回 tea.Cmd 实现 (异步)
                       ├─ ForceCompact / OpenResumeMenu / ClearAndNewSession → 触发对应 sub-flow
```

注意:`UI.Quit()` / `UI.ForceCompact()` 等需要返回 `tea.Cmd` 的动作,实现上通过 `Model` 内部缓存的 "pending command" 字段:UI 方法 push 一个 `tea.Cmd` 到 `m.pendingCmd`,`dispatchSlash` 在 handler 返回后从 `m.pendingCmd` 取出并打包成 tea.Cmd 返回给 Bubble Tea Update。这样 UI 接口不需要返回值,handler 写起来线性。

### 自动补全流

```
keystroke (任意字符) ─► tui.updateIdle
                          │
                          ▼
                  m.handleCompletionKey(msg)
                    ┌─ 菜单 active=true:
                    │    ↑/↓     → menu.MoveUp/Down, 消费
                    │    Tab/⏎   → 执行 menu.Selected() 的 handler, 关闭菜单, 消费
                    │    ESC     → menu.Hide(), 消费
                    │    其他键  → 不消费, 透传 textarea
                    │
                    └─ 菜单 active=false:
                         不消费,透传

(透传 textarea 处理后)
m.syncCompletionFromInput()
  读 textarea.Value()
  首字符是 "/" → menu.Update(value, m.cmdRegistry) → active=true 或刷新候选
  首字符非 "/" → menu.Hide()

(渲染)
view.View():
  textarea 渲染区
  ↓ 如果 m.completion.active:
  m.completion.Render(m.width)  ← inline, 紧贴 textarea
  ↓
  status bar 渲染区
```

## 文件组织

```
internal/command/                    新包
├── command.go         Kind 常量, Command 与 Handler 类型
├── registry.go        Registry: New/Register/Lookup/Visible/PrefixMatch
├── dispatch.go        Parse(input string)
├── ui.go              UI 接口 + NopUI 测试桩
├── builtins.go        RegisterBuiltins(reg) + reviewDirective 常量
├── builtin_local.go   /help /status /memory /permission /session
├── builtin_ui.go      /exit /plan /compact /resume /clear
├── builtin_prompt.go  /do /review
├── registry_test.go   注册中心冲突 / 前缀匹配测试
├── dispatch_test.go   Parse 测试
└── builtins_test.go   12 条命令的注册与 NopUI 调用测试

internal/tui/
├── commands.go        改造: Model 实现 UI 接口 + dispatchSlash + 删旧 handler
├── complete.go        新: completionMenu + handleCompletionKey + syncCompletionFromInput
├── stream.go          改: submit() 调 m.dispatchSlash
├── tui.go             改: Model 加 cmdRegistry + completion 字段, New() 构造 cmdRegistry
├── view.go            改: View() 中插入补全菜单渲染
├── resume.go          改: 将 handleResume 函数体迁到 (m *Model) OpenResumeMenu() 方法,文件仍在 internal/tui/resume.go;删除老 handleResume
└── tui_test.go        改: 旧的 TestTUISlashCompactRoutesToCommand 等用例迁到新分发器

internal/agent/runtime.go  改: 新增 SessionRuntime.ResetForNewSession(sesCtx); ClearAndNewSession 调用此 helper 重置 compact 子状态

cmd/mewcode/main.go    不变 (TUI 构造时内部 wire cmdRegistry)
internal/prompt/prompt.go  ReadyHint 由"硬编码列表" 改为"建议输入 /help 查看可用命令" (去掉与命令清单同步的负担)
internal/prompt/prompt_test.go  改: 跟随 ReadyHint 文案调整断言
```

## 技术决策

| 决策点 | 选择 | 理由 |
|---|---|---|
| 命令系统包归属 | 新建 `internal/command/`,不留在 `internal/tui/` | tui 内 handler 持有 *Model 紧耦合,要满足 G3 必须把命令逻辑搬出 tui 包 |
| 注册方式 | 显式 `RegisterBuiltins(reg)`,不用 init() 副作用 | 测试时能用空 registry,启动顺序明确,易做单测 |
| 冲突检测 | `Register` 内部 panic,信息含具体名字/别名 | 失败快,启动期就报,不会进入运行时 |
| Handler 函数签名 | `func(ctx, UI) error` 而非 `func(*Model) tea.Cmd` | 满足 G3 解耦;handler 返回 non-nil error 时,dispatchSlash 自动 append errorBlock(err.Error()) 到 pendingPrintln,无需 handler 显式调 ui.Error 用户也能看到失败 |
| Idle 守护规则 | dispatchSlash 在调用 handler 前根据 Kind 判定:KindUI 与 KindPrompt 命令在非 idle 状态拒绝(直接 errorBlock 提示);KindLocal 命令任何 state 都可执行 | handler 不再单独检查 ui.Idle() |
| UI 与 tea.Cmd 衔接 | 通过 Model 内部 `pendingCmd` 字段缓冲 | UI 接口方法保持无返回值,handler 写线性代码;dispatchSlash 在 handler 返回后拼装 tea.Cmd |
| Kind 与"是否进 history" | KindPrompt = 调 InjectAndSend; Kind 仅是元数据,实际行为靠 handler 主动调用 | 避免把"是否注入"做成隐式行为;由 handler 显式表达意图,可读 |
| 别名匹配范围 | dispatcher 命中(主名 + 别名都进 byName); 补全菜单仅按主名前缀 | 别名是输入快捷,补全是发现机制,语义不同;本期 12 条命令暂不填别名 |
| 补全菜单实现 | 自实现 inline 渲染,不用 bubbles/list | bubbles/list 占满整屏与产品图不符;inline 多行字符串渲染足够 |
| 补全菜单激活条件 | textarea 首字符为 `/` | 简单可靠;空输入或非"/"开头都不弹 |
| 补全菜单键位归属 | active 时 ↑/↓/Tab/⏎/ESC 都被消费; 关闭时透传 textarea | 用户在菜单激活时不会期望普通编辑;关闭后所有键回到 textarea |
| 老命令收编 | 一次性把 5 条旧 handler 重写为基于 UI 接口的新 handler;不保留过渡 | 双轨维护成本高于一次性重写 |
| /resume 状态机 | UI.OpenResumeMenu 内部仍由 tui 包持有 sessionState 与 list.Model | 避免 command 包知道 bubbletea 类型;ch09 行为完全保留 |
| /clear 实现 | close 旧 writer → 用 compact.NewSessionContext 构造新 SessionContext → 用 session.NewWriter 打开新 writer → 重新构造 conversation.NewWithHooks(onAppend, onReplace)(onAppend 闭包重新捕获新 writer)→ m.runtime.ResetForNewSession(...) → m.iter=0, m.usageIn=0, m.usageOut=0 | 旧 writer 关闭后其 hook 已失效,必须重建 conversation 才能挂上新 writer;旧 JSONL 文件保留,/resume 仍能看到 |
| /memory 数据源 | UI.MemoryFiles() 由 Model 委托给已有的 memMgr | 不重做记忆加载,只新增"列文件名"查询路径 |
| /status 字段渲染 | 6 行 key:value 两列对齐(key 用 padRight); Mode 用 permission.Mode 的 String();ModelName 来源为 m.provider.Model()(provider 为 nil 时返回空串),与 status bar 取 model name 的来源一致,不读 m.engine | String() 已是 camelCase(default/plan/acceptEdits/bypassPermissions) 与设计图一致 |
| ToolCount 数据源 | UI.ToolCount() 由 Model 委托给 m.registry.Count(),即 tool.Registry 已有(若不存在则本期新增)的 O(1) 方法 | 与 cmdRegistry 字段无关,二者并存 |
| 未命中提示文本 | "未知命令: /<name>。输入 /help 查看可用命令" | 唯一硬编码字符串;集中在 commands.go 中 |
| ReadyHint 处理 | 改为通用引导文案("准备好了,输入 /help 查看命令"),不再列具体命令名 | 消除 N7 要求的"硬编码命令清单" |
| 状态栏改动 | 不动 | N11 要求 |