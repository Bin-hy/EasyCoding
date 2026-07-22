# MCP 客户端 Tasks

## 文件清单

| 操作 | 文件 | 职责 |
|------|------|------|
| 改   | `go.mod` / `go.sum` | 添加 `github.com/modelcontextprotocol/go-sdk` 依赖 |
| 新建 | `internal/mcp/config.go` | `Config`/`ServerConfig`、`LoadConfig`、`loadFile`、`expandVars`、`applyExpansion`、`mergeServers`、`validateServer` |
| 新建 | `internal/mcp/config_test.go` | 两层合并 / `${VAR}` 展开 / 字段校验 / 降级 单测 |
| 新建 | `internal/mcp/tool.go` | `callerSession` 接口、`mcpTool`、`adaptTool`、`Execute`、非 text 块告警 once 池 |
| 新建 | `internal/mcp/tool_test.go` | 命名拼接 / 禁用字符 / Execute 成功 / 远端 IsError / 超时 / 协议错 / 非 text 块跳过 单测 |
| 新建 | `internal/mcp/manager.go` | `Manager`、`session`、`NewManager`（并发 + 30s 超时）、`Close`（5s 兜底）、`Tools`、`headerRoundTripper`、`mergeOSEnv` |
| 新建 | `internal/mcp/manager_test.go` | 连接成功/失败/超时、Close 不死锁、并发写共享状态安全 单测 |
| 改   | `cmd/mewcode/main.go` | 装配 `mcp.LoadConfig`、`mcp.NewManager`、注册 MCP 工具、`defer Close` |
| 新建 | `docs/mcp/mcp-servers.example.yaml` | 配置示例（含 stdio / http 各一个，用 `${VAR}`） |

---

## T1: 添加 MCP Go SDK 依赖

**文件：** `go.mod`、`go.sum`
**依赖：** 无
**步骤：**
1. 在仓库根执行 `go get github.com/modelcontextprotocol/go-sdk/mcp@latest`。
2. `go mod tidy` 整理依赖；查看 `go.mod` 确认 `github.com/modelcontextprotocol/go-sdk vX.Y.Z` 出现在 `require` 区块。
3. 写一段最小试编（可直接放进后续 `tool.go` 的 import 中）：`import sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"` 并使用一次 `sdkmcp.NewClient`，验证可用。

**验证：** `go build ./...` 编译通过；`go.mod` 内出现 SDK 依赖行。

## T2: 配置类型与加载（含两层合并 + 变量展开 + 字段校验）

**文件：** `internal/mcp/config.go`、`internal/mcp/config_test.go`
**依赖：** T1
**步骤：**
1. 定义对外类型 `Config`、`ServerConfig`（见 plan.md「核心数据结构」）。
2. 定义内部 `rawConfig { McpServers map[string]rawServer ``yaml:"mcp_servers"`` }`；`rawServer` 含全部字段。
3. `loadFile(path string) (rawConfig, error)`：
   - 文件不存在 → `(rawConfig{}, nil)`；
   - `os.ReadFile` 出错（非 NotExist）→ `(rawConfig{}, err)`；
   - `yaml.Unmarshal` 失败 → `(rawConfig{}, err)`（调用方降级）。
4. `expandVars(s string) (out string, undefined []string)`：
   - 正则 `\$\{([A-Za-z_][A-Za-z0-9_]*)\}` 匹配；用 `os.LookupEnv` 取值；未定义记录变量名到 `undefined`。
5. `applyExpansion(name string, srv *rawServer)`：
   - 对 `srv.Env`、`srv.Headers` 的每个值跑 `expandVars`，原地替换；
   - 收集所有 undefined 变量名，去重；首次出现时 `fmt.Fprintf(os.Stderr, "[mcp] warn: undefined env var ${%s} referenced by server %s\n", v, name)`。
6. `mergeServers(user, project map[string]rawServer) map[string]rawServer`：
   - 新建 map，复制 user；
   - 遍历 project，直接整对象覆盖同名 key。
7. `validateServer(name string, srv rawServer) (ServerConfig, bool)`：
   - `srv.Type` 必为 `"stdio"` 或 `"http"`，否则跳过；
   - `stdio` 必填 `Command`；`http` 必填 `URL`；缺失则跳过；
   - 违规时 `fmt.Fprintf(os.Stderr, "[mcp] warn: skip server %s: %s\n", name, reason)`；返回 `(zero, false)`。
