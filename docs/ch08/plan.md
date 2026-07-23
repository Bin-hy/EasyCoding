# 上下文管理 Plan## 架构概览

ch08 引入一个新的本地包 `internal/compact/`，作为上下文管理的唯一权威入口。包内承担三块职责：

1. **第 1 层预防性压缩**：在每一轮 LLM 请求发出之前，对 `conversation` 中的工具结果做幂等的"超阈值落盘 + 字符串替换"，并把替换决策冻结在一个会话级账本里，保证 prompt cache 前缀逐字节稳定。
2. **第 2 层 LLM 摘要 + 恢复**：在估算 token 触达阈值（或被手动 / 紧急触发）时，调用 provider 跑一次结构化摘要请求，生成 9 部分摘要 + 三段恢复 + 近期原文，构造一个新的 `[]llm.Message` 替换掉旧的对话历史。
3. **辅助子模块**：token 估算（锚定真实 usage + 字符增量）、最近读过文件的并发安全追踪、会话目录管理、PTL 自重试与熔断器。

`internal/compact/` 不直接持有 `Agent`，也不直接管理 `Provider`。它通过一组窄接口与外部模块交互：

| 外部模块 | 交互方向 | 形式 |
|----------|----------|------|
| `internal/agent/` | Agent 调 compact | 主循环每轮请求前调 `ManageContext`；ReadFile 成功后调 `RecoveryState.RecordFile`；捕获 `prompt_too_long` 后调 `ForceCompact` 重试一次 |
| `internal/conversation/` | compact 改 conversation | compact 拿到 `[]llm.Message` 后做字符串替换 / 摘要重建，再用一个新方法 `ReplaceMessages` 整体替换内存数组 |
| `internal/llm/` | compact 调 provider | 摘要请求复用同一份 `llm.Provider.Stream`，但 `Request.Tools` 留空；从 `StreamEvent` 尾部拿 usage 锚定 token 估算 |
| `internal/tui/` | TUI 调 compact | TUI 拿到以 `/` 开头的输入走命令分发；`/compact` 命令调 compact 的 `ForceCompact` 并展示 token 变化系统消息 |
| `internal/config/` | config 喂 compact | `ProviderConfig` 新增 `ContextWindow int`，未配置时按协议给默认值；compact 通过参数拿到当前 provider 的 context_window |

**Agent 生命周期与状态归属调整**：现状的 TUI 在 `beginTurn` 里每轮 `agent.New(...).Run(...)` 重新构造一次 Agent（见 `internal/tui/stream.go:96`），意味着把 compact 的长生命周期状态（替换决策账本、文件追踪、自动摘要熔断计数、usageAnchor、本轮工具切片缓存）放成 Agent 字段会被每轮重置——决策冻结与熔断器立刻失效。

本章引入 `SessionRuntime` 作为 TUI Model 跨 Run 持有的长生命周期状态容器：

```go
// internal/agent/runtime.go（建议新建，或挂在 internal/compact 由 agent re-export）
type SessionRuntime struct {
    Replacement   *compact.ContentReplacementState
    Recovery      *compact.RecoveryState
    AutoTracking  *compact.AutoCompactTrackingState
    Session       *compact.SessionContext
    ContextWindow int
    UsageAnchor   int64 // 上一次主对话路径 Stream 真实 usage 之和；摘要请求不更新此字段
    AnchorMsgLen  int   // anchor 当时 Conversation.Len()，下次估算只算这之后的字符增量
}
```

`agent.New` 构造期接受 `*SessionRuntime` 注入；TUI Model 持有同一份 `*SessionRuntime` 跨轮复用。状态所有权关系：TUI Model 拥有 SessionRuntime；每轮把 SessionRuntime 与 Conversation 一并交给 Agent。compact 是逻辑层，对状态零持有、可重入。

**依赖方向无环**：
- `compact` 不 import `agent` / `config` / `tui` / `cmd`。
- `config` 仅在 `EffectiveContextWindow()` 中读自身常量（`DefaultAnthropicContextWindow` / `DefaultOpenAIContextWindow` 定义在 `internal/config/protocol_defaults.go`，不放 compact 包）。
- `agent` 依赖 `compact` + `conversation` + `llm` + `tool` + `permission`，**不** import `config`。
- `cmd/mewcode` 是唯一同时 import `config` 与 `agent` 的位置，负责把 `providerCfg.EffectiveContextWindow()` 注入 SessionRuntime。
- `tui` 持有 `*SessionRuntime` 与 `*agent.Agent`（或在每轮构造 Agent 时把 runtime 传入）。

## 核心数据结构

```go
// internal/compact/state.go

// ContentReplacementState 是会话级的"工具结果替换决策账本"。
// seenIds 记录已经决策过的 tool_use_id，无论决策是替换还是保留原文。
// replacements 只保存"决定替换"那一支的预览字符串，键是 tool_use_id。
// 同一个 tool_use_id 一旦进入 seenIds 就再也不会被重新评估，保证 prompt cache 稳定。
//
// 并发安全约束：OffloadAndSnip 在执行期间持有 mu 全程加锁（读账本 → 决策 → 落盘 →
// 写账本必须在同一临界区内原子完成），避免出现"已 Seen 但 replacement 未写"的中间态。
// 对外只暴露一个高层方法 DecideOnce 让调用方传入决策回调，由本类型内部统一加锁。
// 一旦预览字符串写入 replacements[id]，本会话内不允许修改（包括其中内嵌的 originalBytes
// 字段）。OffloadAndSnip 永远不重新调用 buildPreview，已 Seen 的 id 直接复用现存字符串。
type ContentReplacementState struct {
    mu           sync.Mutex
    seenIds      map[string]struct{}
    replacements map[string]string
}

func NewContentReplacementState() *ContentReplacementState

// DecideOnce 一次性完成"查账本→决策→写账本"。
// decide 回调在持锁状态下被调用，返回 (是否替换, 预览字符串)。
// 若 id 已 Seen：直接返回现存 replacement（若是 MarkKept 则返回原 content）。
// 若 decide 返回 replace=false：MarkKept，返回原 content。
// 若 decide 返回 replace=true：MarkReplaced，返回 preview。
// 落盘 I/O 应在 decide 内完成；decide 返回错误（通过 replace=false 信号）即不写账本，
// 下一轮可重新评估该 id。
func (s *ContentReplacementState) DecideOnce(id, originalContent string, decide func() (replace bool, preview string)) string

// AutoCompactTrackingState 跟踪自动摘要连续失败次数，用于熔断。
// 手动 / 紧急压缩路径不读这个字段。
type AutoCompactTrackingState struct {
    mu                  sync.Mutex
    ConsecutiveFailures int
}

func NewAutoCompactTrackingState() *AutoCompactTrackingState

// RecoveryState 是 Agent 主循环写、compact 摘要时读的文件追踪状态。
// files 的键是文件绝对路径，避免相对路径在不同 cwd 下错乱。
type RecoveryState struct {
    mu    sync.Mutex
    files map[string]FileReadRecord
}

type FileReadRecord struct {
    Path      string
    Content   string    // 不带行号前缀的纯净字节
    Timestamp time.Time // 最后一次成功读取的时间
}

func NewRecoveryState() *RecoveryState
func (r *RecoveryState) RecordFile(path, content string)
func (r *RecoveryState) Snapshot() []FileReadRecord // 已按时间戳倒序

// SessionContext 是会话生命周期信息。SessionID 进程启动时一次性生成。
// SpillDir 是落盘目录，固定指向 .mewcode/sessions/<session_id>/tool-results/。
type SessionContext struct {
    SessionID string
    SpillDir  string
}

func NewSessionContext(workspace string) (*SessionContext, error)
```

