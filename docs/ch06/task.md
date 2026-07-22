# 权限系统 Tasks## 文件清单

| 操作 | 文件 | 职责 |
|------|------|------|
| 新建 | `internal/permission/mode.go` | `Mode` 四档 + `String`/`ParseMode`；`Decision`/`Category`/`Outcome` |
| 新建 | `internal/permission/blacklist.go` | 内置危险命令正则集 + `hitsBlacklist`（不可配，N1） |
| 新建 | `internal/permission/sandbox.go` | `resolveRoot`、`sandboxOK`、`evalSymlinksOrAncestor`（N2） |
| 新建 | `internal/permission/rule.go` | `Rule`/`RuleSet`、`parseRule`、`match`、`matchPattern`（glob） |
| 新建 | `internal/permission/settings.go` | `Settings` YAML、`loadSettings`、`toRuleSet`、`friendlyName`、`categorize`、`extractTarget` |
| 新建 | `internal/permission/engine.go` | `Engine`、`NewEngine`、`Check` 前四层流水线、`modeFallback`、`StartMode` |
| 新建 | `internal/permission/persist.go` | `ruleFor`、`PersistLocalAllow`（写本地层文件） |
| 新建 | `internal/permission/*_test.go` | 黑名单/沙箱(含祖先回退)/规则/优先级/矩阵/加载降级/解析失败 单测 |
| 改   | `internal/agent/agent.go` | 删 `Mode`（迁 permission）；`Agent` 加 `eng`；`executeBatched(+mode)` 接入 `Check`；`requestApproval`；`ApprovalRequest`/`Event.Approval`；Deny 用 `llm.ToolResult` 构造 |
| 改   | `internal/agent/agent_test.go` | 权限集成(Allow/Deny/Ask/会话/永久)、保序回灌、只读并发不退化、取消、模式迁移 |
| 改   | `internal/tui/tui.go` | `Model.mode`→`permission.Mode`、加 `engine`/`pending`/`approveCursor`；`New` 增参（保持 `(Model,error)`）；`stateApproving` 分派；全局 ctrl+c/esc 覆盖 approving；`shift+tab` 循环模式(`nextMode`) |
| 改   | `internal/tui/stream.go` | `updateStreaming` 处理 Approval；`updateApproving`；`sendOutcome`；`submit` 保留 `/plan`·`/do`（去掉 `/mode`）；`beginTurn` 传 eng |
| 改   | `internal/tui/view.go` | `statusBar` 左侧常驻模式（取代 provider 名）；待批准块渲染（多行三选菜单 + 光标高亮） |
| 改   | `internal/tui/tui_test.go` | `shift+tab` 循环切换、approval 态按键回传、Esc 取消兜底、状态栏显示模式、模式跨轮保持；既有 `/plan`·`/do` 用例适配新 Mode 类型 |
| 改   | `cmd/mewcode/main.go` | 构造 `permission.NewEngine(root)` 注入 `tui.New` |
| 改   | `cmd/smoke/main.go` | 新增 `cwd`、构造引擎、`ModeBypass` 运行；`agent.New` 增参 |
| 改   | `.gitignore` | 追加 `.mewcode/settings.local.yaml` |
| 新建 | `.mewcode/settings.yaml.example` | 权限配置示例（defaultMode + allow/deny） |

---

## T1: permission 基础类型**文件：** `internal/permission/mode.go`
**依赖：** 无
**步骤：**
1. `type Mode int`，常量 `ModeDefault/ModeAcceptEdits/ModePlan/ModeBypass`（iota）。
2. `(Mode) String() string` → `"default"/"acceptEdits"/"plan"/"bypassPermissions"`。
3. `ParseMode(s string) (Mode, bool)`：大小写不敏感识别四档名，未知返回 `(ModeDefault, false)`。
4. `type Decision int`：`Allow/Deny/Ask`。`type Category int`：`CategoryRead/CategoryWrite/CategoryExec`。
5. `type Outcome int`：`OutcomeDenyOnce/OutcomeAllowOnce/OutcomeAllowForever`（人在回路三选一）。

**验证：** `go build ./internal/permission/...`；`ParseMode` 对 `"default"/"acceptEdits"/"plan"/"bypassPermissions"`（含大小写变体）均得 `(对应档, true)`，`ParseMode("x")` 得 `(ModeDefault, false)`。

