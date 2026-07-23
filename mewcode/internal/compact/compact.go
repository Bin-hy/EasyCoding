package compact

import (
	"context"
	"fmt"
	"log"

	"mewcode/internal/conversation"
	"mewcode/internal/llm"
)

// TriggerKind 标明 ManageContext 的触发来源。
type TriggerKind int

const (
	TriggerAuto      TriggerKind = iota // 主循环每轮自动检查
	TriggerManual                       // 用户 /compact
	TriggerEmergency                    // prompt_too_long 紧急压缩
)

// ManageInput 封装 ManageContext 所需的全部入参。
type ManageInput struct {
	Conv           *conversation.Conversation
	Provider       llm.Provider
	ContextWindow  int
	ToolDefs       []llm.ToolDefinition
	Replacement    *ContentReplacementState
	Recovery       *RecoveryState
	AutoTracking   *AutoCompactTrackingState
	Session        *SessionContext
	UsageAnchor    int64
	AnchorMsgLen   int
	EstimatedToken int64
	Trigger        TriggerKind
}

// ManageOutput 封装 ManageContext 返回的统计信息。
type ManageOutput struct {
	BeforeTokens int64
	AfterTokens  int64
}

// ManageContext 是 Agent 每轮请求前必调的唯一上下文管理入口。
func ManageContext(ctx context.Context, in ManageInput) (ManageOutput, error) {
	out := ManageOutput{BeforeTokens: in.EstimatedToken}

	// 若任一必需状态对象为 nil，降级为跳过压缩
	if in.Replacement == nil || in.Recovery == nil || in.AutoTracking == nil || in.Session == nil {
		out.AfterTokens = in.EstimatedToken
		return out, nil
	}

	switch in.Trigger {
	case TriggerManual:
		return manageManual(ctx, in, out)

	case TriggerEmergency:
		return manageEmergency(ctx, in, out)

	default: // TriggerAuto
		return manageAuto(ctx, in, out)
	}
}

// manageManual 处理手动 /compact：跳过 layer1、阈值、熔断。
func manageManual(ctx context.Context, in ManageInput, out ManageOutput) (ManageOutput, error) {
	newMsgs, _, afterTok, err := ForceCompact(ctx, in)
	if err != nil {
		return out, fmt.Errorf("手动压缩失败: %w", err)
	}
	in.Conv.ReplaceMessages(newMsgs)
	out.AfterTokens = afterTok
	return out, nil
}

// manageEmergency 处理紧急压缩：先强制 layer1 再 ForceCompact。
func manageEmergency(ctx context.Context, in ManageInput, out ManageOutput) (ManageOutput, error) {
	// 先强制跑一次 layer1 把大工具结果挪走
	layer1Out, _ := OffloadAndSnip(in.Conv.Messages(), in.Replacement, in.Session)
	in.Conv.ReplaceMessages(layer1Out)

	// 再无条件 ForceCompact
	newMsgs, _, afterTok, err := ForceCompact(ctx, in)
	if err != nil {
		return out, fmt.Errorf("紧急压缩失败: %w", err)
	}
	in.Conv.ReplaceMessages(newMsgs)
	out.AfterTokens = afterTok
	return out, nil
}

// manageAuto 处理自动路径：layer1 → 重估 token → 判断阈值 → 可能触发 layer2。
func manageAuto(ctx context.Context, in ManageInput, out ManageOutput) (ManageOutput, error) {
	// a. 执行 layer1（必须先做，因为 layer1 节省的 token 需要反映在阈值判断里）
	layer1Out, _ := OffloadAndSnip(in.Conv.Messages(), in.Replacement, in.Session)
	in.Conv.ReplaceMessages(layer1Out)

	// b. 用 layer1 之后的 updatedMsgs 重算估算 token
	estTokens := EstimateTokens(in.UsageAnchor, layer1Out, in.AnchorMsgLen)

	// c. sanity check：context_window 过小时跳过自动摘要
	if in.ContextWindow <= SummaryReserve+AutoSafetyMargin {
		log.Printf("[compact] ContextWindow=%d 过小（≤%d），跳过自动 layer2",
			in.ContextWindow, SummaryReserve+AutoSafetyMargin)
		out.AfterTokens = estTokens
		return out, nil
	}

	threshold := in.ContextWindow - SummaryReserve - AutoSafetyMargin

	// d. 未达阈值或熔断中：仅 layer1 生效
	if estTokens < int64(threshold) || in.AutoTracking.Tripped() {
		out.AfterTokens = estTokens
		return out, nil
	}

	// e. 触发自动摘要
	newMsgs, _, afterTok, err := AutoCompact(ctx, in)
	if err != nil {
		return out, fmt.Errorf("自动压缩失败: %w", err)
	}
	in.Conv.ReplaceMessages(newMsgs)
	out.AfterTokens = afterTok
	return out, nil
}