```go
// internal/compact/const.go

const (
    singleResultLimit                 = 50000   // 单条工具结果落盘阈值（字符）
    messageAggregateLimit             = 200000  // 单条 assistant 消息内工具结果聚合阈值（字符）
    summaryReserve                    = 20000   // 给摘要 LLM 输出预留的 token 空间
    autoSafetyMargin                  = 13000   // 自动触发的额外安全余量：防估算误差与单轮波动
    manualSafetyMargin                = 3000    // 手动触发的安全余量：只用来判断摘要请求本身能不能塞下
    recoveryFileLimit                 = 5       // 恢复段最多展示几个文件
    recoveryTokensPerFile             = 5000    // 单个文件快照的 token 上限，超出时保留头部、截掉尾部
    recentKeepTokens                  = 10000   // 摘要后保留近期原文的 token 下界
    recentKeepMessages                = 5       // 摘要后保留近期原文的条数下界
    maxConsecutiveAutoCompactFailures = 3       // 熔断阈值
    ptlRetryLimit                     = 3       // 摘要请求自身 PTL 的"直接重试"次数
    ptlDropPercentage                 = 0.2     // 3 次后每次再丢的比例
    estimateCharsPerToken             = 3.5     // 增量估算的字符/token 比
    previewHeadBytes                  = 2048    // 预览体头部字节数上限
    previewHeadLines                  = 20      // 预览体头部行数上限
)

const defaultAnthropicContextWindow = 200000
const defaultOpenAIContextWindow    = 128000
```

```go
// internal/config/config.go 改动（仅追加字段，不动现有字段顺序与标签）

type ProviderConfig struct {
    Name          string `yaml:"name"`
    Protocol      string `yaml:"protocol"`
    BaseURL       string `yaml:"base_url"`
    APIKey        string `yaml:"api_key"`
    Model         string `yaml:"model"`
    Thinking      bool   `yaml:"thinking"`        // 仅 anthropic 生效
    ContextWindow int    `yaml:"context_window"`  // 新增字段，单位 token，0 表示走协议默认
}

// internal/config/protocol_defaults.go（新文件）
const (
    DefaultAnthropicContextWindow = 200000
    DefaultOpenAIContextWindow    = 128000
)

// 派生方法，给 compact / cmd 用
func (p ProviderConfig) EffectiveContextWindow() int {
    if p.ContextWindow > 0 {
        return p.ContextWindow
    }
    switch p.Protocol {
    case ProtocolAnthropic:
        return DefaultAnthropicContextWindow
    case ProtocolOpenAI:
        return DefaultOpenAIContextWindow
    default:
        return DefaultAnthropicContextWindow
    }
}
```

> **依赖方向说明**：协议默认值常量定义在 `internal/config/protocol_defaults.go`，由 `config` 自身使用；`compact` 包不持有协议默认值常量。`config` 与 `compact` 单向无环。

## 模块设计### compact 包#### `compact.go` - ManageContext 主入口

```go
type ManageInput struct {
    Conv           *conversation.Conversation
    Provider       llm.Provider
    Model          string
    ContextWindow  int
    ToolDefs       []llm.ToolDefinition       // 主循环本轮迭代开头按当前 mode 选好的工具定义切片，恢复段与 Stream 共用此切片
    Replacement    *ContentReplacementState
    Recovery       *RecoveryState
    AutoTracking   *AutoCompactTrackingState
    Session        *SessionContext
    UsageAnchor    int64                       // 上一次主对话路径 Stream 真实 usage 之和
    AnchorMsgLen   int                         // anchor 当时 Conversation.Len()，用于"只算锚点之后的字符增量"
    EstimatedToken int64                       // 调用方算好的本轮估算 token（= anchor + chars/3.5）
    Trigger        TriggerKind                 // TriggerAuto / TriggerManual / TriggerEmergency
}

type ManageOutput struct {
    BeforeTokens int64
    AfterTokens  int64
}

// ManageContext 是 Agent 每轮请求前必调的唯一入口。
// 步骤：
//   1. 若 Trigger == TriggerManual：跳过第 1 层、阈值、熔断；直接 ForceCompact。
//      若 Trigger == TriggerEmergency：先强制跑一次 OffloadAndSnip（layer1），
//      再无条件 ForceCompact——避免摘要请求本身因为大工具结果直接撞 PTL。
//   2. 否则（Auto 路径）：
//      a. 先执行第 1 层 OffloadAndSnip 得到 updatedMsgs；
//      b. 用 EstimateTokens(in.UsageAnchor, updatedMsgs, in.AnchorMsgLen) 重算估算 token
//         （**必须用 layer1 之后的 updatedMsgs**，否则估算会偏高、过早触发 layer2）；
//      c. 若估算 < (ContextWindow - summaryReserve - autoSafetyMargin) 或 AutoTracking.Tripped()：
//         直接返回，仅 layer1 生效；
//      d. 否则 AutoCompact，成功后 ReplaceMessages。
//
// BeforeTokens / AfterTokens 口径：
//   - BeforeTokens = ManageContext 入口处的 in.EstimatedToken（已包含调用方算好的 anchor + 增量）；
//   - AfterTokens = layer2 替换 conversation 后用 EstimateTokens(0, newMsgs, 0) 重新算的值；
//     若只跑了 layer1，AfterTokens = EstimateTokens(in.UsageAnchor, layer1Out, in.AnchorMsgLen)。
func ManageContext(ctx context.Context, in ManageInput) (ManageOutput, error)
```

职责：编排两层调用顺序、决定走自动 / 手动 / 紧急路径、把替换/摘要后的消息写回 `Conversation`、更新熔断器计数。

依赖：`layer1.OffloadAndSnip`、`layer2.AutoCompact`、`layer2.ForceCompact`、`token.EstimateTokens`。

#### `layer1.go` - 单结果与聚合落盘 + 决策冻结

