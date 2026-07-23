# 项目记忆与会话持久化 Plan## 架构概览

本章新增三个独立包，加上对现有包的窄幅修改：

| 新包 | 职责 |
|------|------|
| `internal/instructions` | 三层 MEWCODE.md 加载 + @include 展开 |
| `internal/session` | JSONL 会话写入、列表扫描、加载恢复、过期清理 |
| `internal/memory` | 笔记 CRUD、索引管理、异步 LLM 更新 |

| 已有包 | 改动 |
|--------|------|
| `internal/prompt` | `BuildSystemPrompt` 接受 instructions/memory 参数 |
| `internal/conversation` | 新增 OnAppend/OnReplace 回调 |
| `internal/compact/state.go` | session ID 格式改为 YYYYMMDD-HHMMSS-xxxx；SessionContext 加 SessionDir 字段 |
| `internal/agent` | 每 5 轮 Run 结束后触发记忆更新 |
| `internal/tui` | 新增 /resume 命令和 stateResuming 状态 |
| `cmd/mewcode/main.go` | 启动流程串联指令加载、记忆初始化、会话清理 |

## 核心数据结构### instructions 包

```go
// Loader 负责三层 MEWCODE.md 的加载和 @include 展开。
type Loader struct {
    projectRoot string
    userHome    string
    maxDepth    int // 固定 5
}

// Load 按优先级加载三层指令文件，返回拼接后的完整指令文本。
// 加载失败的层静默跳过，全部为空返回空字符串。
func (l *Loader) Load() (string, error)

// loadFile 加载单个文件，处理 @include 展开。
// boundary 是路径逃逸检测的根边界。
// depth 当前嵌套层数（从 1 开始），visited 环路检测集合。
func (l *Loader) loadFile(path, boundary string, depth int, visited map[string]struct{}) (string, error)
```

### session 包

```go
// Entry 是 JSONL 中一行的结构。
type Entry struct {
    Type        string           `json:"type,omitempty"`         // "compact" 或空
    Role        string           `json:"role,omitempty"`         // "user"/"assistant"/"tool"
    Content     string           `json:"content,omitempty"`
    ToolCalls   []llm.ToolCall   `json:"tool_calls,omitempty"`
    ToolResults []llm.ToolResult `json:"tool_results,omitempty"`
    Timestamp   int64            `json:"ts"`
    Model       string           `json:"model,omitempty"`        // 仅首条消息
}

// Writer 负责向 conversation.jsonl 追加写入。
type Writer struct {
    mu   sync.Mutex
    file *os.File
    enc  *json.Encoder
}

func NewWriter(sessionDir string) (*Writer, error)
func (w *Writer) Append(msg llm.Message, model string, isFirst bool) error
func (w *Writer) WriteCompactMarker() error
func (w *Writer) AppendAll(msgs []llm.Message) error
func (w *Writer) Close() error

// SessionInfo 是会话列表中一项的摘要信息。
type SessionInfo struct {
    ID         string    // session ID（目录名）
    Title      string    // 第一条 user 消息内容（截断）
    ModifiedAt time.Time // 最后修改时间
    Model      string    // 模型标签
    Size       int64     // JSONL 文件大小
    Dir        string    // 会话目录绝对路径
}

// ListSessions 扫描 sessionsDir，返回按修改时间倒序排列的会话列表。
// 只返回包含 conversation.jsonl 且 ID 能解析为新格式的目录。
func ListSessions(sessionsDir string) ([]SessionInfo, error)

// LoadSession 从 conversation.jsonl 恢复消息列表。
// 从最后一个 compact 标记之后加载，跳过坏行，截断孤立工具调用。
func LoadSession(sessionDir string) ([]llm.Message, error)

// CleanExpired 删除超过 maxAge 的会话目录。
// 只处理新格式 ID 的目录，旧格式跳过。
func CleanExpired(sessionsDir string, maxAge time.Duration) error

// OpenWriter 打开已有会话的 JSONL 进行追加写入（恢复场景）。
func OpenWriter(sessionDir string) (*Writer, error)
```

### memory 包

