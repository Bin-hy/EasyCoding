# Skill 系统 Tasks

> 顺序执行。每完成一个任务跑 `go build ./...` 确保编译过；接入主流程的任务（T11、T12、T13、T14）做完后立刻补一次端到端验证再进下一项。

## T1: 扩展 SkillMeta 字段
- 影响文件: `internal/skills/skills.go`（修改）
- 依赖任务: 无
- 完成标准: SkillMeta 增加 `Mode string`、`Model string`、`Context` 升级为 `inline | fork`（已有），追加 `ForkContext string`（取值 `full | recent | none`）；yaml tag 全部 snake_case
- 备注: 旧 `Context` 字段值 `fork` 等同于 `Mode == "fork"`，做兼容转换

## T2: 拆分 parser 子模块
- 影响文件: `internal/skills/parser.go`（新建）、`internal/skills/skills.go`（修改）
- 依赖任务: T1
- 完成标准: 把 `loadSkill` / `parseSkillMD` 移到 parser.go；新增 `parseFrontmatterOnly(path) (SkillMeta, error)` 不读 body 的轻量解析（阶段 1 加载用）；新增 `loadSkillBody(skill *Skill) error` 强制重读 body
- 备注: parser.go 不依赖 catalog，纯函数

## T3: Catalog 改造为两阶段加载
- 影响文件: `internal/skills/catalog.go`（新建，从 skills.go 抽出）、`internal/skills/skills.go`（修改）
- 依赖任务: T2
- 完成标准: `Catalog` 在阶段 1 只装 frontmatter，每个 Skill 的 PromptBody 默认空；新增 `Catalog.GetFull(name) (*Skill, error)` 触发 loadSkillBody（含热重载逻辑：每次都重读，失败回退缓存）；保留 `Get(name) *Skill` 返回轻量版本
- 备注: `LoadFromDirectory` / `LoadSkills` 也要适配两阶段

## T6: Agent ActiveSkills 字段与方法
- 影响文件: `internal/agent/agent.go`（修改）
- 依赖任务: 无（与 T1-T5 并行可做）
- 完成标准: Agent 新增 `ActiveSkills map[string]string`（name → body）；方法 `ActivateSkill(name, body string)`、`ClearActiveSkills`、`GetActiveSkills map[string]string`；Run 主循环每轮迭代开头（在 NotificationFn 注入之后），如 ActiveSkills 非空则拼成一段 system-reminder 注入 conv（标题用 `# Active Skills`）

## T7: Executor.RunInline
- 影响文件: `internal/skills/executor.go`（新建）
- 依赖任务: T3, T6
- 完成标准: `RunInline(ctx, skill, args, agentRef SkillHost) (string, error)`：调用 `skill.Render(args)` 渲染 body → `host.ActivateSkill(skill.Meta.Name, body)` → 对 allowed_tools 做 fail-fast 校验（缺工具立即返回 error）→ 把工具白名单设置到 host.SetToolFilter（封装 Agent.ToolNameFilter）→ 返回 rendered body（作为 user message 走主 loop）
- 备注: 新增 interface `SkillHost { ActivateSkill / SetToolFilter / GetTool(name) }` 实现在 Agent 上，避免 skills 包 import agent

## T8: Executor.RunFork
- 影响文件: `internal/skills/executor.go`（继续）
- 依赖任务: T7
- 完成标准: `RunFork(ctx, skill, args, host SkillForkHost) (summary string, err error)`：
 - 创建新 `conversation.Manager`
 - 按 `skill.Meta.ForkContext` 装填初始历史：`full` 取主对话 last N 条做 LLM 摘要 / `recent` 拷最近 5 条 / `none` 空
 - 把 rendered body 作为 first user message
 - 通过 `SkillForkHost.RunSubAgent(conv, allowedTools) (finalText string, err error)` 跑临时 Agent（实现在 TUI 层注入，复用 agent.Agent.Run + 收集 LoopComplete 文本）
 - 返回 finalText

## T9: LoadSkillTool（系统工具）
- 影响文件: `internal/tools/load_skill.go`（新建）
- 依赖任务: T3, T6
- 完成标准: `LoadSkillTool` 实现 tools.Tool，Name = `LoadSkill`，Category = read；持有 `*skills.Catalog` + `SkillHost`；Execute：catalog.GetFull → host.ActivateSkill → 返回 `"Skill <name> activated. SOP pinned to env."`；标记 SystemTool 接口让 ToolNameFilter 始终放行
- 备注: tools.Tool 接口增加可选 `SystemTool bool` 检测；Agent 的 ToolNameFilter 应用时绕过 system tool

## T10: 系统工具豁免逻辑
- 影响文件: `internal/tools/tool.go`（修改）、`internal/agent/agent.go`（修改）
- 依赖任务: T9
- 完成标准: 新增 `SystemTool` 接口（可选实现），Agent.applyToolFilter 在调 ToolNameFilter 前先 check 是否系统工具；GetAllSchemas 也要保留系统工具
- 备注: LoadSkillTool 与未来其他系统工具的统一通道

## T11: 接入 TUI —— skill 列表与命令注册
- 影响文件: `internal/tui/tui.go`（修改）、`internal/prompt/builder.go`（保留 SkillSection 通道）
- 依赖任务: T3, T7, T8, T9
- 完成标准:
 - `loadSkillsAndBuildPrompt` 调用新 `skills.LoadCatalog`（两级路径扫描），catalog 存到 m.skillCatalog
 - system prompt SkillSection 改成「Available Skills (call LoadSkill to activate)\n- /<name>: <description>\n...」+ LoadSkill 使用说明
 - 每个 skill 注册命令：inline 走 TypePrompt（handler 调 Executor.RunInline），fork 走新增 TypeSkillFork（handler 直接调 Executor.RunFork 并把返回值作为 assistant 消息插入对话）
 - 注册 LoadSkillTool 到 m.registry：`m.registry.Register(&tools.LoadSkillTool{Catalog: catalog, Host: m.ag})`