```go
// OffloadAndSnip 遍历 conv.Messages()，针对每一条 RoleTool 消息上的 ToolResults
// 切片做处理（mewcode 在 conversation.AddToolResults 把一轮工具结果挂在一条 RoleTool
// 消息上，工具结果不在 assistant 消息里）。规则：
//   1. 已经在 Replacement.seenIds 中的工具结果，通过 DecideOnce 拿到现存决策结果
//      （MarkKept → 返回原文；MarkReplaced → 复用 replacements[id]，**不重新构造** preview）；
//   2. 未决策的项进入候选列表，按字节倒序处理：
//      a. 单条 > singleResultLimit：spillSingle 成功 → 改写 Content → MarkReplaced，
//         同时把该项从聚合预算里扣除；
//      b. 然后看剩余项的聚合字节是否 > messageAggregateLimit；继续按倒序逐项落盘，
//         直至剩余聚合 ≤ messageAggregateLimit；
//      c. 未落盘的项 MarkKept。
//   3. 落盘失败时降级为不替换、不写账本（DecideOnce 通过 replace=false 信号实现），下次重试。
//   4. 落盘 → 改写 Content → 写账本 三个动作通过 DecideOnce 在持锁状态下顺序执行，
//      任一步失败回退到 MarkKept；保证 Content 与账本永远一致。
// 返回新的 []llm.Message，纯函数风格，不修改入参。
func OffloadAndSnip(
    msgs []llm.Message,
    state *ContentReplacementState,
    session *SessionContext,
) ([]llm.Message, error)

// spillSingle 把单条 tool_result 内容写入 SpillDir/<tool_use_id>。
// 幂等：文件已存在则不重写、不报错。
func spillSingle(session *SessionContext, toolUseID, content string) error

// buildPreview 构造替换体字符串，包含原始字节数、头部预览、落盘路径、重读提示。
// 头部预览策略：取 min(前 previewHeadLines 行, 前 previewHeadBytes 字节)。
// 调用时机：只在 OffloadAndSnip 内首次决策为替换的瞬间调用一次；之后所有轮次都必须
// 通过 state.DecideOnce 复用 replacements[id] 里存好的字符串，不允许重新调用。
func buildPreview(originalBytes int, head string, spillPath string) string
```

职责：单条 / 聚合判断、落盘 I/O、预览体格式化、账本写入。

依赖：`SessionContext`、`ContentReplacementState`。

#### `layer2.go` - 摘要、PTL 重试、熔断

```go
// AutoCompact 在熔断器未触发时执行，整轮（含 PTL 自重试）失败累加 ConsecutiveFailures，成功清零。
// beforeTok = in.EstimatedToken；afterTok = EstimateTokens(0, newMsgs, 0)。
func AutoCompact(ctx context.Context, in ManageInput) (newMsgs []llm.Message, beforeTok, afterTok int64, err error)

// ForceCompact 手动 / 紧急路径专用：跳过熔断器。
// beforeTok / afterTok 口径同 AutoCompact。失败也不计入熔断。
func ForceCompact(ctx context.Context, in ManageInput) (newMsgs []llm.Message, beforeTok, afterTok int64, err error)

// runSummary 是两条路径的共同核心：构造摘要 prompt、发请求、解析 <summary>、
// 拼接恢复段、追加近期原文边界裁剪。
// 调用入口必须先拍一次 recoverySnapshot := in.Recovery.Snapshot()，整个 runSummary
// 生命周期内只使用这一份快照，避免恢复段渲染期间 RecordFile 写入造成"声明的工具/文件"
// 与"Stream 调用时刻状态"漂移。
func runSummary(ctx context.Context, in ManageInput) ([]llm.Message, error)

// summarizeOnce 发一次摘要请求。
// 实现要点：for ev := range provider.Stream(ctx, req)；text 累加；ev.Usage 捕获；
// ev.Err 非 nil 时立即终止并把该 err 返回；PTL 由调用方通过 errors.Is(err, llm.ErrPromptTooLong)
// 识别并切到 ptlRetry。
// **重要**：摘要请求结束后不更新 SessionRuntime.UsageAnchor；usageAnchor 只由主对话路径维护。
func summarizeOnce(ctx context.Context, in ManageInput, msgs []llm.Message) (string, error)

// ptlRetry 实现 F27 的丢消息组策略：
//   - 前 ptlRetryLimit 次：每次丢最旧的若干"用户提交 + 一组 assistant/tool 往返"分组；
//   - 之后：每次按当前剩余消息组数 × ptlDropPercentage 丢；
//   - 直到摘要请求能塞下，或全部丢光返回错误。
func ptlRetry(ctx context.Context, in ManageInput, msgs []llm.Message, err error) (string, error)

// pickRecentTail 从 msgs 尾部累加，满足以下条件后停止：
//   - 累计估算 token ≥ recentKeepTokens；或
//   - 累计消息数 ≥ recentKeepMessages；
//   - 二者择宽。
// 之后再做"tool_use/tool_result 配对修正"：若截断点夹在配对中间，向前推到 tool_use 之前。
func pickRecentTail(msgs []llm.Message) []llm.Message

// groupByUserTurn 按 F27 的"用户提交 → 一组 assistant/tool 往返"分组，给 ptlRetry 用。
func groupByUserTurn(msgs []llm.Message) [][]llm.Message
```

职责：摘要 LLM 请求构造、PTL 自重试、熔断计数维护、近期原文边界推算。

依赖：`llm.Provider`、`summary_prompt`、`recovery`、`token`、`AutoCompactTrackingState`。

#### `summary_prompt.go` - 摘要 Prompt 模板

```go
// BuildSummaryPrompt 把对话 msgs 嵌入到固定模板里。
// 返回长度为 1 的切片，仅一条 user 消息，其 Content 形如：
//
//   You are summarizing a coding agent conversation. Output in two phases.
//
//   <analysis>
//   （在这里写分析草稿，会被丢弃）
//   </analysis>
//
//   <summary>
//   ## 1 主要请求和意图
//   ## 2 关键技术概念
//   ## 3 文件和代码段
//   ## 4 错误和修复
//   ## 5 问题解决过程
//   ## 6 所有用户消息原文
//   ## 7 待办任务
//   ## 8 当前工作（最详细）
//   ## 9 可能的下一步
//   </summary>
//
//   不要调用任何工具，输出纯文本。
//
//   [conversation]
//   <serializeConversation(msgs) 的输出>
//
// 9 个小节标题在 prompt 中是固定字面字符串，便于 ExtractSummary 解析与测试匹配。
func BuildSummaryPrompt(msgs []llm.Message) []llm.Message

// serializeConversation 把对话扁平化成可读文本（不暴露 ToolCall.Input 原 JSON）：
//   - 每条 user/assistant 消息：role: <content>
//   - assistant 工具调用：[call <name> id=<id> args=<json string>]
//   - tool 消息内的每条 result：[result id=<id> isError=<bool>] <content>
// 行间用 \n 隔开；本函数纯函数，不依赖外部状态，便于单测固定预期文本。
func serializeConversation(msgs []llm.Message) string

// ExtractSummary 从模型返回的整段文本里抠出 <summary>...</summary> 之间的正文。
// <analysis> 部分直接丢弃。提取失败时返回原文 + 一个 warning，避免硬失败。
func ExtractSummary(raw string) string
```

职责：维护摘要 prompt 的全文文案、解析模型输出。

