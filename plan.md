# 多协议 LLM 终端对话客户端 Plan## 技术栈
- 语言：Go
- TUI：charm 生态 v2 线 —— bubbletea（MVU 框架，tea.Println 提交 scrollback）+ bubbles（textarea/spinner/list）
  + lipgloss（样式/布局）+ glamour（markdown 渲染）
- 配置：YAML 解析（gopkg.in/yaml.v3）
- LLM 通信：官方 Go SDK —— anthropic-sdk-go、openai-go/v3（SDK 内部已处理 SSE）

## 架构概览（分层）
1. 入口层 cmd/mewcode —— 加载配置、打印 banner、启动 TUI。
2. 配置层 config —— 读取并校验 .mewcode/config.yaml，给出 providers 列表。
3. LLM 协议层 llm —— 定义协议无关的 Provider 接口与统一消息/流式事件类型；
   anthropic、openai 两个适配器各自封装官方 SDK、统一吐出文本增量（思考增量内部丢弃）。
4. 会话层 conversation —— 进程内维护多轮历史，提供完整上下文。
5. 提示词/资源 prompt —— 内置 system prompt 与启动 banner（ASCII 猫）。
6. 终端层 tui —— bubbletea Model/Update/View，含状态机（选择/空闲/流式）、输入框、对话区、
   spinner+计时、provider 选择列表；以"读一个再续"的 Cmd 把 llm 流式事件接入 Update。

## 数据流（一轮对话）
用户输入 → tui 提交 → conversation 追加 user 消息 → 调 Provider.Stream(ctx, 历史)
→ 得到事件 channel → tui 用 Cmd 逐个读取文本增量并实时显示（spinner 计时同步进行）
→ 收到结束事件 → 用 glamour 渲染整段 → conversation 追加 assistant 消息 → 回到空闲。

## 核心数据结构与接口

```go
// ───────── config 层 ─────────
type Config struct {
    Providers []ProviderConfig `yaml:"providers"`
}
type ProviderConfig struct {
    Name     string `yaml:"name"`     // 状态栏左侧显示
    Protocol string `yaml:"protocol"` // "anthropic" | "openai"
    BaseURL  string `yaml:"base_url"` // 空则用 SDK 默认端点
    APIKey   string `yaml:"api_key"`
    Model    string `yaml:"model"`    // 状态栏右侧显示
    Thinking bool   `yaml:"thinking"` // 仅 anthropic 生效
}
func Load(path string) (*Config, error) // 加载 + 校验

// ───────── llm 层（协议无关）─────────
type Message struct {
    Role    string // "user" | "assistant"
    Content string
}
type StreamEvent struct {
    Text string // 文本增量
    Done bool   // 本轮正常结束
    Err  error  // 出错（与 Done 互斥）
}
type Provider interface {
    Name() string  // -> 状态栏左
    Model() string // -> 状态栏右
    // 发起一轮流式对话；内部注入内置 system prompt 与 thinking 配置；思考增量内部丢弃；
    // 通过 channel 吐出文本增量/结束/错误；ctx 取消即终止。
    Stream(ctx context.Context, msgs []Message) <-chan StreamEvent
}
func New(cfg config.ProviderConfig) (Provider, error) // 按 protocol 构造适配器

// ───────── conversation 层 ─────────
type Conversation struct{ messages []llm.Message }
func (c *Conversation) AddUser(text string)
func (c *Conversation) AddAssistant(text string)
func (c *Conversation) Messages() []llm.Message

// ───────── prompt 层 ─────────
const SystemPrompt = "..."          // 内置固定 system prompt
const CatBanner    = "..."          // ASCII 猫
func RenderBanner(version, cwd string) string

// ───────── tui 层 ─────────
type sessionState int
const (
    stateSelecting sessionState = iota // 多 provider 时的选择界面
    stateIdle                          // 等待用户输入
    stateStreaming                     // 等待/接收模型流（spinner+计时）
)
type Model struct {
    state     sessionState
    textarea  textarea.Model
    spinner   spinner.Model
    list      list.Model            // 仅多 provider 时使用
    renderer  *glamour.TermRenderer
    providers []config.ProviderConfig
    provider  llm.Provider
    conv      *conversation.Conversation
    events    <-chan llm.StreamEvent // 当前流
    curReply  strings.Builder        // 本轮 assistant 增量缓冲（动态区显示，Done 后提交 scrollback）
    turnStart time.Time              // 计时起点
    width, height int
    // 注：完成的消息（用户输入 / 渲染后的助手回复 / 错误）通过 tea.Println 提交到终端
    //     scrollback，不在 Model 内保留；无 viewport。
}
type streamMsg llm.StreamEvent
func waitForEvent(ch <-chan llm.StreamEvent) tea.Cmd // 读一个事件 -> streamMsg
```