## T2: 危险命令黑名单**文件：** `internal/permission/blacklist.go`
**依赖：** 无
**步骤：**
1. 包级 `var blacklist = []*regexp.Regexp{ ... }`，用 `regexp.MustCompile` 编译一组高危模式（见 plan：`rm -rf /|~|$HOME|/*`、`dd of=/dev/`、fork bomb、`mkfs.`、`> /dev/sd|nvme|disk`、`chmod -R 777 /` 等）。
2. `func hitsBlacklist(command string) bool`：任一正则 `MatchString(command)` 即真。
3. 顶部注释声明「启发式、非完备、不可配置放开」（N1）。

**验证：** 单测：`rm -rf /`、`rm -fr ~`、`:(){ :|:& };:`、`dd if=/dev/zero of=/dev/sda` 命中；`rm -rf ./build`、`git status`、`ls -la` 不命中。

## T3: 路径沙箱**文件：** `internal/permission/sandbox.go`
**依赖：** 无
**步骤：**
1. `func resolveRoot(root string) (string, error)`：`filepath.Abs` + `filepath.EvalSymlinks`。
2. `func evalSymlinksOrAncestor(abs string) string`：对存在的目标 `EvalSymlinks`；不存在则逐级取最近**已存在祖先**目录 `EvalSymlinks` 后拼回剩余段（覆盖「新建文件、含未创建中间目录」）。
3. `func (e *Engine) sandboxOK(path string) bool`：空 path 视为 `e.root`；相对路径相对 `e.root` 解析为绝对；`resolved := evalSymlinksOrAncestor(abs)`；返回 `resolved==e.root || strings.HasPrefix(resolved, e.root+string(os.PathSeparator))`。用 `filepath`，不硬编码 `/`。

**验证：** 单测（`t.TempDir()` 造 root + 内外文件 + 符号链接）：root 内文件通过；**root 内但含多级未创建中间目录的新建文件路径通过**（专测祖先回退分支）；`/etc/passwd`、`../outside`、root 内指向 root 外目录的软链接被拒。

## T4: 规则与匹配**文件：** `internal/permission/rule.go`
**依赖：** 无
**步骤：**
1. `type Rule struct { Tool, Pattern string; Allow bool }`；`type RuleSet struct { allow, deny []Rule }`。
2. `parseRule(s string) (Rule, bool)`：解析 `Tool(pattern)` 或 `Tool`；取友好名与括号内模式（可含空格/`*`/`**`）；非法（空、括号不配对）返回 `false`。
3. `matchPattern(pattern, target string) bool`：`pattern==""`→true；命令串整串走「命令 glob」（`*` 匹配任意字符含空格，`**` 等价 `*`）；文件路径按 slash 分段走 `*`（段内）/`**`（跨段）（借鉴 `tool/glob.go` 的 `matchSegments`）。
4. `func (rs RuleSet) match(friendly, target string) (Decision, bool)`：先遍历 `deny`（`Tool==friendly && matchPattern` 命中）→`(Deny,true)`；再 `allow`→`(Allow,true)`；否则 `(_,false)`。

**验证：** 单测：`parseRule("Bash(git *)")`、`parseRule("Read")` 正确；`matchPattern("git *","git status")` 真、`"git *","npm i"` 假；`matchPattern("src/**","src/a/b.go")` 真、`"src/**","docs/x"` 假；同层 deny 与 allow 同时命中时 `match` 返回 `Deny`。

## T5: 配置加载与映射**文件：** `internal/permission/settings.go`
**依赖：** T1, T4
**步骤：**
1. `type Settings struct { DefaultMode string; Permissions struct{ Allow, Deny []string } }`（yaml tag 见 plan）。
2. `loadSettings(path string) (Settings, error)`：文件不存在→空 `Settings`、nil err；读到则 `yaml.Unmarshal`，解析失败→零值 + err（调用方降级，N5）。
3. `toRuleSet(s Settings) RuleSet`：`Permissions.Allow/Deny` 各条 `parseRule`，非法条目跳过；allow 条 `Allow=true`、deny 条 `Allow=false` 分别入 `RuleSet`。
4. `friendlyName(internal string) string`：`bash→Bash, read_file→Read, write_file→Write, edit_file→Edit, glob→Glob, grep→Grep`；未知原样返回。
5. `categorize(internal string, readOnly bool) Category`：`readOnly→CategoryRead`（优先）；否则 `write_file/edit_file→CategoryWrite`、其余（含 `bash`、未知工具）→`CategoryExec`（N7 最严）。
6. `extractTarget(call llm.ToolCall) (target string, isFile bool, ok bool)`：内部对 `call.Input`（json.RawMessage）`json.Unmarshal`——`read_file/write_file/edit_file` 取 `path`（isFile=true）；`glob/grep` 取 `path`（**搜索根目录**，空→`"."`，isFile=true；注：`pattern`/`glob` 字段不参与沙箱）；`bash` 取 `command`（isFile=false）；未知工具→`("",false,false)`；**Unmarshal 失败或缺必填字段→`ok=false`**。

