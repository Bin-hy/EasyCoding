<p align="center">
  <br>
  <pre align="center">
███╗   ███╗███████╗██╗    ██╗ ██████╗ ██████╗ ██████╗ ███████╗
████╗ ████║██╔════╝██║    ██║██╔════╝██╔═══██╗██╔══██╗██╔════╝
██╔████╔██║█████╗  ██║ █╗ ██║██║     ██║   ██║██║  ██║█████╗
██║╚██╔╝██║██╔══╝  ██║███╗██║██║     ██║   ██║██║  ██║██╔══╝
██║ ╚═╝ ██║███████╗╚███╔███╔╝╚██████╗╚██████╔╝██████╔╝███████╗
╚═╝     ╚═╝╚══════╝ ╚══╝╚══╝  ╚═════╝ ╚═════╝ ╚═════╝ ╚══════╝
  </pre>
</p>

<h3 align="center">从零手写的终端 AI Agent，就在命令行里。</h3>

<p align="center">
  <a href="#"><img src="https://img.shields.io/badge/go-1.26%2B-00ADD8?style=flat-square&logo=go" alt="Go 1.26+"></a>
  <a href="#"><img src="https://img.shields.io/badge/license-MIT-blue?style=flat-square" alt="License MIT"></a>
  <a href="#"><img src="https://img.shields.io/badge/status-active%20development-brightgreen?style=flat-square" alt="Status"></a>
</p>

<p align="center">
  <a href="#-为什么选-mewcode">为什么</a> •
  <a href="#-快速开始">快速开始</a> •
  <a href="#-核心能力">核心能力</a> •
  <a href="#-工具系统">工具</a> •
  <a href="#-权限系统">权限</a> •
  <a href="#-mcp-协议">MCP</a> •
  <a href="#-架构">架构</a> •
  <a href="#-配置参考">配置</a> •
  <a href="#-开发">开发</a> •
  <a href="#-路线图">路线图</a>
</p>

---

## 💡 为什么选 MewCode

MewCode 是一个**从零手写的终端 AI Agent**——不是 CLI 包装器，没有 Electron 外壳，不依赖 Node.js 生态。一个二进制文件，两份 YAML 配置，就能在你的终端里和 Claude 或 GPT 一起写代码。

| 对比维度 | Copilot / Cursor | Claude Code | **MewCode** |
|----------|------------------|-------------|-------------|
| 运行环境 | IDE 插件 / Web | Node.js CLI | **单 Go 二进制** |
| 协议 | 绑定各自模型 | Anthropic | **Anthropic + OpenAI 双协议** |
| 自定义端点 | ❌ | ❌ | **✅ 任意兼容 API** |
| 工具系统 | IDE 集成 | 内置 | **可扩展注册中心** |
| 权限模型 | IDE 控制 | 内置 | **五层防御 + 人在回路** |
| MCP 协议 | ❌ | ✅ | **✅ 配置驱动零改码** |
| 学习价值 | 黑盒 | 复杂 | **Go 实现，代码简洁可读** |

MewCode 不试图取代 Claude Code —— 它是对 "AI Agent 如何在终端里工作" 这个问题的一次完整回答。每一行代码都是手写的，每一层抽象都经过推敲。如果你想理解 Agent 的底层机制，或者需要一个轻量、可控、支持自定义端点和 MCP 生态的终端 AI 助手，这就是答案。

## ⚡ 快速开始

### 前置条件

- **Go 1.26+**
- 一个 LLM API 密钥（Anthropic 或 OpenAI 或任意兼容端点）
- 一个终端

### 30 秒跑起来

```bash
git clone https://github.com/your-org/mewcode.git
cd mewcode

# 创建你的配置文件
cp .mewcode/config.yaml.example .mewcode/config.yaml

# 填入 API 密钥（用你喜欢的编辑器）
$EDITOR .mewcode/config.yaml

# 启动！
go run ./cmd/mewcode
```

