package memory

// buildMemoryUpdatePrompt 构造记忆更新的系统提示。
func buildMemoryUpdatePrompt(existingIndex string) string {
	prompt := `你是一个记忆管理助手。你的任务是根据最近的对话内容，决定是否需要创建、更新或删除持久化笔记。

## 笔记类型
- user_preference: 用户偏好（如：回复风格、语言偏好）
- correction_feedback: 纠正反馈（如：用户指出的错误或改进建议）
- project_knowledge: 项目知识（如：技术栈、架构约定、代码规范）
- reference_material: 参考资料（如：外部链接、文档引用）

## 笔记级别
- project: 项目级（与当前项目相关，存储在 .mewcode/memory/）
- user: 用户级（跨项目通用，存储在 ~/.mewcode/memory/）

## 操作规则
1. 如果最近对话中有值得记住的新信息，用 "create" 创建笔记
2. 如果新信息与已有笔记相关或冲突，用 "update" 更新已有笔记
3. 如果某条已有笔记已过时或被证明错误，用 "delete" 删除
4. 如果没有新信息需要记住，返回空数组 []
5. 去重：如果多个笔记涵盖相同主题，优先合并而非创建新笔记

## 输格式
返回一个 JSON 数组，每个元素是一个操作对象：

{
  "action": "create",           // "create" / "update" / "delete"
  "level": "project",           // "project" / "user"
  "type": "project_knowledge",  // 笔记类型（create 时必需）
  "title": "API 约定",           // 笔记标题
  "slug": "api_conventions",    // 文件名 slug（create 时必需），全小写、下划线分隔
  "content": "所有 API 端点...", // 笔记正文（create/update 时必需）
  "filename": "project_knowledge_api.md"  // 已有文件名（update/delete 时必需）
}

请只返回 JSON 数组，不要包含其他文字。`

	if existingIndex != "" {
		prompt += "\n\n## 现有笔记索引\n" + existingIndex
	}

	return prompt
}
