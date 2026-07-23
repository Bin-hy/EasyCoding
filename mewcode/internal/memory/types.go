package memory

import "time"

// NoteType 笔记类型。
type NoteType string

const (
	TypeUserPreference     NoteType = "user_preference"
	TypeCorrectionFeedback NoteType = "correction_feedback"
	TypeProjectKnowledge   NoteType = "project_knowledge"
	TypeReferenceMaterial  NoteType = "reference_material"
)

// Note 一条笔记的内存表示。
type Note struct {
	Type     NoteType
	Title    string
	Slug     string
	Content  string
	Filename string
	Created  time.Time
	Updated  time.Time
}

// UpdateAction LLM 返回的单条操作。
type UpdateAction struct {
	Action   string `json:"action"`   // "create" / "update" / "delete"
	Level    string `json:"level"`    // "project" / "user"
	Type     string `json:"type"`     // NoteType（create 时必需）
	Title    string `json:"title"`    // 笔记标题
	Slug     string `json:"slug"`     // 文件名 slug（create 时必需）
	Content  string `json:"content"`  // 笔记正文（create/update 时必需）
	Filename string `json:"filename"` // 已有文件名（update/delete 时必需）
}