## T12: 接入 TUI —— /skill 管理命令与 /clear 集成
- 影响文件: `internal/commands/commands.go`（修改）、`internal/tui/tui.go`（修改）
- 依赖任务: T11
- 完成标准:
 - 新增 `/skill` 命令：`/skill list` → ctx.SkillCatalog.List 含来源；`/skill info <name>` → 全 frontmatter + path；`/skill reload` → catalog.Reload(workDir) + 重新注册命令
 - `/clear` handler 增加 `if m.ag != nil { m.ag.ClearActiveSkills }`

## T13: 新增 TypeSkillFork 命令类型
- 影响文件: `internal/commands/commands.go`（修改）、`internal/tui/tui.go`（修改）
- 依赖任务: T8, T11
- 完成标准: `TypeSkillFork CommandType = "skill-fork"`；executeCommand 增加 case：调 handler 后把返回的 summary 作为 chatMessage（role=assistant）插入；不触发主 Agent loop

## T14: 接入主流程 —— Agent 注入 SkillHost
- 影响文件: `internal/agent/agent.go`（修改）、`internal/tui/tui.go`（修改）
- 依赖任务: T6, T7, T8
- 完成标准: Agent 实现 SkillHost 接口（ActivateSkill / ClearActiveSkills / SetToolFilter）；TUI 把 m.ag 强转为 skills.SkillHost 传给 Executor 与 LoadSkillTool；fork 路径需要 SkillForkHost.RunSubAgent，由 TUI 提供一个本地实现（开 streaming executor 跑到 LoopComplete 收集最终 assistant 文本）

## T14b: InstallSkillTool（远程安装）
- 影响文件: `internal/skills/install.go`（新建）、`internal/skills/install_tool.go`（新建）、`internal/skills/install_test.go`（新建）、`internal/tui/tui.go`（修改）
- 依赖任务: T3（Catalog.Reload）、T11（registerSkillCommand 抽出）
- 完成标准:
 - `ParseSkillURL(url) (*SkillSource, error)` 支持 skills.sh / github.com tree / raw.githubusercontent.com 三种 URL，拒绝其他 host
 - `Install(src, installRoot) (*InstallReport, error)` 走 GitHub Contents API 递归下载到 staging temp dir，验证含 `SKILL.md` 或 `skill.yaml` 后 atomic rename
 - 限额：单文件 ≤1 MiB、总大小 ≤8 MiB、文件数 ≤64、深度 ≤4
 - `InstallSkillTool` 实现 `tools.Tool`，Name = `InstallSkill`，Category = write；执行后调 `Catalog.Reload` + `OnInstalled(name)` 回调
 - TUI `wireSkillsToAgent` 把 `registerSkillCommand` 抽成可单独调用的方法，作为 OnInstalled 回调
 - SkillSection 文本告知模型「用户给 URL 要求装 skill 时调 InstallSkill」
- 备注: 不依赖本地 `git` 二进制；rate limit 命中（403）时把 GitHub 的错误文本透出给用户

## T15: 单元测试
- 影响文件: `internal/skills/skills_test.go`（修改）、`internal/skills/executor_test.go`（新建）、`internal/tools/load_skill_test.go`（新建）、`internal/agent/agent_test.go`（修改）
- 依赖任务: T1-T14
- 完成标准: 覆盖
 - parser 两阶段：阶段 1 不读 body / 阶段 2 重读热更新
 - 两级覆盖：项目级覆盖用户级
 - Executor.RunInline 钉 SOP + 工具过滤 fail-fast
 - Executor.RunFork 隔离 conv + context: full/recent/none 三档
 - LoadSkillTool 端到端：activate + 简短返回
 - Agent.ActivateSkill / ClearActiveSkills / env 注入
 - 系统工具豁免：ToolNameFilter 设了 LoadSkill 也还在 schema 里
 - /skill list / info / reload 行为
 - /clear 触发 ClearActiveSkills

## T16: 端到端验证
- 影响文件: 无（仅运行验证）
- 依赖任务: T15
- 完成标准:
 - `go build ./...` 通过
 - `go test ./...` 全过
 - 在仓库根目录 TUI 实操：
 1. 创建测试 skill 目录 `.mewcode/skills/test-skill/SKILL.md`（简单 inline SOP），启动后 `/help` 看到 `/test-skill [skill]`
 2. `/skill list` 看到 test-skill 与来源
 3. `/skill info test-skill` 看到完整 frontmatter
 4. `/test-skill` 触发 inline 执行，看到 SOP 注入
 5. 修改 `.mewcode/skills/test-skill/SKILL.md`，不重启 TUI，再次 `/test-skill` 验证热重载生效
 6. 自然语言触发 LoadSkill("test-skill")
 7. `/clear` 后 env-reminder 不再出现旧 SOP
 - 截图或日志留证

## 进度
- [x] T1
- [x] T2
- [x] T3
- [x] T6
- [x] T7
- [x] T8
- [x] T9
- [x] T10
- [x] T11
- [x] T12
- [x] T13
- [x] T14
- [x] T14b
- [x] T15
- [x] T16