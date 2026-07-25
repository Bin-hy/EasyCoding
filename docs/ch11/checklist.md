# Skill 系统 Checklist

> 所有条目必须可勾选、可观测。验收方式写在每项后面的括号里。

## 1. 实现完整性

### 1.1 解析与加载
- [ ] `SkillMeta` 在 `internal/skills/skills.go` 含字段 Name / Description / WhenToUse / Tags / AllowedTools / Context / Mode / Model / ForkContext，yaml tag 全部 snake_case
- [ ] `parseFrontmatterOnly(path) (SkillMeta, error)` 在 `internal/skills/parser.go` 实现，**不**读取 body
- [ ] `loadSkillBody(skill *Skill) error` 在 `internal/skills/parser.go` 实现，强制重读源文件（热重载）
- [ ] `Catalog.GetFull(name) (*Skill, error)` 在 `internal/skills/catalog.go` 实现，每次调用触发 `loadSkillBody`；解析失败回退缓存 + 记 debug log
- [ ] `Catalog.Reload(workDir) error` 在 `internal/skills/catalog.go` 实现
- [ ] 两级加载顺序在 `LoadCatalog(workDir)`：项目 `.mewcode/skills/` > `~/.mewcode/skills/`；同名按优先级覆盖

### 1.2 Executor
- [ ] `internal/skills/executor.go` 含 `RunInline(ctx, skill, args, host) (string, error)` 与 `RunFork(ctx, skill, args, host) (string, error)`
- [ ] inline 调用链：Render → host.ActivateSkill → 工具白名单 fail-fast → host.SetToolFilter → 返回 rendered body
- [ ] fork 调用链：新 conversation.Manager → 按 ForkContext 装填历史（full / recent / none）→ host.RunSubAgent → 返回 finalText
- [ ] `SkillHost` 与 `SkillForkHost` 接口在 `internal/skills/executor.go` 定义

### 1.3 Agent 集成
- [ ] Agent 含 `ActiveSkills map[string]string` 字段
- [ ] `Agent.ActivateSkill(name, body string)` 实现
- [ ] `Agent.ClearActiveSkills()` 实现
- [ ] Agent.Run 主循环每轮迭代开头，如 ActiveSkills 非空则注入 system-reminder（标题 `# Active Skills`，每个 skill 一段，含 name）
- [ ] Agent 实现 SkillHost 接口（编译期可强转）

### 1.4 InstallSkill 远程安装
- [ ] `internal/skills/install.go` 含 `ParseSkillURL` 支持 skills.sh / github.com tree / raw.githubusercontent.com 三种 URL
- [ ] `Install(src, installRoot) (*InstallReport, error)` 走 GitHub Contents API 递归拉取，atomic rename 到 `<installRoot>/<name>/`
- [ ] 限额常量在 install.go：`maxFileSize=1MiB / maxTotalSize=8MiB / maxFileCount=64 / maxRecursionDepth=4`
- [ ] 下载完没有 `SKILL.md` 或 `skill.yaml` 时拒绝安装并清理 staging
- [ ] `internal/skills/install_tool.go` 含 `InstallSkillTool`，Name = `InstallSkill`，Category = write
- [ ] 执行成功后调 `Catalog.Reload(workDir)` + `OnInstalled(name)` 回调
- [ ] TUI 注入 `OnInstalled` 回调指向 `m.registerSkillCommand(name)`，使 `/<name>` 无需重启即可用
- [ ] SkillSection 文本含 "If the user pastes a Skill URL ... call the InstallSkill tool"

### 1.5 LoadSkill 工具与系统工具豁免
- [ ] `internal/tools/load_skill.go` 含 `LoadSkillTool`，Name = `LoadSkill`，Category = read
- [ ] `LoadSkillTool.Execute` 调 `catalog.GetFull` → `host.ActivateSkill` → 返回 `"Skill <name> activated. SOP pinned to env."`
- [ ] `tools.SystemTool` 接口在 `internal/tools/tool.go` 定义；LoadSkillTool 实现该接口
- [ ] Agent.ToolNameFilter 应用时绕过 system tool（系统工具始终可见）