```go
// NoteType 笔记类型。
type NoteType string

const (
    TypeUserPreference    NoteType = "user_preference"
    TypeCorrectionFeedback NoteType = "correction_feedback"
    TypeProjectKnowledge  NoteType = "project_knowledge"
    TypeReferenceMaterial NoteType = "reference_material"
)

// Note 一条笔记的内存表示。
type Note struct {
    Type     NoteType
    Title    string
    Slug     string
    Content  string
    Filename string
    Created  time.Time
    Updated  time.Time
}

// UpdateAction LLM 返回的单条操作。
type UpdateAction struct {
    Action   string `json:"action"`   // "create"/"update"/"delete"
    Level    string `json:"level"`    // "project"/"user"
    Type     string `json:"type"`     // NoteType（create 时必需）
    Title    string `json:"title"`    // 笔记标题
    Slug     string `json:"slug"`     // 文件名 slug（create 时必需）
    Content  string `json:"content"`  // 笔记正文（create/update 时必需）
    Filename string `json:"filename"` // 已有文件名（update/delete 时必需）
}

// Store 管理单级（项目级或用户级）的笔记文件和索引。
type Store struct {
    dir string // .mewcode/memory/ 或 ~/.mewcode/memory/
    mu  sync.Mutex
}

func NewStore(dir string) *Store
func (s *Store) LoadIndex() (string, error)        // 读取 MEMORY.md 内容
func (s *Store) Apply(actions []UpdateAction) error // 执行 create/update/delete
func (s *Store) EnsureDir() error                   // 确保目录存在

// Manager 编排项目级和用户级笔记的加载和更新。
type Manager struct {
    projectStore *Store
    userStore    *Store
    provider     llm.Provider
    model        string
    mu           sync.Mutex // 保护并发更新
}

func NewManager(projectDir, userDir string, provider llm.Provider, model string) *Manager
func (m *Manager) LoadIndex() string                // 合并两级索引，截断到 25KB
func (m *Manager) UpdateAsync(ctx context.Context, recentMsgs []llm.Message) // 异步 LLM 调用
func (m *Manager) SetProvider(provider llm.Provider, model string)           // 切换 provider 后更新
```

### conversation 包（修改）

```go
type Conversation struct {
    mu        sync.Mutex
    messages  []llm.Message
    onAppend  func(llm.Message)   // 可选：消息追加回调
    onReplace func([]llm.Message) // 可选：消息替换回调
}

// NewWithHooks 创建带回调的会话。
func NewWithHooks(onAppend func(llm.Message), onReplace func([]llm.Message)) *Conversation

// NewFromMessages 从已有消息列表创建会话（恢复场景），可选回调。
func NewFromMessages(msgs []llm.Message, onAppend func(llm.Message), onReplace func([]llm.Message)) *Conversation
```

### compact/state.go（修改）

```go
type SessionContext struct {
    SessionID  string // 形如 "20260601-143022-a1b2"
    SessionDir string // <workspace>/.mewcode/sessions/<SessionID>
    SpillDir   string // SessionDir + "/tool-results"
}

// newSessionID 改为 YYYYMMDD-HHMMSS-xxxx 格式。
func newSessionID() string

// OpenSessionContext 打开已有会话目录（恢复场景）。
func OpenSessionContext(workspace, sessionID string) (*SessionContext, error)
```

### prompt 包（修改）

```go
// BuildSystemPrompt 组装完整系统提示。
// instructions 非空时填入 custom-instructions 模块（priority 80）。
// memory 非空时填入 long-term-memory 模块（priority 100）。
func BuildSystemPrompt(instructions, memory string) string
```

### agent 包（修改）

```go
// Option 新增记忆管理器注入。
func WithMemoryManager(m *memory.Manager) Option

// Agent 新增字段。
type Agent struct {
    // ... 已有字段
    memMgr *memory.Manager // 可选：记忆更新管理器
}
```

## 模块交互### 启动流程

```
main()
  ├─ config.Load()
  ├─ instructions.NewLoader(projectRoot).Load() → instructionText
  ├─ memory.NewManager(projectMemDir, userMemDir, nil, "").LoadIndex() → memoryText
  │   （provider 未选定时先空，选定后 SetProvider）
  ├─ compact.NewSessionContext(root) → sesCtx  （新格式 session ID）
  ├─ session.NewWriter(sesCtx.SessionDir) → writer
  ├─ go session.CleanExpired(sessionsDir, 30*24h)  （后台清理）
  ├─ tool.NewDefaultRegistry()
  ├─ mcp.NewManager() → mcpTools → registry.Register()
  ├─ permission.NewEngine()
  ├─ agent.NewSessionRuntime(ctxWindow)
  │   └─ runtime.Session = sesCtx
  └─ tui.New(providers, ..., writer, memMgr, instructionText, memoryText)
       └─ 选定 provider 后：
           ├─ memMgr.SetProvider(provider, model)
           └─ agent.New(..., WithMemoryManager(memMgr))
```

### Agent Loop 与记忆更新

```
Agent.Run(ctx, conv, mode) → ch
  for {
    streamOnce → text / toolCalls
    if 无工具调用（Done）:
      conv.AddAssistant(text)  → Writer.Append (via onAppend)
      // 每 5 轮或检测到显式记忆请求时触发异步记忆更新
      if memMgr != nil:
        runtime.TurnCount++
        recentMsgs := extractRecentTurn(conv)
        if runtime.TurnCount % 5 == 0 || hasMemorySignal(recentMsgs):
          go memMgr.UpdateAsync(ctx, recentMsgs)
      emit Done
      return
    // 有工具调用：继续迭代
    ...
  }
```

### /resume 恢复流程

