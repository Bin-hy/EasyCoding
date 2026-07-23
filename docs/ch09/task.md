# 项目记忆与会话持久化 Tasks## 文件清单

| 操作 | 文件 | 职责 |
|------|------|------|
| 修改 | `internal/compact/state.go` | session ID 格式变更、SessionContext 加 SessionDir、OpenSessionContext |
| 修改 | `internal/conversation/conversation.go` | NewWithHooks、NewFromMessages、回调触发 |
| 修改 | `internal/conversation/conversation_test.go` | 回调测试 |
| 修改 | `internal/prompt/prompt.go` | BuildSystemPrompt 签名变更 |
| 修改 | `internal/prompt/modules.go` | OptionalModules 改为接受参数 |
| 修改 | `internal/prompt/prompt_test.go` | 新签名测试 |
| 新建 | `internal/instructions/loader.go` | Loader 类型、三层加载、@include 展开 |
| 新建 | `internal/instructions/loader_test.go` | @include 深度/环路/逃逸/缺失文件测试 |
| 新建 | `internal/session/writer.go` | Writer、Entry、NewWriter/OpenWriter、Append |
| 新建 | `internal/session/list.go` | ListSessions、SessionInfo |
| 新建 | `internal/session/load.go` | LoadSession、坏行跳过、孤立截断 |
| 新建 | `internal/session/cleanup.go` | CleanExpired、ID 时间戳解析 |
| 新建 | `internal/session/session_test.go` | JSONL 读写、列表、恢复、清理测试 |
| 新建 | `internal/memory/types.go` | NoteType、Note、UpdateAction |
| 新建 | `internal/memory/store.go` | Store、笔记文件 CRUD、索引读写 |
| 新建 | `internal/memory/manager.go` | Manager、LoadIndex、UpdateAsync |
| 新建 | `internal/memory/prompt.go` | 记忆更新 prompt 模板 |
| 新建 | `internal/memory/memory_test.go` | 索引加载、操作执行、截断测试 |
| 修改 | `internal/agent/agent.go` | Run 末尾触发记忆更新 |
| 修改 | `internal/agent/runtime.go` | WithMemoryManager option、memMgr 字段 |
| 修改 | `internal/tui/commands.go` | /resume 命令注册 |
| 新建 | `internal/tui/resume.go` | stateResuming、会话列表项、updateResuming |
| 修改 | `internal/tui/tui.go` | stateResuming 集成、Model 新增字段 |
| 修改 | `cmd/mewcode/main.go` | 启动流程串联 |
| 修改 | `.mewcode/config.yaml.example` | 配置示例补充说明 |

## T1: Session ID 格式变更**文件：** `internal/compact/state.go`
**依赖：** 无
**步骤：**
1. 修改 `newSessionID()`：格式从 `<unix_ts>-<8hex>` 改为 `YYYYMMDD-HHMMSS-<4hex>`。使用 `time.Now().Format("20060102-150405")` 拼接 4 字符随机十六进制
2. `SessionContext` 新增 `SessionDir string` 字段，值为 `<workspace>/.mewcode/sessions/<session_id>`
3. 修改 `NewSessionContext`：先算 SessionDir，SpillDir 改为 `filepath.Join(SessionDir, "tool-results")`
4. 新增 `OpenSessionContext(workspace, sessionID string) (*SessionContext, error)`：不创建目录，只检查目录存在后填充字段
5. 新增 `ParseSessionTime(sessionID string) (time.Time, error)`：从 ID 前 15 位解析 `YYYYMMDD-HHMMSS`，供清理和排序使用

**验证：** `go test ./internal/compact/...` 通过；新 session ID 格式形如 `20260601-143022-a1b2`

## T2: Conversation 回调机制**文件：** `internal/conversation/conversation.go`, `internal/conversation/conversation_test.go`
**依赖：** 无
**步骤：**
1. `Conversation` 结构体新增 `onAppend func(llm.Message)` 和 `onReplace func([]llm.Message)` 两个可选字段
2. 新增 `NewWithHooks(onAppend, onReplace)` 构造函数
3. 新增 `NewFromMessages(msgs, onAppend, onReplace)` 构造函数：用 msgs 初始化 messages 切片（深拷贝），设置回调
4. 在 `AddUser`、`AddAssistant`、`AddAssistantWithToolCalls`、`AddToolResults` 末尾（锁释放后）调用 `onAppend`（如果非 nil）
5. 在 `ReplaceMessages` 末尾（锁释放后）调用 `onReplace`（如果非 nil）
6. 补充测试：验证回调被正确触发，验证无回调时行为不变

**验证：** `go test ./internal/conversation/...` 通过