**验证：** 单测：缺失文件得空且无 err；非法 YAML 得 err；`toRuleSet` 跳过非法条；`friendlyName`/`categorize`（含未知工具→Exec、readOnly 优先）/`extractTarget`（各工具字段、解析失败 ok=false）各分支正确。

## T6: 引擎与前四层流水线**文件：** `internal/permission/engine.go`
**依赖：** T1, T2, T3, T4, T5
**步骤：**
1. `Engine` 结构（见 plan）：`root, blacklist, user/project/local RuleSet, localPath, startMode`。
2. `NewEngine(root string) (*Engine, error)`：
   - `resolveRoot(root)`；**失败时 `e.root` 退化为传入 `root`、四层规则空、`startMode=ModeDefault`，仍返回非 nil `e` + err**（main 注入永不为 nil，Check 不 panic）。
   - 加载三层：user=`~/.mewcode/settings.yaml`（`os.UserHomeDir`）、project=`<root>/.mewcode/settings.yaml`、local=`<root>/.mewcode/settings.local.yaml`；各 `loadSettings`→`toRuleSet`；**单个文件读/解析失败仅降级跳过该文件（视为空），绝不返回致命 err**。
   - `localPath = <root>/.mewcode/settings.local.yaml`。
   - `startMode`：依次取 local/project/user 的 `DefaultMode`（`ParseMode` 成功者优先 local），皆无→`ModeDefault`。
   - **唯一返回非 nil err 的情形是 `resolveRoot` 失败**。
3. `modeFallback(mode Mode, cat Category) Decision`：F5 矩阵——`cat==CategoryRead` 或 `mode==ModeBypass`→`Allow`；`mode==ModeAcceptEdits && cat==CategoryWrite`→`Allow`；其余（default/plan 的 Write/Exec、acceptEdits 的 Exec）→`Ask`。**只产 Allow/Ask**。
4. `Check(mode Mode, call llm.ToolCall, readOnly bool) (Decision, string)`：
   - `cat:=categorize(call.Name,readOnly)`；`friendly:=friendlyName(call.Name)`；`target,isFile,ok:=extractTarget(call)`。
   - ① `cat==CategoryExec && target!="" && hitsBlacklist(target)` → `(Deny,"命中危险命令黑名单：…")`。
   - ② `isFile`：`!ok` → `(Deny,"无法解析文件路径参数，安全拒绝")`；否则 `!sandboxOK(target)` → `(Deny,"路径在项目目录之外："+target)`。
   - ③ 依 `local, project, user` 顺序 `match(friendly, target)`，命中即返回 `(d,"匹配规则：…")`。
   - ④ `modeFallback(mode,cat)` → `(Allow,"")` 或 `(Ask, mode.String()+" 模式下 "+类别+" 类操作需确认")`。
5. `StartMode() Mode { return e.startMode }`。

**验证：** 单测：逐层短路（黑名单先于沙箱/规则；deny 规则先于模式；allow 规则不进模式）；跳层放行（非 Exec 不被黑名单拦、Bash 不被沙箱拦）；模式矩阵逐档逐类断言（含 plan 行 Write/Exec→Ask）；三级优先级（本地 deny 盖项目 allow 等）；`resolveRoot` 失败仍得非 nil 引擎。

## T7: 会话与永久规则写入**文件：** `internal/permission/persist.go`
**依赖：** T5, T6
**步骤：**
1. `ruleFor(call) (Rule, string, bool)`：据 `extractTarget`+`friendlyName` 生成**精确**规则（内存 Rule + YAML 串两种形态）——`bash`→`Bash(<command>)`；文件类→`Write(<relpath>)`/`Read(<relpath>)` 等（relpath=相对 root 的 slash 路径）；Bash 命令串经 `escapeGlob` 转义字面 glob 元字符防止规则被泛化；解析失败/未知→`(Rule{},"",false)`。
2. `PersistLocalAllow(call llm.ToolCall) error`：`loadSettings(e.localPath)`（缺失则空）→ 追加规则串到 `Permissions.Allow`（去重）→ `yaml.Marshal` → 确保目录存在 → `os.WriteFile`；同步把规则并入 `e.local.allow`。