依赖：无（纯模板 + 字符串解析）。

#### `recovery.go` - 三段恢复

```go
// BuildRecoveryAttachment 构造摘要后的"恢复三段"内容。
// 调用方必须先在 runSummary 入口拍一次快照 snapshot := recovery.Snapshot()，
// 把快照而非 *RecoveryState 传入本函数，避免恢复段渲染期间另一个 goroutine
// 通过 RecordFile 改变状态导致漂移。
// 三段：
//   1. 最近读过的文件快照：取 snapshot 前 recoveryFileLimit 个（已按时间戳倒序），
//      单文件 > recoveryTokensPerFile token 时保留头部对应字符片段，
//      截掉尾部多余内容，并在尾部追加 (content truncated)；
//   2. 当前可用工具列表：直接来自入参 toolDefs（与 Stream 调用同一切片引用），
//      保证恢复段宣称的工具集与 Request.Tools 完全一致；
//   3. 边界提示消息：固定文案。
// 返回 llm.Message{Role: RoleUser, Content: "..."}。摘要消息与恢复消息合并到
// 同一条 user 消息上输出（见 layer2.runSummary 拼装规则），避免 user/user 连续；
// 本函数只负责返回"恢复三段"的内容片段，layer2 会与摘要文本拼到同一条 user 消息上。
func BuildRecoveryAttachment(
    snapshot []FileReadRecord,
    toolDefs []llm.ToolDefinition,
) string

// renderFileBlock 渲染单个文件快照：路径 / 时间戳 / 内容片段（必要时截断）。
func renderFileBlock(rec FileReadRecord) string

// renderToolsBlock 渲染工具列表：每行一个工具名 + 用途 + 参数 schema 摘要。
func renderToolsBlock(defs []llm.ToolDefinition) string

// boundaryNotice 是边界提示消息的固定文案常量。
const boundaryNotice = `...固定文案：需要文件原文/错误原文/用户原话时，请使用文件读取工具重新读取对应路径，不要依据摘要内容做猜测...`
```

职责：把 RecoveryState 快照 + toolDefs 组合成一段稳定文本。

依赖：`FileReadRecord`、`llm.ToolDefinition`。

#### `token.go` - Token 估算

```go
// EstimateTokens 锚定最近一次 provider usage + 之后新增消息的字符增量。
// 入参语义：
//   - anchor: 上一次主对话路径 Stream 真实 usage 之和（int64，与 llm.Usage 字段类型保持一致）；
//   - allMsgs: 当前 Conversation.Messages() 完整切片；
//   - anchorMsgLen: 当 anchor 被记录时 Conversation.Len() 的值，
//     表示锚点之前已被这份 usage 算进的消息条数；
//   - 函数只把 allMsgs[anchorMsgLen:] 这部分的字符累加，避免把已含在 anchor 里的历史
//     重复计算。
//   - 入参 allMsgs 必须是已经经过 OffloadAndSnip 处理（layer1 之后）的消息切片；
//     否则估算偏高，会过早触发 layer2。
//   - 返回 anchor + ceil(sum(chars(msg)) / estimateCharsPerToken)。
// 锚点为 0、anchorMsgLen 为 0（首轮 / 摘要后）时退化为纯字符估算。
func EstimateTokens(anchor int64, allMsgs []llm.Message, anchorMsgLen int) int64

// UsageAnchor 把 Stream 尾事件中的 usage 合并成单一锚点值（int64，无溢出风险）。
// 等价于 u.InputTokens + u.OutputTokens + u.CacheRead + u.CacheWrite。
func UsageAnchor(u llm.Usage) int64

// messageChars 计算单段消息切片的字符总量。
// 累加 len(Content) + 每个 ToolCalls[i].Input 长度 + 每个 ToolResults[i].Content 长度。
func messageChars(msgs []llm.Message) int
```

职责：纯函数估算。

依赖：无。

### Agent 主循环改造（`internal/agent/agent.go`）

Agent 通过 `SessionRuntime` 拿到所有长生命周期状态；Agent 自身只新增轻量字段：

```go
type Agent struct {
    // ... 原有字段（provider / registry / version / eng）
    runtime *SessionRuntime  // 由 New 注入，跨 Run 复用
    runMu   sync.Mutex       // 保证 Run 与 RunForceCompact 不并发
}

// New 签名扩展为可选参数版本，避免破坏现有调用点：
//   func New(p llm.Provider, r *tool.Registry, version string, eng *permission.Engine,
//            opts ...Option) *Agent
// Option 包含 WithRuntime(*SessionRuntime)。当不传 runtime 时构造一个空 runtime
// 让现有测试不爆，但本章的 mewcode/smoke 入口必须显式传入。
```

主循环关键改动：

1. **本轮迭代开头**：按当前 `permission.Mode` 选 `defs := a.registry.Definitions()` 或 `ReadOnlyDefinitions()`，把同一份 `defs` 切片同时作为 `ManageInput.ToolDefs` 和 `streamOnce` 的 `Request.Tools`，保证恢复段宣称的工具与请求 tools 来自同一引用。`defs` 不缓存到 Agent 字段（避免 mode 切换时复用旧切片），但同一轮迭代内被 ManageContext 与 streamOnce 共用。
2. **每轮 streamOnce 之前**：构造 `ManageInput`：`UsageAnchor = a.runtime.UsageAnchor`、`AnchorMsgLen = a.runtime.AnchorMsgLen`、`EstimatedToken = compact.EstimateTokens(UsageAnchor, conv.Messages(), AnchorMsgLen)`、`Trigger = TriggerAuto`。调 `compact.ManageContext`，错误走错误流程；ManageContext 内部已经把消息列表写回 conversation。
3. **streamOnce 签名扩展为返回 error**：把现有 `(text string, calls []llm.ToolCall, usage *llm.Usage, ok bool)` 改成 `(text string, calls []llm.ToolCall, usage *llm.Usage, err error)`。错误来源是 `StreamEvent.Err`（mewcode 的 `Provider.Stream` 接口只返回 channel，无 error 返回值，错误通过事件流投递）。streamOnce 在收到 ev.Err 时累加的 text 不写回 Conversation（保证 Conversation 状态与 Stream 调用前一致，紧急压缩可以安全地 ReplaceMessages）。
4. **Stream 完成后**（仅主对话路径）：从尾事件中读 `usage`，调 `compact.UsageAnchor(*usage)` 更新 `a.runtime.UsageAnchor`，同时 `a.runtime.AnchorMsgLen = a.runtime.conv.Len()`。摘要请求结束后**不**更新这两个字段。
5. **ReadFile 工具调用成功后**：在 `executeBatched` 内、工具 worker goroutine 内同步执行：检测 `toolName == "read_file"` 且 `tool.Result.IsError == false`（注意 IsError 来自 `tool.Result`，不是 `llm.ToolResult`），把 `llm.ToolCall.Input`（json.RawMessage）反序列化成 `map[string]any`，取出 `path` 字段（与 `internal/tool/read_file.go` 定义的参数名一致），`filepath.Abs(path)` 后用 `os.ReadFile(absPath)` 拿纯净字节，调 `a.runtime.Recovery.RecordFile(absPath, string(b))`。读盘失败吞掉。调用必须在 `conv.AddToolResults(results)` **之前**完成（同 goroutine 顺序），保证下一次 ManageContext 能看到本轮 ReadFile 的记录。
6. **错误捕获 / 紧急压缩**：在主循环内捕获 `streamOnce` 返回的 `err`，用 `errors.Is(err, llm.ErrPromptTooLong)` 判断。命中时：
   - 用一个迭代级局部变量 `emergencyRetried := false` 锁定一次性重试。若已为 true 则按正常错误上抛。
   - 调 `compact.ManageContext(ctx, in)` with `Trigger = TriggerEmergency`：内部先做一次 OffloadAndSnip 再 ForceCompact。
   - 紧急压缩成功后把 `a.runtime.UsageAnchor = 0`、`a.runtime.AnchorMsgLen = 0`（conversation 已重建），用 `EstimateTokens(0, conv.Messages(), 0)` 重新算估算 token；若估算已低于 `ContextWindow - manualSafetyMargin` 则置 `emergencyRetried = true` 后重试本轮 streamOnce；否则视为不可恢复，按错误上抛，不做第二次紧急压缩。
   - 紧急路径里 ForceCompact 内部若遇 PTL 走 ptlRetry，全程不调 AutoTracking 任何方法。