```
TUI: /resume → stateResuming
  ├─ session.ListSessions(sessionsDir) → items
  ├─ 显示 list.Model（上下选择 + 搜索）
  ├─ Enter 选择：
  │   ├─ session.LoadSession(selectedDir) → msgs
  │   ├─ 检查孤立工具调用 → 截断
  │   ├─ 估算 token → 超限则压缩
  │   ├─ 检查时间跨度 → 超 6h 追加提醒
  │   ├─ conversation.NewFromMessages(msgs, onAppend, onReplace) → newConv
  │   ├─ compact.OpenSessionContext(root, selectedID) → newSesCtx
  │   ├─ session.OpenWriter(selectedDir) → newWriter
  │   ├─ 替换 TUI 的 conv、writer、sesCtx、runtime.Session
  │   ├─ 显示 "已恢复会话 <id>，共 N 条消息"
  │   └─ stateIdle
  └─ Esc → stateIdle（不变）
```

### JSONL 写入时序

```
用户输入 "hello"
  → conv.AddUser("hello")
    → onAppend(Message{Role:"user", Content:"hello"})
      → Writer.Append(msg, model, isFirst=true)
        → {"role":"user","content":"hello","ts":1717200000,"model":"gpt-5.4-mini"}\n

Agent 回复 "hi!"
  → conv.AddAssistant("hi!")
    → onAppend(Message{Role:"assistant", Content:"hi!"})
      → Writer.Append(msg, model, isFirst=false)
        → {"role":"assistant","content":"hi!","ts":1717200005}\n

压缩触发
  → conv.ReplaceMessages(newMsgs)
    → onReplace(newMsgs)
      → Writer.WriteCompactMarker()
        → {"type":"compact","ts":1717201000}\n
      → Writer.AppendAll(newMsgs)
        → 逐条追加新消息
```

## 文件组织

```
internal/
├── instructions/
│   ├── loader.go         — Loader 类型、Load、loadFile、@include 展开
│   └── loader_test.go    — 三层加载、@include 深度/环路/逃逸测试
├── session/
│   ├── writer.go         — Writer、Entry、NewWriter、Append、WriteCompactMarker
│   ├── list.go           — ListSessions、SessionInfo、扫描逻辑
│   ├── load.go           — LoadSession、坏行跳过、孤立截断
│   ├── cleanup.go        — CleanExpired、ID 时间戳解析
│   └── session_test.go   — JSONL 读写、列表、恢复、清理测试
├── memory/
│   ├── store.go          — Store、笔记文件 CRUD、索引读写
│   ├── manager.go        — Manager、LoadIndex、UpdateAsync、更新 prompt
│   ├── types.go          — NoteType、Note、UpdateAction 类型定义
│   └── memory_test.go    — 索引加载、操作执行、截断测试
├── prompt/
│   └── prompt.go         — BuildSystemPrompt 签名变更（+instructions, +memory）
│   └── modules.go        — OptionalModules 改为接受参数
├── conversation/
│   └── conversation.go   — NewWithHooks、NewFromMessages、回调触发
├── compact/
│   └── state.go          — newSessionID 格式变更、SessionDir 字段、OpenSessionContext
├── agent/
│   ├── agent.go          — 每 5 轮 Run 末尾触发 memMgr.UpdateAsync
│   └── runtime.go        — WithMemoryManager option
├── tui/
│   ├── commands.go       — /resume 注册
│   ├── resume.go         — stateResuming、会话列表项、updateResuming
│   └── tui.go            — stateResuming 状态集成、Model 新增 writer/memMgr 字段
└── cmd/mewcode/
    └── main.go           — 启动流程串联
```

## 技术决策

| 决策点 | 选择 | 理由 |
|--------|------|------|
| 指令文件格式 | 手写 Markdown | 用户直接编辑，不需要特殊工具；与系统提示纯文本注入无缝衔接 |
| @include 深度限制 | 5 层 | 足够覆盖合理的模块化拆分，又不会因为误配无限递归 |
| 会话存储格式 | JSONL 追加写 | 追加快、崩溃安全（只丢最后一行）、无需维护索引文件 |
| 压缩后 JSONL 处理 | compact 标记行 + 追加新消息 | 保持追加语义，恢复时从最后 compact 标记开始加载 |
| session ID 格式 | YYYYMMDD-HHMMSS-xxxx | 人类可读，可直接从 ID 解析时间戳用于过期清理和排序 |
| 记忆更新触发点 | 每 5 轮或检测到"记住"关键词时 | 定时提取控制频率；关键词检测保证显式请求不漏 |
| 记忆去重策略 | LLM 判断 | 语义级去重比机械字符串匹配更准确，且实现简单 |
| 记忆注入方式 | 索引注入系统提示 | 约 2-3K tokens 开销可控，模型通过索引感知全貌，详情按需读文件 |
| Conversation 回调 | 构造时注入函数 | 最小侵入，不需要引入事件总线；未设置回调时零开销 |
| /resume 列表组件 | 复用 bubbletea list.Model | 与已有 provider 选择列表一致的交互模式，减少代码和认知负担 |
| 记忆 provider | 复用主会话 provider | 简单直接，不引入额外配置；未来可扩展为配置专用 provider |