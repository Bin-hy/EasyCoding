# 权限系统 Plan> 技术栈：Go；沿用 anthropic-sdk-go / openai-go（本章**不改 provider 适配层**）。权限判定全部落在 agent 编排层与新增的 permission 包，与协议无关。

## 架构概览ch06 新增一个 **permission 包**承载前四层防御与配置加载，并在 **agent 包**把判定接入工具执行链、由 agent 编排承载第五层人在回路；**tui 包**新增「待批准」交互态承载人在回路的 UI；**main** 负责装配引擎并注入。**不改 llm / provider 适配层**（N6 跨协议一致天然成立）。

> 五层边界澄清：`permission.Engine.Check` 实现**前四层**（黑名单/沙箱/规则/模式兜底），以返回 `Ask` 作为「请走第五层」的信号；**第五层人在回路由 agent 在 Ask 后编排驱动**（发 Approval 事件、阻塞等决策）。二者合称五层。

- **permission 包（新增）**：定义 `Mode`（四档）、`Decision`（Allow/Deny/Ask）、`Category`（Read/Write/Exec）；实现前四层判定 `Engine.Check`；持有黑名单正则集、沙箱（项目根 + 符号链接解析）、三级规则集（user/project/local 三个配置文件）、模式兜底矩阵、友好名映射与路径提取。对外暴露 `Check`、本地规则持久化、配置加载。仅依赖 `llm`（取 `ToolCall`）与标准库。
- **agent 包（改造）**：`Mode` 类型迁移到 permission 包（`ModeNormal`→`ModeDefault`，新增 `ModeAcceptEdits`/`ModeBypass`）；`Agent` 持有 `*permission.Engine`；`executeBatched` 在执行每个工具前调用 `Engine.Check`——Allow 执行、Deny 直接产被拒结果、Ask 发 `Event{Approval}` 并阻塞等用户决策；新增 `ApprovalRequest` 事件类型与决策回传通道。plan 档的只读工具集与提醒沿用 ch04（键 `mode==permission.ModePlan`）。
- **tui 包（改造）**：`Model.mode` 改为 `permission.Mode`，持有 `*permission.Engine`；新增 `stateApproving` 态与待批准请求渲染/按键处理；**全局 ctrl+c/esc 分派从仅 `stateStreaming` 扩展到 `stateStreaming || stateApproving`**（见下，否则 approving 态 ctrl+c 会退出整个程序）；**新增全局 `shift+tab` 按键循环切换权限模式**（仅 idle 态生效）；状态栏左侧改为**常驻显示当前权限模式（取代 provider 名）**；把会话/永久放行的规则写入交给引擎（经 agent 在 Loop 内应用，TUI 只回传用户选择）。
- **main（改造）**：用项目根（`os.Getwd()` 解析符号链接）构造 `permission.NewEngine`、注入 tui。
- **smoke（改造）**：非交互，以 `ModeBypass` 运行（无法人在回路、避免阻塞在 Ask），构造一个根于 `cwd` 的引擎。

数据流（单个工具调用）：
```
agent.executeBatched(calls, mode)
  └→ readOnly 实参由批类型决定（只读批=true / 串行批=false，等价于 registry.IsReadOnly(name)）
     d, reason := Engine.Check(mode, call, readOnly)   // 前四层，短路：
       ① 黑名单(仅 Exec 类)  → 命中 Deny
       ② 沙箱(仅文件类)      → 逃逸 Deny
       ③ 规则引擎(三级)      → 命中 allow→Allow / deny→Deny
       ④ 模式兜底矩阵        → Allow 或 Ask
  d==Allow → tool.Execute
  d==Deny  → llm.ToolResult{ToolCallID, Content:reason, IsError:true} 回灌
  d==Ask   → (第五层) emit Event{Approval:{name,args,reason,Respond}} → 阻塞 <-Respond
              用户三选一(↑↓+回车 / 数字键 1·2·3) → AllowOnce(执行) /
                        AllowForever(eng.PersistLocalAllow+执行) / DenyOnce(回灌)
```