> **Run 与 RunForceCompact 互斥**：Agent 在 `Run` 入口先 `a.runMu.Lock()`，结束时 Unlock；`RunForceCompact` 入口也先 Lock。保证手动 /compact 不与正在进行的 Run 并发触发 ManageContext。

> **Run 期间 Registry 不可变**：本章承诺主循环开头算出的 toolDefs 在一次 Run 调用内保持稳定；MCP 工具的注册/注销只允许发生在 Run 之间。如果未来需要 Run 中动态增删工具，必须重新约定 toolDefs 重算时机并同步刷新恢复段缓存。

> **压缩状态事件 emit**（兑现 spec F24a / F24b）：`Event` 结构新增 `Compact *CompactEvent` 字段（`internal/agent/agent.go`）。主循环在以下两个时机向 `ch` emit 状态事件，让 TUI 在 LLM 摘要请求还在跑的时候就能立刻显示"压缩中"前缀，避免用户以为程序卡死：
>
> - **自动路径**（主循环步骤 2 内）：若本轮 `EstimateTokens` 已超 `ContextWindow - summaryReserve - autoSafetyMargin`（即必然要走 layer 2），在调 `compact.ManageContext` **之前** emit `Event{Compact: &CompactEvent{Phase: CompactPhaseBeforeAuto}}`；ManageContext 返回后 emit `Event{Compact: &CompactEvent{Phase: CompactPhaseAfterAuto, Before, After, Err}}`。如果本轮估算未超阈值（只跑 layer 1 / 什么都不做），**不发任何 Compact 事件**——layer 1 是静默操作。
> - **紧急路径**（主循环步骤 6 内）：在 `Trigger = TriggerEmergency` 调 ManageContext **之前** emit `Event{Compact: &CompactEvent{Phase: CompactPhaseBeforeEmergency}}`；ManageContext 返回后 emit `Event{Compact: &CompactEvent{Phase: CompactPhaseAfterEmergency, Before, After, Err}}`。
> - **手动路径**（`/compact` / RunForceCompact）：不走 Compact 事件路径，由 TUI handleCompact 直接拿到 (before, after, err) 元组通过 tea.Msg 回投，文案统一格式见后文 TUI 渲染段。
>
> ```go
> // internal/agent/agent.go
> type CompactPhase int
> const (
>     CompactPhaseBeforeAuto      CompactPhase = iota + 1
>     CompactPhaseAfterAuto
>     CompactPhaseBeforeEmergency
>     CompactPhaseAfterEmergency
> )
>
> type CompactEvent struct {
>     Phase  CompactPhase
>     Before int64 // After 状态有意义；Before 状态置 0
>     After  int64 // 同上
>     Err    error // After 状态可能非 nil（PTL 重试全部失败或熔断生效）
> }
>
> type Event struct {
>     // ... 既有字段
>     Compact *CompactEvent // 新增：压缩生命周期事件
> }
> ```

### Conversation 改造（`internal/conversation/conversation.go`）

新增一个整体替换方法，并补充内部 mutex 保护：

```go
// Conversation 内部新增 mu sync.Mutex（Messages / ReplaceMessages / AddXxx 都加锁），
// 防止 ReplaceMessages 与 Messages 并发时拿到部分写入的切片。

// ReplaceMessages 把内存数组整体替换为传入的 msgs。
// compact 摘要后用这个方法一次性丢弃旧历史并装入"摘要 + 恢复 + 近期原文"。
// 不暴露切片引用，做深拷贝（含 ToolCalls / ToolResults 切片）以免外部继续持有旧切片。
func (c *Conversation) ReplaceMessages(msgs []llm.Message)
```

> **性能评估**：每轮 ManageContext 都会调 ReplaceMessages（layer1-only 时也要写回，否则 OffloadAndSnip 的字符串替换不会作用于下一轮）。25 轮 × 数十条消息 × 数百 KB 字符串的深拷贝在毫秒级完成，与摘要 LLM 请求几十秒耗时相比可忽略；不做对象池。

### TUI 命令分发（`internal/tui/`）

`internal/tui/stream.go` 现有 `submit()` 内部已经有针对 `/exit` / `/plan` / `/do` 的 switch 分支。本章把这三个命令一并迁移到统一注册表，并新增 `/compact`：

```go
// internal/tui/commands.go（新文件）

// dispatchCommand 检查输入是否以 "/" 开头；命中则返回对应命令处理器；
// 未以 "/" 开头返回 nil, false；以 "/" 开头但未注册则返回 unknownCommand handler。
func dispatchCommand(input string) (commandHandler, bool)

type commandHandler func(ctx context.Context, model *Model) tea.Cmd

// builtinCommands 注册表初始填四项：迁移现有 /exit / /plan / /do，新增 /compact。
var builtinCommands = map[string]commandHandler{
    "/exit":    handleExit,
    "/plan":    handlePlan,
    "/do":      handleDo,
    "/compact": handleCompact,
}

// handleCompact 在 goroutine 里调 model.runtime.RunForceCompact(ctx)
// （等价于 model.agent.RunForceCompact）；完成后通过 tea.Msg 把 (before, after, err)
// 抛回 Update 循环，由 Update 决定打印系统消息：成功 "已压缩，token 从 X 降至 Y"，
// 失败 "压缩失败: <err>"。命令路径不调 conv.AddUser，不写入对话历史。
```

Model 字段调整：

```go
type Model struct {
    // ... 原有字段
    runtime *agent.SessionRuntime  // 新增：跨 Run 持有的长生命周期状态
    agent   *agent.Agent           // 新增：常驻 Agent 实例（在 beginTurn 内复用，不再每轮 New）
}
```