**验证：** 单测（`t.TempDir()` 作 root）：`PersistLocalAllow` 后 `localPath` 文件含该 allow 条、再 `NewEngine` 重载仍 `Allow`；幂等：重复 `PersistLocalAllow` 不报错且不重复写文件。

## T8: agent 接入权限（模式迁移 + 判定 + 人在回路）**文件：** `internal/agent/agent.go`
**依赖：** T6, T7
**步骤：**
1. **模式迁移**：删除 agent 内 `Mode`/`ModeNormal`/`ModePlan` 定义，import `permission`，全部改用 `permission.Mode`/`ModeDefault`/`ModePlan`；`Run` 形参 `mode permission.Mode`；`mode==permission.ModePlan` 处不变（defs 选只读、PlanReminder 注入）。
2. `Agent` 加 `eng *permission.Engine`；`New(p, r, version, eng)`。
3. 新增 `type ApprovalRequest struct{ Name, Args, Reason string; Respond chan permission.Outcome }`；`Event` 加 `Approval *ApprovalRequest`。
4. `requestApproval(ctx, call, reason, ch) (permission.Outcome, bool)`：`respond:=make(chan permission.Outcome,1)`；`emit(Event{Approval:{Name:call.Name, Args:argsPreview(call.Input), Reason:reason, Respond:respond}})`；`select{ case o:=<-respond: (o,true); case <-ctx.Done(): (0,false) }`。
5. `executeBatched(ctx, calls, mode, ch)`（增 `mode` 形参）接入。**Deny 结果统一用 `llm.ToolResult{ToolCallID:calls[k].ID, Content:reason, IsError:true}` 构造**（agent 包无 errResult）：
   - 只读批：每个 `k` 先 `d,reason:=a.eng.Check(mode,calls[k],true)`；按调用序发 `PhaseStart`；`d==Deny`→ `results[k]=ToolResult{...IsError:true}`、`done[k]=true`、**不纳入并发**；否则照旧并发。并发执行完按调用序发 `PhaseEnd`（**Deny 项也发，IsError=true**，与有副作用 Deny 一致）。
   - 串行有副作用：`d,reason:=a.eng.Check(mode,calls[i],false)`；`Allow`→执行；`Deny`→`ToolResult{...IsError:true}`；`Ask`→`o,ok:=a.requestApproval(...)`；`!ok`→取消收尾（`completed=false`，沿用既有路径）；按 `o`：AllowOnce→执行；AllowForever→`a.eng.PersistLocalAllow(calls[i])`（err 仅记不阻断）+执行；DenyOnce→被拒结果。
6. `Run` 调 `executeBatched(ctx, calls, mode, ch)`。

**验证：** `go build ./internal/agent/...`（配合 T9）；轻量自检：表驱动断言 `requestApproval` 在 ctx 已取消时返回 `(0,false)`、不阻塞。

## T9: agent 单测**文件：** `internal/agent/agent_test.go`
**依赖：** T8
**步骤：**
1. 既有 ch04/ch05 用例：`New(...)` 增 `eng` 实参（`permission.NewEngine(t.TempDir())`）；`ModeNormal`→`ModeDefault`；fake provider 签名不变。
2. 新增：
   - **Deny 回灌不中断**：构造 deny（沙箱外路径或会话 deny）→ 模型请求该工具 → 工具结果 `IsError`、Loop 继续到次轮（脚本化 fake）。
   - **保序回灌**：单批含「被拒调用 + 放行调用」→ 断言结果按原 `calls` 下标序、各自 `ToolCallID` 正确配对（被拒 IsError、放行正常），不串位。
   - **Ask 人在回路**：default 下请求 `write_file` → 收 `Event{Approval}` → 向 `Respond` 送 `AllowOnce`/`DenyOnce`，断言执行/回灌生效。
   - **永久放行**：送 `AllowForever`，断言 `localPath` 文件被写、含 allow 条。
   - **只读并发不退化**：一批只读不产生任何 `Approval` 事件；被沙箱拦的只读得 errResult、其余仍并发完成。
   - **取消**：在 `Approval` 等待中 `cancel()` → Loop 干净收尾、历史合法、无 goroutine 泄漏（`-race`/超时保护）。
   - **plan 迁移**：`ModePlan` 仍只放只读工具、注入计划提醒（沿用 ch05 断言，类型换名）。

