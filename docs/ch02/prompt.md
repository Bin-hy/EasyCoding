# 我的初步想法
我要从零开始做一个命令行AI助手(Coding Agent)，叫MewCode，类似ClaudeCode。用[你的语言]开发。
 
这一步的目标是：用户在终端启动MewCode后，进入一个交互式对话界面（TUI），可以输入问题，MewCode调用大模型API，把回复流式地
逐字打印出来。支持多轮对话，AI能记住之前说过的话。
技术要求：
支持AnthropicClaude和OpenAI两种API后端，通过配置文件切换流式用SSE，不是等全部生成完再返回
支持Claude的extended thinking
Provider层要抽象成统一接口，以后方便加新的后端
这一步不做 tooluse、文件操作、代码编辑这些agent 功能，纯对话就行。
 
配置格式：用 YAML 配置文件管理 LLM 供应商信息，六个配置字段：
name 供应商标识名，方便区分多个配置
protocol 决定走哪家协议
model 指定模型
base_url 指定请求的地址
api_key 做认证
thinking 是否启用扩展思考，可选