`beginTurn` 不再每轮 `agent.New(...)`：构造期一次性 `m.agent = agent.New(m.provider, m.registry, m.version, m.engine, agent.WithRuntime(m.runtime))`；`beginTurn` 只调 `m.agent.Run(turnCtx, m.conv, m.mode)`。

Agent 层新增 `RunForceCompact(ctx) (before, after int64, err error)` 给 TUI 调用：内部先 `a.runMu.Lock()` 等待主循环空闲，再构造 ManageInput with `Trigger = TriggerManual`，调 `compact.ManageContext`，从 Output 取 BeforeTokens / AfterTokens 返回。

**TUI 渲染 Compact 事件**（兑现 spec F24a / F24b）：`internal/tui/stream.go` 的 `updateStreaming` 在 `streamMsg` 处理上新增 `msg.Compact != nil` 分支，按 Phase 渲染系统消息后继续 `waitForEvent` 拉下一帧，**不写入 conversation**：

| Phase | 渲染文案 |
|-|-|
| `CompactPhaseBeforeAuto` | `"正在压缩上下文..."` |
| `CompactPhaseBeforeEmergency` | `"上下文撞墙，自动压缩中..."` |
| `CompactPhaseAfterAuto` / `AfterEmergency` (Err == nil) | `"已压缩，token 从 <Before> 降至 <After>"` |
| `CompactPhaseAfterAuto` / `AfterEmergency` (Err != nil) | `"压缩失败：<Err>"` |

格式化逻辑抽出一个内部函数 `formatCompactNotice(phase, before, after, err) string`，让 `handleCompact` 的 tea.Msg 回投路径（手动 `/compact`）也复用同一个函数渲染完成态文案，确保自动 / 紧急 / 手动三条路径的文案风格一致。

### config 改造（`internal/config/`）

- `ProviderConfig` 增加 `ContextWindow int` 字段并支持从 YAML 读取。
- 新增 `EffectiveContextWindow()` 方法：配置 > 0 返回配置值；否则按 protocol 给默认值（anthropic→200000，openai→128000，其他 protocol→200000 作为保守默认）。
- `internal/config/config_test.go` 增加：未配置 / 配置为 0 / 配置为正数 / 未知 protocol 四种情况的断言。

### `.mewcode/config.yaml.example` 更新

在 providers 数组里给每个 provider 加上 `context_window` 示例值与注释：

```yaml
providers:
  - name: claude
    protocol: anthropic
    api_key: sk-ant-xxx
    model: claude-sonnet-4-5
    context_window: 200000   # 可选，未配置时按 protocol 默认（anthropic 200000、openai 128000）
```

## 模块交互**正常路径（自动触发）：**

```
用户输入 (TUI)
    │ 非 / 开头
    ▼
Agent.Run() goroutine
    │
    ├─[迭代 N 开头]→ registry.Definitions() / ReadOnlyDefinitions() ──→ defs（本轮复用）
    │
    │   ┌─────────────────────────────────────────────────┐
    │   │            compact.ManageContext                │
    │   │  ┌──────────────────────────────────────────┐   │
    │   │  │ 1. layer1.OffloadAndSnip                 │   │
    │   │  │    - 查 ContentReplacementState 账本     │   │
    │   │  │    - 新 id：判断 single / aggregate      │   │
    │   │  │    - 落盘到 SpillDir/<tool_use_id>       │   │
    │   │  │    - 写入账本（冻结决策）                │   │
    │   │  └──────────────────────────────────────────┘   │
    │   │              │                                   │
    │   │              ▼                                   │
    │   │  ┌──────────────────────────────────────────┐   │
    │   │  │ 2. token.EstimateTokens                  │   │
    │   │  │    = anchor + chars / 3.5                │   │
    │   │  └──────────────────────────────────────────┘   │
    │   │              │                                   │
    │   │              ▼                                   │
    │   │  estimated >= window-20000-13000 且未熔断？      │
    │   │              │ 是                                │
    │   │              ▼                                   │
    │   │  ┌──────────────────────────────────────────┐   │
    │   │  │ 3. layer2.AutoCompact                    │   │
    │   │  │    a. BuildSummaryPrompt（无工具）       │   │
    │   │  │    b. Provider.Stream → <summary> 解析   │   │
    │   │  │    c. BuildRecoveryAttachment (3 段)     │   │
    │   │  │    d. pickRecentTail + 配对修正          │   │
    │   │  │    e. 拼接成 newMsgs                     │   │
    │   │  │    f. Conversation.ReplaceMessages       │   │
    │   │  │    g. 成功→失败计数清零；失败→+1，熔断   │   │
    │   │  └──────────────────────────────────────────┘   │
    │   └─────────────────────────────────────────────────┘
    │
    ├─→ streamOnce: Provider.Stream(ctx, Request{ messages, tools=defs })
    │       │
    │       ├─正常完成 → 读尾事件 usage → usageAnchor 更新
    │       │
    │       └─prompt_too_long → 紧急压缩路径（见下）
    │
    └─→ executeBatched 工具调用
            │
            └─ReadFile 成功 → os.ReadFile 纯净字节 → RecoveryState.RecordFile
```

**紧急压缩路径（provider 撞墙）：**

```
Provider.Stream 返回 ev.Err 命中 ErrPromptTooLong
    │
    ▼
streamOnce 返回 err（已累加的 text 不写入 Conversation，保证状态原子）
    │
    ▼
主循环：emergencyRetried 已为 true? → 是：按错误上抛
    │ 否
    ▼
compact.ManageContext(Trigger=Emergency)
    │   - 跳过阈值检查、跳过熔断器
    │   - 先强制跑一次 OffloadAndSnip（layer1）把大工具结果挪走
    │   - 再 ForceCompact → runSummary → ReplaceMessages
    │     若 runSummary 内部撞 PTL → 走 F27 的 ptlRetry（不调 AutoTracking）
    ▼
重置锚点：runtime.UsageAnchor=0、runtime.AnchorMsgLen=0
重新估算：est := EstimateTokens(0, conv.Messages(), 0)
    │
    ▼
est < ContextWindow - manualSafetyMargin？
    ├─是 → emergencyRetried=true → 重试本轮 streamOnce
    │       ├─成功 → 继续主循环
    │       └─再次 PTL → 按错误上抛，不再做第二次紧急压缩
    └─否 → 视为不可恢复，按错误上抛
```

**手动压缩路径：**

```
TUI 输入 "/compact"
    │
    ▼
dispatchCommand 命中 → 不发 LLM
    │
    ▼
agent.RunForceCompact
    │
    ▼
compact.ManageContext(Trigger=Manual)
    │   - 同 Emergency 路径
    ▼
返回 (before, after)
    │
    ▼
TUI push 系统消息 "已压缩，token 从 X 降至 Y"
```

**摘要请求自身 PTL：**

