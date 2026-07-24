# Skill 系统 Plan

## 架构概览

新增一个 `internal/skills` 包承载所有 Skill 相关的"数据 + 加载 + 执行 + 激活态"逻辑，与现有 `internal/command`、`internal/tool`、`internal/prompt`、`internal/agent` 通过细窄接口交互。

按职责拆解：

- **internal/skills**：核心包。包含数据结构（`Skill`、`SkillMeta`、`ActiveEntry`）、`SKILL.md` 解析、Catalog 两级路径扫描与覆盖、Skill 执行器（inline / fork 分支）、`ActiveSkills` 跨轮列表、`$ARGUMENTS` 渲染、InstallSkill zip 解压（zip-slip 防护）
- **internal/tool/load_skill.go**：新增 LoadSkill 工具实现。是系统工具，永远可见，受不带权限拦截
- **internal/tool/install_skill.go**：新增 InstallSkill 工具实现。普通工具，受权限模式约束
- **internal/tool/registry.go**：扩展—增加"系统工具"标记与 `FilterByAllowed(allowed []string)` 切片导出能力
- **internal/command**：扩展—`RegisterSkillsAsCommands(reg, catalog, executor)` 把 Catalog 中每个 Skill 注册为 KindPrompt 命令；新增 `/skill` 命令（KindLocal，列出 Catalog）；UI 接口扩展 `ListCatalogSkills / ListActiveSkills / ClearActiveSkills`
- **internal/prompt**：扩展—`OptionalModules` 中现有的"active-skills"槽位重命名为"skills-catalog"，承载第一阶段名字+描述列表；新增 `RenderActiveSkillsBlock(entries) string` 函数供 env context 拼装
- **internal/agent**：扩展—`SessionRuntime` 新增 `ActiveSkills *skills.ActiveSkills` 字段；`Agent` 新增 `WithCatalog` / `WithSkillExecutor` 选项；`Run` 每轮重建 `sys` 时把 Catalog 列表传入 `BuildSystemPrompt`、`envText` 拼接时调用 `RenderActiveSkillsBlock`；新增 `ClearActiveSkills() / ActivateSkill / ListActive` 入口供 UI 与工具调用
- **internal/tui**：扩展—Model 持有 catalog 引用与执行器；`handleClear` 路径在 `ClearAndNewSession` 后调 `ActiveSkills.Clear`；UI 接口对应新增方法实现

## 核心数据结构

### SkillMeta

```go
package skills

type SkillMeta struct {
    Name         string   `yaml:"name"`
    Description  string   `yaml:"description"`
    AllowedTools []string `yaml:"allowed_tools,omitempty"`
    Mode         string   `yaml:"mode,omitempty"`         // "inline" / "fork"
    ForkContext  string   `yaml:"fork_context,omitempty"` // "none" / "recent" / "full"
    Model        string   `yaml:"model,omitempty"`
}
```

约定：`Mode` 为空或 "inline" 视作 inline；`Mode == "fork"` 视作 fork；其它值打 warning 后按 inline 处理。`ForkContext` 仅 fork 时生效，缺省 "none"。

### Skill

```go
type Skill struct {
    Meta       SkillMeta
    PromptBody string      // SKILL.md 去 frontmatter 后的正文（启动时缓存，执行时重读覆盖）
    SourceDir  string      // 绝对路径，重读 SKILL.md 时用
    Source     SkillSource // User / Project
}

type SkillSource int

const (
    SourceUser SkillSource = iota
    SourceProject
)

func (s SkillSource) String() string // "user" / "project"
```

### Catalog

```go
type Catalog struct {
    mu     sync.RWMutex
    byName map[string]*Skill
    order  []string // 按 name 排序的稳定迭代序
}

func LoadCatalog(workDir string) *Catalog
func (c *Catalog) Reload(workDir string)            // 内部锁保护，原子替换
func (c *Catalog) Get(name string) (*Skill, bool)
func (c *Catalog) List() []*Skill                   // 按 order
func (c *Catalog) Names() []string
func (c *Catalog) ValidateTools(reg *tool.Registry) []ValidationIssue // fail-fast 检查
```

