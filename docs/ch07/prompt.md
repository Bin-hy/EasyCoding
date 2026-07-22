# 我的初步想法
这一步的目标是：实现一个 MCP 客户端，让 MewCode 在启动时自动发现并注册外部 MCP Server 提供的工具。用户在配置文件里声明 Server 列表，MewCode 就能通过标准化的 MCP 协议把这些工具无缝接进工具中心，Agent 使用时完全无感。

技术要求：

- 支持两种传输：本地子进程走 stdio 管道，远程走 Streamable HTTP
- 按 JSON-RPC 2.0 收发消息，处理请求和响应的异步配对（请求带 id，回包按 id 关联）
- 一次会话分三步：初始化握手、列出工具、调用工具
- 用适配层把发现到的远端工具包装成 MewCode 已有的 Tool 接口注册进去，Agent 调用时无感
- 多个 Server 的连接做缓存和生命周期管理，单个 Server 挂了不影响其他
- 从配置文件读取 Server 列表，支持用户级、项目级两层合并

这一步先不做 MCP 的资源、提示词、采样这些非工具能力，也不做 Server 健康检查和自动重连。

配置格式：

- 在配置文件里用一个 map 声明 Server 列表，每个 key 是 Server 名字
- stdio 类型填 command、args、env（env 的值支持 ${VAR} 展开）
- HTTP 类型填 url 和 headers（值同样支持 ${VAR} 展开）
- 同样按用户级、项目级两层合并，后面的盖前面的