```
summarizeOnce 收到 prompt_too_long
    │
    ▼
groupByUserTurn(msgs) → groups
    │
    ├─第 1~3 次：每次丢最旧的 1 组 → 重试 summarizeOnce
    │
    └─第 4 次起：丢 len(剩余) * 0.2 组 → 重试
        │
        ├─成功 → 返回
        └─groups 全空 → 返回错误（上层熔断计数 + 1）
```

## 文件组织

```
internal/compact/
├── compact.go         — ManageContext 主入口、Trigger 枚举、编排两层调用
├── layer1.go          — OffloadAndSnip / spillSingle / buildPreview
├── layer2.go          — AutoCompact / ForceCompact / runSummary / summarizeOnce / ptlRetry / pickRecentTail / groupByUserTurn
├── summary_prompt.go  — BuildSummaryPrompt 模板 + serializeConversation + ExtractSummary 解析
├── recovery.go        — RecoveryState / FileReadRecord / BuildRecoveryAttachment / boundaryNotice
├── token.go           — EstimateTokens / UsageAnchor / messageChars
├── state.go           — ContentReplacementState (DecideOnce) / AutoCompactTrackingState / SessionContext
├── const.go           — 全部硬编码常量
├── compact_test.go    — ManageContext 集成单测（fakeProvider 驱动）
├── layer1_test.go     — 单条 / 聚合 / 幂等 / 决策冻结 / 落盘失败降级
├── layer2_test.go     — 摘要流程 / PTL 重试 / 熔断计数 / 近期原文边界 / 配对修正
├── summary_prompt_test.go — Prompt 文本断言 + <summary> 解析
├── recovery_test.go   — 文件快照排序 / 截断 / 工具集合一致性 / 并发写 / boundaryNotice 稳定
└── token_test.go      — 锚点 + 字符增量 / usage 合并
```

`internal/agent/agent.go` 改动：
- 新增 `*SessionRuntime` 注入字段与 `runMu sync.Mutex`；`New` 改为变参选项（`WithRuntime`）以便保留对现有测试的兼容。
- 把 `streamOnce` 签名改成返回 `(text, calls, usage, err error)`；错误由内部从 `StreamEvent.Err` 捕获。
- 主循环本轮迭代开头按 mode 选 `defs := registry.Definitions()` 或 `ReadOnlyDefinitions()`，同一份切片传给 ManageContext.ToolDefs 与 Request.Tools。
- 每轮 `streamOnce` 前调 `compact.ManageContext(Trigger=TriggerAuto)`。
- 主对话路径 `streamOnce` 完成后更新 `runtime.UsageAnchor` 与 `runtime.AnchorMsgLen`；摘要路径不更新。
- 在工具结果回填阶段对 ReadFile 调用 `os.ReadFile` 纯净字节并写入 `Recovery`（同 goroutine、`AddToolResults` 之前）。
- 捕获 `llm.ErrPromptTooLong` → ManageContext(Trigger=TriggerEmergency) → 重新估算后同迭代重试一次。
- 新增 `RunForceCompact(ctx) (before, after int64, err error)` 给 TUI 调；入口先 `runMu.Lock()`。

`internal/agent/runtime.go`（新文件）：定义 `SessionRuntime` 结构与构造函数；定义 `Option` 函数式选项与 `WithRuntime`。

`internal/agent/agent_test.go` 改动：
- 已有 `fakeProvider` 扩展能力：① 在脚本最后一帧之前发送 `StreamEvent{Usage: &llm.Usage{...}}`；② 支持按调用次数序列化错误投递（包括包装好的 `ErrPromptTooLong`）。
- 新增"撞墙后紧急压缩成功"与"紧急压缩后再次撞墙上抛"两个用例。

`internal/conversation/conversation.go` 改动：新增 `mu sync.Mutex`；新增 `ReplaceMessages(msgs []llm.Message)`，做深拷贝；已有 `AddXxx` / `Messages` / `Len` / `LastRole` 全部加锁。

`internal/conversation/conversation_test.go` 改动：新增 `ReplaceMessages` 的直接断言用例。

`internal/llm/provider.go` 改动：
- 新增 `ErrPromptTooLong` 哨兵错误（`var ErrPromptTooLong = errors.New("...")`）。
- `ToolDefinition`（line 34）已是导出类型，无需改动。

`internal/llm/anthropic.go` / `internal/llm/openai.go` 改动：捕获 provider 返回的"上下文过长"错误码或消息片段，包装成 `fmt.Errorf("%w: %v", llm.ErrPromptTooLong, origErr)` 并通过 `trySend(ctx, ch, StreamEvent{Err: wrapped})` 投递（接口签名只有 channel 返回，错误走事件流）。

`internal/llm/anthropic_test.go` / `internal/llm/openai_test.go`：注入构造好的错误返回，断言：① 典型 `prompt_too_long` / `context_length_exceeded` 被 Stream 转换成 wrapped err 投递到 `StreamEvent.Err`；② `errors.Is(err, llm.ErrPromptTooLong)` 命中；③ 其他 4xx/5xx 错误不被错误地包装为 PTL。

`internal/tui/commands.go`（新文件）：`dispatchCommand` + `handleExit` / `handlePlan` / `handleDo` / `handleCompact` + 未知命令兜底。
`internal/tui/stream.go`：`submit()` 内原 switch 分支改用 `dispatchCommand` 调用；命令路径不调 `conv.AddUser`，不写入对话历史。
`internal/tui/tui.go`：Model 新增 `runtime *agent.SessionRuntime` 与 `agent *agent.Agent` 字段；`New` 构造期一次性构造 Agent 并保存。
`internal/tui/tui_test.go`：① `/compact` 走命令路径不发 LLM；② `/unknown` 友好提示；③ 迁移后 `/exit` / `/plan` / `/do` 行为不回归三个用例。

`internal/config/config.go`：`ProviderConfig` 追加 `ContextWindow int yaml:"context_window"`，加 `EffectiveContextWindow()`；现有字段顺序与标签不变。
`internal/config/protocol_defaults.go`（新文件）：`DefaultAnthropicContextWindow = 200000`、`DefaultOpenAIContextWindow = 128000`。
`internal/config/config_test.go`：新增四种情况断言。

`cmd/mewcode/main.go`：启动阶段调 `compact.NewSessionContext(workspace)`、`compact.NewContentReplacementState()`、`compact.NewRecoveryState()`、`compact.NewAutoCompactTrackingState()`，组装为 `SessionRuntime`；待 provider 选定后注入 `EffectiveContextWindow()`；把 `*SessionRuntime` 交给 TUI Model。

`cmd/smoke/main.go`：同样按新签名构造 Agent；smoke 场景的 ContextWindow 可固定 200000。

`.gitignore`：追加 `.mewcode/sessions/`，避免开发者跑一次 mewcode 后 `git status` 出现一大坨 session 子目录。

`.mewcode/config.yaml.example`：新增 `context_window` 字段示例与注释。

## 技术决策

