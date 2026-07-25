// Package task 提供后台子 Agent 任务管理与生命周期追踪。
//
// Manager 管理所有后台子 Agent 的启动、状态追踪、取消与完成通知。
// 4 个内置工具（TaskList/TaskGet/TaskStop/SendMessage）供主 Agent 查询和操控后台任务。
package task