## 核心数据结构### permission.Mode（迁移自 agent + 扩展）
```go
type Mode int
const (
    ModeDefault   Mode = iota // 只读 Allow / 文件写 Ask / 命令执行 Ask
    ModeAcceptEdits           // 文件写 Allow / 命令执行 Ask
    ModePlan                  // 仅只读工具可见（沿用 ch04）；矩阵同 default 作防御兜底
    ModeBypass                // 全 Allow（黑名单/沙箱仍拦）
)
func (Mode) String() string           // "default"/"acceptEdits"/"plan"/"bypassPermissions"
func ParseMode(s string) (Mode, bool) // 大小写不敏感识别四档名；未知返回 (ModeDefault,false)
```

### permission.Decision / Category
```go
type Decision int
const ( Allow Decision = iota; Deny; Ask )

type Category int
const ( CategoryRead Category = iota; CategoryWrite; CategoryExec )
```

### permission.Rule / RuleSet
```go
type Rule struct {
    Tool    string // 友好名：Bash/Read/Write/Edit/Glob/Grep
    Pattern string // 模式段；"" 表示匹配该工具全部调用
    Allow   bool   // true=allow，false=deny
}
type RuleSet struct { allow, deny []Rule } // 单层规则集（一个文件或会话内存层）
func (RuleSet) match(friendly, target string) (Decision, bool) // 先 deny 再 allow；返回 (Allow|Deny, 命中?)
```

### permission.Settings（单个 YAML 文件结构，F4）
```go
type Settings struct {
    DefaultMode string `yaml:"defaultMode"` // 可选：default/acceptEdits/plan/bypassPermissions
    Permissions struct {
        Allow []string `yaml:"allow"`
        Deny  []string `yaml:"deny"`
    } `yaml:"permissions"`
}
```

### permission.Engine（核心，前四层 + 配置）
```go
type Engine struct {
    root      string           // 项目根（绝对、已解析符号链接）
    blacklist []*regexp.Regexp // 内置危险命令正则（不可配，N1）
    user, project, local RuleSet // 三级规则集
    localPath string           // 永久放行的写入目标（本地层文件）
    startMode Mode             // 启动默认模式（取自配置）
}
```

### permission.Outcome（人在回路三选一结果）
```go
type Outcome int
const (
    OutcomeDenyOnce     Outcome = iota // 拒绝本次
    OutcomeAllowOnce                   // 允许本次（不留规则）
    OutcomeAllowForever                // 永久允许（+写本地层文件，精确匹配）
)
```

### agent.ApprovalRequest / Event（新增，人在回路回路 F8）
```go
type ApprovalRequest struct {
    Name    string                  // 工具内部名（用于展示 ● name(args)）
    Args    string                  // 参数预览
    Reason  string                  // 触发 Ask 的原因（模式 + 类别）
    Respond chan permission.Outcome // 缓冲=1：TUI 回传用户选择，agent 单次接收
}
// Event 新增字段：
//   Approval *ApprovalRequest // 非空：请求人在回路批准，消费者须回传 Respond 后再续读事件
```

## 核心接口### permission 包
```go
// 构造：解析项目根、加载三层配置、编译黑名单、确定启动模式。
// 即使发生致命错误（仅当项目根不可解析时），也返回非 nil 的“空规则安全引擎”
// （root 退化为传入值、四层规则空、startMode=ModeDefault）+ err；配置文件格式错误绝不致错，只降级该文件为空。
func NewEngine(root string) (*Engine, error)

// 前四层判定（agent 每次执行工具前调用）；readOnly 由调用方按批类型给定（等价 registry.IsReadOnly）。
// 返回 (裁决, 原因)；原因文案见下「Decision→reason 来源表」。
func (*Engine) Check(mode Mode, call llm.ToolCall, readOnly bool) (Decision, string)

// 永久放行由 agent 在人在回路后应用（生成精确规则）：
func (*Engine) PersistLocalAllow(call llm.ToolCall) error // 永久：精确 allow 规则写入 local 文件 + 内存

func (*Engine) StartMode() Mode // 启动默认模式
```

