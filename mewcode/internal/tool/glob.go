package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// globArgs 按模式找文件工具的参数
type globArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

// globTool 按 glob 模式查找匹配文件
type globTool struct{}

func (t *globTool) Name() string   { return "glob" }
func (t *globTool) ReadOnly() bool { return true }
func (t *globTool) Description() string {
	return "按 glob 模式查找匹配文件，返回匹配的文件路径列表。支持 ** 跨任意层级目录。"
}

func (t *globTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "glob 模式，如 **/*.go、*.md",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "搜索起始路径，默认为当前工作目录",
			},
		},
		"required": []string{"pattern"},
	}
}

func (t *globTool) Execute(ctx context.Context, args json.RawMessage) Result {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}

	var a globArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return errorResult("参数解析失败: %v", err)
	}
	if a.Pattern == "" {
		return errorResult("参数 pattern 不能为空")
	}

	root := a.Path
	if root == "" {
		root = "."
	}

	var matches []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		// 检查 ctx 是否已取消
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err != nil {
			return nil // 跳过无法访问的路径
		}

		// 跳过隐藏目录
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
			return fs.SkipDir
		}

		if d.IsDir() {
			return nil
		}

		// 计算相对路径
		relPath, err := filepath.Rel(root, path)
		if err != nil {
			relPath = path
		}

		// 自实现支持 ** 的 glob 匹配
		if matchGlob(a.Pattern, relPath) {
			matches = append(matches, relPath)
		}
		return nil
	})

	if err != nil {
		return errorResult("遍历文件失败: %v", err)
	}

	if len(matches) == 0 {
		return Result{Content: fmt.Sprintf("无匹配到模式 %s 的文件", a.Pattern)}
	}

	// 排序并限制 ≤100
	sort.Strings(matches)
	if len(matches) > 100 {
		matches = matches[:100]
		matches = append(matches, "[truncated]")
	}

	return Result{Content: strings.Join(matches, "\n")}
}

// matchGlob 自实现支持 ** 的 glob 匹配。
// ** 匹配任意层级目录（含零层）；普通通配沿用 path.Match。
func matchGlob(pattern, path string) bool {
	// 将 pattern 和 path 按 / 分段
	patParts := strings.Split(pattern, "/")
	pathParts := strings.Split(path, "/")

	return matchSegments(patParts, pathParts)
}

// matchSegments 对分段后的模式与路径做 DP 匹配，** 跨任意层级。
func matchSegments(pat, path []string) bool {
	if len(pat) == 0 {
		return len(path) == 0
	}

	// 如果模式只剩一个 **，匹配所有剩余路径
	if len(pat) == 1 && pat[0] == "**" {
		return true
	}

	if len(path) == 0 {
		// path 已耗尽，看看 pat 是否只剩 **
		for _, p := range pat {
			if p != "**" {
				return false
			}
		}
		return true
	}

	if pat[0] == "**" {
		// ** 匹配 0 层（跳过 **）或 1+ 层（跳过 path 首段）
		return matchSegments(pat[1:], path) || matchSegments(pat, path[1:])
	}

	matched, _ := filepath.Match(pat[0], path[0])
	if !matched {
		return false
	}
	return matchSegments(pat[1:], path[1:])
}
