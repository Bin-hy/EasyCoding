package hook

import (
	"encoding/json"
	"fmt"
	"strings"
)

// GetByPath 从 Payload 中按 . 分隔的路径提取字段值并转为字符串。
//
//   - 字段路径如 "tool_input.command" → 先取 map["tool_input"]，再取其下的 map["command"]
//   - 嵌套对象转为 JSON 字符串
//   - bool/数字 转为字符串（fmt.Sprint）
//   - 路径中任意节点为 nil 或不存在 → 返回空串 ""
func GetByPath(p Payload, path string) string {
	if p == nil {
		return ""
	}

	parts := strings.Split(path, ".")
	var current any = map[string]any(p)

	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		v, exists := m[part]
		if !exists || v == nil {
			return ""
		}
		current = v
	}

	// 终端值转字符串
	switch v := current.(type) {
	case string:
		return v
	case bool:
		return fmt.Sprint(v)
	case float64:
		return fmt.Sprint(v)
	case map[string]any, []any:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	default:
		return fmt.Sprint(v)
	}
}

// EvalCondition 对 Payload 求值条件表达式。
// c == nil → 无条件触发（返回 true）。
// c.Mode == CombineAllOf → 所有原子条件都满足才返回 true。
// c.Mode == CombineAnyOf → 任一原子条件满足即返回 true。
func EvalCondition(c *Condition, p Payload) bool {
	if c == nil {
		return true
	}

	if len(c.Atoms) == 0 {
		return true
	}

	switch c.Mode {
	case CombineAllOf:
		for _, a := range c.Atoms {
			val := GetByPath(p, a.Field)
			if a.Matcher == nil {
				continue
			}
			if !a.Matcher.Match(val) {
				return false
			}
		}
		return true

	case CombineAnyOf:
		for _, a := range c.Atoms {
			val := GetByPath(p, a.Field)
			if a.Matcher == nil {
				continue
			}
			if a.Matcher.Match(val) {
				return true
			}
		}
		return false

	default:
		return false
	}
}
