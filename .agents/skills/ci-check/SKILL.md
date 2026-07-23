---
name: ci-check
description: |
  本地 CI 检查与自动修复。在完成一个开发节点后手动调用。
  读取 .github/workflows/ci.yml 定义的检查项，在本地执行等价命令（lint、test、build），
  发现问题后自动分析并修复，修复后重新检查直到全部通过。
  触发词：/ci-check、跑 CI、ci 检查、运行 CI、check ci、验证 ci、检查代码、跑一下检查。
---

# CI 本地检查与自动修复

在本地模拟 GitHub Actions CI 流水线的检查项，发现问题自动修复。

## 执行流程

### 第一步：读取 CI 配置

读取 `.github/workflows/ci.yml`，提取所有 job 和关键命令，确定要检查的项目和目录。关注：
- `working-directory` — 项目子目录
- lint 工具的配置（golangci-lint 版本、参数）
- test 命令（`go test` 的参数如 `-race`、`-coverprofile`）
- build 的目标平台矩阵

### 第二步：逐项检查

对 CI 中定义的每个项目，按 lint → test → build 顺序执行：

**Lint 检查：**
- 优先使用 `golangci-lint run`（在对应 `working-directory` 下执行）
- 如果 golangci-lint 未安装，降级使用 `go vet ./...`
- 如果项目有 `.golangci.yml` 配置文件，golangci-lint 会自动读取

**Test 检查：**
- 执行 `go test -v -race ./...`（在对应 `working-directory` 下执行）
- 关注测试失败的具体用例和错误信息

**Build 检查：**
- 对 CI 中定义的每个目标平台（GOOS/GOARCH）执行 `go build`
- 使用 `CGO_ENABLED=0` 和 `-ldflags="-s -w"` 确保与 CI 一致
- 输出到临时目录（如 `/tmp/ci-check/`），不污染项目

### 第三步：分析并修复

如果任何步骤失败：

1. **仔细阅读错误输出**，理解根本原因
2. **分类问题**：语法错误、类型不匹配、导入问题、测试逻辑错误、lint 警告
3. **修复代码**：
   - 语法/类型错误 → 直接修正代码
   - 测试失败 → 分析是测试用例问题还是业务逻辑问题，修复对应代码
   - lint 警告 → 按 golangci-lint 规则修正
   - 构建失败 → 检查平台特定代码、构建标签、CGO 依赖
4. **修复后回到第二步重新检查**，直到全部通过

### 第四步：报告

所有检查通过后，汇报结果摘要：
- 各步骤的通过状态
- 修复了哪些问题（如有）
- 总耗时

## 注意事项

- **不要跳过检查**，即使认为某步与改动无关
- **修复时遵循项目现有代码风格**，不引入新依赖或重构无关代码
- **如果同一个检查连续 3 轮仍未通过**，停下来向我汇报阻塞原因，不要无限循环修复
- **golangci-lint 如果未安装**，告知我可以用 `brew install golangci-lint` 安装以获得更完整的 lint 检查
- **仅检查 CI 中定义的目录**，不要扩展到其他文件