### 1.6 命令集成
- [ ] 每个 skill 自动注册为 `/<name>` 命令，描述末尾含 `[skill]`
- [ ] inline skill 命令 Type 为 `TypePrompt`，fork skill 命令 Type 为 `TypeSkillFork`
- [ ] `commands.TypeSkillFork` 在 `internal/commands/commands.go` 定义
- [ ] TUI executeCommand 对 TypeSkillFork case：调 handler 返回 summary → 作为 assistant chatMessage 插入对话
- [ ] `/skill list / info <name> / reload` 子命令在 `internal/commands/commands.go` 注册
- [ ] `/clear` handler 调用 `m.ag.ClearActiveSkills()`

## 2. 接入完整性（杜绝死代码）

- [ ] `grep -rn "skills.LoadCatalog" --include="*.go" .` 命中 `internal/tui/tui.go` 至少 1 个非测试调用
- [ ] `grep -rn "ActivateSkill" --include="*.go" ./internal` 命中 Agent 方法定义 + Executor + LoadSkillTool 三处调用
- [ ] `grep -rn "ClearActiveSkills" --include="*.go" ./internal` 命中 `/clear` handler 调用
- [ ] `grep -rn "LoadSkillTool\b\|\"LoadSkill\"" --include="*.go" ./internal` 命中 tool 定义 + tui 注册 + 至少 1 个测试
- [ ] `grep -rn "TypeSkillFork" --include="*.go" ./internal` 命中 commands 定义 + TUI dispatch
- [ ] `grep -rn "RunInline\|RunFork" --include="*.go" ./internal/skills` 命中 Executor 定义 + TUI handler 调用
- [ ] `grep -rn "Catalog.GetFull" --include="*.go" ./internal` 命中 catalog 定义 + LoadSkillTool 调用
- [ ] `grep -rn "InstallSkillTool\|ParseSkillURL" --include="*.go" ./internal` 命中 install 定义 + TUI 注册 + install_test
- [ ] `grep -rn "SystemTool() bool" --include="*.go" ./internal` 命中接口定义 + LoadSkillTool 实现
- [ ] TUI Model `ag` 字段有 `skillCatalog` / 在 loadSkillsAndBuildPrompt 写入 / LoadSkillTool 拿到引用

## 3. 编译与测试

- [ ] `cd . && go build ./...` 通过
- [ ] `cd . && go test ./internal/skills/...` 全部通过
- [ ] `cd . && go test ./internal/tools/...` 全部通过
- [ ] `cd . && go test ./internal/agent/...` 全部通过
- [ ] `go vet ./...` 无警告

## 4. 端到端验证（TUI 实操）

> 操作目录在仓库根 `.`，启动 `go run ./cmd/mewcode`

- [ ] 创建测试 skill 目录 `.mewcode/skills/test-skill/SKILL.md`（简单 inline SOP），启动后输 `/help`，看到 `/test-skill [skill]` 和 `/skill` 都列出
- [ ] 输 `/skill list`，输出含 test-skill 名称 + 来源（project / user）
- [ ] 输 `/skill info test-skill`，输出含完整 frontmatter（含 mode / allowed_tools） + 文件路径
- [ ] `/test-skill` 触发 inline 执行，SOP 注入到 Agent 环境
- [ ] 修改 `.mewcode/skills/test-skill/SKILL.md` 一行，**不重启** TUI，再输 `/test-skill`，看到新行进入 prompt（热重载验证）
- [ ] 自然语言触发 LoadSkill("test-skill")，system-reminder 里出现该 skill 的 SOP
- [ ] 输 `/clear`，立即输任意消息，Agent system-reminder 里**不再出现** Active Skills 段
- [ ] 启动时在 catalog 里塞一个 `allowed_tools: [NonExistentTool]` 的 skill，看到启动 log 报 fail-fast 错误（或调用时立刻报错）
- [ ] LoadSkill 工具调用时**不**弹权限提示（read-only 类别 + auto-allow）
- [ ] 在 TUI 输入「装这个 skill：https://www.skills.sh/anthropics/skills/frontend-design」，模型调 InstallSkill；返回安装路径与文件数；立即输 `/frontend-design` 触发新装的 skill（无需 TUI 重启）
- [ ] InstallSkill 失败路径：输错误 URL → 看到具体 host / 格式不对的错误文本；输不存在的 repo → 看到 404 错误透出

## 5. 文档

- [ ] `specs/go/ch11/spec.md` 更新到课程全量版（不是验收版）
- [ ] `specs/go/ch11/tasks.md` 16 个任务全部勾上
- [ ] `specs/go/ch11/checklist.md` 全部条目勾上
- [ ] commit 信息：`feat(ch11): full skill system per course design [spec/tasks/checklist closed]`