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
  <a href="#-工具系统">工具</a> •
  <a href="#-架构">架构</a> •
  <a href="#-配置参考">配置</a> •
  <a href="#-开发">开发</a> •
  <a href="#-路线图">路线图</a>
</p>

---

## 💡 为什么选 MewCode

MewCode 是一个**从零手写的终端 AI Agent**——不是 CLI 包装器，没有 Electron 外壳，不依赖 Node.js 生态。一个二进制文件，一份 YAML 配置，就能在你的终端里和 Claude 或 GPT 一起写代码。

| 对比维度 | Copilot / Cursor | Claude Code | **MewCode** |
|----------|------------------|-------------|-------------|
| 运行环境 | IDE 插件 / Web | Node.js CLI | **单 Go 二进制** |
| 协议 | 绑定各自模型 | Anthropic | **Anthropic + OpenAI 双协议** |
| 自定义端点 | ❌ | ❌ | **✅ 任意兼容 API** |
| 工具系统 | IDE 集成 | 内置 | **可扩展注册中心** |
| 学习价值 | 黑盒 | 复杂 | **Go 实现，代码简洁可读** |

MewCode 不试图取代 Claude Code —— 它是对 "AI Agent 如何在终端里工作" 这个问题的一次完整回答。每一行代码都是手写的，每一层抽象都经过推敲。如果你想理解 Agent 的底层机制，或者需要一个轻量、可控、支持自定义端点的终端 AI 助手，这就是答案。

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
| <kbd>Ctrl</kbd> + <kbd>C</kbd> | 退出 |
| `/exit` | 优雅退出 |

## ✨ 核心能力

### 🧠 多协议引擎

一份配置，多端点自由切换。`protocol` 字段决定适配层，上层代码对协议完全无感。

- **Anthropic** — 原生 Messages API，扩展思考
- **OpenAI** — Chat Completions API，兼容端点
- **自定义端点** — 设置 `base_url`，接入任意 OpenAI-compatible 服务

### 💬 真正的 TUI 体验

