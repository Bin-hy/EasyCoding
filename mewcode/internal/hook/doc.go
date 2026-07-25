// Package hook 提供 Agent 生命周期挂钩系统。
//
// 在 Agent 生命周期的 11 个固定时刻挂自动化动作，用 YAML 声明式
// 配置"事件 + 条件 + 动作"三要素规则，在事件 emit 时由引擎分派执行。
//
// 四大动作类型：
//   - shell：执行 shell 命令，通过 exit code 2 表达拦截
//   - prompt：注入提示词到下一轮 reminder 区
//   - http：发 HTTP 请求，通过响应 body {"decision":"block"} 表达拦截
//   - subagent：启动子 Agent（本期占位，仅打日志）
//
// 复用 permission.Matcher 做条件表达式底层匹配。
package hook
