# MCP 客户端 Plan

> 技术栈：Go（go 1.25.8）；使用 **官方 SDK** `github.com/modelcontextprotocol/go-sdk/mcp` 承载协议层（JSON-RPC 编解码、initialize 握手、stdio 与 Streamable HTTP 传输）。本章新增 **mcp 包** 与 main 装配，**不改 tool / agent / tui / permission / llm / config / conversation / prompt**。

## 架构概览

- **mcp 包（新增）**：承载 MCP 客户端的全部职责——配置加载与两层合并、`${VAR}` 展开、字段校验、调用 SDK 建立 stdio / HTTP 会话、把远端工具适配成内置 `tool.Tool`、统一管理生命周期。仅依赖 `tool`、SDK 与标准库；不依赖 agent / tui / permission / conversation。
- **main（改造）**：在 `tool.NewDefaultRegistry()` 之后、`permission.NewEngine` 与 `tui.New` 之前，加载 mcp 配置 → 启动 Manager → 把 Manager 产出的工具注册进 registry → 退出时 `defer mgr.Close()`。
- **tool 包（零改）**：`Registry.Register` 与 `Tool` 接口本就是开放抽象，直接吃 mcpTool 实例；`IsReadOnly` 对 MCP 工具返回正确值。
- **agent / tui 包（零改）**：工具流转链路对工具来源透明。
- **permission 包（零改）**：`friendlyName` 对未知名原样返回 → 规则可写 `mcp__<server>__<tool>`；`categorize` 在 `readOnly==true` 时走 CategoryRead、否则归 CategoryExec → 模式兜底矩阵自然命中；`extractTarget` 对未知工具返回 `("", false, false)`，黑名单与沙箱自动跳过。
- **llm / provider（零改）**：工具定义透传，协议无关。

数据流（单次调用）：
```
agent.executeBatched(calls, mode)
  └→ eng.Check(...)  → Allow → registry.Execute(name, args)
       └→ mcpTool.Execute(ctx, args)              [本章新增工具实现]
            ├→ ctx2 = context.WithTimeout(ctx, 30s)
            ├→ session.CallTool(ctx2, {Name: remoteName, Arguments: map})
            └→ 拼接 text content / 映射 isError / 协议错转 IsError
       └→ tool.Result{Content, IsError}            ── 回灌 conv
```

## 核心数据结构

### mcp.Config / mcp.ServerConfig（对外）
```go
// Config 是 mcp_servers 在内存中的归一化形式（已展开 ${VAR}、已合并、已校验）。
type Config struct {
    Servers map[string]ServerConfig // key = server 名
}

// ServerConfig 是单个 MCP server 的完整定义。
type ServerConfig struct {
    Type    string            // "stdio" | "http"
    Command string            // stdio 必填
    Args    []string          // stdio 可选
    Env     map[string]string // stdio 可选（已展开）
    URL     string            // http 必填
    Headers map[string]string // http 可选（已展开）
}
```

### mcp.Manager（对外不透明）
```go
type Manager struct {
    mu       sync.Mutex
    sessions []*session  // 已建立成功的 server 会话（用于 Close）
    tools    []tool.Tool // 已适配好的工具（供 main 注册进 registry）
}

type session struct {
    name string
    cs   *sdkmcp.ClientSession
}
```

### 工具适配（包内私有）
```go
// mcpTool 实现 tool.Tool。
type mcpTool struct {
    fullName   string         // "mcp__<server>__<tool>"
    remoteName string         // server 上的原始工具名
    descr      string
    schema     map[string]any // JSON Schema 透传
    readOnly   bool           // 仅来自远端 annotations.readOnlyHint==true
    cs         callerSession  // 接口形式持有，便于单测注入 stub
}

// callerSession 是 mcpTool 依赖的最小会话能力（生产实现是 *sdkmcp.ClientSession）。
type callerSession interface {
    CallTool(ctx context.Context, params *sdkmcp.CallToolParams) (*sdkmcp.CallToolResult, error)
}
```

## 核心接口

