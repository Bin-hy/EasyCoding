# Skill 系统 Spec

## 1. 背景

MewCode 用户会反复输入一组类似的 prompt（commit message 规范、代码审查清单、跑测试的项目类型识别）。当前所有 prompt 要么写死在源码 Slash Command（`/review`）里，要么用户每次手敲，三个痛点：(1) 不能复用与分发，(2) 工具一多模型选错的概率指数级上升，(3) 没有任务级的工具白名单和上下文隔离。Skill 把可复用 SOP 装进可编辑的 Markdown 文件，配渐进式披露与执行模式，同时解决上述三个问题。

## 2. 目标

把 SKILL.md 升级为「带 frontmatter + 附属资源」的能力包。启动时只把 `name + description` 注入对话给 Agent 看；Agent 通过 LoadSkill 工具按需把完整 SOP 钉到环境上下文。inline 模式 SOP 在主对话内执行，fork 模式独立子 Agent 隔离执行后把结果回流。`/` 显式触发与意图识别自动触发共用同一套执行器。

## 3. 功能需求

### 解析与加载
- F1: `SkillMeta` 字段：name / description / when_to_use / tags / allowed_tools / context / mode / model；`mode` 取 `inline | fork`（默认 inline），`context` 取 `full | recent | none`（仅 fork 模式生效，默认 `none`）
- F2: 单文件 `SKILL.md`（YAML frontmatter + body）与目录型（SKILL.md + references/）两种磁盘布局
- F3: 两级搜索路径加载，优先级 `项目 .mewcode/skills/` > `~/.mewcode/skills/`，同名按优先级覆盖；解析失败单条跳过并记日志
- F4: 两阶段加载：阶段 1 启动时只解析 frontmatter（不读 body），阶段 2 由 LoadSkill 工具按需读取 body

### 执行
- F5: `Skill.Render(args)` 把 `$ARGUMENTS` 替换为参数；缺占位符且 args 非空时在末尾追加 `## User Request` 段
- F6: inline 执行：把 SOP 通过 `Agent.ActivateSkill(name, body)` 钉到 env context，下一轮 Agent Loop 起每轮重建时 SOP 都在最显眼位置；同时按 `allowed_tools` 过滤当前会话工具集
- F7: fork 执行：在独立 `conversation.Manager` 里跑临时 Agent，按 `context` 字段决定历史携带策略（full = 主对话摘要 / recent = 最近 5 条 / none = 完全隔离），子 Agent 完成后把最终 assistant 文本作为 assistant 消息回流主对话
- F8: 工具白名单：执行 skill 前过滤 `tools.Registry`，只保留 `allowed_tools` 中声明的工具与系统工具；启动加载阶段做 fail-fast 依赖检查，白名单中出现不存在的工具立刻报错
- F9: 系统工具豁免：`LoadSkill` 标记为 system tool，工具过滤时总是可见，支持 Skill 嵌套调用

### LoadSkill 工具与意图识别
- F10: `LoadSkillTool`：read-only，输入 `{name: string}`；执行两件事——调 `Agent.ActivateSkill` 钉 SOP，返回一句简短确认（不返回完整 SOP，避免 tool_result 占用空间）
- F11: 启动期 system prompt 含「可用 Skill 列表」段（只 name + description + LoadSkill 调用指引），通过 prompt builder 的 `SkillSection` 通道注入

### 命令集成
- F12: 每个 skill 自动注册为 `/` 短命令，描述末尾标注 `[skill]`；inline skill 走 TypePrompt 路径，fork skill 走新增的 TypeSkillFork 路径
- F13: `/skill list | info  | reload` 管理子命令：list 列出已加载 skill 与来源；info 显示完整 frontmatter 与文件路径；reload 重新扫描目录并重建 catalog

### 热更新与清理
- F17: 每次 skill 执行时重新读取源文件（仅 body，frontmatter 走启动期缓存），文件修改即时生效；解析失败回退到缓存版本并记日志
- F18: `/clear` 命令在清对话历史时调 `Agent.ClearActiveSkills()` 把激活 skill 列表也清空

### 远程安装
- F20: `InstallSkillTool` 让用户把 URL 发给 mewcode、由 Agent 自动安装到 `~/.mewcode/skills//`
 - 支持三种 URL：`https://www.skills.sh///` / `https://github.com///tree//` / `https://raw.githubusercontent.com/.../SKILL.md`
 - 走 GitHub Contents API 递归拉取目录树（无需本地 git），单文件 ≤1 MiB、总大小 ≤8 MiB、文件数 ≤64、深度 ≤4
 - 暂存到兄弟 tempdir，验证含 SKILL.md 后 atomic rename 到位
 - 安装后自动 `Catalog.Reload` + 单条 `registerSkillCommand`，无需 TUI 重启即可 `/` 与 `LoadSkill` 触发