**Check → reason 文案来源表**（统一文案，供 Deny 回灌与 Ask 展示一致）：

| 裁决来源 | reason 文案（示例） |
|---|---|
| 黑名单命中 | `命中危险命令黑名单：<命令片段>` |
| 沙箱逃逸 | `路径在项目目录之外：<target>` |
| deny 规则命中 | `匹配 deny 规则：<Tool(pattern)>` |
| 模式兜底 Ask | `<mode> 模式下 <category> 类操作需确认` |
| Allow（各来源） | `""`（空，无需展示） |

**内部辅助函数**（标注所属文件）：
```go
// settings.go：
func friendlyName(internal string) string            // bash→Bash, read_file→Read, write_file→Write, edit_file→Edit, glob→Glob, grep→Grep；未知原样
func categorize(internal string, readOnly bool) Category // 见下判定表
func extractTarget(call llm.ToolCall) (target string, isFile bool, ok bool) // 见下：内部 json.Unmarshal call.Input
// rule.go：
func parseRule(s string) (Rule, bool)
func matchPattern(pattern, target string) bool       // glob：* 任意串；** 仅文件路径跨段
// engine.go：
func modeFallback(mode Mode, cat Category) Decision   // F5 矩阵；只读/bypass→Allow，否则 Allow|Ask
// blacklist.go：
func hitsBlacklist(command string) bool
// sandbox.go：
func (*Engine) sandboxOK(path string) bool
```

**`categorize` 判定表**（区分 Write 与 Exec 靠内部名，因二者 readOnly 均为 false）：

| 内部工具名 | Category | 说明 |
|---|---|---|
| read_file / glob / grep（或 readOnly==true） | CategoryRead | 只读 |
| write_file / edit_file | CategoryWrite | 文件写 |
| bash | CategoryExec | 命令执行 |
| 未知/未注册工具（readOnly==false） | **CategoryExec** | N7 最严：归命令执行类，触发模式 Ask；但**黑名单层只对真正的 bash 命令短路**，未知工具因 `extractTarget` 取不到 command 而 isFile=false、target=""，不会被黑名单/沙箱拦，落到规则→模式兜底（Exec→Ask）。`readOnly==true` 一律 Read，优先于名字判定。 |

**`extractTarget` 解析与失败归属**（blocker 修复，关 N7/AC15）：
- 内部对 `call.Input`（`json.RawMessage`）做 `json.Unmarshal` 取字段：`read_file/write_file/edit_file` 取 `path`；`glob/grep` 取 `path`（**搜索根目录**，空→`"."`；注意：glob/grep 真正遍历目标是 `pattern`/`glob` 字段，沙箱只围栏其搜索根 `path`——见决策表「glob/grep 沙箱盲区」）；`bash` 取 `command`（isFile=false）。
- 返回 `(target, isFile, ok)`：`ok=false` 表示解析失败或缺必填字段。
- **失败归属**：
  - 文件类工具 `ok=false`（Input 不可解析 / 缺 path）→ `Check` 在沙箱层**直接判 Deny**（`无法解析文件路径参数，安全拒绝`），不静默放行。
  - bash `ok=false`（缺 command）→ command 视为空串，**不命中黑名单**（不短路），落到规则→模式兜底（Exec→Ask），由人在回路兜底，绝不直接 Allow。
  - 未知工具 → `isFile=false`、走 Exec 类模式兜底 Ask。

### agent 包（签名变更）
```go
func New(p llm.Provider, r *tool.Registry, version string, eng *permission.Engine) *Agent
func (a *Agent) Run(ctx context.Context, conv *conversation.Conversation, mode permission.Mode) <-chan Event
```