`LoadCatalog` 按顺序扫描：
1. `~/.mewcode/skills/*` 子目录（`source=user`）
2. `<workDir>/.mewcode/skills/*` 子目录（`source=project`）

后扫到的同名 `name` 覆盖前者。

### ActiveSkills

```go
type ActiveEntry struct {
    Name string
    Body string // 激活那一刻磁盘上的 SKILL.md 正文
}

type ActiveSkills struct {
    mu      sync.Mutex
    entries []ActiveEntry // 保持激活顺序
    names   map[string]int // 重复激活的话覆盖原位置内容
}

func (a *ActiveSkills) Activate(name, body string)
func (a *ActiveSkills) Clear()
func (a *ActiveSkills) Snapshot() []ActiveEntry // 拷贝出当前列表（env 装配用）
```

### Executor

```go
type Executor struct {
    catalog  *Catalog
    runtime  *agent.SessionRuntime // 持有 ActiveSkills 等跨轮状态
    registry *tool.Registry
    provider llm.Provider          // 默认 provider；fork 时可用 Skill.Model 切换
    eng      *permission.Engine
    version  string
}

func NewExecutor(...) *Executor

// 入口：被 Slash 命令 handler 调用
func (e *Executor) Execute(ctx context.Context, ui command.UI, name, args string) error

// inline 路径直接通过 ui.InjectAndSend
// fork 路径起子 Agent 跑完后通过 ui.AppendAssistantNotice 写回主对话
```

## 模块设计

### internal/skills/parser.go
**职责**：解析单个 Skill 目录 → `*Skill`
**对外接口**：`func parseSkillDir(dir string, source SkillSource) (*Skill, error)`
**依赖**：`gopkg.in/yaml.v3`（已在 go.mod 中）

解析流程：
1. 读 `<dir>/SKILL.md`，分离 frontmatter（两行 `---` 之间）与 body
2. yaml.Unmarshal frontmatter → SkillMeta；校验 name 合法性、mode / fork_context 取值
4. 组装 Skill 返回

### internal/skills/catalog.go
**职责**：两级路径扫描与覆盖管理
**对外接口**：`LoadCatalog / Reload / Get / List / Names / ValidateTools`
**依赖**：`internal/skills/parser`

`ValidateTools`：遍历 Catalog 中所有 Skill 的 `Meta.AllowedTools`，确认每个名字都能在传入的 `*tool.Registry` 里 Get 到；记录所有不通过项返回。

### internal/skills/render.go
**职责**：把 Skill body 渲染为最终注入文本（inline 和 fork 路径都先经过这一层）
**对外接口**：`RenderBody(skill *Skill, args string) string`

逻辑：
- 替换所有 `$ARGUMENTS` 出现
- 若无占位符且 args 非空，在末尾追加 `\n\n## User Request\n\n<args>`
- 若 `AllowedTools` 非空，在 body 顶部插一段 ```\nThis skill is designed to use only these tools: <list>. Prefer them over other tools when possible.\n\n---\n\n```

### internal/skills/executor.go
**职责**：inline / fork 分发与执行
**对外接口**：`NewExecutor / Execute`

inline 分支：
1. 从 Catalog 取 Skill
2. 从磁盘重读 `SKILL.md`（失败回退缓存）
3. RenderBody
4. `ui.InjectAndSend(displayLabel, body)` —— displayLabel 例如 `/<name>`

fork 分支：
1. 从 Catalog 取 Skill
2. 从磁盘重读 `SKILL.md`
3. RenderBody
4. 按 `ForkContext` 构造初始 Conversation：
   - none：仅一条 user 消息（renderedBody）
   - recent：从主 conversation 拷最近 5 条原始消息，再追加 renderedBody
   - full：先用 `compact.SummarizeForFork(ctx, mainConv)`（基于 ch09 现成的摘要管道）产出摘要文本，作为一条 system 或 user 消息插入，再追加 renderedBody
5. 选 provider：默认主 provider；`Skill.Model` 非空时调 `llm.NewProvider(skillModel)` 重新构造
6. 构造子 Agent：复用 `agent.New(provider, registry, version, eng, agent.WithRuntime(forkRuntime))`，子 runtime 是独立 NewSessionRuntime
7. 子 Agent.Run → 消费 channel 直到 Done；累计 token 用量
8. 把累计 token 写回主 runtime 的 anchor（usage += sub）
9. 取子对话的最后一条 assistant 文本作为 finalText
10. `ui.AppendAssistantMessage(finalText)`（新增 UI 方法）—— 主对话历史新增一条 assistant 消息