基于 [Bubble Tea](https://github.com/charmbracelet/bubbletea) 的全功能终端界面，而不是 `print()` + `readline()`：

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
│  Claude                            claude-opus-4-8                   │
└──────────────────────────────────────────────────────────────────────┘
```

- **流式渲染** — SSE 逐 token 打字机输出，`Imagining…(3s)` 倒计时
- **Markdown 美化** — 回复通过 Glamour 渲染，代码块高亮、表格对齐
- **工具执行可见** — Claude Code 同款 `● Tool(args)` → `⎿  结果摘要` 展示
- **Provider 选择** — 多端点配置时，启动先选供应商再进入对话

### 🔄 单轮 Agent 闭环

每次用户请求跑一个完整的 Agent 循环——不是简单的问答，而是 "思考 → 行动 → 观察 → 回答"：

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
   │    顺序执行工具 1..N
   │     ● Tool(args) → ⎿ 结果
   │           │
   │    结果回灌进对话历史
   │           │
   ▼           ▼
请求#2 ──→ 模型基于工具结果续答 ──→ 最终文本 ──→ 渲染到屏幕
```

与 Claude Code 的差异：当前实现是 **单轮闭环**（一次请求最多一轮工具调用），适合单文件编辑、代码搜索、命令执行等任务。

## 🔧 工具系统

MewCode 不是聊天机器人——它能**动手干活**。工具系统是这一切的核心。

### 设计原则

- **协议无关** — 工具定义与 Anthropic/OpenAI 的 tool schema 解耦，注册中心统一导出
- **失败不崩溃** — 所有错误被包装成结构化结果回灌给模型，让它自己调整，不中断会话
- **可扩展** — 实现 `Tool` 接口 + `Register` 即可新增工具

### 接口

```go
type Tool interface {
    Name() string                // 模型看到的工具名
    Description() string         // 给模型看的用途说明
    Parameters() map[string]any  // JSON Schema 参数定义
    Execute(ctx context.Context, args json.RawMessage) Result
}
```

### 内置工具

| 工具 | 签名 | 能力 |
|------|------|------|
| `read_file` | `read_file(path)` | 读取文件，自动带行号；文件不存在返回结构化错误 |
| `write_file` | `write_file(path, content)` | 覆盖写入，自动创建父目录 |
| `edit_file` | `edit_file(path, old_string, new_string)` | **精确字符串替换**——唯一匹配才执行，0 或 >1 匹配均报错，让模型自己修正 |
| `bash` | `bash(command)` | 执行 Shell 命令，30s 超时，返回 stdout/stderr/exit code |
| `glob` | `glob(pattern)` | 按 glob 模式匹配文件路径 |
| `grep` | `grep(pattern, path?)` | 在代码中搜索文本，返回 `文件:行:内容` |

> `edit_file` 的设计参考了 Claude Code —— 不按行号（容易漂移），而是对原文片段做唯一匹配替换。这是 AI 编程助手的核心操作原语。

### 安全边界

- **`bash` 无沙箱** — 当前命令直接在你本机 Shell 中运行。MewCode 信任模型判断，建议在可感知的目录下使用。
- **文件操作限定工作目录** — 所有路径相对于启动目录解析。
- 未来规划：权限确认层、危险命令拦截。

## 📐 架构

```
mewcode/
│
├── cmd/mewcode/main.go          ── 入口：加载配置 → 初始化注册中心 → 启动 TUI
│
├── internal/
│   │
│   ├── config/                  ── YAML 配置加载与校验
│   │   └── 字段：name, protocol, base_url, api_key, model, thinking
│   │
│   ├── llm/                     ── 协议抽象层
│   │   ├── provider.go          ── Provider 接口 + Message / StreamEvent 类型
│   │   ├── anthropic.go         ── Anthropic Messages API 适配器
│   │   └── openai.go            ── OpenAI Chat Completions 适配器
│   │
│   ├── conversation/            ── 对话历史管理器
│   │   └── 维护 user / assistant / tool 多轮消息列表
│   │
│   ├── agent/                   ── 单轮闭环编排引擎
│   │   └── Run(ctx, conv) → <-chan Event
│   │       ├── 请求#1：收集 preamble + ToolCalls
│   │       ├── 顺序执行工具，发 ToolEvent
│   │       └── 请求#2：回灌结果，收集最终文本
│   │
│   ├── tool/                    ── 工具抽象 + 注册中心 + 六个核心工具
│   │   ├── tool.go              ── Tool 接口 + Result 类型
│   │   ├── registry.go          ── 注册中心（Register / Get / Definitions / Execute）
│   │   ├── read_file.go         ── 读文件（带行号）
│   │   ├── write_file.go        ── 写文件（自动 mkdir）
│   │   ├── edit_file.go         ── 字符串匹配替换
│   │   ├── bash.go              ── Shell 执行（30s timeout）
│   │   ├── glob.go              ── Glob 文件匹配
│   │   └── grep.go              ── 代码文本搜索
│   │
│   ├── tui/                     ── Bubble Tea 终端界面
│   │   ├── tui.go               ── Model / Update / 状态机
│   │   ├── view.go              ── 视图渲染：用户块、助手块、工具行、状态栏
│   │   ├── stream.go            ── agent Event → bubbletea Msg 桥接
│   │   └── select.go            ── 多 Provider 选择列表
│   │
│   └── prompt/                  ── System Prompt + ASCII Logo Banner
│
├── .mewcode/
│   ├── config.yaml              ── 你的密钥配置（已 .gitignore）
│   └── config.yaml.example      ── 配置模板
│
└── go.mod                       ── Go 1.26 / Bubble Tea v2 / Anthropic SDK / OpenAI SDK
```

### 数据流

```
用户键盘输入
     │
     ▼
 TUI (Bubble Tea) ──→ Agent.Run(ctx, conv)
                           │
                           ├─→ Provider.Stream(ctx, msgs, tools) ──→ LLM API
                           │       │
                           │       ▼ SSE 事件流
                           │       │
                           │   Text 增量 ──→ TUI 渲染打字机输出
                           │   ToolCalls ──→ Registry.Execute() ──→ Shell/FS
                           │       │                    │
                           │       │              工具结果回灌
                           │       │                    │
                           │       ▼                    ▼
                           │   Provider.Stream() #2 ──→ 最终文本 ──→ TUI Markdown 渲染
                           │
                           ▼
                      Event 流 → chan → bubbletea Msg → Update → View
```

## ⚙️ 配置参考

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

## 🧪 开发

### Spec 驱动工作流

每个功能/章节遵循 Spec 驱动开发：

```
prompt.md ──→ spec.md ──→ plan.md ──→ task.md ──→ checklist.md
                                                          │
                                                   代码实现 + 验收
```

所有设计文档位于 [`docs/`](docs/) 目录。

### 运行测试

```bash
# 单元测试
go test ./...

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
            "input": map[string]any{
                "type":        "string",
                "description": "输入参数说明",
            },
        },
        "required": []string{"input"},
    }
}
func (t *myTool) Execute(ctx context.Context, args json.RawMessage) tool.Result {
    // 执行逻辑；失败返回 tool.Result{IsError: true, Content: "错误原因"}
}

// 2. 注册
reg.Register(&myTool{})
```

### IDE / Editor 集成

项目根目录的 `CLAUDE.md` 配置了项目约定（中文注释、Spec 驱动、tmux E2E）。如果你也用 Claude Code 开发本项目，它已经准备就绪。

## 🗺️ 路线图

| 阶段 | 内容 | 状态 |
|------|------|------|
| ch02 | 多协议对话引擎：配置、Provider、流式 SSE、TUI、多轮上下文 | ✅ 完成 |
| ch03 | 工具系统：Tool 抽象、注册中心、六个核心工具、单轮 Agent 闭环 | ✅ 完成 |
| ch04 | 会话持久化：保存/恢复对话历史 | 🚧 规划中 |
| ch05 | 多轮工具循环：模型连续调用工具直到满意为止 | 📋 待排期 |
| ch06 | 权限与安全：危险操作确认、命令白名单、沙箱模式 | 📋 待排期 |
| ch07 | MCP 协议：接入 Model Context Protocol 生态 | 📋 待排期 |
| ch08 | 插件系统：用户自定义工具加载器 | 📋 待排期 |

> 路线图会随着开发推进持续更新。欢迎提 Issue 和 PR！

## 🙏 致谢

MewCode 的许多设计灵感来自：

- [Claude Code](https://claude.ai/code) — Agent 闭环、工具行 UI 风格、edit_file 匹配原语
- [Bubble Tea](https://github.com/charmbracelet/bubbletea) — 让 Go TUI 开发变成享受
- [Charm](https://charm.sh) — Lip Gloss + Glamour 让终端也能很美
- 所有用 `anthropic-sdk-go` 和 `openai-go` 构建 Agent 的先行者们

## 📄 许可

[MIT](LICENSE) © 2026 Bin-hy
