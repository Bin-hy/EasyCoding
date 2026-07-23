package session

import (
	"fmt"
	"os"
	"time"

	"mewcode/internal/compact"
)

// CleanExpired 删除超过 maxAge 的会话目录。
// 只处理新格式 ID 的目录，旧格式跳过。
func CleanExpired(sessionsDir string, maxAge time.Duration) error {
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	now := time.Now()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()

		t, err := compact.ParseSessionTime(id)
		if err != nil {
			// 旧格式或非 session 目录，跳过
			continue
		}

		if now.Sub(t) > maxAge {
			dirPath := sessionsDir + "/" + id
			if err := os.RemoveAll(dirPath); err != nil {
				fmt.Fprintf(os.Stderr, "[session] 清理过期会话失败 %s: %v\n", id, err)
			}
		}
	}

	return nil
}