任一步骤出错：返回 finalText = `[skill <name> failed: <reason>]`，仍以 assistant 消息写入主对话。

### internal/skills/install.go
**职责**：InstallSkill 的核心逻辑——下载 zip、校验路径、解压到 ~/.mewcode/skills/
**对外接口**：`InstallFromURL(ctx context.Context, source string, catalog *Catalog) (skillName string, err error)`

流程：
1. 通过 net/http 下载 source 到临时文件（限时 60s、限大小 50MB）
2. 用 archive/zip 打开
3. 严格校验：所有路径必须以 `<topDir>/` 起头、`<topDir>` 满足 F3 命名、内部不含 `..`、不含绝对路径、不含符号链接
4. 解压到 `~/.mewcode/skills/<topDir>/`
5. 调用 `catalog.Reload(workDir)` 触发热重载
6. 返回 `<topDir>` 作为 skillName

### internal/tool/load_skill.go
**职责**：LoadSkill 工具实现
**对外接口**：实现 `tool.Tool` 接口

```go
type LoadSkillTool struct {
    catalog  *skills.Catalog
    active   *skills.ActiveSkills
    registry *tool.Registry
}

// Name/Description/Parameters/ReadOnly/IsSystem/Execute
```

`IsSystem() bool { return true }`——新加在 Tool 接口（或者通过 type assertion 探测）。`Execute` 流程：
1. 解析 args.name
2. catalog.Get(name) → 不存在返回 `unknown skill: <name>`
3. 重读 SKILL.md 获取最新 body
4. `active.Activate(name, body)`
6. 返回 `Skill <name> activated. SOP pinned to env context.`

### internal/tool/install_skill.go
**职责**：InstallSkill 工具实现
**对外接口**：实现 `tool.Tool`

```go
type InstallSkillTool struct {
    catalog *skills.Catalog
    workDir string
}
```

`ReadOnly() bool { return false }`（写盘 + 网络），`IsSystem() bool { return false }`。Execute 直接调 `skills.InstallFromURL`，返回成功消息或错误。

### internal/tool/registry.go
**修改**：
- Tool 接口新增 `IsSystem() bool` 方法（默认 false）；现有 6 个工具与 MCP 工具默认实现返回 false
- LoadSkill 工具 IsSystem 返回 true
- 新增 `Registry.SystemDefinitions() []llm.ToolDefinition`（仅返回系统工具）
- 新增 `Registry.DefinitionsFiltered(allowed []string) []llm.ToolDefinition`（按白名单 + 系统工具豁免过滤）

注：本期不在主 agent loop 里用 `DefinitionsFiltered` 改主对话工具集——按 spec F27 决议，inline 模式不真过滤。但 fork 模式子 Agent 用该方法构造工具集。

### internal/prompt/modules.go
**修改**：
- `OptionalModules(instructions, memory string)` 改为 `OptionalModules(instructions, memory, skillsCatalog string)`
- 原 priority 90 槽位由 "active-skills" 重命名为 "skills-catalog"，内容由调用方传入
- 增加常量 `prioSkillsCatalog = 90`，删除 `prioActiveSkills`

### internal/prompt/prompt.go
**修改**：
- `BuildSystemPrompt(instructions, memory string)` 改为 `BuildSystemPrompt(instructions, memory, skillsCatalog string)`
- 增加 `RenderActiveSkillsBlock(entries []skills.ActiveEntry) string`，输出形如：
  ```
  ## Active Skills

  ### Skill: commit

  <body>

  ### Skill: review

  <body>
  ```
  entries 空时返回空字符串
- 增加 `RenderSkillsCatalog(items []SkillCatalogItem) string`，输出 skills-catalog 模块内容；items 空时返回空字符串

为避免 prompt 包反向依赖 skills 包，新增类型：
```go
type SkillCatalogItem struct {
    Name        string
    Description string
}
type ActiveSkillEntry struct {
    Name string
    Body string
}
```