8. `LoadConfig(root string) (Config, error)`：
   - 用户级 = `filepath.Join(home, ".mewcode", "config.yaml")`（`os.UserHomeDir` 失败时跳过用户层不致错）；项目级 = `filepath.Join(root, ".mewcode.yaml")`。
   - 两层各自 `loadFile`；err（非 NotExist）→ 一行 stderr 告警 + 该层视为空。
   - 对每层各 server 跑 `applyExpansion`。
   - `mergeServers` 后逐个 `validateServer`，收齐合法 server 组装 `Config`。
   - 永不返回 error（签名留 error 仅为未来扩展，当前实现恒为 `nil`）。

**验证：** `go build ./internal/mcp/...`；`go test ./internal/mcp/...` 覆盖：
- 两文件缺失 → `Config.Servers` 为空、无 err；
- 仅用户级 / 仅项目级 / 都有（同名 server 项目级胜出，断言字段为项目级值）；
- 文件格式非法 → 跳过该层、其它正常加载、stderr 有告警（可在测试中重定向 stderr 断言）；
- `${VAR}` 已定义 → 展开为环境值；未定义 → 空串 + 告警；`command`/`args` 中含 `${VAR}` → 不展开（保留字面量）；
- type 缺失 / type 非法 / stdio 缺 command / http 缺 url → 该 server 被跳过，其它 server 不受影响。

## T3: 工具适配（mcpTool）

**文件：** `internal/mcp/tool.go`、`internal/mcp/tool_test.go`
**依赖：** T1
**步骤：**
1. `import sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"`；`import "mewcode/internal/tool"`。
2. 定义最小接口 `callerSession` 与 `mcpTool` 结构体（见 plan.md「核心数据结构」）。
3. 实现 `tool.Tool` 接口的 5 个方法（`Name`/`Description`/`Parameters`/`ReadOnly` 均返回字段；`Execute` 见下）。
4. `adaptTool(serverName string, t *sdkmcp.Tool, cs callerSession) (*mcpTool, bool)`：
   - `fullName := "mcp__" + serverName + "__" + t.Name`；
   - 用包级编译好的 `var validName = regexp.MustCompile(``^[A-Za-z0-9_-]+$``)` 校验 `fullName`，不通过 → `(nil, false)` + stderr 告警 `[mcp] warn: skip tool <fullName>: name contains illegal characters`。
   - `descr := t.Description`；空则兜底 `"来自 MCP server " + serverName + " 的工具 " + t.Name`。
   - `schema`：`b, _ := json.Marshal(t.InputSchema)`；`var m map[string]any`；`json.Unmarshal(b, &m)`；若 `m == nil || len(m) == 0` → 用 `map[string]any{"type": "object"}` 兜底。
   - `readOnly := t.Annotations != nil && t.Annotations.ReadOnlyHint`。
5. `Execute(ctx context.Context, args json.RawMessage) tool.Result`：
   - `ctx2, cancel := context.WithTimeout(ctx, 30*time.Second); defer cancel()`。
   - 把 `args` 解到 `map[string]any`：空 / 全空白 → `nil`；否则 `json.Unmarshal`，失败 → `tool.Result{Content: "参数解析失败: " + err.Error(), IsError: true}`。
   - `res, err := m.cs.CallTool(ctx2, &sdkmcp.CallToolParams{Name: m.remoteName, Arguments: argMap})`。
   - `err != nil` → `tool.Result{Content: "MCP 工具调用失败: " + err.Error(), IsError: true}`。
   - 否则遍历 `res.Content`：
     - 类型断言 `*sdkmcp.TextContent`：把 `.Text` 拼到 strings.Builder（块间用 `"\n"` 分隔）；
     - 非 text 块：计数 + 通过包级 `var nonTextWarnOnce sync.Map` 对 `fullName` `LoadOrStore` 一次 stderr 告警 `[mcp] warn: tool <fullName> returned non-text content blocks (dropped)`。
   - 返回 `tool.Result{Content: collected, IsError: res.IsError}`。

