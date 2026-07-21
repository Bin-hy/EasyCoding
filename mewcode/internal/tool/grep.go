package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// grepArgs 搜代码内容工具的参数
type grepArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
	Glob    string `json:"glob"`
}

// grepTool 在文件内容中搜索，返回命中位置
type grepTool struct{}

// grepMatch 单条搜索结果
type grepMatch struct {
	file    string
	lineNum int
	content string
}

func (t *grepTool) Name() string { return "grep" }
func (t *grepTool) Description() string {
	return "在文件内容中搜索匹配的文本（RE2 正则），返回 file:line:content 命中列表。"
}

func (t *grepTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "搜索的正则表达式（RE2 语法）",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "搜索起始路径，默认为当前工作目录",
			},
			"glob": map[string]any{
				"type":        "string",
				"description": "文件名过滤模式，如 *.go（仅搜索匹配文件）",
			},
		},
		"required": []string{"pattern"},
	}
}

func (t *grepTool) Execute(ctx context.Context, args json.RawMessage) Result {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}

	var a grepArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return errorResult("参数解析失败: %v", err)
	}
	if a.Pattern == "" {
		return errorResult("参数 pattern 不能为空")
	}

	// 编译正则
	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return errorResult("正则表达式无效: %v", err)
	}

	root := a.Path
	if root == "" {
		root = "."
	}

	var matches []grepMatch
	maxResults := 100
	longLineWarned := false

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err != nil {
			return nil
		}

		// 跳过隐藏目录
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
			return fs.SkipDir
		}

		if d.IsDir() {
			return nil
		}

		// glob 文件名过滤
		if a.Glob != "" {
			matched, _ := filepath.Match(a.Glob, d.Name())
			if !matched {
				return nil
			}
		}

		// 已收集足够结果则停止
		if len(matches) >= maxResults {
			return fs.SkipAll
		}

		// 搜索文件内容
		relPath, _ := filepath.Rel(root, path)
		fileMatches := grepFile(re, path, relPath, &longLineWarned, maxResults-len(matches))
		matches = append(matches, fileMatches...)

		return nil
	})

	if err != nil {
		return errorResult("搜索过程中出错: %v", err)
	}

	if len(matches) == 0 {
		return Result{Content: fmt.Sprintf("未找到匹配 %s 的内容", a.Pattern)}
	}

	// 构建输出
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].file != matches[j].file {
			return matches[i].file < matches[j].file
		}
		return matches[i].lineNum < matches[j].lineNum
	})

	var out strings.Builder
	for _, m := range matches {
		out.WriteString(fmt.Sprintf("%s:%d: %s\n", m.file, m.lineNum, m.content))
	}
	if len(matches) >= maxResults {
		out.WriteString("[truncated]\n")
	}
	if longLineWarned {
		out.WriteString("[warning: 部分超长行被跳过，搜索结果可能不完整]\n")
	}

	return Result{Content: strings.TrimRight(out.String(), "\n")}
}

// grepFile 在单个文件中搜索匹配行
func grepFile(re *regexp.Regexp, path, relPath string, longLineWarned *bool, max int) []grepMatch {
	var matches []grepMatch

	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// 设置 1MB 缓冲区限制，超出则标注
	scanner.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if re.MatchString(line) {
			// 截断超长行
			if len(line) > 500 {
				line = line[:500] + "..."
			}
			matches = append(matches, grepMatch{relPath, lineNum, line})
			if len(matches) >= max {
				return matches
			}
		}
	}

	// scanner 因行超长而 Err 则标注
	if scanner.Err() != nil && !*longLineWarned {
		*longLineWarned = true
	}

	return matches
}