```go
// 加载并合并两层配置；返回归一化的 Config。
// - root: 项目根（用来定位 <root>/.mewcode.yaml）
// - 文件不存在 → 视为空层；格式非法 → 跳过该层 + stderr 告警（降级，N1）
// - 内部完成 ${VAR} 展开与字段校验（非法 server 直接剔除，N2）
// - 永不返回 error；签名留 error 仅为未来扩展（当前实现恒为 nil）
func LoadConfig(root string) (Config, error)

// 启动 Manager：并发连接所有 server，每个 server 30s 超时，失败仅跳过 + 告警。
// 阻塞直到所有 server 的尝试结束（成功 / 失败 / 超时）。
// version 透传到 Implementation.Version（便于 server 端识别 mewcode 版本）。
func NewManager(ctx context.Context, cfg Config, version string) *Manager

// 返回适配好的工具列表（按 server 名 → 工具名 稳定排序）。
func (m *Manager) Tools() []tool.Tool

// 关闭所有会话（stdio 子进程终止、HTTP DELETE）；总超时 5s 兜底，绝不阻塞退出。
func (m *Manager) Close()
```

## 模块设计

### internal/mcp/config.go
**职责：** 加载两层 YAML、合并、展开 `${VAR}`、校验。
**关键点：**
- 内部 `rawConfig { McpServers map[string]rawServer `yaml:"mcp_servers"` }`；`rawServer` 含全部可能字段（Type/Command/Args/Env/URL/Headers）。
- `loadFile(path) (rawConfig, error)`：文件不存在 → 空 + nil err；`yaml.Unmarshal` 失败 → 零值 + err（调用方降级）。
- `expandVars(s string) (out string, undefined []string)`：正则 `\$\{([A-Za-z_][A-Za-z0-9_]*)\}`，未定义变量记录到 undefined（供告警）。**仅作用于 env/headers 的值**。
- `applyExpansion(name string, srv *rawServer)`：对 env/headers 的每个值跑 expandVars；未定义变量在 stderr 输出 `[mcp] warn: undefined env var ${X} referenced by server <name>`。
- `mergeServers(user, project map[string]rawServer)`：遍历 user；遍历 project，同名直接整对象覆盖。
- `validateServer(name string, srv rawServer) (ServerConfig, bool)`：
  - `Type` 必为 `"stdio"` 或 `"http"`，否则跳过；
  - `stdio` 必填 `Command`；`http` 必填 `URL`；缺失则跳过；
  - 违规时 stderr 告警 `[mcp] warn: skip server <name>: <reason>`。
- `LoadConfig(root string)`：
  - 用户级 = `os.UserHomeDir() + "/.mewcode/config.yaml"`；项目级 = `<root>/.mewcode.yaml`。
  - 两层各自 `loadFile` + `applyExpansion`；任一层解析失败 stderr 一行告警并跳过（该层视为空）。
  - `mergeServers` 后逐个 `validateServer`，组装 `Config`。

### internal/mcp/manager.go
**职责：** 连接 server、缓存会话、关闭。
**关键点：**
- `NewManager(ctx, cfg, version)`：
  - 对每个 server 起 goroutine 并发连接；`sync.WaitGroup` 等齐。
  - 每个 goroutine：`ctx2, cancel := context.WithTimeout(ctx, 30*time.Second); defer cancel()`。
  - 按 type 构造 transport：
    - **stdio**：`cmd := exec.CommandContext(ctx2, srv.Command, srv.Args...)`；`cmd.Env = mergeOSEnv(srv.Env)`；`cmd.Stderr = os.Stderr`；`transport := &sdkmcp.CommandTransport{Command: cmd}`。
    - **http**：`hc := &http.Client{Transport: &headerRoundTripper{base: http.DefaultTransport, headers: srv.Headers}}`；`transport := &sdkmcp.StreamableClientTransport{Endpoint: srv.URL, HTTPClient: hc, DisableStandaloneSSE: true}`。
  - `client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "mewcode", Version: version}, nil)`。
  - `cs, err := client.Connect(ctx2, transport, nil)` ← 自动完成 initialize 握手。
  - `lst, err := cs.ListTools(ctx2, nil)`；err → 一行 stderr 告警，**调 `cs.Close()` 释放连接**（避免连了但列工具失败时的连接泄漏），return。
  - 对每个返回工具调 `adaptTool(serverName, t, cs)`，成功的 push 到 manager 切片。
  - 所有写共享状态走 `manager.mu`。
- `mergeOSEnv(extra map[string]string)`：把 `os.Environ()` 转成 map，再用 `extra` 覆盖，最后还原为 `KEY=VAL` 切片返回。同名宿主变量被 extra 覆盖（让 server 配置注入的凭据生效）。
- `headerRoundTripper{ base http.RoundTripper; headers map[string]string }`：`RoundTrip(req)` 中 `for k,v := range headers { req.Header.Set(k, v) }`，再 `base.RoundTrip(req)`。
- `Tools()`：稳定排序（先 server 名再 tool 名）。
- `Close()`：对每个 session 起 goroutine 调 `cs.Close()`；`select { case <-allDone: case <-time.After(5*time.Second): }` 兜底。

