// Package compact 提供两层上下文压缩能力，在有限 token 预算内保持长时间会话可用。
package compact

// 第 1 层：工具结果预防性压缩常量（包内私有）
const (
	// 单条工具结果超过此字节数时触发落盘替换
	singleResultLimit = 50000
	// 单条 RoleTool 消息内工具结果聚合字节数超过此阈值时触发落盘
	messageAggregateLimit = 200000
)

// 第 2 层：LLM 摘要相关常量
const (
	// SummaryReserve 给摘要 LLM 输出预留的 token 空间
	SummaryReserve = 20000
	// AutoSafetyMargin 自动触发的安全余量：防止估算误差与单轮波动
	AutoSafetyMargin = 13000
	// ManualSafetyMargin 手动触发 / 紧急压缩的安全余量：仅检查摘要请求本身能否塞下
	ManualSafetyMargin = 3000
)

// 恢复段常量（包内私有）
const (
	recoveryFileLimit     = 5
	recoveryTokensPerFile = 5000
)

// 近期原文保留常量（包内私有）
const (
	recentKeepTokens   = 10000
	recentKeepMessages = 5
)

// 自动摘要熔断常量（包内私有）
const (
	maxConsecutiveAutoCompactFailures = 3
)

// 摘要请求自身 PTL 重试常量（包内私有）
const (
	ptlRetryLimit     = 3
	ptlDropPercentage = 0.2
)

// Token 估算常量（包内私有）
const (
	estimateCharsPerToken = 3.5
)

// 预览体常量（包内私有）
const (
	previewHeadBytes = 2048
	previewHeadLines = 20
)