**验证：** `go test ./internal/agent/...`；`go test -race ./internal/agent/...` 无竞争、无超时。

## T10: TUI 接入（模式切换 + 待批准态）**文件：** `internal/tui/tui.go`、`internal/tui/stream.go`、`internal/tui/view.go`
**依赖：** T8
**步骤：**
1. `tui.go`：`Model.mode permission.Mode`；加 `engine *permission.Engine`、`pending *agent.ApprovalRequest`、`approveCursor int`（待批准菜单光标）；`New(providers, version, registry, engine) (Model, error)`（**保持 (Model,error) 返回，仅末尾增形参**）存引擎、`m.mode=engine.StartMode()`；`stateApproving` 常量；`Update` 在 `stateApproving` 分派 `updateApproving`；**全局 ctrl+c/esc 分派条件 `m.state==stateStreaming` 改为 `stateStreaming || stateApproving`**，approving 态取消时先向 `m.pending.Respond` 送 `OutcomeDenyOnce`（缓冲=1 不阻塞）再 `m.turnCancel()`；**新增 `case "shift+tab":`（仅 `m.state==stateIdle` 生效）`m.mode = nextMode(m.mode)` 并 `tea.Println(noticeBlock("已切换到 X 模式"))`**；`nextMode(m Mode) Mode` 为本包小函数，`(m+1)%4` 循环 default→acceptEdits→plan→bypassPermissions→default。
2. `stream.go`：
   - `beginTurn`：`agent.New(m.provider, m.registry, m.version, m.engine)`。
   - `updateStreaming`：`case msg.Approval != nil:` → `m.pending=msg.Approval`；`m.state=stateApproving`；**返回 nil（不 waitForEvent）**。
   - `updateApproving(msg)`：维护 `m.approveCursor`（0/1/2）；`up`/`k`、`down`/`j` 循环移光标；`enter`/`space` 提交当前光标项；数字键 `1`/`2`/`3` 直选；`y`=AllowOnce、`n`/`d`=DenyOnce 便捷键。索引→`Outcome` 经 `outcomeForIndex`（0=AllowOnce、1=AllowForever、2=DenyOnce）。选定后回 `stateStreaming`、清 `pending`，返回 `tea.Batch(sendOutcome(req,outcome), waitForEvent(m.events))`。`updateStreaming` 收到 Approval 时把 `approveCursor` 重置为 0。
   - `submit`：保留 `/plan`(→ModePlan)`/do`(→ModeDefault，注入执行指令)`/exit`，作为计划工作流专用入口/出口；**不新增 `/mode`**（模式切换统一走 Shift+Tab，见步骤 1）。
3. `view.go`：
   - `statusBar`：**左侧不再显示 provider 名，改为常驻显示当前权限模式**——ModeDefault→`DEFAULT`(灰/绿)、ModeAcceptEdits→`ACCEPT EDITS`、ModePlan→`PLAN`(黄)、ModeBypass→`BYPASS`(红)；右侧模型名 + token 用量不变。
   - `View` 在 `stateApproving`：渲染**多行待批准块** `approvalBlock(m.pending, m.approveCursor)`——`● <动作名>` + 缩进参数预览 + 灰字原因 + `是否继续?` + 三行菜单（光标项 `> `+高亮、其余 `  `）`1. 允许本次 / 2. 永久允许（写入本地配置） / 3. 拒绝本次` + 底部灰字 `↑↓ 选择 · 回车确认 · Esc 取消`。

**验证：** `go build ./...`（配合 T11）。

