# 多协议 LLM 终端对话客户端 Tasks

> 模块路径：`mewcode`（Go 1.25）。内部包导入前缀 `mewcode/internal/...`。

## 文件清单

| 操作 | 文件 | 职责 |
|------|------|------|
| 新建 | `go.mod` / `go.sum` | 模块定义与依赖 |
| 新建 | `.mewcode/config.yaml.example` | 配置模板 |
| 修改 | `.gitignore` | 忽略 `.mewcode/config.yaml` |
| 新建 | `internal/config/config.go` | Config/ProviderConfig、Load、校验 |
| 新建 | `internal/prompt/prompt.go` | SystemPrompt、CatBanner、RenderBanner |
| 新建 | `internal/llm/provider.go` | Provider 接口、Message、StreamEvent、New 工厂 |
| 新建 | `internal/conversation/conversation.go` | 单会话多轮历史 |
| 新建 | `internal/llm/anthropic.go` | anthropic 适配器 |
| 新建 | `internal/llm/openai.go` | openai 适配器 |
| 新建 | `internal/tui/tui.go` | Model、Init/Update/View、状态机、Run |
| 新建 | `internal/tui/stream.go` | waitForEvent Cmd、streamMsg、spinner 计时 |
| 新建 | `internal/tui/select.go` | provider 选择（list） |
| 新建 | `internal/tui/view.go` | View 拼装、状态栏、错误样式、markdown 定型 |
| 新建 | `cmd/mewcode/main.go` | 入口装配 |

---

## T1: 初始化 Go module 与依赖
**文件：** `go.mod`、`go.sum`、`cmd/mewcode/main.go`（临时占位）
**依赖：** 无
**步骤：**
1. `go mod init mewcode`。
2. 写一个临时 `cmd/mewcode/main.go`，`package main` + 空 `main()` 打印版本，确保可构建。
3. `go get` 拉取依赖（以实际解析版本为准，写入 go.mod）：
   - TUI：bubbletea v2、bubbles v2、lipgloss v2、glamour v2
     （当前文档导入前缀为 `charm.land/...`，以 `go get` 实际成功的路径为准）
   - SDK：`github.com/anthropics/anthropic-sdk-go`、`github.com/openai/openai-go/v3`
   - 配置：`gopkg.in/yaml.v3`
4. `go mod tidy`。

**验证：** `go build ./...` 成功；`go list -m all` 能看到上述依赖。

## T2: config 包
**文件：** `internal/config/config.go`
**依赖：** T1
**步骤：**
1. 定义 `Config{Providers []ProviderConfig}` 与 `ProviderConfig{Name,Protocol,BaseURL,APIKey,Model,Thinking}`，带 `yaml` tag。
2. 实现 `Load(path string) (*Config, error)`：读文件 → `yaml.Unmarshal` → 校验。
3. 校验：Providers 非空；逐项 name/protocol/api_key/model 非空；protocol ∈ {"anthropic","openai"}。
   失败返回形如 `providers[1].api_key 不能为空` 的可读错误。
4. 文件不存在 / YAML 解析失败 → 返回可读错误（非 panic）。

**验证：** 写最小单测：合法配置返回正确条数；缺字段/非法 protocol/文件缺失分别返回非 nil 错误。`go test ./internal/config/`。

## T3: 配置模板与忽略
**文件：** `.mewcode/config.yaml.example`、`.gitignore`
**依赖：** T2
**步骤：**
1. 写 `.mewcode/config.yaml.example`：含 anthropic 条目（含 thinking）与一段注释掉的 openai 条目示例，字段与 ProviderConfig 对齐。
2. `.gitignore` 追加 `.mewcode/config.yaml`。

**验证：** 复制 example 为 `.mewcode/config.yaml` 后 `config.Load` 通过；`git status` 确认 `.mewcode/config.yaml` 被忽略。