skills.Catalog 和 skills.ActiveSkills 提供两个适配方法 `ToPromptItems()` / `ToPromptEntries()` 把内部类型转换到 prompt 包的类型上。

### internal/agent/runtime.go
**修改**：
- `SessionRuntime` 新增字段 `ActiveSkills *skills.ActiveSkills`
- `NewSessionRuntime` 初始化空 `ActiveSkills`
- `ResetForNewSession` 同时 `r.ActiveSkills.Clear()`

### internal/agent/agent.go
**修改**：
- 新增 `WithCatalog(c *skills.Catalog) Option`：注入 catalog 引用（用于第一阶段列表与 ClearActiveSkills 入口）
- 新增 `Agent.ActivateSkill(name, body)` / `ClearActiveSkills()` 方法，转发到 `runtime.ActiveSkills`
- Run 内每轮重建 sys 时：
  ```go
  sys := prompt.BuildSystemPrompt(a.instructionText, a.memoryText,
      prompt.RenderSkillsCatalog(a.catalog.ToPromptItems()))
  envText := prompt.GatherEnvironment(...).Render() + "\n\n" +
      prompt.RenderActiveSkillsBlock(a.runtime.ActiveSkills.ToPromptEntries())
  ```
  （`a.catalog` 为 nil 时跳过；进度提示放在 sub-tasks）

### internal/command/registry.go + skills.go (新建)
**职责**：把 Catalog 注册为 KindPrompt 命令；新增 /skill 命令；UI 接口扩展
**对外接口**：
- `RegisterSkillsAsCommands(reg *Registry, catalog *skills.Catalog, exec *skills.Executor)`
- 提供给 reload 路径调用的 `RemoveSkillCommands(reg *Registry)`
- 新增内置 `/skill` 命令（KindLocal）

reg.Register 时给每个 Skill 添加 Hidden=false 的 Command；命令的 Handler 闭包捕获 skill.Name 与 executor，调用 `exec.Execute(ctx, ui, name, "")`（本期不支持参数；future 在 dispatcher 加参数后填）。

注：当前 ch10 的 Slash dispatch 是零参数，Skill 显式调用本期也走零参数。`$ARGUMENTS` 替换仅在 LoadSkill + 后续 user message 的隐式场景下被替换为空——这是合理的简化（参数交互通过 Skill 后续轮次的对话进行）。

为了支持 Reload 时清理旧命令，Registry 新增 `RemoveAll(filter func(*Command) bool)` 或 `RemoveSkillCommands()` 入口。

### internal/command/ui.go
**修改**：
- UI 接口新增方法：
  - `ListCatalogSkills() []SkillSummary`（每条含 name/description/source/mode）
  - `ListActiveSkills() []string`
  - `ClearActiveSkills()`
  - `AppendAssistantMessage(text string)`（fork 路径用，把子 Agent 的 finalText 写入主对话历史）
- NopUI 提供零值实现

### internal/command/builtins.go
**修改**：
- 修改 `handleClear`：在调 `ui.ClearAndNewSession()` 后追加 `ui.ClearActiveSkills()`
- 新增 `Name: "skill"`、KindLocal、Handler = handleSkill 的注册块

### internal/tui/*
**修改**：
- Model 持有 `*skills.Catalog`、`*skills.Executor`
- 实现新增的 UI 方法：`ListCatalogSkills` / `ListActiveSkills` / `ClearActiveSkills` / `AppendAssistantMessage`
- `tui.New` 接受新参数并接入

### cmd/mewcode/main.go
**修改**：
- 启动时构造 `*skills.Catalog`、`*skills.ActiveSkills` 并注入到 SessionRuntime
- 注册 LoadSkill / InstallSkill 内置工具
- 在工具注册完成后调 `catalog.ValidateTools(registry)`；对每条 issue 打 warning 并把该 Skill 从 Catalog 中移除（保留其它）
- 调 `command.RegisterSkillsAsCommands` 完成自动注册
- 把 catalog/executor 传给 tui

## 模块交互

### 启动期