### internal/mcp/tool.go
**职责：** 把 SDK 返回的 `*sdkmcp.Tool` 适配为 `tool.Tool`。
**关键点：**
- `adaptTool(serverName string, t *sdkmcp.Tool, cs callerSession) (*mcpTool, bool)`：
  - `fullName = "mcp__" + serverName + "__" + t.Name`。
  - **禁用字符校验**：用正则 `^[A-Za-z0-9_-]+$` 校验 `fullName`，否则返回 `(nil, false)` + stderr 告警 `[mcp] warn: skip tool <fullName>: contains illegal characters`。
  - `descr`：`t.Description` 为空时兜底 `"来自 MCP server " + serverName + " 的工具 " + t.Name`。
  - `schema`：`b, _ := json.Marshal(t.InputSchema); var m map[string]any; json.Unmarshal(b, &m)`；解出空 map 时给 `{"type":"object"}` 兜底（避免 provider 拒收空 schema）。
  - `readOnly`：`t.Annotations != nil && t.Annotations.ReadOnlyHint`（nil-safe）。
- `(*mcpTool).Name() string { return m.fullName }`、`Description/Parameters/ReadOnly` 同理。
- `(*mcpTool).Execute(ctx, args)`：
  - `ctx2, cancel := context.WithTimeout(ctx, 30*time.Second); defer cancel()`。
  - 把 `json.RawMessage` 解到 `map[string]any`（空参数视为 `nil`）；解析失败 → `Result{Content: "参数解析失败: ...", IsError: true}`。
  - `res, err := m.cs.CallTool(ctx2, &sdkmcp.CallToolParams{Name: m.remoteName, Arguments: argMap})`。
  - `err != nil` → `Result{Content: "MCP 工具调用失败: " + err.Error(), IsError: true}`（含 ctx 超时）。
  - 否则：遍历 `res.Content`，把 `*sdkmcp.TextContent` 的 `.Text` 拼接；非 text 块计数，首次出现时 stderr 一行告警（per fullName 限一次，用包级 `sync.Map` + `LoadOrStore`）。
  - 返回 `tool.Result{Content: collected, IsError: res.IsError}`。

### cmd/mewcode/main.go（改造）
位置：在 `registry := tool.NewDefaultRegistry()` 之后、`permission.NewEngine` 之前插入：
```go
mcpCfg, _ := mcp.LoadConfig(root)
mgr := mcp.NewManager(context.Background(), mcpCfg, version)
defer mgr.Close()
for _, t := range mgr.Tools() {
    registry.Register(t)
}
```
（`root` 复用现有的 `os.Getwd()` 结果；version 复用 `const version`。）

### cmd/smoke/main.go（不改）
smoke 用 `NewDefaultRegistry` 不接 MCP，保持非交互简单。

## 文件组织

```
mewcode/
├── internal/mcp/
│   ├── config.go        — 新：Config/ServerConfig、LoadConfig、loadFile、expandVars、mergeServers、validateServer
│   ├── config_test.go   — 新：两层合并 / 变量展开 / 字段校验 / 降级 单测
│   ├── manager.go       — 新：Manager、NewManager（并发+30s 超时）、Close（5s 兜底）、Tools、headerRoundTripper、mergeOSEnv
│   ├── manager_test.go  — 新：连接成功/失败/超时、Close 不死锁、共享状态并发安全
│   ├── tool.go          — 新：mcpTool、callerSession、adaptTool、Execute
│   └── tool_test.go     — 新：命名拼接、禁用字符跳过、Execute 各分支（成功/远端 IsError/超时/协议错/非 text 块）
├── cmd/mewcode/main.go  — 改：装配 mcp.Manager，注册 MCP 工具，defer Close
├── go.mod               — 改：添加 github.com/modelcontextprotocol/go-sdk
├── go.sum               — 改：依赖校验和
├── docs/mcp/
│   ├── spec.md / plan.md / task.md / checklist.md
│   └── mcp-servers.example.yaml — 新：配置示例（用 ${VAR}）
└── （其它包零改）
```

## 技术决策