## 模块设计### 模块 config
职责：读取并校验 .mewcode/config.yaml，产出 providers 列表。
对外接口：Load(path) (*Config, error)；Config.Providers。
校验规则：列表非空；每项 name/protocol/api_key/model 非空；protocol ∈ {anthropic, openai}。
         任一不满足 → 返回可读错误（指明哪个 provider 的哪个字段）。
依赖：gopkg.in/yaml.v3、标准库 os。

### 模块 llm
职责：定义协议无关的 Provider 接口与统一消息/事件类型；按 protocol 构造适配器。
对外接口：Provider 接口、Message、StreamEvent、New(cfg) (Provider, error)。
子单元：
  - anthropic 适配器：封装 anthropic-sdk-go。把 []Message 转为 SDK 的 MessageParam，
    注入 System=内置 prompt、按 cfg.Thinking 设 ThinkingConfig；NewStreaming 迭代，
    取 TextDelta -> StreamEvent.Text，遇 ThinkingDelta 丢弃，结束发 Done，错误发 Err。
    base_url 非空时 option.WithBaseURL 覆盖。
  - openai 适配器：封装 openai-go/v3。把 []Message 转为 ChatCompletionMessageParamUnion，
    首条插入 SystemMessage(内置 prompt)；NewStreaming 迭代取 Choices[0].Delta.Content，
    结束/出错按 stream.Err() 发 Done/Err。base_url 非空时 WithBaseURL 覆盖；thinking 忽略。
共同点：各适配器内部起 goroutine 迭代 SDK 流并向 channel 推 StreamEvent，
       ctx.Done() 时停止；channel 在结束/出错后关闭。
依赖：anthropics/anthropic-sdk-go、openai/openai-go/v3、本模块 prompt、config。

### 模块 conversation
职责：进程内维护单会话多轮历史（user/assistant 交替）。
对外接口：AddUser、AddAssistant、Messages()。
依赖：llm（Message 类型）。

### 模块 prompt
职责：提供内置 system prompt 与 ASCII 猫 banner 文本。
对外接口：SystemPrompt 常量、CatBanner 常量、RenderBanner(version, cwd) string。
依赖：无。

### 模块 tui
职责：bubbletea 应用，承载选择/对话/流式/错误的全部交互与渲染。
对外接口：New(providers []config.ProviderConfig) Model；Run() error。
内部职责：
  - 启动时若 providers 多于一项 -> stateSelecting（list 选择）；否则直接进 stateIdle 并构造 provider。
  - stateIdle：textarea 接收输入；Enter 提交（Alt+Enter 换行）；/exit 或 Ctrl+C 退出。
  - 提交：conv.AddUser -> provider.Stream(ctx, conv.Messages()) -> 存 events -> 起 waitForEvent
    与 spinner.Tick；记 turnStart；切 stateStreaming。
  - 提交时 tea.Println 提交用户输入块到 scrollback。
  - stateStreaming：每个 streamMsg 追加 curReply（底部动态区逐字显示）并续读；spinner+“Imagining…(Ns)”计时；
    Done -> glamour 渲染整段定型 -> tea.Println 提交到 scrollback、conv.AddAssistant、清缓冲、回 stateIdle；
    Err -> tea.Println 提交错误块、回 stateIdle。
  - 窗口尺寸变化：同步 textarea/renderer 宽度（N6）。
依赖：bubbletea/v2(tea.Println)、bubbles/v2(textarea/spinner/list)、lipgloss/v2、glamour/v2、
     本项目 llm、conversation、config、prompt。

### 模块 cmd/mewcode（入口）
职责：装配与启动。
流程：config.Load -> prompt.RenderBanner 打印 -> tui.New(providers) -> tui.Run()。
失败处理：配置错误打印可读信息并非零退出（N4）。
依赖：config、tui、prompt。

## 模块交互### 调用链（启动）
main → config.Load(".mewcode/config.yaml")
     → 若 err：打印可读错误、非零退出
     → prompt.RenderBanner(version, cwd) 打印
     → tui.New(cfg.Providers) → tui.Run()
       → providers==1：内部 llm.New(cfg[0]) 构造 provider，进 stateIdle
       → providers>1 ：进 stateSelecting

### 时序（多 provider 选择）
stateSelecting:
  list 显示各 provider 的 name + model
  用户方向键移动、Enter 选定
  → llm.New(选定 cfg) 构造 provider
  → 状态栏更新为 provider.Name()/Model()
  → 进 stateIdle

### 时序（一轮对话，核心）
stateIdle:
  用户在 textarea 输入，Enter 提交
  → conv.AddUser(text)
  → events = provider.Stream(ctx, conv.Messages())
  → turnStart = now；curReply.Reset()
  → 返回 batch(waitForEvent(events), spinner.Tick)；切 stateStreaming

