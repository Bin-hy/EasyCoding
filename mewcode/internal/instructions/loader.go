package instructions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxIncludeDepth @include 最大嵌套深度（从 MEWCODE.md 算第 1 层）。
const maxIncludeDepth = 5

// Loader 负责三层 MEWCODE.md 的加载和 @include 展开。
type Loader struct {
	projectRoot string
	userHome   string
}

// NewLoader 创建指令加载器。
// projectRoot 为项目根目录路径。
func NewLoader(projectRoot string) *Loader {
	userHome, _ := os.UserHomeDir()
	return &Loader{projectRoot: projectRoot, userHome: userHome}
}

// Load 按优先级加载三层指令文件，返回拼接后的完整指令文本。
// 三层路径为：
//  ① <projectRoot>/MEWCODE.md（项目级，最高优先级）
//  ② <projectRoot>/.mewcode/MEWCODE.md（项目配置级）
//  ③ ~/.mewcode/MEWCODE.md（用户级，最低优先级）
// 加载失败的层静默跳过，全部为空返回空字符串。
func (l *Loader) Load() (string, error) {
	paths := []struct {
		path     string
		boundary string // 路径逃逸检测的根边界
	}{
		{filepath.Join(l.projectRoot, "MEWCODE.md"), l.projectRoot},
		{filepath.Join(l.projectRoot, ".mewcode", "MEWCODE.md"), l.projectRoot},
	}
	if l.userHome != "" {
		paths = append(paths, struct {
			path     string
			boundary string
		}{filepath.Join(l.userHome, ".mewcode", "MEWCODE.md"), filepath.Join(l.userHome, ".mewcode")})
	}

	var parts []string
	for _, p := range paths {
		content, err := l.loadFile(p.path, p.boundary, 1, make(map[string]struct{}))
		if err != nil {
			// 文件不存在等错误静默跳过
			continue
		}
		content = strings.TrimSpace(content)
		if content != "" {
			parts = append(parts, content)
		}
	}

	return strings.Join(parts, "\n\n"), nil
}

// loadFile 加载单个文件，处理 @include 展开。
// path: 当前文件路径
// boundary: 路径逃逸检测的根边界
// depth: 当前嵌套层数（从 1 开始）
// visited: 环路检测集合（已解析为绝对路径的文件集合）
func (l *Loader) loadFile(path, boundary string, depth int, visited map[string]struct{}) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}

	// 环路检测
	if _, ok := visited[absPath]; ok {
		return fmt.Sprintf("<!-- @include 检测到环路，已跳过: %s -->", path), nil
	}
	visited[absPath] = struct{}{}

	// 深度限制
	if depth > maxIncludeDepth {
		return fmt.Sprintf("<!-- @include 超过最大嵌套深度，已跳过: %s -->", path), nil
	}

	// 路径逃逸检测（必须在解析为绝对路径之后）
	if !strings.HasPrefix(absPath, boundary) {
		return fmt.Sprintf("<!-- @include 路径超出允许范围，已跳过: %s -->", path), nil
	}

	// 读取文件
	data, err := os.ReadFile(absPath)
	if err != nil {
		// 文件不存在等，静默跳过
		return "", err
	}

	// 二进制文件检测（前 512 字节包含 \x00）
	if len(data) > 0 {
		checkLen := len(data)
		if checkLen > 512 {
			checkLen = 512
		}
		for _, b := range data[:checkLen] {
			if b == 0 {
				return fmt.Sprintf("<!-- @include 文件为二进制格式，已跳过: %s -->", path), nil
			}
		}
	}

	content := string(data)
	baseDir := filepath.Dir(absPath)

	// 逐行扫描处理 @include
	lines := strings.Split(content, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "@include ") {
			// 验证 @include 在独占行上（trimmed 等于 line 去换行符，说明整行只有 @include）
			includePath := strings.TrimSpace(strings.TrimPrefix(trimmed, "@include "))
			if includePath != "" {
				// 解析 @include 的文件路径（相对于当前文件所在目录）
				targetPath := filepath.Join(baseDir, includePath)
				included, err := l.loadFile(targetPath, boundary, depth+1, visited)
				if err != nil {
					// 找不到的文件静默跳过
					continue
				}
				result = append(result, included)
				continue
			}
		}
		result = append(result, line)
	}

	return strings.Join(result, "\n"), nil
}
