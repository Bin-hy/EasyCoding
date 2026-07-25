# Hook 生命周期挂钩系统 Checklist

> 每一项通过运行代码或观察行为来验证,聚焦系统行为。

## 实现完整性### 权限匹配器扩展

- [ ] permission.Matcher 接口存在,四种实现(exact / glob / regex / not)各自可单独编译并运行(验证：`go test ./internal/permission/ -run TestMatcher -v` 通过)
- [ ] permission.Rule 已替换 Pattern 为 Matcher 字段,parseRule 能识别 `=` / `~` / `!` 前缀(验证：`go test ./internal/permission/ -run TestParseRule -v` 通过)
- [ ] toRuleSet 在 parseRule 失败时输出 stderr 错误日志(验证：单测构造非法 rule 串,捕获 os.Stderr 输出含 `parse failed`)

### Hook 包

- [ ] internal/hook 包存在且编译通过(验证:`go build ./internal/hook/...`)
- [ ] 11 个 Event 常量全部声明且 IsBlocking 仅对 PreToolUse / UserPromptSubmit 返回 true(验证:`go test ./internal/hook/ -run TestEvent -v`)
- [ ] Loader 能解析合法 YAML 并构造 Engine(验证:loader_test 全部通过)
- [ ] Loader 对字段缺失 / 枚举错 / async+拦截事件冲突 / matcher 编译失败均报 stderr 并跳过该条(验证:对应 loader_test 子用例通过)
- [ ] Engine.Dispatch 按声明顺序执行 rule 且拦截后中断后续(验证:engine_test TestDispatchBlocking 通过)
- [ ] Executor 的 shell exit 2 触发 Blocked、exit 0 放行、其它非 0 视为失败不拦截(验证:executor_test TestRunShell* 通过)
- [ ] Executor 的 HTTP 在 body 含 `{"decision":"block","reason":"..."}` 时触发 Blocked(验证:executor_test TestRunHttp* 通过)
- [ ] Executor 的 prompt 动作通过 ExecutionResult.Prompt 字段返回文本(验证:executor_test TestRunPrompt 通过)
- [ ] Executor 的 subagent 动作仅 stderr 输出占位日志、不阻塞(验证:executor_test TestRunSubagent 通过)
- [ ] only_once 状态在 SessionRuntime 上,/clear 与 /resume 时被 ResetForNewSession 清空(验证:runtime_test TestResetForNewSession 通过)

### agent / tui 集成

- [ ] agent.WithHookEngine 选项存在,agent 内部 dispatchHook 在 11 个 emit 点全部调用(验证:agent_test TestHookEmit 覆盖每个事件)
- [ ] tui submit() 在 UserPromptSubmit 拦截时不消费 textarea、显示 errorBlock(验证:tui_test TestSubmitBlocked 通过)
- [ ] tui Init() 末尾派发 SessionStart 事件(验证:tui_test TestInitDispatchSessionStart 或集成测试)
- [ ] /clear / /resume / /exit 触发 SessionEnd(验证:tui_test TestClearDispatchSessionEnd 等)
- [ ] main.go 退出前兜底 SessionEnd(验证:main 调用链审查)
- [ ] /hooks 命令注册到命令表(验证:tui_test 中 /hooks 命令存在 + 输出格式正确)
- [ ] PendingReminders 在 Agent.Run 取出后被清空(验证:runtime_test TestTakeReminders 通过)

## 集成

- [ ] hook.Engine 与 permission.Matcher 共用同一套匹配实现(验证:hook 包不重复实现 exact/regex/glob)
- [ ] hook.Engine 接入 agent.Run 后所有现有 agent_test 不破坏(验证:`go test ./internal/agent/...` 全过)
- [ ] hook.Engine 接入 tui 后所有现有 tui_test 不破坏(验证:`go test ./internal/tui/...` 全过)
- [ ] PreToolUse 拦截结果当 tool_result 回灌后,LLM 视角看到的是 `IsError=true` 的 ToolResult,Content 含 `[hook <name>] <reason>`(验证:agent_test 检查 results[k] 字段)
- [ ] reminder 注入路径与 plan reminder 协同——同一轮 LLM 请求的 reminder 串同时含两类(验证:agent_test 中构造 plan 模式 + hook prompt 注入,断言 reminder 串包含两段)

## 编译与测试

- [ ] 项目编译无错误:`go build ./...`
- [ ] 所有单元测试通过:`go test ./...`
- [ ] vet 检查通过:`go vet ./...`(无配置 lint 则跳过)

## 端到端场景(tmux 实跑)

每个场景在 tmux 内启动一个 mewcode 实例完成,验证人工/可视化行为。

### 场景 1:PreToolUse shell 拦截 write_file**预置:** 在 `.mewcode/hooks.yaml` 写一条 hook:
```yaml
hooks:
  - name: block-write
    event: PreToolUse
    if:
      all_of:
        - field: tool_name
          match: { type: exact, value: write_file }
    action:
      type: shell
      command: "echo blocked by hook >&2; exit 2"
```