```
main:
  ├─ tool.NewDefaultRegistry()
  ├─ mcp.AttachServers(registry)              // 已有
  ├─ skills.LoadCatalog(workDir)              // 两级路径扫描
  ├─ tool.Register(LoadSkillTool)             // 系统工具
  ├─ tool.Register(InstallSkillTool)
  ├─ catalog.ValidateTools(registry)          // fail-fast 检查
  │     不通过项 → 打 warning + 从 catalog 移除
  ├─ skills.NewExecutor(catalog, registry, ...)
  ├─ command.RegisterBuiltins(cmdReg)         // ch10 内置命令
  ├─ command.RegisterSkillsAsCommands(cmdReg, catalog, executor)
  ├─ command.RegisterSkillCmd(cmdReg)         // /skill (新)
  └─ tui.New(... catalog, executor, ...)
```

### Skill 显式调用（/commit）

```
user → submit → command.Dispatch(/commit)
       → handler 调 executor.Execute(ctx, ui, "commit", "")
                 ├ inline: render → ui.InjectAndSend → agent.Run 注入主对话
                 └ fork: render → 子 Agent.Run → finalText → ui.AppendAssistantMessage
```

### Skill 意图触发（自然语言）

```
user 输入"帮我提交一下" → agent.Run loop
   └ streamOnce 拿到 LLM 调 LoadSkill({name:"commit"})
        → tool.Execute → LoadSkillTool.Execute
              ├ catalog.Get → 重读 SKILL.md
              ├ active.Activate("commit", body)
              └ 返回 tool_result
   下一轮迭代:
        sys = BuildSystemPrompt(...catalog清单不变)
        envText = ... + RenderActiveSkillsBlock(["commit" -> body])
        ↑ Agent 现在看得到完整 SOP
```

### /clear

```
/clear handler → ui.ClearAndNewSession() (ch10) → ui.ClearActiveSkills()
                                                       └ runtime.ActiveSkills.Clear()
下轮 envText 中 active-skills 块为空字符串
```

### Reload (InstallSkill 后或者未来 /skill reload)

```
InstallSkill.Execute → skills.InstallFromURL
   └ 解压完毕 → catalog.Reload(workDir)
                ├ 重新扫描两级路径
                ├ 通过 mu 锁原子替换 byName / order
                └ command 端不会立刻感知—但 dispatcher 每轮按命令名查找 reg，
                   Reload 完成后下次 /<name> 即可命中新 Skill。然而启动时已注册的
                   旧命令仍在 registry 中。为简化，提供下面策略：
```

进一步：`catalog.Reload` 返回 (added, removed []string)，InstallSkill 工具拿到结果后调 cmdReg `RemoveSkillCommands` + `RegisterSkillsAsCommands`，确保 /help 和补全菜单立即同步。

### Fork 模式

```
executor.Execute (fork) →
   ┌──────────────────── 子 Agent ────────────────────┐
   │ 新 Conversation 按 fork_context 初始化            │
   │ Agent.New(provider, registry, version, eng,       │
   │           WithRuntime(forkRuntime))               │
   │ run.Run(ctx, conv, defaultMode)                   │
   │ 累计 token, 取末尾 assistant text                  │
   └───────────────────────────────────────────────────┘
   将 finalText 作为一条 assistant 消息插入主 conv
```

注：fork 模式下子 Agent 的 registry 是用 `Registry.DefinitionsFiltered(allowed)` 构造的临时 registry（共享底层 Tool 实例），系统工具豁免列入。

## 文件组织