看到 MEWCODE 大字和 `就绪 — 开始对话吧！` 就成功了。输入问题，按 `Enter` 发送。

### 键盘映射

| 按键 | 行为 |
|------|------|
| <kbd>Enter</kbd> | 发送消息 |
| <kbd>Alt</kbd> + <kbd>Enter</kbd> | 换行 |
| <kbd>Shift</kbd> + <kbd>Tab</kbd> | 循环切换权限模式（Default → Accept Edits → Plan → Bypass） |
| <kbd>Esc</kbd> | 取消本轮（streaming / 人在回路时） |
| <kbd>Ctrl</kbd> + <kbd>C</kbd> | 取消本轮 / 退出程序（idle 时） |
| `/exit` | 优雅退出 |
| `1` `2` `3` | 人在回路弹窗：允许本次 / 永久允许 / 拒绝 |

## ✨ 核心能力

### 🧠 多协议引擎

一份配置，多端点自由切换。`protocol` 字段决定适配层，上层代码对协议完全无感。

- **Anthropic** — 原生 Messages API，扩展思考、Prompt Cache
- **OpenAI** — Chat Completions API，兼容端点
- **自定义端点** — 设置 `base_url`，接入任意 OpenAI-compatible 服务（DeepSeek、通义千问、Ollama …）

### 💬 真正的 TUI 体验