**步骤:**
- [ ] tmux 启动 mewcode
- [ ] 给 LLM 输入"创建一个文件 hello.txt 内容是 hi"
- [ ] LLM 应触发 write_file,工具被拦截
- [ ] scrollback 内 tool_result 显示 `[hook block-write] blocked by hook`、文件未创建
- [ ] LLM 收到反馈后调整回应,不死循环

### 场景 2:SessionStart prompt 注入**预置:**
```yaml
hooks:
  - name: zh-cn-default
    event: SessionStart
    action:
      type: prompt
      text: "默认用 zh-CN 回复"
```

**步骤:**
- [ ] tmux 重启 mewcode
- [ ] 立刻发一句英文输入"hi there"
- [ ] LLM 应该用中文回复(因为 reminder 区注入了 zh-CN 指令)

### 场景 3:PostToolUse async shell 后台 gofmt**预置:**
```yaml
hooks:
  - name: gofmt-after-write
    event: PostToolUse
    if:
      all_of:
        - field: tool_name
          match: { type: exact, value: write_file }
        - field: tool_input.path
          match: { type: glob, value: "**/*.go" }
        - field: is_error
          match: { type: exact, value: "false" }
    action:
      type: shell
      command: "gofmt -w \"$(jq -r .tool_input.path)\""
    async: true
    timeout: 5s
```

**步骤:**
- [ ] tmux 启动 mewcode
- [ ] 让 LLM 写一个故意排版不整齐的 Go 文件(如缩进错乱)
- [ ] LLM 完成写入后主对话立即进入下一轮,不停顿
- [ ] 验证文件被 gofmt 格式化(可手动 `cat` 该文件)

### 场景 4:UserPromptSubmit 拦截 delete 关键字**预置:**
```yaml
hooks:
  - name: warn-delete
    event: UserPromptSubmit
    if:
      all_of:
        - field: prompt
          match: { type: regex, value: "(?i)delete" }
    action:
      type: shell
      command: "echo \"用户消息含 delete 关键字\" >&2; exit 2"
```

**步骤:**
- [ ] tmux 启动 mewcode
- [ ] 输入"请帮我 delete 那个文件"
- [ ] 输入被拦截,scrollback 内显示 `[hook warn-delete] 用户消息含 delete 关键字`
- [ ] 输入框内容仍在(被退回用户重新编辑)
- [ ] LLM 端未收到这条 user 消息(不发起请求)

### 场景 5:Stop HTTP 通知**预置:**
- 本地起 echo server:`python3 -m http.server 9999 --bind 127.0.0.1` 或 nc -l
```yaml
hooks:
  - name: notify-stop
    event: Stop
    action:
      type: http
      url: "http://127.0.0.1:9999/done"
      method: POST
```

**步骤:**
- [ ] tmux 启动 mewcode
- [ ] 让 LLM 简单回答一个问题后停止
- [ ] echo server 收到一次 POST,body 含 `"event":"Stop"`

### 场景 6:only_once + PreUserMessage**预置:**
```yaml
hooks:
  - name: first-turn
    event: PreUserMessage
    only_once: true
    action:
      type: shell
      command: "echo first-turn-fired >&2"
```

**步骤:**
- [ ] tmux 启动 mewcode
- [ ] 第一轮发任意消息,stderr 出现 `first-turn-fired`
- [ ] 第二轮发消息,stderr 没有再次出现
- [ ] 执行 `/clear` 进新会话,再发消息,stderr 重新出现 `first-turn-fired`

### 场景 7:错误配置不阻断启动**预置:** hooks.yaml 含一条非法 hook:
```yaml
hooks:
  - name: bad-async
    event: PreToolUse
    async: true
    action:
      type: shell
      command: "echo x"
  - name: good-hook
    event: SessionStart
    action:
      type: shell
      command: "echo ok"
```

**步骤:**
- [ ] tmux 启动 mewcode
- [ ] mewcode 启动期 stderr 打印 `hook "bad-async": async not allowed for blocking events, skipped`
- [ ] mewcode 仍然成功进入 idle 状态
- [ ] `/hooks` 命令仅列出 good-hook、未列 bad-async

### 场景 8:/hooks 命令**预置:** 一份包含 3 条合法 hook 的 hooks.yaml(任意 event 组合)

**步骤:**
- [ ] tmux 启动 mewcode
- [ ] 输入 `/hooks` 回车
- [ ] 输出按 event 分组,每条一行 `  <name>  <event>  <action.type>  [flags]`
- [ ] 末尾显示 `Loaded from: .../hooks.yaml`

### 场景 9:端到端组合(AC17)**预置:** hooks.yaml 包含场景 1、2、3、4 全部 hook

**步骤:**
- [ ] tmux 启动 mewcode
- [ ] 首轮:SessionStart 注入 zh-CN(场景 2),Agent 准备就绪
- [ ] 输入"帮我创建 hello.go,然后 gofmt 一下"
- [ ] LLM 调 write_file 创建文件 → 被场景 1 的 hook 拦截 →LLM 重试(可能换 edit_file)或换 bash 调 gofmt
- [ ] 整个过程不卡顿、无 panic
- [ ] /hooks 命令仍可工作显示 4 条 hook