| 决策点 | 选择 | 理由 |
|---|---|---|
| 协议层实现 | 官方 Go SDK（`github.com/modelcontextprotocol/go-sdk`） | 用户拍板；避免自研 JSON-RPC/握手/帧；SDK 已处理 stdio 与 Streamable HTTP |
| 配置文件位置 | 项目级 `<root>/.mewcode.yaml` + 用户级 `~/.mewcode/config.yaml` | 用户拍板；项目级 dotfile 一眼可见、与现有 `.mewcode/config.yaml`（providers 凭据）分离 |
| 配置层数 | 仅两层，无本地级 | 用户拍板；`${VAR}` 已让密钥不入配置，本地层冗余 |
| 合并语义 | server 名维度，项目级完整覆盖 | 避免字段级半合并出畸形 server |
| server 类型字段 | 显式 `type: stdio\|http` | 不靠字段嗅探（防止误判）；未来扩展易加（如 sse） |
| 变量展开范围 | 仅 env/headers 的值 | 避免 command/args/server 名/工具名被环境间接影响；凭据走 env/headers 已足够 |
| 未定义变量 | 空串 + 一次性告警（不阻断） | server 自决无凭据时是否能跑；mewcode 不替它拍板 |
| 工具命名 | `mcp__<server>__<tool>` | 用户拍板；Claude Code 风格；LLM 工具名安全字符；一眼识别来源 |
| 启动连接策略 | 同步进 TUI 前完成 + 并发每 server 30s 超时 + 失败跳过 | 进 TUI 时工具集稳定；并发缩短总时延；隔离避免单 server 拖死启动 |
| 调用超时 | 30s 硬编码，转 IsError | 与连接同值；不中断 Loop；避免长卡 |
| readOnly 适配 | 严格只信 `annotations.readOnlyHint==true` | 默认走 Ask，最严；声明只读才放行 |
| 资源/提示词/采样/roots | 不实现 | 本章只覆盖工具能力 |
| 独立 SSE 通道 | `DisableStandaloneSSE: true` | 只用请求-响应；省一条长连接；减少复杂度 |
| 非 text 内容块 | 静默丢弃 + 一次性告警 | 模型只能消费文本；丢弃比假装回灌更诚实 |
| 错误回灌 | 协议错/超时均转 IsError | 与不中断 Loop 的契约一致 |
| 退出关闭 | 每 session.Close 并发 + 5s 总超时兜底 | 避免某 server 卡死阻塞退出 |
| permission 接入方式 | 零改动；靠 `friendlyName` 原样 + `categorize` 按 readOnly 优先 | 复用现成链路；权限规则可写 `mcp__server__tool` 与 `mcp__server__*` |
| HTTP 自定义 headers | `http.RoundTripper` 包装注入 | SDK 暴露 `HTTPClient` 字段；RoundTripper 是标准 Go 做法；不引入额外抽象 |
| OAuth | 不实现完整流程 | 用户预换 token 写 headers；本章范围最小化 |
| Execute 接口注入 | mcpTool 持 `callerSession` 接口而非具体 `*ClientSession` | 单测可注入 stub；生产代码无运行时开销 |

## 模块交互

```
main.main()
  ├─ tool.NewDefaultRegistry()                    // 6 内置工具
  ├─ mcp.LoadConfig(root)                         // 读两层 yaml + ${VAR} 展开 + 校验
  ├─ mcp.NewManager(ctx, cfg, version)            // 并发连接所有 server，30s/各
  │     └─ 对每个 server：
  │         ├─ 构造 transport（stdio:CommandTransport / http:StreamableClientTransport）
  │         ├─ NewClient + Connect（自动 initialize 握手）
  │         ├─ ListTools
  │         └─ adaptTool 包装成 mcpTool
  ├─ for t in mgr.Tools(): registry.Register(t)
  ├─ permission.NewEngine(root)
  ├─ tui.New(...) ; m.Run()
  └─ defer mgr.Close()                            // stdio 终止子进程，HTTP DELETE 会话；5s 总超时兜底
```

调用链（Agent 视角，工具来源透明）：
```
agent.executeBatched(calls, mode)
  └ permission.Check(mode, call, registry.IsReadOnly(call.Name))
       (MCP 工具：friendlyName 原样；categorize：readOnly==true→Read, 否则→Exec；
        extractTarget(未知工具)→isFile=false,target="" → 黑名单/沙箱自动跳过)
  └ Allow → registry.Execute(name, args)
       └ mcpTool.Execute(ctx, args)
            ├ context.WithTimeout(ctx, 30s)
            └ cs.CallTool → 拼接 text / 映射 IsError / 协议错转 IsError
  └ tool.Result 回灌 conv
```

依赖方向（无环）：`main → mcp → {tool, llm, SDK, 标准库}`；`mcp` 不依赖 agent / tui / permission / conversation。