**验证：** `go test ./internal/mcp/...` 覆盖：
- 合法 server 名 + 工具名 → adaptTool 返回成功；含 `.` / `@` 等非法字符 → 跳过 + 告警；
- description 空 → 兜底文案出现；schema nil → `{"type":"object"}`；schema 透传成功；
- `t.Annotations == nil` → readOnly=false（不 panic）；`ReadOnlyHint=true` → readOnly=true；
- Execute：注入 stub `callerSession`，覆盖：成功（多 text 块拼接） / 远端 `IsError=true` 映射 / `CallTool` 返回 err 转 `IsError=true` / ctx 超时（用 stub 阻塞 + 短超时单测覆盖，或直接断 `errors.Is(err, context.DeadlineExceeded)` 转 IsError） / 非 text 块跳过 + collected 仅含 text。

## T4: 连接管理器（Manager）

**文件：** `internal/mcp/manager.go`、`internal/mcp/manager_test.go`
**依赖：** T2、T3
**步骤：**
1. 定义 `Manager` 与 `session` 结构（见 plan.md）。
2. `headerRoundTripper` 与 `RoundTrip` 实现：
   ```go
   type headerRoundTripper struct {
       base    http.RoundTripper
       headers map[string]string
   }
   func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
       req = req.Clone(req.Context())
       for k, v := range h.headers {
           req.Header.Set(k, v)
       }
       return h.base.RoundTrip(req)
   }
   ```
3. `mergeOSEnv(extra map[string]string) []string`：把 `os.Environ()` 转 map，用 extra 覆盖同名键，再还原为 `KEY=VAL` 切片。
4. `NewManager(ctx context.Context, cfg Config, version string) *Manager`：
   - 内部 `mgr := &Manager{}`；`var wg sync.WaitGroup`。
   - 对 `cfg.Servers` 中每个 `(name, srv)`，`wg.Add(1)` 起 goroutine。
   - goroutine 内：
     - `ctx2, cancel := context.WithTimeout(ctx, 30*time.Second); defer cancel(); defer wg.Done()`。
     - 按 srv.Type 构造 transport。
     - `client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "mewcode", Version: version}, nil)`。
     - `cs, err := client.Connect(ctx2, transport, nil)`；err → 告警 `[mcp] warn: connect server <name> failed: <err>` + return。
     - `lst, err := cs.ListTools(ctx2, nil)`；err → 告警 + `cs.Close()` + return。
     - 对每个 tool 调 `adaptTool`；成功的入临时 slice。
     - 取 `mgr.mu`：`mgr.sessions = append(...)`、`mgr.tools = append(mgr.tools, adapted...)`。
   - `wg.Wait()` 后稳定排序 `mgr.tools`（先 server 名再 tool 名；用 `sort.Slice` + `mcpTool.fullName` 即可，因为 fullName 已带 server 前缀）。
   - 返回 `mgr`。
5. `Tools() []tool.Tool`：返回 `m.tools` 的拷贝（防外部修改）。
6. `Close()`：
   - 对每个 `session` 起 goroutine 调 `cs.Close()`；`WaitGroup` 等齐。
   - 用 `done := make(chan struct{})` + `time.After(5*time.Second)` 实现总超时兜底；超时即 return 不等。

**验证：** `go test ./internal/mcp/...` 覆盖：
- 空 `cfg` → Manager 无 sessions、`Tools()` 空、`Close()` 立即返回；
- 失败隔离：构造一个 stdio server 指向不存在的 command + 一个用单测注入 stub 的成功"server"，断言 stub 工具被注册、失败 server 仅产生告警；
- 超时收尾：注入一个会卡住的连接 stub（接口替身把 SDK 调用替换成手写阻塞），把 30s 在测试中通过 `var connectTimeout = 30*time.Second` 包级变量改为短值（如 200ms），断言超时窗口内被跳过；
- Close 兜底：注入一个 Close 阻塞的 session，断言 `Close()` 在 5s 内（测试中改为短值）返回；
- 并发安全：`go test -race` 通过。

实现注释：把 30s 与 5s 改成包级 `var` 而非 `const`，便于单测在 setup 中临时改小，结束 restore。

## T5: main 接线

**文件：** `cmd/mewcode/main.go`
**依赖：** T2、T3、T4
**步骤：**
1. import `mewcode/internal/mcp` 与 `context`（若没有）。
2. 在 `registry := tool.NewDefaultRegistry()` 行之后、`permission.NewEngine` 之前插入：
   ```go
   mcpCfg, _ := mcp.LoadConfig(root)
   mgr := mcp.NewManager(context.Background(), mcpCfg, version)
   defer mgr.Close()
   for _, t := range mgr.Tools() {
       registry.Register(t)
   }
   ```
3. `root` 复用现有 `os.Getwd()` 结果（已在 main 中）；`version` 复用 `const version`。