## 4. 非功能需求

- N1: 单个 skill 文件解析失败不能阻断其他 skill 加载，错误走 debug log
- N2: 启动加载阶段（阶段 1）不读 body，确保 1000 个 skill 也能秒级启动
- N3: fork 模式必须隔离 conversation，主对话状态不被子 Agent 修改
- N4: 工具过滤通过 `Agent.ToolNameFilter` 钩子实现，过滤动态生效不要求重启 Agent
- N5: LoadSkill 工具调用不弹权限提示（read-only 类别）
- N6: 同名 skill 在多个搜索路径出现时，高优先级路径的版本覆盖低优先级

## 5. 设计概要

### 核心数据结构
- `SkillMeta`：扩展 mode / model / context 三个字段
- `Skill`：Meta + PromptBody（懒加载）+ SourceDir + IsDirectory
- `Catalog`：name → *Skill；新增 `GetFull(name) (*Skill, error)` 强制重读 body；两级路径扫描（项目 > 用户）
- `Executor`：`RunInline(ctx, skill, args, ag, conv)` 与 `RunFork(ctx, skill, args, ag, conv) (string, error)`
- `LoadSkillTool`：实现 tools.Tool 接口；持有 *Catalog 与 *Agent 引用，标记 system tool
- Agent 新增字段与方法：`ActiveSkills map[string]string`、`ActivateSkill(name, body)`、`ClearActiveSkills()`、Agent Loop 每轮把 ActiveSkills 注入 system-reminder

### 主流程
1. 启动：TUI `loadSkillsAndBuildPrompt` → `skills.LoadCatalog(workDir)` 两级扫描，每个 skill 只读 frontmatter
2. system prompt 注入：把 catalog 的 `{name, description}` 列表 + LoadSkill 用法说明，通过 SkillSection 喂给 prompt builder
3. 命令注册：每个 skill 注册 `/` 命令；LoadSkillTool 也在启动期注册进 tools.Registry
4. 主 Agent 循环每轮迭代开头：把 `agent.ActiveSkills` 字典的所有 SOP 拼成 system-reminder 注入 conv（与 ch04 的 NotificationFn / Plan Mode reminder 同一通道）
5. 显式调用 `/commit`：handler 调 `Executor.RunInline(commit, args, ag, conv)` → 内部 `ag.ActivateSkill("commit", body)` + 应用工具白名单 ToolNameFilter → 返回 rendered body 作为 user message → Agent loop
6. 意图识别：Agent 调 `LoadSkillTool({name: "commit"})` → 工具执行 `ActivateSkill` → 返回 `"Skill commit activated. SOP pinned to env."`
7. fork 调用 `/review`：TUI 同步走 `Executor.RunFork` → 新 conv + 过滤 registry + 临时 Agent + Run 到完成 → 把 final text 作为 assistant 消息进主对话
8. `/clear`：清 conv → 调 `ag.ClearActiveSkills()` → 后续轮不再注入旧 SOP

### 调用链
- 启动：main → tui.New → `loadSkillsAndBuildPrompt` → `skills.LoadCatalog` + `register skill commands` + `register LoadSkillTool`
- inline 显式：用户 `/commit` → TUI executeCommand → handler → `Executor.RunInline` → ActivateSkill → user message → Agent loop（每轮 env 注入 SOP + 工具过滤）
- fork 显式：用户 `/review` → TUI executeCommand → handler → `Executor.RunFork`（同步阻塞）→ assistant 消息回流
- 意图触发：Agent 在某轮调用 `LoadSkillTool` → catalog.GetFull → ActivateSkill → 下一轮 SOP 钉在 env 里
- 清理：用户 `/clear` → TUI → conv reset + `ag.ClearActiveSkills`

### 与其他模块的交互
- 上行依赖：TUI（注入 system prompt、注册命令、fork 同步执行、InstallSkill OnInstalled 回调）、Agent（ActiveSkills 字段 + env 注入 + ToolNameFilter）、conversation.Manager（fork 用独立实例）、prompt.builder（SkillSection 通道）、tools.RegistryTool）
- 下行：fork 模式调 internal `Agent.Run`，但是 skills 包不直接 import agent 包，通过接口注入（避免循环依赖）；`InstallSkill` 走标准库 `net/http` + GitHub Contents API，不依赖 `git` 二进制

## 7. 完成定义

见 [checklist.md](checklist.md)，所有条目勾上即完成。