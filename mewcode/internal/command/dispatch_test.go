package command

import (
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		input   string
		name    string
		isSlash bool
	}{
		{"", "", false},
		{"   ", "", false},
		{"hello", "", false},
		{"/", "", true},
		{"/help", "help", true},
		{"  /HELP  ", "help", true},
		{"/help xx", "", true},    // 有尾随参数，Lookup miss
		{"/help  ", "help", true}, // 尾随空白可接受
		{"//double", "", true},    // 视为参数（name 空但 isSlash=true）
	}

	for _, tt := range tests {
		name, isSlash := Parse(tt.input)
		if name != tt.name || isSlash != tt.isSlash {
			t.Errorf("Parse(%q) = (%q, %v)，期望 (%q, %v)", tt.input, name, isSlash, tt.name, tt.isSlash)
		}
	}
}