| 决策点 | 选择 | 理由 |
|--------|------|------|
| 包结构与命名 | `internal/compact/` 单包，子文件按职责拆分（layer1 / layer2 / recovery / token） | 上下文管理逻辑高度内聚，对外只暴露 `ManageContext` 等少量函数。单包简化导入路径，多文件保证可读性。子包拆分会引入循环引用风险（layer2 既要 token 又要 recovery）。 |
| ContentReplacementState 临界区 | OffloadAndSnip 持 mu 全程；账本读写不暴露给外部，杜绝 TOCTOU 翻转 | 账本对外只通过 DecideOnce 这一个高层方法操作（持锁 + 回调内决策 + 同临界区写入），消除"读账本→落盘→写账本"之间的并发翻转窗口。 |
| AutoCompactTrackingState 独立于 ContentReplacementState | 拆成两个 struct | 熔断只用于自动路径，手动 / 紧急完全绕过；放一起会让"是否应该读熔断字段"在调用点变得模糊。两个 struct 都内嵌 `sync.Mutex` 保证并发安全。 |
| 9 部分 + 两阶段摘要 prompt 内嵌 | 直接写在 `summary_prompt.go` 的常量字符串里 | Prompt 是产品规范的一部分，不需要从外部加载；放代码里方便 review 与版本回滚。也避免在测试里读文件。9 个小节标题用固定字面字符串，便于 ExtractSummary 与单测匹配。 |
| 摘要请求不传 tools | Request.Tools 留空 | 摘要本身是"压缩历史"的语义动作，模型不应该在摘要阶段发起新工具调用。保留 tools 会让模型混淆任务，且消耗额外 token。 |
| ReadFile 后用 os.ReadFile 重读纯净字节 | 工具 worker goroutine 内同步重读 | 工具返回字符串带行号前缀（mewcode 现有实现），直接拿来做恢复段会让模型把行号当成代码的一部分。重读一次磁盘成本可忽略；同步顺序保证下一次 ManageContext 能观察到本轮记录。 |
| 主循环本轮迭代开头算 toolDefs | 局部变量复用，不缓存到 Agent 字段 | F17 要求恢复段声明的工具集合和 Stream 调用的 tools 严格一致。同一轮迭代按 mode 选好后，把同一份切片同时传给 ManageContext.ToolDefs 与 Request.Tools，引用一致即逐项一致。 |
| EstimateTokens 用 3.5 字符/token | 硬编码 estimateCharsPerToken=3.5 | 锚定真实 usage 已经是主力，字符比例只用于两次真实请求之间的近似。3.5 是英文+代码混合场景下的常用经验值，过细的差异会被锚点纠正。 |
| 紧急压缩只重试一次 | 同迭代内 emergencyRetried 锁定一次性重试 | 紧急压缩已经丢掉了一大段历史，如果重试还失败说明问题不是 token 而是其他（如单条 user 消息就超长）。多次重试只会让用户等更久。重试前必须重估 token 低于 `context_window - manualSafetyMargin`，否则视为不可恢复。 |
| session_id 不持久化 | 进程启动生成 `<unix_ts>-<short_random>` | 单进程会话边界等于进程边界，不需要恢复。`.mewcode/sessions/` 留作调试副产物，外部脚本/用户决定清理时机。 |
| 阈值硬编码 + 仅 context_window 走 config | 单项 config 暴露 | context_window 由 provider 决定，跨 provider 必须可配。其余阈值若开放为配置会指数级放大测试矩阵，且没有跨用户的差异化需求。本章不开放为配置项；调整属于代码变更。 |
| Layer 1 落盘失败降级为不替换 | 不进 seenIds，下次重试 | 磁盘问题是瞬时的可恢复故障，不应该让对话因此中断。N6 错误隔离的直接体现。 |
| Layer 2 PTL 重试中按"用户提交 + 一组往返"分组 | groupByUserTurn 抽成独立函数 | F27 的语义保证最早被丢的是最旧的一整轮交互，不会把同一轮的 user/assistant/tool 拆成半截。独立函数便于单元测试。 |
| Conversation 内部 mutex + ReplaceMessages 深拷贝 | 加锁 + 拷贝整数组 | 摘要后 compact 把 newMsgs 交出去就不应该再被外部改动；Conversation 也不应该暴露内部切片指针。深拷贝在 25 轮 × 数百 KB 量级下耗时毫秒级，与摘要 LLM 请求耗时相比可忽略；不再做对象池。 |
| TUI 命令分发用 map[string]handler | 极简注册表（4 项：/exit、/plan、/do、/compact） | 本章只有这几个内置命令，O(1) 查找已经够用；分支/参数解析框架不在本章范围。 |
| 命令路径不写入 conversation | UI 层 push 系统消息，不调 conv.AddUser | `/compact` 等命令不属于对话语义，进入 conversation 会污染下一轮 LLM 输入。系统消息只在 TUI 视图层展示。 |
| 摘要 + 恢复段合并为一条 user 消息 | runSummary 输出新对话首条是单条 user，content 内嵌 9 部分摘要 + 三段恢复 | Anthropic 协议禁止 user/user 连续；分两条会导致 400 错误。合并后续接近期原文（无论首条是 user / assistant / tool 都不破坏交替）。摘要写在前、三段恢复紧随其后，全部装在一个 user.Content 字符串里。 |
| pickRecentTail 配对修正与 role 衔接 | 截断点前推 + 必要时插入 assistant 占位 | 截断点夹在 tool_use/tool_result 中间时，向前推到 tool_use 之前；若拼接后导致 summary(user) 紧接近期原文首条 user，则在 recovery 段后、近期原文前插入一条 assistant 衔接占位，保证 Anthropic user/assistant 交替约束。 |
| ProviderConfig 新增方法 EffectiveContextWindow | 派生方法而非构造时折算 | 配置加载时不知道 protocol 默认值表，把默认值表收敛到方法里，让 config 加载逻辑保持纯字段映射。也便于后续追加新 protocol 默认值。 |
| ErrPromptTooLong 作为 llm 包哨兵错误 | `errors.Is` 判断 | 不同 provider 返回的具体错误结构差异大（HTTP 400 vs structured error），统一成哨兵错误后 agent 主循环只需要一处判断。anthropic/openai provider 通过 `trySend(ctx, ch, StreamEvent{Err: wrapped})` 把 PTL 错误投递到事件流，主循环从 `StreamEvent.Err` 用 `errors.Is` 检测。 |
| context_window 注入时机 | provider 选定后由 cmd/mewcode 注入 SessionRuntime，本会话内不变 | mewcode 启动期 TUI 可能选 provider，等用户选定后才能确定 context_window；本章不支持运行期切 provider；切换 provider 等同于重新启动进程。 |
| context_window 下界检查 | 必须 > `summaryReserve + autoSafetyMargin`（即 > 33000） | 低于此值时 `ContextWindow - 33000` 为非正数，自动阈值判断永远成立，每轮都会触发摘要导致死循环；ManageContext 在入口对 ContextWindow 做 sanity check，过小时跳过自动 layer2 并写一条警告日志。 |