```
mewcode/
├── cmd/mewcode/main.go                # 接线：构造 catalog / executor / 注册工具与命令
├── internal/
│   ├── skills/                        # 新包
│   │   ├── types.go                   # SkillMeta / Skill / SkillSource / ActiveEntry
│   │   ├── parser.go                  # parseSkillDir, parseSkillMD
│   │   ├── catalog.go                 # Catalog: LoadCatalog / Reload / Get / List / Names / ValidateTools
│   │   ├── active.go                  # ActiveSkills
│   │   ├── render.go                  # RenderBody, $ARGUMENTS 替换, allowed_tools 顶部提示
│   │   ├── executor.go                # Executor.Execute (inline / fork)
│   │   ├── install.go                 # InstallFromURL（zip 下载与 zip-slip 防护）
│   │   └── adapter.go                 # ToPromptItems / ToPromptEntries 桥接到 prompt 包
│   ├── tool/
│   │   ├── registry.go                # 修改：IsSystem 标记 + DefinitionsFiltered
│   │   ├── load_skill.go              # 新：LoadSkill 工具
│   │   ├── install_skill.go           # 新：InstallSkill 工具
│   ├── command/
│   │   ├── builtins.go                # 修改：改 handleClear、加 /skill
│   │   ├── builtin_skill.go           # 新：handleSkill (KindLocal 列表)
│   │   ├── skills.go                  # 新：RegisterSkillsAsCommands / RemoveSkillCommands
│   │   └── ui.go                      # 修改：新增 4 个 UI 方法 + NopUI 兜底
│   ├── prompt/
│   │   ├── modules.go                 # 修改：active-skills → skills-catalog
│   │   ├── prompt.go                  # 修改：BuildSystemPrompt 增 catalog 参数
│   │   ├── skills_block.go            # 新：RenderActiveSkillsBlock / RenderSkillsCatalog / 类型
│   │   └── environment.go             # 不动
│   ├── agent/
│   │   ├── runtime.go                 # 修改：SessionRuntime.ActiveSkills 字段
│   │   ├── agent.go                   # 修改：WithCatalog / Run 内构造 sys 与 env 拼接
│   │   └── runtime_test.go            # 修改（如需）
│   └── tui/
│       ├── tui.go                     # 修改：持有 catalog/executor + 实现新 UI 方法
│       └── ...
└── docs/ch11/
    ├── spec.md
    ├── plan.md
    ├── task.md
    └── checklist.md
```

## 技术决策

| 决策点 | 选择 | 理由 |
|--------|------|------|
| 数据格式 | 仅 SKILL.md（frontmatter+body） | 与 README 一致；解析路径单一；不引入 yaml/md 分离的认知负担 |
| Skill 形态 | 必须是目录 | 与 references 自然契合；将来扩展空间大 |
| 优先级覆盖 | 用户 < 项目 | 与 npm/git 习惯一致 |
| 第一阶段注入位置 | system prompt 模块（priority 90） | 享受 prompt cache 稳定前缀 |
| 第二阶段注入位置 | env context（每轮重建） | 多 Skill 同激活、嵌套场景下 SOP 永远靠前；prompt cache 失效是设计意图 |
| LoadSkill 入参 | 仅 name | 与"意图识别"语义一致；参数走后续 user message 更自然 |
| LoadSkill 权限 | read-only + 系统工具 | 没有外部副作用；为支持嵌套必须豁免 allowed_tools |
| InstallSkill 权限 | 普通工具，受权限模式约束 | 写盘+网络，必须走授权 |
| fork 模式实现 | Go 端起子 Agent | 直接复用现成 Agent.Run，不依赖将来 SubAgent 章节 |
| fork_context 默认 | none | "隔离"才是 fork 本意；需要带上下文的显式声明 |
| allowed_tools 在 inline 模式 | 仅 fail-fast + SOP 提示 | 避免 inline 期间动态切换工具集的生命周期复杂度；安全靠 ch08 权限引擎兜底 |
| Skill 与已有命令冲突 | 跳过加载 + warning | 保护内置命令的可靠性；Skill 想替换内置命令需要用户主动改源码 |
| 解析失败 | 跳过单个 Skill，warning，不阻断 | 与 instructions loader 一致的容错策略 |
| 热加载 | InstallSkill 后主动 Reload；Execute 时重读 body | 用户改 SKILL.md 下次执行立即生效；新装 Skill 不需要重启 |
| Skill 列表数据流 | adapter 桥接，prompt 包不依赖 skills 包 | 避免循环依赖 |
| UI 接口扩展 | 4 个新方法 + NopUI 全量实现 | 与 ch10 风格一致 |
| 闭包循环变量 | 显式 `name := skill.Name` 拷贝再用 | Go 1.22+ 已修，仍显式拷贝为可读性 |
| Skill 自身参数 | 本期 /<name> 仅零参数；后续轮次对话补 | 与 ch10 F7 一致，不破坏 dispatcher |