### tui 包
```go
// 现有签名保持 (Model, error) 不变，仅末尾增 engine 形参：
func New(providers []config.ProviderConfig, version string, registry *tool.Registry, engine *permission.Engine) (Model, error)
```

## 模块设计### permission 包
**职责：** 前四层判定、配置加载与合并、黑名单、沙箱、规则匹配、模式矩阵、会话/永久规则写入。
**关键点：**
- **`Check` 流水线（F6，短路）**：
  1. `cat==CategoryExec && target!="" && hitsBlacklist(target)` → `Deny`（N1，最高优先，bypass 也拦）。
  2. 文件类（`isFile`）：`ok==false` → `Deny`（路径参数不可解析）；否则 `!sandboxOK(target)` → `Deny`（N2）。
  3. 规则引擎：按 `local → project → user` 顺序，每层 `match(friendly, target)`；命中 allow→`Allow`、deny→`Deny`，**就近命中即返回**。
  4. 未命中 → `modeFallback(mode, cat)` → `Allow` 或 `Ask`。
- **黑名单（F1/N1）**：包内一组编译好的 `*regexp.Regexp` 常量集合，匹配命令串。示例模式：`rm\s+(-[a-zA-Z]*[rf][a-zA-Z]*\s+)+(/|~|\$HOME|/\*)`、`dd\s+.*of=/dev/`、`:\(\)\s*\{.*\|.*&\s*\}`（fork bomb）、`mkfs\.`、`>\s*/dev/(sd|hd|nvme|disk)`、`chmod\s+-R\s+0?777\s+/` 等。注释标明「启发式、非完备、不可配置放开」。
- **沙箱（F2/N2）**：`sandboxOK(path)`——空 path 视为 root；相对路径相对 `root` 解析；`resolved := evalSymlinksOrAncestor(abs)`（存在则 `filepath.EvalSymlinks`；不存在则逐级回退到最近**已存在祖先**目录 `EvalSymlinks` 后拼回剩余段）；返回 `resolved==root || strings.HasPrefix(resolved, root+sep)`。用 `filepath` 而非硬编码 `/`。
- **规则解析**：`parseRule("Bash(git *)")`→`{Tool:"Bash",Pattern:"git *"}`；`"Read"`→`Pattern:""`（全匹配）。加载时 allow/deny 两列分别解析；非法条目跳过并降级（N5）。
- **匹配（`matchPattern`）**：命令用「命令 glob」——`*` 匹配任意字符（含空格），其余字面，`**` 等价 `*`；文件路径用 `*`（段内）/`**`（跨段）匹配（可借鉴 `tool/glob.go` 的 `matchSegments` 思路），目标为项目相对 slash 路径。`Pattern==""` 恒匹配。
- **`PersistLocalAllow`**（人在回路「永久」调用）：据 `extractTarget` 生成**精确**规则（`Bash(<command>)` 或 `Write(<relpath>)` 等，无通配），追加进 local 文件的 `permissions.allow` 并重写 + 同步 `local` 层内存；失败仅返回 err（agent 侧记录不阻断执行）。
- **配置加载**：`loadSettings(path)`：文件不存在→空 `Settings`、nil err；`yaml.Unmarshal` 失败→零值 + err（`NewEngine` 据此降级跳过该文件，**不向上抛致命 err**）。`NewEngine` 顺序加载 user/project/local，`startMode` 依次取 local/project/user 的 `DefaultMode`（`ParseMode` 成功者，local 优先），皆空→`ModeDefault`。**唯一可能返回致命 err 的情形是 `resolveRoot` 失败**，此时仍返回非 nil 空规则安全引擎 + err。
**依赖：** `llm`（ToolCall）、`gopkg.in/yaml.v3`、标准库。不依赖 agent/tool/tui。