## T3: 项目指令加载器**文件：** `internal/instructions/loader.go`, `internal/instructions/loader_test.go`
**依赖：** 无
**步骤：**
1. 定义 `Loader` 结构体：`projectRoot`、`userHome`、`maxDepth`（常量 5）
2. 实现 `NewLoader(projectRoot string) *Loader`：userHome 用 `os.UserHomeDir()` 取
3. 实现 `Load() (string, error)`：按优先级扫描三个路径，每个调 `loadFile`，结果用 `\n\n` 拼接
4. 实现 `loadFile(path, boundary string, depth int, visited map[string]struct{}) (string, error)`：
   - 检查 depth > maxDepth → 返回警告注释
   - 解析绝对路径，检查 visited → 环路警告
   - 检查绝对路径 `strings.HasPrefix` boundary → 逃逸警告
   - 读取文件内容，检查前 512 字节有 `\x00` → 二进制警告
   - 逐行扫描，匹配 `@include ` 开头的独占行 → 递归 loadFile 展开
   - 返回展开后的完整内容
5. 测试用例：三层加载优先级、@include 正常展开、5 层深度截断、环路检测、路径逃逸、缺失文件跳过、二进制文件跳过

**验证：** `go test ./internal/instructions/...` 通过

## T4: Session Writer**文件：** `internal/session/writer.go`
**依赖：** T1（SessionDir 字段）
**步骤：**
1. 定义 `Entry` 结构体（JSON tag）
2. 实现 `NewWriter(sessionDir string) (*Writer, error)`：在 sessionDir 下创建/打开 `conversation.jsonl`（`os.O_CREATE|os.O_APPEND|os.O_WRONLY`），初始化 json.Encoder
3. 实现 `OpenWriter(sessionDir string) (*Writer, error)`：同 NewWriter 但不创建目录，直接追加打开
4. 实现 `Append(msg llm.Message, model string, isFirst bool) error`：构造 Entry，isFirst 时填充 Model 字段，加锁 → Encode → Sync → 解锁
5. 实现 `WriteCompactMarker() error`：写入 `{"type":"compact","ts":...}`
6. 实现 `AppendAll(msgs []llm.Message) error`：逐条调 Append（model 空、isFirst false）
7. 实现 `Close() error`：关闭文件句柄

**验证：** `go build ./internal/session/...` 编译通过

## T5: 会话列表扫描**文件：** `internal/session/list.go`
**依赖：** T1（ParseSessionTime）
**步骤：**
1. 定义 `SessionInfo` 结构体
2. 实现 `ListSessions(sessionsDir string) ([]SessionInfo, error)`：
   - `os.ReadDir(sessionsDir)` 遍历子目录
   - 对每个目录：尝试 `ParseSessionTime(dirName)` → 失败则跳过（旧格式）
   - 检查 `conversation.jsonl` 是否存在 → 不存在则跳过
   - 打开 JSONL，逐行读到第一条 role=user 的消息 → 提取 content 作为 Title（截断到 50 字符）
   - 从第一条消息的 model 字段提取 Model
   - 文件 Stat 获取 Size 和 ModifiedAt
   - 按 ModifiedAt 倒序排列返回

**验证：** `go build ./internal/session/...` 编译通过

## T6: 会话加载恢复**文件：** `internal/session/load.go`
**依赖：** T4（Entry 类型）
**步骤：**
1. 实现 `LoadSession(sessionDir string) ([]llm.Message, error)`：
   - 逐行读取 JSONL，解析为 Entry
   - JSON 解析失败的行跳过（坏行容错）
   - 记录最后一个 `type:"compact"` 标记的行号
   - 从最后 compact 标记之后开始构建 `[]llm.Message`
   - 扫描结尾：如果最后一条是 assistant 且有 ToolCalls，但后面没有 tool 消息 → 截断掉该条
   - 返回 messages
2. 提取 `truncateOrphanedToolCalls(msgs []llm.Message) []llm.Message` 为独立函数便于测试

**验证：** `go build ./internal/session/...` 编译通过

## T7: 会话过期清理**文件：** `internal/session/cleanup.go`
**依赖：** T1（ParseSessionTime）
**步骤：**
1. 实现 `CleanExpired(sessionsDir string, maxAge time.Duration) error`：
   - 遍历子目录
   - `ParseSessionTime(dirName)` → 失败跳过
   - 时间距今超过 maxAge → `os.RemoveAll(dirPath)`
   - 单个删除失败记录日志继续

**验证：** `go build ./internal/session/...` 编译通过