**验证：** `go build ./...`；无 MCP 配置时 `go run ./cmd/mewcode` 能正常进 TUI、内置 6 工具可用；配置一个 command 不存在的 stdio server 时进 TUI 不阻塞、stderr 显示连接失败告警。

## T6: 配置示例

**文件：** `docs/mcp/mcp-servers.example.yaml`
**依赖：** 无（可与 T2 并行）
**步骤：**
1. 内容（用 YAML 注释说明放置位置与覆盖语义）：
   ```yaml
   # 项目级放 <root>/.mewcode.yaml；用户级放 ~/.mewcode/config.yaml。
   # 同名 server 项目级完整覆盖用户级。
   # env / headers 的值支持 ${VAR} 从宿主环境变量展开；command/args 不展开。
   mcp_servers:
     github:
       type: stdio
       command: npx
       args: ["-y", "@modelcontextprotocol/server-github"]
       env:
         GITHUB_TOKEN: "${GITHUB_TOKEN}"
     local-sqlite:
       type: stdio
       command: python
       args: ["-m", "mcp_server_sqlite", "--db", "./data.db"]
     example-http:
       type: http
       url: "https://mcp.example.com/mcp"
       headers:
         Authorization: "Bearer ${EXAMPLE_TOKEN}"
   ```

**验证：** 在 `config_test.go` 增加一个用例，读取此示例文件断言三个 server 都被解析成功。

## T7: tmux 端到端实跑（CLAUDE.md 开发原则）

**文件：** —
**依赖：** T1–T6
**步骤：**
1. 准备一个真实可用的 stdio MCP server。优先用 `npx -y @modelcontextprotocol/server-everything`（官方示例 server，自带 echo / add 等基础工具）；若无 npx，可临时用一个最小 Python/JS server。
2. 在项目根写一个临时 `.mewcode.yaml` 指向它：
   ```yaml
   mcp_servers:
     demo:
       type: stdio
       command: npx
       args: ["-y", "@modelcontextprotocol/server-everything"]
   ```
3. `tmux` 起 mewcode：
   - 启动日志（stderr）显示 server 连接成功 + 工具数；TUI 状态栏正常；
   - 让模型调用 `mcp__demo__echo` 一类工具：default 模式下弹人在回路 → 允许本次 → 工具结果回灌 → 模型续答；
   - 选"永久允许"后，本地权限规则被写入；重启 mewcode 后再调同工具不再弹窗（验证永久规则与 MCP 命名空间联动）；
   - 切到 bypassPermissions：调用不弹窗；但让模型跑 `rm -rf /` 仍被内置黑名单拦下（MCP 工具不绕过黑名单的内置作用域）；
   - Esc 取消弹窗：干净回到 idle，不退出程序；
   - `q` 退出 mewcode 后 `ps -ef | grep server-everything` 确认子进程已终止；
4. 配置一个 command 不存在的 server + 一个能跑的 server：启动 stderr 有失败告警，能跑的 server 工具仍可用。

**验证：** 上述全部观察通过；删除临时 `.mewcode.yaml`，恢复项目根干净。

## T8: 全量编译测试与规范

**文件：** —
**依赖：** T1–T7
**步骤：**
1. `gofmt -l .`（应无输出）；goimports 分组检查（`mewcode/internal/mcp` 应在本地包组）。
2. `go vet ./...`（应无告警）。
3. `go build ./...`；`go test ./...`；`go test -race ./internal/mcp/... ./internal/agent/... ./internal/tui/...`。
4. `git grep -E '(Bearer|sk-|ghp_|github_pat_)[A-Za-z0-9_-]{16,}'`（应无命中：凭据不落盘）。
5. `git check-ignore -q docs/mcp/mcp-servers.example.yaml` 不需要忽略（示例只含 `${VAR}`）。

**验证：** 全部通过。

## 执行顺序

```
T1(SDK 依赖) ─┬─→ T2(config) ─┐
              │                ├─→ T4(manager) ─→ T5(main 接线) ─→ T7(tmux 实跑) ─→ T8(规范)
              └─→ T3(tool)   ─┘
                                 └─→ T6(配置示例)（可与 T2 并行）
```
依赖：T2,T3 ← T1；T4 ← {T2,T3}；T5 ← {T2,T3,T4}；T6 独立于 T3、T4（可在 T2 完成后做）；T7 ← T1–T5；T8 ← 全部。