stateStreaming（循环）:
  spinner.TickMsg → 推进 spinner + 计算 elapsed → 再次 spinner.Tick
  streamMsg：
    - Text 非空 → curReply 追加（底部动态区逐字显示）→ waitForEvent 续读
    - Done       → glamour 渲染 curReply → tea.Println 提交 scrollback → conv.AddAssistant → 回 stateIdle
    - Err        → tea.Println 提交"可区分样式"错误块 → 回 stateIdle（不退出）
  期间 textarea 不接受提交（N1：界面仍响应，完成内容可用终端原生滚动回看）

### 时序（退出）
任意状态：输入 "/exit"（stateIdle 识别）或 Ctrl+C
  → cancel(ctx)（终止进行中的流）→ tea.Quit → bubbletea 还原终端（N7）

### 数据流图
config.yaml ──Load──> []ProviderConfig ──New──> Provider
用户输入 ──> conversation(+user) ──Messages()──> Provider.Stream
Provider.Stream ──goroutine 迭代 SDK 流──> chan StreamEvent
chan ──waitForEvent Cmd──> Update(streamMsg) ──> 底部动态区(纯文本流式)
Done ──glamour──> tea.Println 提交 scrollback & conversation(+assistant)

## 文件组织
```
mewcode/
├── cmd/
│   └── mewcode/
│       └── main.go              — 入口：加载配置、打印 banner、启动 TUI
├── internal/
│   ├── config/
│   │   └── config.go            — Config/ProviderConfig 类型、Load 与校验
│   ├── llm/
│   │   ├── provider.go          — Provider 接口、Message、StreamEvent、New 工厂
│   │   ├── anthropic.go         — anthropic 适配器（封装 anthropic-sdk-go）
│   │   └── openai.go            — openai 适配器（封装 openai-go/v3）
│   ├── conversation/
│   │   └── conversation.go      — 单会话多轮历史
│   ├── prompt/
│   │   └── prompt.go            — SystemPrompt、CatBanner、RenderBanner
│   └── tui/
│       ├── tui.go               — Model/Init/Update/View、状态机、Run
│       ├── stream.go            — waitForEvent Cmd 与 streamMsg 处理
│       ├── select.go            — provider 选择（list）相关
│       └── view.go              — 各状态的 View 拼装、状态栏、错误样式
├── .mewcode/
│   └── config.yaml              — 运行配置（providers 列表）；附 config.yaml.example
├── go.mod
└── go.sum
```
说明：
- 版本固定到 task.md 用 go get 实际解析为准；预期：
  charm.land/bubbletea/v2、charm.land/bubbles/v2、charm.land/lipgloss/v2、charm.land/glamour/v2、
  github.com/anthropics/anthropic-sdk-go、github.com/openai/openai-go/v3、gopkg.in/yaml.v3。
- tui 拆 4 个文件按职责切分；若实现时过碎可合并，不影响接口。
- .mewcode/config.yaml 含真实密钥，应在 .gitignore 忽略；提交一份 config.yaml.example。

## 技术决策

| 决策点 | 选择 | 理由 |
|--------|------|------|
| 语言 | Go | 项目既定（mewcode go 线） |
| TUI 框架 | charm v2 线（bubbletea/bubbles/lipgloss） | 用户选定；GA 最新；MVU 适配异步流式 |
| markdown 渲染 | glamour v2 | 生态一致；NewTermRenderer+WordWrap 宽度自适应（N6） |
| LLM 通信 | 官方 SDK（anthropic-sdk-go / openai-go/v3） | 用户选定；SDK 内置 SSE 解析，省去手写 |
| 协议抽象 | 统一 Provider 接口 + 两适配器 | 满足 F3/N3；上层不感知协议 |
| 流式接入 TUI | "读一个再续" waitForEvent Cmd + channel | 内聚、无需持有 Program 引用；界面不阻塞（N1） |
| 流式渲染策略 | 流式纯文本 + Done 后 glamour 定型 | glamour 需完整块；增量渲染会抖动（F8） |
| 渲染模型 | inline + tea.Println 提交 scrollback（Claude Code / Ink `<Static>` 风格） | 完成消息写入终端原生滚动历史，退出后保留、可用终端原生滚轮回看；仅"输入框 + 正在流式的回复 + 状态栏"为动态重绘区。**取代早期 viewport 固定滚动区方案** |
| thinking | 仅 anthropic 生效（ThinkingConfig）；openai 忽略 | OpenAI reasoning 不经 chat completions 返回正文；思考内容本就丢弃 |
| 计时 | turnStart + spinner tick 计算 elapsed | 自请求即计时，复用 spinner 动画驱动（F12） |
| provider 选择 | 单份直进 / 多份 list 选择 | 满足 F2 |
| 历史 | 进程内 slice，单会话 | 满足 F6；不持久化 |
| system prompt | 内置常量，适配器注入 | 满足 F4；conversation 保持纯 user/assistant |
| 配置 | .mewcode/config.yaml + yaml.v3；密钥入 .gitignore | 用户既定路径；N5 密钥安全 |
| 错误处理 | 运行时错误经 StreamEvent.Err 显示，不退出 | 满足 F11 |