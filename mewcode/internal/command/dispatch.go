package command

import "strings"

// Parse 解析斜杠命令输入。
// 返回 (name, isSlash)：
//   - ("", false)：非 "/" 开头（送给 LLM）
//   - ("/", true)：仅 "/"（Lookup 必然 miss）
//   - (name, true)：合法的 "/<name>" 格式
//   - ("", true)："/" 开头但有尾随参数（Lookup 必然 miss，走未命中提示）
func Parse(input string) (name string, isSlash bool) {
	input = strings.TrimSpace(input)
	if input == "" || input[0] != '/' {
		return "", false
	}

	// 仅 "/"
	if input == "/" {
		return "", true
	}

	// 去掉前导 "/"
	rest := input[1:]

	// 双斜杠等畸形输入 → Lookup miss
	if rest[0] == '/' {
		return "", true
	}

	// 找到第一个空白的位置
	spaceIdx := strings.IndexAny(rest, " \t")
	if spaceIdx == 0 {
		// 输入如 "/ help"，尾随空白在前
		return "", true
	}
	if spaceIdx > 0 {
		// 有尾随参数，如 "/help xx" → Lookup 必然 miss
		return "", true
	}

	// 纯命令名，小写化
	return strings.ToLower(rest), true
}