### agent 包（agent.go）
**职责：** 在工具执行链接入前四层判定；承载第五层人在回路；模式类型迁移。
**关键点：**
- `Mode` 相关常量/类型从 agent 删除，改用 `permission.Mode`；`Run` 形参与 `mode==permission.ModePlan` 判断更新；`defs` 选择、`PlanReminder` 注入逻辑不变（仅类型换名）。无论 plan 来自 `/plan` 命令还是 `defaultMode=plan` 配置，agent 一律按 `mode==ModePlan` 应用只读工具集 + 计划提醒。
- `Agent` 加 `eng *permission.Engine` 字段；`New` 增参。
- **被拒结果构造**：agent 包内无 `errResult`（那是 tool 包未导出函数）；Deny 分支直接构造 `llm.ToolResult{ToolCallID:calls[k].ID, Content:reason, IsError:true}`（与既有 executeBatched 结果构造一致）。
- `executeBatched(ctx, calls, mode, ch)`（增 `mode` 形参）：
  - **只读批**：对区间内每个 `calls[k]` 先 `d,reason:=eng.Check(mode,calls[k],true)`；按调用序发 `PhaseStart`；`d==Deny`→ 预置 `results[k]=ToolResult{...,Content:reason,IsError:true}`、`done[k]=true`、**不纳入并发执行**；`d==Allow`→ 纳入并发（只读永不 Ask，N3 并发不退化）。并发执行完后按调用序发 `PhaseEnd`（Deny 项 `IsError=true`、Allow 项为真实结果），**Deny 与 Allow 项的开始/结束事件均按调用序**，与有副作用 Deny 行为一致。
  - **有副作用串行**：`d,reason:=eng.Check(mode,calls[i],false)`；`Allow`→执行；`Deny`→`ToolResult{...,Content:reason,IsError:true}`；`Ask`→`a.requestApproval(ctx,calls[i],reason,ch)` 拿 `Outcome`：`!ok`→取消收尾（`completed=false`，沿用既有取消路径）；AllowOnce→执行；AllowForever→`eng.PersistLocalAllow`（err 仅记不阻断）+执行；DenyOnce→被拒结果。
- `requestApproval(ctx, call, reason, ch) (permission.Outcome, bool)`：`respond:=make(chan permission.Outcome,1)`；`emit(Event{Approval:&ApprovalRequest{Name:call.Name,Args:argsPreview(call.Input),Reason:reason,Respond:respond}})`；`select{ case o:=<-respond: (o,true); case <-ctx.Done(): (0,false) }`。

