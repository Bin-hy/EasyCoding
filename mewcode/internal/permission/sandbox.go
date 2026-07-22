package permission

import (
	"os"
	"path/filepath"
	"strings"
)

// resolveRoot 将用户指定的项目根规整为（已解析符号链接的）绝对路径。
func resolveRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return root, err
	}
	return filepath.EvalSymlinks(abs)
}

// evalSymlinksOrAncestor 对存在的目标做 EvalSymlinks；
// 不存在则逐级回退到最近已存在祖先目录解析后拼回剩余段。
//
// 覆盖"新建文件、含未创建中间目录"的场景：假设
// root=/a（已存在），目标=/a/b/c/new.go（b/c 尚不存在），
// 则回退 /a/b/c → /a/b → /a（存在），解析 /a 的符号链接后拼回 b/c/new.go。
func evalSymlinksOrAncestor(abs string) string {
	// 先尝试直接解析
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved
	}

	// 逐级回退到最近已存在祖先
	dir := filepath.Dir(abs)
	for dir != abs && dir != "." && dir != "/" {
		resolved, err := filepath.EvalSymlinks(dir)
		if err == nil {
			// 解析成功的祖先 + 剩余相对段
			rel, _ := filepath.Rel(dir, abs)
			return filepath.Join(resolved, rel)
		}
		abs = dir
		dir = filepath.Dir(abs)
		if dir == abs {
			break
		}
	}

	// 连根目录都不可解析——返回原始输入（最保守）
	return abs
}

// sandboxOK 判断给定路径是否落在项目根目录内。
// 空 path 视为 root；相对路径相对 root 解析。
// 先解析符号链接（或回退到最近祖先），再做前缀比对。
func (e *Engine) sandboxOK(path string) bool {
	if path == "" {
		path = e.root
	}

	// 相对路径相对 root 解析为绝对
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(e.root, path)
	}

	// 规整路径（清理 .. 等）
	abs = filepath.Clean(abs)

	// 解析符号链接（或祖先回退）
	resolved := evalSymlinksOrAncestor(abs)

	sep := string(os.PathSeparator)
	return resolved == e.root || strings.HasPrefix(resolved, e.root+sep)
}
