package compact

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// SessionContext 会话生命周期信息，进程启动时一次性生成。
type SessionContext struct {
	SessionID  string // 格式：YYYYMMDD-HHMMSS-xxxx
	SessionDir string // <workspace>/.mewcode/sessions/<SessionID>
	SpillDir   string // SessionDir + "/tool-results"
}

// newSessionID 生成会话唯一标识。
// 格式：YYYYMMDD-HHMMSS-xxxx，前半段取本地时间，后半段 4 字符随机十六进制防碰撞。
// 优先用 crypto/rand；失败时降级为伪随机。
func newSessionID() string {
	timePart := time.Now().Format("20060102-150405")

	var hexStr string
	b := make([]byte, 2) // 2 字节 = 4 字符十六进制
	if _, err := rand.Read(b); err == nil {
		hexStr = hex.EncodeToString(b)
	} else {
		// crypto/rand 不可用时降级
		n, _ := rand.Int(rand.Reader, big.NewInt(1<<16))
		if n == nil {
			n = big.NewInt(0)
		}
		hexStr = fmt.Sprintf("%04x", n.Uint64()&0xFFFF)
	}
	return fmt.Sprintf("%s-%s", timePart, hexStr)
}

// NewSessionContext 创建会话上下文并建立落盘目录。
func NewSessionContext(workspace string) (*SessionContext, error) {
	id := newSessionID()
	sessionDir := filepath.Join(workspace, ".mewcode", "sessions", id)
	spillDir := filepath.Join(sessionDir, "tool-results")
	if err := os.MkdirAll(spillDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建会话目录失敗: %w", err)
	}
	return &SessionContext{
		SessionID:  id,
		SessionDir: sessionDir,
		SpillDir:   spillDir,
	}, nil
}

// OpenSessionContext 打开已有会话目录，不创建新目录。
// workspace 为项目根目录，sessionID 为已有的 session ID。
func OpenSessionContext(workspace, sessionID string) (*SessionContext, error) {
	sessionDir := filepath.Join(workspace, ".mewcode", "sessions", sessionID)
	info, err := os.Stat(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("会话目录不存在: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("会话路径不是目录: %s", sessionDir)
	}
	return &SessionContext{
		SessionID:  sessionID,
		SessionDir: sessionDir,
		SpillDir:   filepath.Join(sessionDir, "tool-results"),
	}, nil
}

// ParseSessionTime 从新格式 session ID 中解析出时间戳。
// 新格式前 15 位为 "YYYYMMDD-HHMMSS"。
// 旧格式（如 "<unix_ts>-<hex>"）无法解析，返回 error。
func ParseSessionTime(sessionID string) (time.Time, error) {
	if len(sessionID) < 15 {
		return time.Time{}, fmt.Errorf("session ID 太短，非新格式: %s", sessionID)
	}
	timeStr := sessionID[:15] // "YYYYMMDD-HHMMSS"
	t, err := time.ParseInLocation("20060102-150405", timeStr, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("无法从 session ID 解析时间: %w", err)
	}
	return t, nil
}

// ContentReplacementState 会话级工具结果替换决策账本。
// seenIds 记录已决策的 tool_use_id；replacements 保存决定替换的预览字符串。
// 同一 id 一旦进入 seenIds 就不可翻转。
type ContentReplacementState struct {
	mu           sync.Mutex
	seenIds      map[string]struct{}
	replacements map[string]string
}

// NewContentReplacementState 构造替换决策账本。
func NewContentReplacementState() *ContentReplacementState {
	return &ContentReplacementState{
		seenIds:      make(map[string]struct{}),
		replacements: make(map[string]string),
	}
}

// IsSeen 判断 id 是否已被决策（无论替换还是保留）。
func (s *ContentReplacementState) IsSeen(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.seenIds[id]
	return ok
}

// DecideOnce 在持锁状态下完成"查账本 → 决策 → 写账本"原子操作。
// decide 回调在持锁时调用，返回 (decision, preview)：
//   - "kept": 写 seenIds，不写 replacements，返回原 content
//   - "replaced": 写 seenIds + replacements，返回 preview
//   - "skip": 不写账本，返回原 content（下一轮可重试）
//
// 若 id 已 Seen：直接返回账本存量结果。
func (s *ContentReplacementState) DecideOnce(id, original string, decide func() (decision, preview string)) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, seen := s.seenIds[id]; seen {
		if replaced, ok := s.replacements[id]; ok {
			return replaced
		}
		return original
	}

	decision, preview := decide()
	switch decision {
	case "replaced":
		s.seenIds[id] = struct{}{}
		s.replacements[id] = preview
		return preview
	case "kept":
		s.seenIds[id] = struct{}{}
		return original
	default: // "skip"
		return original
	}
}

// AutoCompactTrackingState 跟踪自动摘要连续失败次数，用于熔断。
type AutoCompactTrackingState struct {
	mu                  sync.Mutex
	ConsecutiveFailures int
}

// NewAutoCompactTrackingState 构造熔断追踪器。
func NewAutoCompactTrackingState() *AutoCompactTrackingState {
	return &AutoCompactTrackingState{}
}

// RecordSuccess 记录一次成功，清零连续失败计数。
func (a *AutoCompactTrackingState) RecordSuccess() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ConsecutiveFailures = 0
}

// RecordFailure 记录一次失败，累加计数。
func (a *AutoCompactTrackingState) RecordFailure() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ConsecutiveFailures++
}

// Tripped 熔断器是否跳闸。
func (a *AutoCompactTrackingState) Tripped() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ConsecutiveFailures >= maxConsecutiveAutoCompactFailures
}

// FileReadRecord 记录一次成功读取文件的纯净字节与时间戳。
type FileReadRecord struct {
	Path      string    // 文件绝对路径
	Content   string    // 不带行号前缀的纯净内容
	Timestamp time.Time // 最后一次成功读取时间
}

// RecoveryState 文件追踪状态，Agent 主循环写、compact 摘要时读。
// 键为文件绝对路径，并发安全。
type RecoveryState struct {
	mu    sync.Mutex
	files map[string]FileReadRecord
}

// NewRecoveryState 构造文件追踪状态。
func NewRecoveryState() *RecoveryState {
	return &RecoveryState{files: make(map[string]FileReadRecord)}
}

// RecordFile 记录一次文件读取。content 应为不带行号前缀的纯净字节。
// 若 path 不是绝对路径则自动补全。
func (r *RecoveryState) RecordFile(path, content string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	absPath := path
	if !filepath.IsAbs(absPath) {
		var err error
		absPath, err = filepath.Abs(path)
		if err != nil {
			absPath = path
		}
	}
	r.files[absPath] = FileReadRecord{
		Path:      absPath,
		Content:   content,
		Timestamp: time.Now(),
	}
}

// Snapshot 返回按时间戳倒序排列的文件读取记录副本。
func (r *RecoveryState) Snapshot() []FileReadRecord {
	r.mu.Lock()
	defer r.mu.Unlock()

	list := make([]FileReadRecord, 0, len(r.files))
	for _, rec := range r.files {
		list = append(list, rec)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Timestamp.After(list[j].Timestamp)
	})
	return list
}