### tui 包（tui.go / stream.go / view.go；select.go 不动）
**职责：** 新增待批准交互态；模式切换命令；状态栏模式徽标；全局取消覆盖 approving 态。
**关键点：**
- `Model`：`mode agent.Mode`→`mode permission.Mode`（初值 `engine.StartMode()`）；加 `engine *permission.Engine`、`pending *agent.ApprovalRequest`。
- `New(providers, version, registry, engine)`（保持返回 `(Model, error)`）：存引擎、置初始模式。
- **全局按键分派（blocker 修复）**：`Update` 顶部 `ctrl+c`/`esc` 的 `m.state==stateStreaming` 条件改为 `m.state==stateStreaming || m.state==stateApproving`；在 approving 态触发取消时，先向 `m.pending.Respond` 送 `OutcomeDenyOnce`（缓冲=1 不阻塞，兜底解 agent 阻塞），再 `m.turnCancel()`。
- `updateStreaming` 处理 `msg.Approval != nil`：保存 `m.pending`、切 `stateApproving`，**返回 nil（不 `waitForEvent`）**——agent 正阻塞等回传。
- `updateApproving`：维护光标 `approveCursor`（0/1/2，进入 approving 态时由 `updateStreaming` 重置为 0）；`up`/`k`、`down`/`j` 循环移动光标；`enter` 提交当前光标项；数字键 `1`/`2`/`3` 直选并提交；另 `y`=允许本次、`n`/`d`=拒绝本次 便捷键。索引→`Outcome` 由 `outcomeForIndex` 显式映射（0=AllowOnce、1=AllowForever、2=DenyOnce）。选定后回 `stateStreaming`、清 `pending`，返回 `tea.Batch(sendOutcome(req,outcome), waitForEvent(m.events))`（`sendOutcome` 是把 `outcome` 送入 `req.Respond` 的 `tea.Cmd`）。
- `View`/`stateApproving`：渲染**多行待批准块**——`● <动作名>` + 缩进参数预览、灰字触发原因、`是否继续?`、三行菜单（当前光标项以 `> ` + 高亮色，其余 `  ` 前缀）`1. 允许本次 / 2. 永久允许（写入本地配置） / 3. 拒绝本次`、底部灰字 `↑↓ 选择 · 回车确认 · Esc 取消`；`approvalBlock(req, cursor)` 据 `cursor` 高亮当前项。
- **Shift+Tab 循环切换**：在 `Update` 顶部全局 KeyPress 分支加 `case "shift+tab":`（仅 `stateIdle` 生效，streaming/approving 态忽略）；`m.mode = nextMode(m.mode)`，`nextMode` 即 `(mode+1)%4`，循环 default→acceptEdits→plan→bypassPermissions→default（四档全循环，含 bypass，用户拍板）；打印一行 noticeBlock 提示新模式。切到/切出 plan 时同样作用于 agent（mode==ModePlan 即只读 defs + PlanReminder），但 Shift+Tab **不**注入 `/do` 的执行指令。
- `submit`：保留 `/plan`(→ModePlan)`/do`(→ModeDefault，固定回 default 并注入执行指令)`/exit`，作为计划工作流的专用入口/出口；**不再新增 `/mode` 命令**（模式切换统一走 Shift+Tab）。
- `statusBar`：左侧改为**常驻显示当前权限模式**（取代 provider 名）：ModeDefault→`DEFAULT`(灰/绿)、ModeAcceptEdits→`ACCEPT EDITS`、ModePlan→`PLAN`(黄)、ModeBypass→`BYPASS`(红)；右侧保留模型名 + token 用量不变。可在启动提示行（prompt 包的 ready hint）补「Shift+Tab 切换权限模式」。

### main / smoke
- `main.go`：`root,_:=os.Getwd()`；`eng,err:=permission.NewEngine(root)`；`err!=nil` 仅 `fmt.Fprintln(os.Stderr,"权限引擎降级:",err)` 后**继续**（`eng` 必非 nil——NewEngine 致命错也返回空规则安全引擎）；`m,err:=tui.New(cfg.Providers, version, registry, eng)`。
- `smoke/main.go`：新增 `cwd,_:=os.Getwd()`（import os）；`eng,_:=permission.NewEngine(cwd)`；`agent.New(p, tool.NewDefaultRegistry(), "dev", eng)`；`Run(ctx, conv, permission.ModeBypass)`。确认 smoke 现有用例文件操作目标均在 cwd 子树内（否则会被沙箱拦）。

## 模块交互

```
main → permission.NewEngine(root) → tui.New(..., eng)
TUI ─按 shift+tab→ m.mode 循环切换 default→acceptEdits→plan→bypass→default（跨轮保持）
TUI ─beginTurn→ agent.New(provider, registry, version, eng).Run(ctx, conv, m.mode)
  agent.executeBatched(calls, mode):
    d, reason = eng.Check(mode, call, readOnly(批类型))   // 前四层
    Allow → tool.Execute
    Deny  → ToolResult{Content:reason,IsError:true}  ──回灌──→ conv.AddToolResults
    Ask   → Event{Approval{...,Respond}} ──→ TUI(stateApproving)   // 第五层（三选一菜单）
                                          ←── Respond<-Outcome ──
            AllowForever → eng.PersistLocalAllow(call) (写本地层文件)
            → 执行(AllowOnce/AllowForever) 或 回灌(DenyOnce)
```