## T4: prompt 包
**文件：** `internal/prompt/prompt.go`
**依赖：** T1
**步骤：**
1. 定义 `SystemPrompt` 常量（一段简洁的固定 system prompt）。
2. 定义 `CatBanner` 常量（ASCII 猫：`/\_/\`、`( o.o )`、`> ^ <`）。
3. 实现 `RenderBanner(version, cwd string) string`：拼出"猫 + MewCode vX + cwd + 就绪提示行"。

**验证：** `go build ./internal/prompt/`；临时打印 `RenderBanner("0.1.0", "/tmp")` 观察含三要素与提示行。

## T5: llm 包骨架
**文件：** `internal/llm/provider.go`
**依赖：** T2
**步骤：**
1. 定义 `Message{Role,Content}`、`StreamEvent{Text,Done,Err}`。
2. 定义 `Provider` 接口：`Name() string`、`Model() string`、`Stream(ctx, []Message) <-chan StreamEvent`。
3. 实现 `New(cfg config.ProviderConfig) (Provider, error)`：按 `cfg.Protocol` 分派 anthropic/openai；未知协议返回错误。（适配器在 T7/T8 实现，先留构造分派与编译占位。）

**验证：** `go build ./internal/llm/`（占位构造可暂时返回 not-implemented 错误）。

## T6: conversation 包
**文件：** `internal/conversation/conversation.go`
**依赖：** T5
**步骤：**
1. 定义 `Conversation{messages []llm.Message}`。
2. 实现 `AddUser(text)`、`AddAssistant(text)`、`Messages() []llm.Message`（返回副本或只读切片）。

**验证：** 单测：连续 AddUser/AddAssistant 后 Messages() 顺序与角色正确。`go test ./internal/conversation/`。

## T7: anthropic 适配器
**文件：** `internal/llm/anthropic.go`
**依赖：** T5、T4
**步骤：**
1. 构造 SDK client：`option.WithAPIKey(cfg.APIKey)`；`cfg.BaseURL` 非空时加 `option.WithBaseURL`。
2. `Stream`：起 goroutine；组装 `MessageNewParams{Model, MaxTokens, System=prompt.SystemPrompt, Messages=转换后的历史}`；`cfg.Thinking` 为真时设 ThinkingConfig（启用 + 预算）。
3. `NewStreaming` 迭代：`event.AsAny()` → `ContentBlockDeltaEvent` → `delta.AsAny()`：`TextDelta` 推 `StreamEvent{Text}`；`ThinkingDelta` 丢弃。
4. 迭代结束推 `StreamEvent{Done:true}`；`stream.Err()` 非空推 `StreamEvent{Err}`。`ctx.Done()` 时退出。结束关闭 channel。

**验证：** `go build ./internal/llm/`。联调留到 T14；可加一个用假 key 触发错误、确认 Err 事件到达的本地手测。

## T8: openai 适配器
**文件：** `internal/llm/openai.go`
**依赖：** T5、T4
**步骤：**
1. 构造 SDK client：`option.WithAPIKey`；`cfg.BaseURL` 非空时 `option.WithBaseURL`。
2. `Stream`：起 goroutine；`ChatCompletionNewParams{Model, Messages}`，首条 `SystemMessage(prompt.SystemPrompt)` + 历史转 User/Assistant 消息。
3. `NewStreaming` 迭代：`evt.Choices[0].Delta.Content` 非空推 `StreamEvent{Text}`。
4. 迭代结束推 `Done`；`stream.Err()` 推 `Err`；`ctx.Done()` 退出；结束关闭 channel。`thinking` 字段忽略。

**验证：** `go build ./internal/llm/`；同 T7 的错误路径手测。

## T9: tui Model 骨架
**文件：** `internal/tui/tui.go`
**依赖：** T1、T2、T5、T6
**步骤：**
1. 定义 `sessionState`（selecting/idle/streaming）与 `Model`（含 textarea/spinner/list/renderer/providers/provider/conv/events/curReply/turnStart/width/height）。**无 viewport**——完成的消息走终端 scrollback。
2. `New(providers)`：初始化组件（textarea：Prompt=`❯ `、Placeholder=`Send a message...`、Alt+Enter 换行、ShowLineNumbers=false；spinner；glamour renderer）；providers>1 → stateSelecting，否则 `llm.New(providers[0])` 进 stateIdle。
3. `Init()`：`tea.Batch(textarea.Focus(), tea.Println(banner))`——把启动横幅提交到 scrollback（打印一次）。
4. `Update`：处理 `tea.WindowSizeMsg`（同步 textarea 宽度、list 尺寸、重建 renderer 宽度）、`tea.KeyPressMsg`（Ctrl+C → cancel+Quit）。
5. `View()`：先返回占位拼装（详细 view 在 T12）。
6. `Run() error`：`tea.NewProgram(m).Run()`（inline 渲染，不使用 alt screen；v2 无 WithAltScreen）。

**验证：** `go build ./internal/tui/`；`go vet ./internal/tui/`。

## T10: tui 流式接入与计时
**文件：** `internal/tui/stream.go`、`internal/tui/tui.go`
**依赖：** T9、T5
**步骤：**
1. 定义 `streamMsg llm.StreamEvent` 与 `waitForEvent(ch) tea.Cmd`（从 channel 读一个 → 返回 streamMsg；channel 关闭也返回带 Done 的信号）。
2. stateIdle 提交逻辑：识别 `/exit`；否则 `conv.AddUser`、`events=provider.Stream(ctx,conv.Messages())`、`turnStart=time.Now()`、`curReply.Reset()`、清空 textarea、切 stateStreaming、返回 `tea.Batch(tea.Println(userBlock(text)), waitForEvent(events), m.spinner.Tick)`（用户输入块即提交到 scrollback）。
3. stateStreaming 处理 streamMsg：Text → `curReply` 追加（动态区由 View 直接显示）+ `waitForEvent` 续读；Done → glamour 定型 + `conv.AddAssistant` + 返回 `tea.Println(定型块)` 提交 scrollback + 回 stateIdle；Err → 返回 `tea.Println(errorBlock)` + 回 stateIdle。
4. spinner.TickMsg：推进 spinner、`elapsed=time.Since(turnStart)`、组装 `Imagining… (Ns)`、再次 Tick（仅 streaming 态）。

**验证：** `go build ./internal/tui/`。逻辑联调在 T14。

## T11: tui provider 选择
**文件：** `internal/tui/select.go`
**依赖：** T9、T2、T5
**步骤：**
1. 用 bubbles `list` 列出 providers（每项展示 `name`（`model`））。
2. stateSelecting 下方向键移动、Enter 选定 → `llm.New(选定)` → 设 provider → 进 stateIdle。
3. 选择态 View 渲染 list。

**验证：** `go build ./internal/tui/`；用 2 条 provider 配置启动应出现选择列表（T14 验证）。

## T12: tui View 拼装与渲染（scrollback 模型）
**文件：** `internal/tui/view.go`
**依赖：** T9、T4、T10
**步骤：**
1. banner 不在 View 中渲染——由 T9 的 `Init` 用 `tea.Println` 提交到 scrollback（打印一次）。
2. `View` 只渲染**动态区**：选择态显示 list；对话态自上而下 = 正在流式的回复（`●` + curReply + `Imagining… (Ns)`，仅 streaming 时）+ 带边框输入框 + 状态栏。
3. 状态栏：lipgloss 左 `provider.Name()`、右 `provider.Model()`，两端对齐。
4. 完成块（提交到 scrollback 的内容）：`userBlock(text)` = `●` + 文本；`renderMarkdown(reply)` = `●` + glamour 渲染（用 lipgloss 颜色降级）；均**无 You/MewCode 文字标签**。
5. 错误样式：`errorBlock(err)` 用可区分（如红色）lipgloss 样式，`●` 前缀。
6. 长行：动态区/完成块按宽度软换行，避免长错误/URL 被截断。

**验证：** `go build ./internal/tui/`；`go vet ./...`。

## T13: 入口装配
**文件：** `cmd/mewcode/main.go`（替换 T1 占位）
**依赖：** T2、T4、T9
**步骤：**
1. `config.Load(".mewcode/config.yaml")`；err → 打印可读信息、`os.Exit(1)`。
2. 打印 `prompt.RenderBanner(version, cwd)`（或交由 tui 首屏渲染，二选一保持一致）。
3. `m := tui.New(cfg.Providers)`；`m.Run()`；err → 非零退出。

**验证：** `go build ./...` 成功；缺配置时运行打印可读错误并非零退出。

## T14: 端到端联调
**文件：** 无（运行验证）
**依赖：** T1–T13
**步骤：**
1. 用真实 anthropic 配置（thinking:true）跑：多轮对话、流式逐字、Imagining 计时、Done 后 markdown 定型、思考内容不出现。
2. 用 openai 协议配置跑：同样多轮 + 流式。
3. 配两条 provider：启动出现选择列表，选定后状态栏正确。
4. 故意用错误 key：错误在对话区显示且不退出，可继续。
5. `/exit` 与 Ctrl+C：安全退出、终端无残留。

**验证：** 逐条对照 checklist.md 记录证据。

## 执行顺序
```
T1 ─┬─ T2 ─┬─ T3
    │      └─ T5 ─┬─ T6
    │             ├─ T7
    │             └─ T8
    ├─ T4
    └─ T9 ─┬─ T10
           ├─ T11
           └─ T12
T2,T4,T9 ─ T13
T1..T13 ─ T14
```
（T4 可与 T2/T5 并行；T7、T8 可并行；T10/T11/T12 在 T9 后可并行推进。）