## T11: TUI 单测**文件：** `internal/tui/tui_test.go`
**依赖：** T10
**步骤：**
1. 既有 `/plan`·`/do` 用例适配 `permission.Mode`（`ModePlan`/`ModeDefault`）。
2. 新增：
   - 连续模拟 `shift+tab`（idle 态）→ 断言 `m.mode` 依次 ModeDefault→ModeAcceptEdits→ModePlan→ModeBypass→ModeDefault、停留 idle、每次返回提示块。
   - 模拟收到 `Approval` 事件 → `m.state==stateApproving`、`m.pending` 已设、`approveCursor==0`；按 `down` 再 `enter`→`Respond` 收到 `OutcomeAllowForever`；另测数字键 `1`→`OutcomeAllowOnce`、`3`→`OutcomeDenyOnce`，回 `stateStreaming`。
   - approving 态按 `Esc`/`Ctrl+C`→ 触发取消、`Respond` 收到兜底 `OutcomeDenyOnce`、不退出程序。
   - `statusBar` 左侧在各模式显示对应模式名（DEFAULT/ACCEPT EDITS/PLAN/BYPASS），且**不含 provider 名**。
   - **模式跨轮保持**：Shift+Tab 切到 acceptEdits 后再 `beginTurn`，断言 `m.mode` 仍为 acceptEdits（不被重置）。

**验证：** `go test ./internal/tui/...`。

## T12: main / smoke / 配置文件接线**文件：** `cmd/mewcode/main.go`、`cmd/smoke/main.go`、`.gitignore`、`.mewcode/settings.yaml.example`
**依赖：** T6, T8, T10
**步骤：**
1. `main.go`：`root,_:=os.Getwd()`；`eng,err:=permission.NewEngine(root)`；`err!=nil` 仅 `fmt.Fprintln(os.Stderr,"权限引擎降级:",err)` 后**继续**（`eng` 必非 nil）；`m,err:=tui.New(cfg.Providers, version, registry, eng)`（保持 `m,err:=` 接收与既有 err 处理）。
2. `smoke/main.go`：新增 `cwd,_:=os.Getwd()`（import `os`）；`eng,_:=permission.NewEngine(cwd)`；`agent.New(p, tool.NewDefaultRegistry(), "dev", eng)`；`Run(ctx, conv, permission.ModeBypass)`。
3. `.gitignore`：在「本地配置」段追加 `.mewcode/settings.local.yaml`。
4. `.mewcode/settings.yaml.example`：示例——`defaultMode: default`；`permissions.allow: ["Bash(git *)", "Bash(go test ./...)"]`；`permissions.deny: ["Bash(rm *)", "Read(.env)", "Write(.env)"]`；注释说明三层文件与优先级，并注明**只读类默认即 Allow，allow 规则主要用于提前放行 Bash/Write，deny 规则可对只读做围栏（如 Read(.env)）**。

**验证：** `go build ./...` 全绿；`go run ./cmd/smoke` 在含 write_file 的脚本下**不阻塞、跑完**（确认 ModeBypass 跳过 Ask）；`go run ./cmd/mewcode` 能正常启动进对话。

## T13: 全量编译测试与规范**文件：** —
**依赖：** T1–T12
**步骤：**
1. `gofmt -l .`（无输出）、goimports 分组检查（permission 为本地包）。
2. `go vet ./...`（无告警）。
3. `go build ./...`、`go test ./...`、`go test -race ./internal/agent/... ./internal/permission/... ./internal/tui/...`。
4. 确认 `.mewcode/settings.local.yaml` 已被 gitignore（`git check-ignore`）；检索输出无 api_key 明文。
5. **tmux 实跑冒烟**（CLAUDE.md 开发原则第 2 条）：default 下写文件触发 Ask 弹窗；Shift+Tab 循环到 bypassPermissions 后不再 Ask、状态栏左侧显示 `BYPASS`；`rm -rf /` 在 bypass 下仍被拦。

**验证：** 全部通过。

## 执行顺序

```
T1(类型) ─┬───────────────────────────────────┐
T2(黑名单)─┤                                    │
T3(沙箱) ──┤                                    ├─→ T6(引擎/流水线) ─→ T7(规则写入)
T4(规则) ──┴─→ T5(配置/映射) ───────────────────┘                          │
                                                                            │
                                              T6,T7 ─→ T8(agent 接入) ─┬─→ T9(agent 单测)
                                                                       ├─→ T10(TUI 接入) ─┬─→ T11(TUI 单测)
                                                                       │                  │
                                                          T6,T8,T10 ─→ T12(main/smoke/配置)
全部 ─→ T13(编译/测试/race/gofmt/vet/tmux)
```
（依赖：T5←{T1,T4}；T6←{T1,T2,T3,T4,T5}；T7←{T5,T6}；T8←{T6,T7}；T9←T8；T10←T8；T11←T10；T12←{T6,T8,T10}；T13←全部。）