## T8: Session 包测试**文件：** `internal/session/session_test.go`
**依赖：** T4, T5, T6, T7
**步骤：**
1. TestWriter_AppendAndRead：写入 3 条消息 → 逐行读回验证 JSON 结构
2. TestWriter_CompactMarker：写入消息 → compact 标记 → 新消息 → LoadSession 只返回 compact 后的
3. TestLoadSession_BadLineSkip：插入坏行 → 被跳过，其余正常
4. TestLoadSession_OrphanedToolCalls：末尾是带 tool_calls 的 assistant → 被截断
5. TestListSessions：创建 3 个 session 目录 → 列表返回 3 项，按时间倒序
6. TestListSessions_SkipsOldFormat：混合新旧格式目录 → 只返回新格式
7. TestCleanExpired：创建一个 31 天前和一个 1 天前的目录 → 只删 31 天前的

**验证：** `go test ./internal/session/...` 通过

## T9: 笔记类型与存储**文件：** `internal/memory/types.go`, `internal/memory/store.go`
**依赖：** 无
**步骤：**
1. `types.go`：定义 NoteType 常量、Note 结构体、UpdateAction 结构体
2. `store.go`：
   - `NewStore(dir string) *Store`
   - `EnsureDir() error`：`os.MkdirAll`
   - `LoadIndex() (string, error)`：读取 `MEMORY.md` 内容；不存在返回空字符串
   - `Apply(actions []UpdateAction) error`：
     - create：写 frontmatter + content 到 `<type>_<slug>.md`，在 MEMORY.md 追加一行
     - update：重写文件内容和 frontmatter，更新 MEMORY.md 对应行
     - delete：删除文件，移除 MEMORY.md 对应行

**验证：** `go build ./internal/memory/...` 编译通过

## T10: 记忆管理器**文件：** `internal/memory/manager.go`, `internal/memory/prompt.go`
**依赖：** T9
**步骤：**
1. `prompt.go`：定义记忆更新的系统提示模板（中文），包含规则说明和 JSON 输出格式
2. `manager.go`：
   - `NewManager(projectDir, userDir string, provider llm.Provider, model string) *Manager`
   - `LoadIndex() string`：合并项目级和用户级索引，截断到 25KB
   - `SetProvider(provider, model)`：延迟设置（启动时 provider 未选定）
   - `UpdateAsync(ctx context.Context, recentMsgs []llm.Message)`：
     - 启动 goroutine
     - 加锁（防并发更新）
     - 构造记忆更新请求：系统提示 + 最近消息 + 现有索引
     - 调用 provider.Stream（不传 Tools）
     - 收集完整回复，解析 JSON 数组
     - 按 level 分发到 projectStore.Apply / userStore.Apply
     - 失败记录日志不上抛

**验证：** `go build ./internal/memory/...` 编译通过

## T11: Memory 包测试**文件：** `internal/memory/memory_test.go`
**依赖：** T9, T10
**步骤：**
1. TestStore_CreateNote：Apply create → 文件存在、frontmatter 正确、MEMORY.md 有对应行
2. TestStore_UpdateNote：Apply update → 文件内容更新、MEMORY.md 对应行更新
3. TestStore_DeleteNote：Apply delete → 文件不存在、MEMORY.md 对应行消失
4. TestManager_LoadIndex：两级各有索引 → 合并返回，项目级在前
5. TestManager_LoadIndex_Truncate：构造超 25KB 索引 → 截断 + truncated 标注
6. TestManager_UpdateAsync_ParsesResponse：mock provider 返回 JSON → 笔记文件被创建

**验证：** `go test ./internal/memory/...` 通过

## T12: BuildSystemPrompt 参数化**文件：** `internal/prompt/prompt.go`, `internal/prompt/modules.go`, `internal/prompt/prompt_test.go`
**依赖：** 无
**步骤：**
1. `modules.go`：`OptionalModules` 改为 `OptionalModules(instructions, memory string) []Module`，用参数填充 Content
2. `prompt.go`：`BuildSystemPrompt` 改为 `BuildSystemPrompt(instructions, memory string) string`，传参给 OptionalModules
3. 更新所有调用 `BuildSystemPrompt` 的地方（agent.go 中的 streamOnce），传入对应参数
4. 更新测试：验证非空参数时模块出现在系统提示中，空参数时模块被跳过

**验证：** `go test ./internal/prompt/...` 通过

## T13: /resume 命令注册**文件：** `internal/tui/commands.go`
**依赖：** 无
**步骤：**
1. 在 `builtinCommands` map 中注册 `/resume` → `handleResume`
2. `handleResume` 函数：检查状态 == stateIdle，调用 `beginResume()` 返回进入选择列表的命令

**验证：** `go build ./internal/tui/...` 编译通过

## T14: 会话列表 UI**文件：** `internal/tui/resume.go`, `internal/tui/tui.go`
**依赖：** T5（ListSessions）, T13
**步骤：**
1. `tui.go`：
   - `sessionState` 新增 `stateResuming = 4`
   - `Model` 新增字段：`writer *session.Writer`、`memMgr *memory.Manager`、`instructionText string`、`memoryText string`、`sessionsDir string`
   - `New()` 签名扩展：接收 writer、memMgr、instructionText、memoryText
   - `Update` 方法中 `stateResuming` 分发到 `updateResuming`
   - `View` 方法中 `stateResuming` 渲染会话列表