依赖方向（无环）：`tui → {agent, permission, config, llm, ...}`；`agent → {permission, llm, tool, conversation, prompt}`；`permission → llm`。`llm` 不变、不 import permission。

## 文件组织

```
mewcode/
├── internal/permission/
│   ├── mode.go            — 新:Mode 四档 + String/ParseMode;Decision/Category;Outcome
│   ├── engine.go          — 新:Engine、NewEngine、Check 前四层流水线、modeFallback、StartMode
│   ├── blacklist.go       — 新:内置危险命令正则集 + hitsBlacklist（不可配，N1）
│   ├── sandbox.go         — 新:sandboxOK、evalSymlinksOrAncestor、resolveRoot（N2）
│   ├── rule.go            — 新:Rule/RuleSet、parseRule、match、matchPattern(glob)
│   ├── settings.go        — 新:Settings YAML、loadSettings、toRuleSet、friendlyName、categorize、extractTarget
│   ├── persist.go         — 新:PersistLocalAllow、ruleFor（写本地层文件）
│   └── *_test.go          — 新:黑名单/沙箱(含祖先回退)/规则/优先级/矩阵/加载降级/解析失败 单测
├── internal/agent/
│   ├── agent.go           — 改:删 Mode（迁 permission）;Agent 加 eng;executeBatched(+mode)接入 Check;requestApproval;ApprovalRequest/Event.Approval;Deny 用 llm.ToolResult 构造
│   └── agent_test.go      — 改/新:权限集成(Allow/Deny/Ask/会话/永久)、保序、只读并发不退化、取消、模式迁移
├── internal/tui/
│   ├── tui.go             — 改:Model.mode→permission.Mode、加 engine/pending;New 增参;stateApproving 分派;全局 ctrl+c/esc 覆盖 approving;shift+tab 循环模式(nextMode)
│   ├── stream.go          — 改:updateStreaming 处理 Approval;updateApproving;sendOutcome;submit 保留 /plan·/do(去掉 /mode);beginTurn 传 eng
│   ├── view.go            — 改:statusBar 左侧常驻模式(取代 provider 名);待批准块渲染
│   └── tui_test.go        — 改:shift+tab 循环切换、approval 态按键回传、Esc 取消兜底、状态栏常驻模式、模式跨轮保持
├── internal/config/       — 不改（provider 配置与 permission settings 分离）
├── cmd/mewcode/main.go    — 改:构造 permission.Engine 注入 tui
├── cmd/smoke/main.go      — 改:cwd + 构造引擎、ModeBypass 运行
├── .gitignore             — 改:加 .mewcode/settings.local.yaml
└── .mewcode/settings.yaml.example — 新:权限配置示例（defaultMode + allow/deny）
```

## 技术决策

