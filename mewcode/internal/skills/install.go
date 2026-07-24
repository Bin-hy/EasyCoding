package skills

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Install 限额常量。
const (
	maxInstallFileSize  = 1 << 20   // 1 MiB 单文件
	maxInstallTotalSize = 8 << 20    // 8 MiB 总量
	maxInstallFileCount = 64         // 文件数
	maxInstallDepth     = 4          // 目录深度
	installHTTPTimeout  = 60 * time.Second
)

// InstallReport 安装结果报告。
type InstallReport struct {
	Name     string // Skill 名称
	Dir      string // 安装目标目录
	FileCount int   // 安装的文件数
}

// ParseSkillURL 解析三种 Skill URL，返回 (skillName, apiURL, error)。
// 支持：
// 1. skills.sh: https://www.skills.sh/<org>/<name>/<version>
// 2. GitHub tree: https://github.com/<owner>/<repo>/tree/<ref>/<path>
// 3. Raw: https://raw.githubusercontent.com/.../SKILL.md
func ParseSkillURL(rawURL string) (string, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", fmt.Errorf("无效 URL: %w", err)
	}

	switch u.Host {
	case "www.skills.sh", "skills.sh":
		// https://www.skills.sh/<org>/<name>/<version>
		parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
		if len(parts) < 3 {
			return "", "", fmt.Errorf("skills.sh URL 格式错误，预期 /<org>/<name>/<version>")
		}
		name := parts[1]
		// 使用 GitHub raw API 作为后端
		apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/skills/%s", parts[0], parts[1], parts[1])
		return name, apiURL, nil

	case "github.com":
		// https://github.com/<owner>/<repo>/tree/<ref>/<path>
		parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
		if len(parts) < 4 || parts[2] != "tree" {
			return "", "", fmt.Errorf("GitHub URL 格式错误，预期 /<owner>/<repo>/tree/<ref>/<path>")
		}
		name := parts[len(parts)-1]
		apiPath := strings.Join(parts[3:], "/")
		apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s?ref=%s",
			parts[0], parts[1], apiPath, parts[3])
		return name, apiURL, nil

	case "raw.githubusercontent.com":
		// https://raw.githubusercontent.com/<owner>/<repo>/<ref>/path/SKILL.md
		// 直接返回原始 URL 供单文件下载
		name := filepath.Base(strings.TrimSuffix(u.Path, "/SKILL.md"))
		return name, rawURL, nil

	default:
		return "", "", fmt.Errorf("不支持的 Skill URL host: %s（支持 skills.sh、github.com、raw.githubusercontent.com）", u.Host)
	}
}

// Install 从 GitHub API 递归下载 Skill 并安装到 installRoot。
// installRoot 通常为 ~/.mewcode/skills/。
func Install(name, apiURL, installRoot string) (*InstallReport, error) {
	// 创建 staging temp dir
	staging, err := os.MkdirTemp("", "mewcode-skill-*")
	if err != nil {
		return nil, fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(staging)

	// 递归下载
	client := &http.Client{Timeout: installHTTPTimeout}
	var totalSize int64
	var fileCount int
	_, err = fetchGitHubDir(client, apiURL, staging, 0, &totalSize, &fileCount)
	if err != nil {
		return nil, err
	}

	// 验证含 SKILL.md
	skillMDPath := filepath.Join(staging, "SKILL.md")
	if _, err := os.Stat(skillMDPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("下载内容不含 SKILL.md，拒绝安装")
	}

	// Atomic rename 到目标目录
	targetDir := filepath.Join(installRoot, name)
	// 移除旧版本
	_ = os.RemoveAll(targetDir)
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		return nil, fmt.Errorf("创建目标目录失败: %w", err)
	}
	if err := os.Rename(staging, targetDir); err != nil {
		return nil, fmt.Errorf("安装失败: %w", err)
	}

	return &InstallReport{
		Name:     name,
		Dir:      targetDir,
		FileCount: fileCount,
	}, nil
}

// fetchGitHubDir 递归下载 GitHub API 目录内容。
func fetchGitHubDir(client *http.Client, apiURL string, dest string, depth int, totalSize *int64, fileCount *int) (int, error) {
	if depth > maxInstallDepth {
		return 0, fmt.Errorf("目录深度超过限制 (%d)", maxInstallDepth)
	}
	if *fileCount >= maxInstallFileCount {
		return 0, fmt.Errorf("文件数超过限制 (%d)", maxInstallFileCount)
	}
	if *totalSize >= maxInstallTotalSize {
		return 0, fmt.Errorf("总大小超过限制 (%d MiB)", maxInstallTotalSize>>20)
	}

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "mewcode")

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("GitHub API 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return 0, fmt.Errorf("GitHub API rate limit 命中 (403): %s", string(body))
	}
	if resp.StatusCode == http.StatusNotFound {
		return 0, fmt.Errorf("资源不存在 (404): %s", apiURL)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("GitHub API 返回 %d", resp.StatusCode)
	}

	// 如果返回的是单个文件对象（raw URL），直接下载
	var items []githubContentItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		// 可能返回的是单文件对象
		return 0, fmt.Errorf("解析 GitHub API 响应失败: %w", err)
	}

	downloaded := 0
	for _, item := range items {
		if *fileCount >= maxInstallFileCount {
			break
		}

		targetPath := filepath.Join(dest, item.Name)

		if item.Type == "dir" {
			// 递归下载子目录
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return downloaded, err
			}
			n, err := fetchGitHubDir(client, item.URL, targetPath, depth+1, totalSize, fileCount)
			downloaded += n
			if err != nil {
				return downloaded, err
			}
		} else if item.Type == "file" {
			if item.Size > maxInstallFileSize {
				return downloaded, fmt.Errorf("文件 %s 大小 %d 超过单文件限制 (%d)", item.Name, item.Size, maxInstallFileSize)
			}
			*totalSize += int64(item.Size)
			if *totalSize > maxInstallTotalSize {
				return downloaded, fmt.Errorf("总大小超过限制 (%d MiB)", maxInstallTotalSize>>20)
			}

			// 下载文件内容
			if item.DownloadURL == "" {
				continue
			}
			if err := downloadFile(client, item.DownloadURL, targetPath); err != nil {
				return downloaded, fmt.Errorf("下载 %s 失败: %w", item.Name, err)
			}
			*fileCount++
			downloaded++
		}
	}

	return downloaded, nil
}

// githubContentItem GitHub Contents API 返回的条目。
type githubContentItem struct {
	Name        string `json:"name"`
	Type        string `json:"type"` // "file" or "dir"
	Size        int    `json:"size"`
	DownloadURL string `json:"download_url"`
	URL         string `json:"url"`
}

// downloadFile 下载单个文件。
func downloadFile(client *http.Client, url, dest string) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "mewcode")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, io.LimitReader(resp.Body, maxInstallFileSize))
	return err
}
