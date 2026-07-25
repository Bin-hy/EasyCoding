package hook

import (
	"context"
	"fmt"
	"os"
	"sync"
)

// Engine Hook 事件分派引擎。
// 持有已编译规则列表、only_once 触发记录、加载来源文件列表。
type Engine struct {
	rules   []Rule   // 按加载顺序（YAML 声明序）
	sources []string // 加载来源文件路径列表

	mu        sync.Mutex
	onceFired map[string]bool // only_once 已触发的 hook name
}

// DispatchResult 一次事件分派的汇总结果。
type DispatchResult struct {
	Blocked          bool     // 是否有拦截类 hook 命中并表达拦截
	Reason           string   // 拦截原因
	BlockingHookName string   // 触发拦截的 hook name
	InjectedPrompts  []string // prompt 动作产生的文本，按声明序
}

// NewEngine 构造 Hook 引擎。
func NewEngine(rules []Rule, sources []string) *Engine {
	return &Engine{
		rules:      rules,
		sources:    sources,
		onceFired:  make(map[string]bool),
	}
}

// Dispatch 按事件分派规则：匹配 event → 跳过 onceFired → 求值条件 → 执行动作。
//
// 流程：
//  1. 过滤匹配 event 的 rule
//  2. 跳过 onceFired 中已触发的 only_once rule
//  3. 串行求值 if 条件
//  4. 命中条件后按 action.type 分发到 Executor
//  5. async 起 goroutine 后立即继续；同步等结果
//  6. 拦截类事件下首个表达 Blocked 的结果中断后续同事件 rule
//  7. prompt 动作的 text 累加到 InjectedPrompts
//
// ctx 取消时同步等待、async 执行中都会及时退出。
func (e *Engine) Dispatch(ctx context.Context, event Event, payload Payload) DispatchResult {
	var result DispatchResult
	exec := NewExecutor()

	for i := range e.rules {
		rule := &e.rules[i]

		// ① 事件不匹配
		if rule.Event != event {
			continue
		}

		// ② only_once 已触发
		e.mu.Lock()
		if rule.OnlyOnce && e.onceFired[rule.Name] {
			e.mu.Unlock()
			continue
		}
		e.mu.Unlock()

		// ③ 条件求值
		if !EvalCondition(rule.If, payload) {
			continue
		}

		// ④ async 执行
		if rule.Async {
			go func(r Rule) {
				execRes := exec.Run(context.Background(), r, payload, false)
				if execRes.Err != nil {
					fmt.Fprintf(os.Stderr, "[hook %s] %s failed: %v\n", r.Name, event, execRes.Err)
				}
				// async 不进入 InjectedPrompts、Blocked 判定
			}(*rule)
			e.markOnce(rule)
			continue
		}

		// ⑤ 同步执行
		blocking := IsBlocking(event)
		execRes := exec.Run(ctx, *rule, payload, blocking)

		// ⑥ 处理结果
		if execRes.Err != nil {
			fmt.Fprintf(os.Stderr, "[hook %s] %s failed: %v\n", rule.Name, event, execRes.Err)
			e.markOnce(rule)
			continue // hook 自身失败不拦截
		}

		if execRes.Prompt != "" {
			result.InjectedPrompts = append(result.InjectedPrompts, execRes.Prompt)
		}

		if execRes.Blocked && blocking {
			result.Blocked = true
			result.Reason = execRes.Reason
			result.BlockingHookName = rule.Name
			e.markOnce(rule)
			break // 首个拦截命中后中断后续
		}

		e.markOnce(rule)
	}

	return result
}

// markOnce 将 rule 的 name 加入 onceFired 集合（仅当 OnlyOnce 为 true 时）。
func (e *Engine) markOnce(rule *Rule) {
	if !rule.OnlyOnce {
		return
	}
	e.mu.Lock()
	e.onceFired[rule.Name] = true
	e.mu.Unlock()
}

// ResetForNewSession 清空 only_once 集合（/clear、/resume 进新会话时调用）。
func (e *Engine) ResetForNewSession() {
	e.mu.Lock()
	e.onceFired = make(map[string]bool)
	e.mu.Unlock()
}

// Rules 返回规则列表（仅读，供 /hooks 命令使用）。
func (e *Engine) Rules() []Rule {
	return e.rules
}

// Sources 返回加载来源文件路径列表。
func (e *Engine) Sources() []string {
	return e.sources
}
