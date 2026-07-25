package agent

import (
	"context"
	"errors"
	"fmt"

	"mewcode/internal/compact"
	"mewcode/internal/conversation"
	"mewcode/internal/hook"
	"mewcode/internal/llm"
	"mewcode/internal/permission"
	"mewcode/internal/prompt"
)

// ErrMaxTurnsReached 表示子 Agent 触达最大迭代轮数。
var ErrMaxTurnsReached = errors.New("达到最大轮数")

// maxUnknownRunSub 子 Agent 连续未知工具上限（比主 Agent 更严格）。
const maxUnknownRunSub = 2

// RunToCompletion 执行子 Agent 的"跑到底"循环（spec F9）。
//
// 复用主 Run 的 streamOnce / executeBatched / manageContextAuto 逻辑，区别：
//   - 不通过 channel 返回事件（由 events 参数透传），最终返回 finalText
//   - maxTurns 由 a.maxTurns 决定（若 0 则用 maxIterations）
//   - 不触发 memory update（子 Agent 上下文短，不需要）
//   - 接受可选的 events 通道，把内部事件转发出去供 TaskManager/TUI 消费
func (a *Agent) RunToCompletion(ctx context.Context, conv *conversation.Conversation, task string, events chan<- Event) (string, error) {
	// 把 task 作为 user 消息追加（如果非空）
	if task != "" {
		conv.AddUser(task)
	}

	// 确保运行时初始化
	if a.runtime == nil {
		a.runtime = &SessionRuntime{ContextWindow: 200000}
	}

	// 计算 maxTurns
	turns := a.maxTurns
	if turns == 0 {
		turns = maxIterations
	}

	// 采集环境信息与装配系统提示
	env := prompt.GatherEnvironment(a.version, a.provider.Model())

	// Skill catalog 注入
	var skillsCatalogText string
	if a.catalog != nil {
		items := a.catalog.ToPromptItems()
		promptItems := make([]prompt.SkillCatalogItem, len(items))
		for i, item := range items {
			promptItems[i] = prompt.SkillCatalogItem{Name: item.Name, Description: item.Description}
		}
		skillsCatalogText = prompt.RenderSkillsCatalog(promptItems)
	}
	sys := prompt.BuildSystemPrompt(a.instructionText, a.memoryText, skillsCatalogText)

	// 如果子 Agent 有自定义系统提示，使用它
	if a.systemPrompt != "" {
		sys = a.systemPrompt
	}

	// 权限模式
	mode := permission.ModeDefault
	if a.permissionModeSet {
		mode = a.permissionMode
	}

	unknownRun := 0

	for iter := 1; iter <= turns; iter++ {
		// 检查 ctx 取消
		if ctx.Err() != nil {
			return lastAssistantText(conv), ctx.Err()
		}

		// 按 mode 取工具集
		var defs []llm.ToolDefinition
		if mode == permission.ModePlan {
			defs = a.registry.ReadOnlyDefinitions()
		} else if len(a.allowedTools) > 0 {
			defs = a.registry.DefinitionsFiltered(a.allowedTools)
		} else {
			defs = a.registry.Definitions()
		}

		// 上下文管理
		anchor, anchorLen := a.runtime.GetAnchor()
		cw := a.runtime.ContextWindow
		est := compact.EstimateTokens(anchor, conv.Messages(), anchorLen)

		in := compact.ManageInput{
			Conv:           conv,
			Provider:       a.provider,
			ContextWindow:  cw,
			ToolDefs:       defs,
			Replacement:    a.runtime.Replacement,
			Recovery:       a.runtime.Recovery,
			AutoTracking:   a.runtime.AutoTracking,
			Session:        a.runtime.Session,
			UsageAnchor:    anchor,
			AnchorMsgLen:   anchorLen,
			EstimatedToken: est,
			Trigger:        compact.TriggerAuto,
		}

		willSummarize := est >= int64(cw-compact.SummaryReserve-compact.AutoSafetyMargin)
		if willSummarize {
			a.dispatchHook(ctx, hook.EventPreCompact, a.basePayload(hook.EventPreCompact, mode))
			emitEvent(events, Event{Compact: &CompactEvent{Phase: CompactPhaseBeforeAuto}})
		}

		out, mcErr := compact.ManageContext(ctx, in)

		if willSummarize {
			a.dispatchHook(ctx, hook.EventPostCompact, hook.Payload{
				"event":         "PostCompact",
				"trigger":       "auto",
				"before_tokens": out.BeforeTokens,
				"after_tokens":  out.AfterTokens,
			})
			emitEvent(events, Event{Compact: &CompactEvent{
				Phase: CompactPhaseAfterAuto, Before: out.BeforeTokens, After: out.AfterTokens, Err: mcErr,
			}})
		}
		if mcErr != nil {
			return lastAssistantText(conv), mcErr
		}

		// 构建 reminder
		reminder := a.buildReminder(mode, iter)

		// Hook: PreUserMessage
		a.dispatchHook(ctx, hook.EventPreUserMessage, a.basePayload(hook.EventPreUserMessage, mode))

		// 环境文本
		envText := env.Render()
		if a.runtime != nil && a.runtime.ActiveSkills != nil {
			entries := a.runtime.ActiveSkills.ToPromptEntries()
			if len(entries) > 0 {
				promptEntries := make([]prompt.ActiveSkillEntry, len(entries))
				for i, e := range entries {
					promptEntries[i] = prompt.ActiveSkillEntry{Name: e.Name, Body: e.Body}
				}
				envText = envText + "\n\n" + prompt.RenderActiveSkillsBlock(promptEntries)
			}
		}

		// 流式请求本轮（使用内部 channel 收集事件）
		internalCh := make(chan Event, 32)
		text, calls, usage, sErr := streamOnce(ctx, a.provider, conv.Messages(), defs, sys, envText, reminder, internalCh)

		// 转发 internal 事件到外部 events channel
		drainEvents(internalCh, events)

		if sErr != nil && ctx.Err() != nil {
			return lastAssistantText(conv), nil
		}
		if sErr != nil {
			return lastAssistantText(conv), sErr
		}

		// 更新锚点
		if usage != nil {
			a.runtime.UpdateAnchor(compact.UsageAnchor(usage), conv.Len())
		}

		// Usage 事件
		if usage != nil {
			emitEvent(events, Event{Usage: &Usage{
				Input:      usage.InputTokens,
				Output:     usage.OutputTokens,
				CacheWrite: usage.CacheWrite,
				CacheRead:  usage.CacheRead,
			}})
		}

		// 无工具调用：自然完成
		if len(calls) == 0 {
			final := ensureFinal(nil, text)
			conv.AddAssistant(final)
			return final, nil
		}

		// 有工具调用：记录 assistant 回合
		conv.AddAssistantWithToolCalls(text, calls)

		// 统计未知工具
		if allUnknown(a.registry, calls) {
			unknownRun++
		} else {
			unknownRun = 0
		}

		// 保序分批并发执行（含权限判定）
		results, completed := a.executeBatched(ctx, calls, mode, events)

		// 工具结果回灌
		conv.AddToolResults(results)

		// 执行中被取消
		if !completed {
			return lastAssistantText(conv), ctx.Err()
		}

		// 连续未知工具上限
		if unknownRun >= maxUnknownRunSub {
			return lastAssistantText(conv), fmt.Errorf("连续多轮只产生未知工具调用")
		}
	}

	// 触达迭代上限
	text := lastAssistantText(conv)
	return text, ErrMaxTurnsReached
}

// lastAssistantText 获取对话历史中最后一条 assistant 消息的文本。
func lastAssistantText(conv *conversation.Conversation) string {
	msgs := conv.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleAssistant {
			return msgs[i].Content
		}
	}
	return ""
}

// emitEvent 非阻塞地向 events channel 发送事件。
func emitEvent(ch chan<- Event, ev Event) {
	if ch == nil {
		return
	}
	select {
	case ch <- ev:
	default:
	}
}

// drainEvents 从 internal channel 非阻塞地读取所有剩余事件并转发到 external channel。
func drainEvents(internal <-chan Event, external chan<- Event) {
	if external == nil {
		return
	}
	for {
		select {
		case ev := <-internal:
			select {
			case external <- ev:
			default:
			}
		default:
			return
		}
	}
}