基于 [Bubble Tea](https://github.com/charmbracelet/bubbletea) 的全功能终端界面：

```
┌─ MewCode v0.1.0 ────────────────────────────────────────────────────┐
│                                                                      │
│  ● 帮我重构 auth 模块，把所有 API 调用提取到独立的 client 层        │
│                                                                      │
│  ●                                                                   │
│  好的，让我先看看 auth 模块的现有结构。                              │
│                                                                      │
│  ● Glob(src/auth/**/*.go)           ⠋ Running…                      │
│    ⎿  src/auth/handler.go                                            │
│       src/auth/middleware.go                                          │
│       src/auth/repository.go                                          │
│                                                                      │
│  ● Read(src/auth/handler.go)                                         │
│    ⎿  1  package auth                                               │
│       2                                                              │
│       3  import (                                                    │
│       ⋮                                                            │
│                                                                      │
│  ╭──────────────────────────────────────────────────────────────────╮│
│  │ ❯                                                               ││
│  ╰──────────────────────────────────────────────────────────────────╯│
│  Claude                            claude-opus-4-8      DEFAULT     │
└──────────────────────────────────────────────────────────────────────┘
```

- **流式渲染** — SSE 逐 token 打字机输出
- **Markdown 美化** — 回复通过 Glamour 渲染，代码块高亮、表格对齐
- **工具执行可见** — Claude Code 同款 `● Tool(args)` → `⎿  结果摘要` 展示
- **Provider 选择** — 多端点配置时，启动先选供应商再进入对话
- **权限模式指示** — 状态栏右侧实时显示当前模式（DEFAULT / ACCEPT EDITS / PLAN / BYPASS）

### 🔄 多轮 ReAct Agent 闭环

不是"一次请求 → 一次回复"的简单问答，而是 **思考 → 行动 → 观察 → 思考 → …** 的完整 ReAct 循环（最多 10 轮迭代）：

```
用户消息
   │
   ▼
请求#1 ──→ 模型决定调用哪些工具
   │           │
   │      (无工具) ──→ 直接返回文本 ──→ 渲染到屏幕
   │
   │      (有工具)
   │           │
   │    只读工具并发执行 (glob + grep + read_file 同时跑)
   │    有副作用工具串行执行 (write_file → bash → …)
   │    每轮走权限判定 (黑名单 → 沙箱 → 规则 → 模式兜底 → 人在回路)
   │    工具结果回灌进对话历史
   │           │
   ▼           ▼
请求#2 ──→ 模型基于工具结果续答，可能继续调工具
   │           │
   │    同上流程……
   │           │
   ▼           ▼
请求#N ──→ 最终文本 ──→ Markdown 渲染到屏幕
```

关键特性：
- **只读并发** — `read_file`、`glob`、`grep` 等只读工具同一轮内并发执行，减少等待
- **连续未知工具检测** — 模型连续 3 轮幻觉出不存在的工具名时主动终止
- **历史一致性** — 取消/出错后自动补齐 `assistant` 尾消息，确保下一轮请求不报 400
- **Token 用量追踪** — 实时显示输入/输出 token，支持 Anthropic Cache 读写量

### 🛡️ 五层权限防御

每一层按优先级依次判定，命中即短路，五层全过的操作才真正执行：

```
① 黑名单          rm -rf /、curl | sh 等危险命令正则拦截（bypass 也拦）
   │
   ▼ 未命中
② 沙箱            文件操作限定在项目目录内，禁止读写 ../ 外部路径
   │
   ▼ 未命中
③ 规则引擎        用户级 / 项目级 / 本地级 三层 allow / deny 规则优先级匹配
   │               支持 Bash(cmd) / Write(*.go) / mcp__github__* 等模式
   │
   ▼ 未命中
④ 模式兜底        Default → 只读写 Allow、文件写 Ask、命令执行 Ask
                  AcceptEdits → 文件写 Allow、命令执行 Ask
                  Plan → 仅只读工具可见
                  Bypass → 全 Allow（③④ 跳过，①② 仍然生效）
   │
   ▼ 判 Ask
⑤ 人在回路        TUI 弹窗三选一：允许本次 / 永久允许（写本地规则）/ 拒绝
```

Shift+Tab 实时切换模式，状态栏即时反馈，不需要重启。

### 🔌 MCP 协议

给 MewCode 装上扩展坞——通过 [Model Context Protocol](https://modelcontextprotocol.org) 接入社区海量工具生态：

```yaml
# .mewcode.yaml（项目根，可提交 git）
mcp_servers:
  github:                         # 远端工具自动注册为
    type: stdio                   # mcp__github__create_issue
    command: npx                  # mcp__github__search_repos …
    args: ["-y", "@modelcontextprotocol/server-github"]
    env:
      GITHUB_TOKEN: "${GITHUB_TOKEN}"   # 凭据走环境变量，不落盘

  internal-api:
    type: http
    url: "https://mcp.internal.example.com"
    headers:
      Authorization: "Bearer ${INTERNAL_TOKEN}"
```

- **两种传输** — stdio（本地子进程）和 Streamable HTTP（远程端点）
- **配置驱动** — 声明 server 列表即可，零改码，启动时自动发现并注册所有工具
- **两层配置合并** — 用户级 `~/.mewcode/config.yaml` + 项目级 `.mewcode.yaml`，同名 server 项目级覆盖
- **命名空间隔离** — `mcp__<server>__<tool>` 前缀，杜绝与内置工具及多 server 间冲突
- **失败隔离** — 单个 server 挂了 / 超时了只跳过它自己，不影响其它 server 和内置工具
- **无感适配** — MCP 工具和内置工具走完全相同的 Tool 接口、权限链路、Agent 编排

### 🧩 系统提示工程化

不再是写死一段 Prompt 字符串。系统提示通过**模块化装配**生成：

- **7 个固定模块** — 按 Priority 排序拼接，跨调用逐字节稳定（供 Anthropic Prompt Cache 命中）
- **3 个可选空槽** — 供后续扩展动态注入
- **环境信息段** — 每轮变化的 OS / Shell / 日期 / 工作目录单独渲染，不破坏缓存段
- **Plan Mode 提醒** — 首轮注入完整约束，后续每 3 轮注入简化版，控制 token 消耗

## 🔧 工具系统

MewCode 不是聊天机器人——它能**动手干活**。工具系统是这一切的核心。

### 设计原则

- **协议无关** — 工具定义与 Anthropic/OpenAI 的 tool schema 解耦，注册中心统一导出
- **失败不崩溃** — 所有错误被包装成结构化结果回灌给模型，让它自己调整，不中断会话
- **可扩展** — 实现 `Tool` 接口 + `Register` 即可新增工具，MCP 工具自动适配无需手写

### 接口

```go
type Tool interface {
    Name() string                // 模型看到的工具名
    Description() string         // 给模型看的用途说明
    Parameters() map[string]any  // JSON Schema 参数定义
    ReadOnly() bool              // true = 只读（可并发执行 & Plan Mode 放行）
    Execute(ctx context.Context, args json.RawMessage) Result
}
```

### 内置工具

| 工具 | 签名 | 分类 | 能力 |
|------|------|------|------|
| `read_file` | `read_file(path)` | 只读 | 读取文件，自动带行号；文件不存在返回结构化错误 |
| `write_file` | `write_file(path, content)` | 文件写 | 覆盖写入，自动创建父目录 |
| `edit_file` | `edit_file(path, old_string, new_string)` | 文件写 | **精确字符串替换**——唯一匹配才执行，0 或 >1 匹配均报错 |
| `bash` | `bash(command)` | 命令执行 | 执行 Shell 命令，30s 超时，返回 stdout/stderr/exit code |
| `glob` | `glob(pattern)` | 只读 | 按 glob 模式匹配文件路径 |
| `grep` | `grep(pattern, path?)` | 只读 | 在代码中搜索文本，返回 `文件:行:内容` |

> `edit_file` 参考 Claude Code —— 不按行号（容易漂移），对原文片段做唯一匹配替换。这是 AI 编程助手的核心操作原语。

### 扩展工具

通过 MCP 协议接入的外部工具自动注册进同一个注册中心，Agent 调用时与内置工具完全无感。例如配置了 GitHub MCP server 后，Agent 就能直接 `mcp__github__create_issue`、`mcp__github__search_repos`，无需任何胶水代码。

## ⚙️ 配置参考

### LLM Provider 配置

```yaml
# .mewcode/config.yaml
providers:
  # ── Anthropic（推荐）────────────────────────────
  - name: "Claude"               # 显示名称（状态栏左侧）
    protocol: "anthropic"        # 协议：anthropic | openai
    model: "claude-opus-4-8"     # 模型 ID（状态栏右侧）
    base_url: ""                 # 自定义端点（空 = 默认）
    api_key: "sk-ant-..."        # API 密钥
    thinking: true               # 扩展思考（仅 Anthropic 生效）

  # ── OpenAI ──────────────────────────────────────
  - name: "ChatGPT"
    protocol: "openai"
    model: "gpt-5"
    base_url: ""                 # 可填兼容端点，如 DeepSeek、通义千问
    api_key: "sk-..."

  # ── 本地模型（示例：Ollama）─────────────────────
  - name: "Ollama Local"
    protocol: "openai"
    model: "llama3.1"
    base_url: "http://localhost:11434/v1"
    api_key: "ollama"            # Ollama 不需要真实密钥，但不能为空
```

| 字段 | 必填 | 说明 |
|------|------|------|
| `name` | ✅ | 可读名称，显示在状态栏左侧 |
| `protocol` | ✅ | `anthropic` 或 `openai` |
| `model` | ✅ | 模型标识符 |
| `api_key` | ✅ | API 密钥（Ollama 等本地服务可填任意值） |
| `base_url` | ❌ | 自定义 API 端点。留空使用 SDK 默认地址 |
| `thinking` | ❌ | 仅 Anthropic：开启 Claude 的扩展思考模式 |

**多 Provider 选择**：配置文件有多个 provider 时，MewCode 启动后先展示方向键列表让你选，选定后再进入对话。

### MCP Server 配置

```yaml
# .mewcode.yaml（项目根，可提交到 git）
mcp_servers:
  github:
    type: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github"]
    env:
      GITHUB_TOKEN: "${GITHUB_TOKEN}"    # 从环境变量展开，不写明文

  local-sqlite:
    type: stdio
    command: python3
    args: ["-m", "mcp_server_sqlite", "--db", "./data.db"]

  example-http:
    type: http
    url: "https://mcp.example.com/mcp"
    headers:
      Authorization: "Bearer ${EXAMPLE_TOKEN}"
```

| 字段 | 必填 | 说明 |
|------|------|------|
| `type` | ✅ | `stdio` 或 `http` |
| `command` | stdio 必填 | 启动子进程的命令 |
| `args` | ❌ | 命令行参数 |
| `env` | ❌ | 注入子进程的环境变量（值支持 `${VAR}` 展开） |
| `url` | http 必填 | Streamable HTTP 端点 |
| `headers` | ❌ | 每次 HTTP 请求注入的头（值支持 `${VAR}` 展开） |

**两层配置合并**：`~/.mewcode/config.yaml`（用户级）+ `<root>/.mewcode.yaml`（项目级），同名 server 项目级完整覆盖。`${VAR}` 未定义时展开为空串并告警，不阻断启动。

### 权限规则配置

```yaml
# .mewcode/settings.yaml（项目级 / 用户级均可）
defaultMode: acceptEdits          # 可选：default | acceptEdits | plan | bypassPermissions

permissions:
  allow:
    - "Bash(go test ./...)"       # 精确放行特定命令
    - "Write(*.md)"               # 放行所有 .md 文件写入
    - "Read"                      # 放行全部只读工具
    - "mcp__github__*"            # 放行 GitHub MCP server 所有工具

  deny:
    - "Bash(rm *)"                # 精确拒绝危险命令
    - "Write(*.env)"              # 禁止写入 .env 文件
```

三层配置优先级：**本地** (`settings.local.yaml`) > **项目** (`settings.yaml`) > **用户** (`~/.mewcode/settings.yaml`)。`settings.local.yaml` 由"永久允许"操作自动写入，已 gitignore。

## 📐 架构

```
mewcode/
│
├── cmd/
│   ├── mewcode/main.go          ── 入口：加载配置 → MCP → 注册工具 → 权限引擎 → 启动 TUI
│   └── smoke/main.go            ── 冒烟测试：非交互式验证 Agent Loop 核心流程
│
├── internal/
│   │
│   ├── config/                  ── YAML 配置加载与校验
│   │   └── ProviderConfig：name, protocol, base_url, api_key, model, thinking
│   │
│   ├── llm/                     ── 协议抽象层
│   │   ├── provider.go          ── Provider 接口 + Message / StreamEvent / ToolCall / System
│   │   ├── anthropic.go         ── Anthropic Messages API 适配器（含 Cache 控制）
│   │   └── openai.go            ── OpenAI Chat Completions 适配器
│   │
│   ├── conversation/            ── 对话历史管理器
│   │   └── 维护 user / assistant / tool 多轮消息列表，角色交替校验
│   │
│   ├── agent/                   ── 多轮 ReAct 编排引擎
│   │   └── Run(ctx, conv, mode) → <-chan Event
│   │       ├── 装配系统提示 + 环境段（每轮复用缓存）
│   │       ├── 流式发出请求 → Text 增量 / ToolCalls
│   │       ├── 只读工具并发执行 / 有副作用工具串行执行
│   │       ├── 每轮走五层权限判定 → Allow / Deny / Ask（人在回路）
│   │       └── 结果回灌历史 → 模型续答 → 最多 10 轮迭代
│   │
│   ├── tool/                    ── 工具抽象 + 注册中心 + 六个核心工具
│   │   ├── tool.go              ── Tool 接口（含 ReadOnly）+ Result 类型
│   │   ├── registry.go          ── 注册中心（Register / Get / Definitions / Execute）
│   │   ├── read_file.go         ── 读文件（带行号）
│   │   ├── write_file.go        ── 写文件（自动 mkdir）
│   │   ├── edit_file.go         ── 字符串匹配替换
│   │   ├── bash.go              ── Shell 执行（30s 超时）
│   │   ├── glob.go              ── Glob 文件匹配
│   │   └── grep.go              ── 代码文本搜索
│   │
│   ├── permission/              ── 五层权限防御系统
│   │   ├── mode.go              ── Mode 四档 + Decision + Category + Outcome
│   │   ├── engine.go            ── Check() 前四层流水线
│   │   ├── blacklist.go         ── ① 危险命令正则（rm -rf / curl | sh …）
│   │   ├── sandbox.go           ── ② 文件路径沙箱
│   │   ├── rule.go              ── ③ 规则引擎 + 模式匹配（glob / 命令串）
│   │   ├── settings.go          ── YAML 配置加载 + 友好名 / 分类 / 目标提取
│   │   └── persist.go           ── 永久放行写本地层
│   │
│   ├── mcp/                     ── MCP 客户端
│   │   ├── config.go            ── 两层 YAML 合并 + ${VAR} 展开 + 字段校验
│   │   ├── manager.go           ── 并发连接管理 + 30s 超时 + 5s 关闭兜底
│   │   └── tool.go              ── 远端工具适配为 tool.Tool（mcp__<server>__<tool>）
│   │
│   ├── prompt/                  ── 系统提示工程化
│   │   ├── prompt.go            ── BuildSystemPrompt / AssembleSystem / RenderBanner
│   │   ├── modules.go           ── 7 固定模块 + 3 可选空槽（按 Priority 排序）
│   │   ├── environment.go       ── 环境信息采集（OS / Shell / 日期 / 工作目录）
│   │   └── reminder.go          ── Plan Mode 提醒（首轮全量 / 后续简化，按轮次注入）
│   │
│   └── tui/                     ── Bubble Tea 终端界面
│       ├── tui.go               ── Model / Update / 状态机 / 人在回路弹窗
│       ├── view.go              ── 视图渲染：用户块、助手块、工具行、状态栏
│       ├── stream.go            ── agent Event → bubbletea Msg 桥接
│       └── select.go            ── 多 Provider 选择列表
│
├── .mewcode/
│   ├── config.yaml              ── LLM 密钥配置（已 .gitignore）
│   ├── config.yaml.example      ── LLM 配置模板
│   ├── settings.yaml            ── 项目级权限规则（可提交）
│   ├── settings.local.yaml      ── 个人永久放行记录（已 .gitignore）
│   └── settings.yaml.example    ── 权限规则模板
│
├── .mewcode.yaml                ── 项目级 MCP server 声明（可提交）
├── docs/mcp/mcp-servers.example.yaml
└── go.mod                       ── Go 1.26 / Bubble Tea v2 / Anthropic SDK / OpenAI SDK / MCP SDK
```

### 数据流

```
用户键盘输入
     │
     ▼
 TUI (Bubble Tea) ──→ Agent.Run(ctx, conv, mode)
                           │
                           ├─→ prompt.BuildSystemPrompt() ──→ 缓存命中
                           ├─→ prompt.GatherEnvironment()  ──→ 变化段
                           │
                           ▼  ReAct 循环（最多 10 轮）
                           │
                           ├─→ Provider.Stream(ctx, msgs, tools, sys, reminder) ──→ LLM API
                           │       │
                           │       ▼ SSE 事件流
                           │       │
                           │   Text 增量 ──→ TUI 渲染打字机
                           │   ToolCalls ──→ permission.Check() 五层判定
                           │       │            │
                           │       │       Allow → 执行（只读并发 / 写+Exec 串行）
                           │       │       Deny  → 结构化错误回灌
                           │       │       Ask   → TUI 人在回路弹窗 → 等待用户抉择
                           │       │                    │
                           │       │              工具结果回灌 conv
                           │       │
                           │       ▼
                           │   Provider.Stream() #N ──→ 续答 / 再调工具 / 完成
                           │
                           ▼
                      Event 流 → chan → bubbletea Msg → Update → View
```

## 🧪 开发

### Spec 驱动工作流

每个功能/章节遵循 Spec 驱动开发：

```
prompt.md ──→ spec.md ──→ plan.md ──→ task.md ──→ checklist.md
                                                          │
                                                   代码实现 + 验收
```

所有设计文档位于 [`docs/`](docs/) 目录，每个章节独立一个子目录。

### 运行测试

```bash
# 单元测试
go test ./...

# 竞态检测（重点守护 agent / mcp / tui 并发安全）
go test -race ./internal/mcp/... ./internal/agent/... ./internal/tui/...

# 冒烟测试（非交互式 Agent Loop 全流程验证，需有效 API key）
go run ./cmd/smoke

# 端到端测试（在 tmux 中进行真实交互）
tmux new -s mewcode-e2e
go run ./cmd/mewcode
# 输入真实的编程任务，观察工具调用与回复
# 对照 docs/chXX/checklist.md 逐项验收
```

### 添加新工具

```go
// 1. 实现 Tool 接口
type myTool struct{}

func (t *myTool) Name() string        { return "my_tool" }
func (t *myTool) Description() string { return "用中文描述工具用途" }
func (t *myTool) Parameters() map[string]any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "input": map[string]any{"type": "string", "description": "输入参数说明"},
        },
        "required": []string{"input"},
    }
}
func (t *myTool) ReadOnly() bool { return false }
func (t *myTool) Execute(ctx context.Context, args json.RawMessage) tool.Result {
    // 执行逻辑；失败返回 tool.Result{IsError: true, Content: "错误原因"}
}

// 2. 注册（在 main.go 中 reg.Register(&myTool{})）
```

更好的方式是通过 MCP 协议接入外部工具——只需在 `.mewcode.yaml` 中声明 server，零 Go 代码，工具自动注册并走完整权限链路。

### IDE / Editor 集成

项目根目录的 `CLAUDE.md` 配置了项目约定（中文注释、Spec 驱动、tmux E2E）。如果你也用 Claude Code 开发本项目，它已经准备就绪。

## 🗺️ 路线图

| 阶段 | 内容 | 状态 |
|------|------|------|
| ch02 | 多协议对话引擎：配置、Provider、流式 SSE、TUI、多轮上下文 | ✅ 完成 |
| ch03 | 工具系统：Tool 抽象、注册中心、六个核心工具 | ✅ 完成 |
| ch04 | Agent 多轮 ReAct 闭环：只读并发、副作用串行、迭代上限、连续幻觉检测 | ✅ 完成 |
| ch05 | 五层权限防御：黑名单 → 沙箱 → 规则引擎 → 模式兜底 → 人在回路 | ✅ 完成 |
| ch06 | 系统提示工程化：模块化装配、缓存通道、Plan Mode 按轮次注入 | ✅ 完成 |
| ch07 | MCP 协议：stdio / HTTP 双传输、配置驱动自动发现、命名空间隔离、失败隔离 | ✅ 完成 |
| ch08 | 会话持久化：保存 / 恢复对话历史 | 🚧 规划中 |
| ch09 | LSP 集成：代码智能（跳转定义、查找引用、诊断） | 📋 待排期 |
| ch10 | 插件系统：用户自定义工具加载器 | 📋 待排期 |

> 路线图会随着开发推进持续更新。欢迎提 Issue 和 PR！

## 🙏 致谢

MewCode 的许多设计灵感来自：

- [Claude Code](https://claude.ai/code) — ReAct Agent 闭环、工具行 UI 风格、edit_file 匹配原语、MCP 协议
- [Bubble Tea](https://github.com/charmbracelet/bubbletea) — 让 Go TUI 开发变成享受
- [Charm](https://charm.sh) — Lip Gloss + Glamour 让终端也能很美
- [Model Context Protocol](https://modelcontextprotocol.org) — 开放的工具生态标准
- 所有用 `anthropic-sdk-go` 和 `openai-go` 构建 Agent 的先行者们

## 📄 许可

[MIT](LICENSE) © 2026 Bin-hy