2. `resume.go`：
   - 定义 `sessionItem` 实现 `list.DefaultItem`：Title() 返回会话标题，Description() 返回 "3 hours ago · model · 1.3KB"
   - `beginResume() tea.Cmd`：调 `ListSessions` → 构建 list.Model → 赋给 Model.resumeList → state = stateResuming
   - `updateResuming(msg) tea.Cmd`：
     - Enter：取 selectedItem → 执行恢复流程 → state = stateIdle
     - Esc：state = stateIdle
     - 其他：list.Update
   - 恢复流程封装为 `doResumeSession(info SessionInfo) tea.Cmd`：
     - LoadSession → 构建 newConv → 检查孤立/token/时间跨度
     - OpenSessionContext + OpenWriter
     - 替换 Model 的 conv、writer、runtime.Session
     - 返回系统消息

**验证：** `go build ./internal/tui/...` 编译通过

## T15: Agent 记忆更新触发**文件：** `internal/agent/agent.go`, `internal/agent/runtime.go`
**依赖：** T10（Manager）
**步骤：**
1. `runtime.go`：Agent 结构体新增 `memMgr *memory.Manager` 字段；新增 `WithMemoryManager` option
2. `agent.go`：在 Run 循环的 Done 分支（模型回复无工具调用），after `conv.AddAssistant(text)` 后：
   - 提取最近一轮消息（从最后一条 user 到当前 assistant）
   - 递增 `runtime.TurnCount`，满足任一条件时调 `memMgr.UpdateAsync`：① `TurnCount % 5 == 0`；② `hasMemorySignal(recentMsgs)` 检测到"记住/记忆/别忘/remember/memo"关键词
3. `agent.go`：`streamOnce` 中 `BuildSystemPrompt` 调用改为传入 instructionText 和 memoryText（从 Agent 字段获取）
4. Agent 新增 `instructionText` 和 `memoryText` 字段，由 TUI 在构造时传入

**验证：** `go build ./internal/agent/...` 编译通过

## T16: main.go 启动流程串联**文件：** `cmd/mewcode/main.go`
**依赖：** T1, T2, T3, T4, T10, T12, T14, T15
**步骤：**
1. 在 config.Load 之后、tool.NewDefaultRegistry 之前插入：
   - `instructions.NewLoader(root).Load()` → instructionText
   - `memory.NewManager(projectMemDir, userMemDir, nil, "")` → memMgr（provider 待选）
   - `memMgr.LoadIndex()` → memoryText
2. 在 NewSessionContext 之后：
   - `session.NewWriter(sesCtx.SessionDir)` → writer
3. 在 permission.NewEngine 之后：
   - `go session.CleanExpired(sessionsDir, 30*24*time.Hour)`
4. 修改 `conversation.New()` → `conversation.NewWithHooks(writer.OnAppend, writer.OnReplace)` 
   其中 writer.OnAppend 和 writer.OnReplace 是闭包，内部调 Writer 方法
5. 修改 `tui.New(...)` 调用：传入 writer、memMgr、instructionText、memoryText
6. 在 TUI 的 provider 选定回调中：调 `memMgr.SetProvider(provider, model)`

**验证：** `go build ./cmd/mewcode/...` 编译通过；`go vet ./...` 无警告

## T17: 配置示例更新**文件：** `.mewcode/config.yaml.example`
**依赖：** 无
**步骤：**
1. 在配置示例中添加注释，说明 MEWCODE.md 的加载路径和优先级
2. 说明 memory 和 sessions 目录的用途

**验证：** 目视检查示例文件内容完整

## 执行顺序

```
T1（Session ID）─┐
T2（Conv 回调）──┤
T3（指令加载）──┤
T9（笔记存储）──┤── 独立基础模块，可并行
T12（Prompt 参数化）─┤
T13（/resume 注册）──┤
T17（配置示例）──────┘

T4（Session Writer）── 依赖 T1
T5（会话列表）──────── 依赖 T1
T6（会话加载）──────── 依赖 T4
T7（会话清理）──────── 依赖 T1
T8（Session 测试）──── 依赖 T4,T5,T6,T7

T10（记忆管理器）──── 依赖 T9
T11（Memory 测试）─── 依赖 T9,T10

T14（会话列表 UI）─── 依赖 T5,T13
T15（Agent 记忆触发）── 依赖 T10,T12
T16（main.go 串联）─── 依赖 T1,T2,T3,T4,T10,T12,T14,T15
```