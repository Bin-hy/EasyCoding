// Package mcp 实现 MCP 客户端：配置加载、连接管理、工具适配。
package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Config 是 mcp_servers 在内存中的归一化形式（已展开 ${VAR}、已合并、已校验）。
type Config struct {
	Servers map[string]ServerConfig // key = server 名
}

// ServerConfig 是单个 MCP server 的完整定义。
type ServerConfig struct {
	Type    string            // "stdio" | "http"
	Command string            // stdio 必填
	Args    []string          // stdio 可选
	Env     map[string]string // stdio 可选（已展开）
	URL     string            // http 必填
	Headers map[string]string // http 可选（已展开）
}

// rawConfig 是 YAML 文件的反序列化中间结构。
type rawConfig struct {
	McpServers map[string]rawServer `yaml:"mcp_servers"`
}

// rawServer 含 YAML 中全部可能字段。
type rawServer struct {
	Type    string            `yaml:"type"`
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Env     map[string]string `yaml:"env"`
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers"`
}

// 变量展开正则：仅匹配 \$\{VAR_NAME\}，VAR_NAME 由字母、数字、下划线组成。
var varPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// loadFile 从指定路径加载 YAML 配置。
// 文件不存在 → 空 + nil；YAML 解析失败 → 零值 + error。
func loadFile(path string) (rawConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return rawConfig{}, nil
		}
		return rawConfig{}, err
	}

	var r rawConfig
	if err := yaml.Unmarshal(data, &r); err != nil {
		return rawConfig{}, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}

// expandVars 对字符串中的 ${VAR} 做环境变量展开。
// 返回的 undefined 记录所有未定义变量名。
func expandVars(s string) (string, []string) {
	var undefined []string
	out := varPattern.ReplaceAllStringFunc(s, func(match string) string {
		// 提取变量名（去掉 ${ 和 }）
		varName := match[2 : len(match)-1]
		val, ok := os.LookupEnv(varName)
		if !ok {
			undefined = append(undefined, varName)
			return ""
		}
		return val
	})
	return out, undefined
}

// collectUndefined 从 expandVars 返回的 undefined 切片中去重收集。
func collectUndefined(all *[]string, incoming []string) {
	seen := make(map[string]bool)
	for _, v := range *all {
		seen[v] = true
	}
	for _, v := range incoming {
		if !seen[v] {
			seen[v] = true
			*all = append(*all, v)
		}
	}
}

// applyExpansion 对单个 rawServer 的 env / headers 值的值做 ${VAR} 展开。
// 未定义变量一次性 stderr 告警。
func applyExpansion(name string, srv *rawServer) {
	var undefined []string

	expandedEnv := make(map[string]string, len(srv.Env))
	for k, v := range srv.Env {
		out, undef := expandVars(v)
		expandedEnv[k] = out
		collectUndefined(&undefined, undef)
	}
	srv.Env = expandedEnv

	expandedHeaders := make(map[string]string, len(srv.Headers))
	for k, v := range srv.Headers {
		out, undef := expandVars(v)
		expandedHeaders[k] = out
		collectUndefined(&undefined, undef)
	}
	srv.Headers = expandedHeaders

	for _, v := range undefined {
		fmt.Fprintf(os.Stderr, "[mcp] warn: undefined env var ${%s} referenced by server %s\n", v, name)
	}
}

// mergeServers 两层合并：项目级同名 server 完整覆盖用户级。
func mergeServers(user, project map[string]rawServer) map[string]rawServer {
	merged := make(map[string]rawServer, len(user)+len(project))
	for k, v := range user {
		merged[k] = v
	}
	for k, v := range project {
		merged[k] = v // 完整覆盖
	}
	return merged
}

// validateServer 校验单个 server 定义，返回归一化 ServerConfig 与是否合法。
func validateServer(name string, srv rawServer) (ServerConfig, bool) {
	// type 必为 "stdio" 或 "http"
	if srv.Type != "stdio" && srv.Type != "http" {
		reason := fmt.Sprintf("unknown type %q", srv.Type)
		if srv.Type == "" {
			reason = "missing type field"
		}
		fmt.Fprintf(os.Stderr, "[mcp] warn: skip server %s: %s\n", name, reason)
		return ServerConfig{}, false
	}

	// stdio 必填 Command
	if srv.Type == "stdio" && srv.Command == "" {
		fmt.Fprintf(os.Stderr, "[mcp] warn: skip server %s: stdio type requires command field\n", name)
		return ServerConfig{}, false
	}

	// http 必填 URL
	if srv.Type == "http" && srv.URL == "" {
		fmt.Fprintf(os.Stderr, "[mcp] warn: skip server %s: http type requires url field\n", name)
		return ServerConfig{}, false
	}

	return ServerConfig(srv), true
}

// LoadConfig 加载并合并用户级与项目级两层 MCP 配置。
// - root: 项目根（用来定位 <root>/.mewcode.yaml）
// - 文件不存在 → 视为空层；格式非法 → 跳过该层 + stderr 告警
// - 内部完成 ${VAR} 展开与字段校验
// - 永不返回 error（签名留 error 仅为未来扩展，当前实现恒为 nil）
func LoadConfig(root string) (Config, error) {
	// 用户级配置
	home, err := os.UserHomeDir()
	var userRaw rawConfig
	if err == nil {
		userPath := filepath.Join(home, ".mewcode", "config.yaml")
		userRaw, err = loadFile(userPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[mcp] warn: failed to load user config %s: %v\n", userPath, err)
			userRaw = rawConfig{} // 降级为空
		}
	}

	// 项目级配置
	projPath := filepath.Join(root, ".mewcode.yaml")
	projRaw, err := loadFile(projPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[mcp] warn: failed to load project config %s: %v\n", projPath, err)
		projRaw = rawConfig{} // 降级为空
	}

	// 对每层各 server 做变量展开
	for name := range userRaw.McpServers {
		srv := userRaw.McpServers[name]
		applyExpansion(name, &srv)
		userRaw.McpServers[name] = srv
	}
	for name := range projRaw.McpServers {
		srv := projRaw.McpServers[name]
		applyExpansion(name, &srv)
		projRaw.McpServers[name] = srv
	}

	// 合并
	merged := mergeServers(userRaw.McpServers, projRaw.McpServers)

	// 校验 + 组装
	cfg := Config{Servers: make(map[string]ServerConfig, len(merged))}
	for name, srv := range merged {
		if sc, ok := validateServer(name, srv); ok {
			cfg.Servers[name] = sc
		}
	}

	return cfg, nil
}