| 决策点 | 选择 | 理由 |
|--------|------|------|
| 权限判定落点 | 独立 permission 包(前四层) + agent 编排层(第五层) | 与 provider 解耦（N6 免费）；逻辑内聚、可单测；不污染 tool/llm |
| 五层短路 | `Check` 顺序 黑名单→沙箱→规则→模式 单函数 early-return；Ask 作第五层信号 | 满足 F6；黑名单/沙箱按类别跳过；规则就近命中即返回；人在回路在 agent |
| 黑名单不可配 | 包内编译好的正则常量、无加载入口 | N1：任何配置/模式都碰不到它；bypass 也拦 |
| 黑名单完备性 | 启发式、显式声明非完备 | 不可能穷尽危险命令；防御纵深由沙箱+规则+人在回路补 |
| 沙箱解析顺序 | 先 EvalSymlinks（或最近祖先）再前缀比对 | N2：防软链接逃逸；新建文件按已存在祖先判，避免误判 |
| 沙箱不管命令执行 | Bash 不做路径围栏 | 无法可靠静态解析任意命令的文件访问；交黑名单+规则+模式 |
| glob/grep 沙箱盲区 | extractTarget 取其搜索根 `path` 做围栏；`pattern` 不参与沙箱 | glob/grep 真正遍历目标是 pattern，但任意 pattern 的越界遍历由工具内部 `WalkDir`(不跟随目录软链接)限制；沙箱对 glob/grep 为**尽力围栏搜索根**，登记为已知盲区 |
| Mode 归属 | 迁到 permission 包、四档统一 | 用户拍板「统一一个模式轴」；mode 是权限概念，agent/tui 共用 |
| 模式切换方式 | Shift+Tab 循环四档（含 bypass）；保留 /plan·/do | 用户拍板用 Shift+Tab、四档全循环；/plan·/do 保留计划工作流的执行语义；不再设 /mode 命令 |
| 状态栏左侧内容 | 常驻显示当前权限模式，取代 provider 名 | 用户拍板「别展示 provider 名、展示权限模式」；右侧模型名+用量不变 |
| plan 语义 | 沿用 ch04 硬限制（只读工具集+提醒）+ /do | 用户拍板；矩阵 plan 行仅防御性兜底；/plan 与 defaultMode=plan 都按 ModePlan 应用 |
| 模式兜底值域 | 只产 Allow/Ask（无 Deny 档） | 用户拍板矩阵；Deny 仅来自黑名单/沙箱/deny 规则/人在回路 |
| 规则优先级 | 会话>本地>项目>用户；同层 deny 优先 allow | 用户拍板「越靠近会话越优先」；deny 优先更安全 |
| 永久放行落点 | 写本地层 `.mewcode/settings.local.yaml`（gitignore） | 用户拍板；不进 git、不影响队友（对齐 Claude Code don't-ask-again） |
| 自动规则泛化 | 不泛化，只生成精确规则 | 自动猜泛化模式有误放行风险；泛化交用户手写 |
| 规则名 | 友好名 Bash/Read/Write/Edit/Glob/Grep ↔ 内部名映射 | 用户示例即友好名；对齐 Claude Code 习惯，规则更可读 |
| 参数解析失败归属 | 文件类不可解析→Deny；bash 缺 command→落 Ask；未知工具→Exec/Ask | N7/AC15 安全默认，绝不静默 Allow |
| 人在回路选项集 | 三选一（允许本次/永久/拒绝）+ 菜单式 ↑↓·回车·数字键直选、默认高亮允许本次 | 用户拍板 1:1 复刻 Claude Code；永久=精确写本地配置；砍掉本会话 Outcome（会话级层移除，规则只走三个文件层） |
| 人在回路回路 | Event{Approval{Respond chan}} + agent 阻塞 select | bubbletea 单线程 Update 不能阻塞；事件+回传通道是其惯用法；ctx 取消可解阻塞（N4） |
| Respond 通道 | 缓冲=1 | TUI 送决策永不阻塞；取消竞态下兜底送 DenyOnce 不泄漏 |
| approving 态取消 | 全局 ctrl+c/esc 分派覆盖 stateApproving | 否则 approving 态 ctrl+c 走 tea.Quit 退出程序，违 N4 |
| 会话/永久规则写入方 | agent 在 Loop 内调引擎（TUI 只回传 Outcome） | 引擎状态变更集中一处；职责清晰 |
| 只读权限检查 | 批内逐个 Check，但只读永不 Ask | N3：保留 ch04 并发；只读最多被沙箱/deny 规则拦为 Deny，无需交互 |
| settings 与 config 分离 | 新 settings.yaml(.local) 而非塞进 config.yaml | 权限配置与 provider 凭据职责不同；config.yaml 已精确 gitignore（含密钥），settings 项目级需可提交 |
| smoke 运行模式 | ModeBypass、根于 cwd | 非交互无法人在回路；bypass 跳过 Ask（黑名单/沙箱仍在），用例文件操作须落 cwd 内 |
| NewEngine 失败处理 | 致命错(仅 resolveRoot)也返回非 nil 空规则安全引擎 + err | main 注入永不为 nil、Check 不 panic；配置格式错只降级不致错（N5） |