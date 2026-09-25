# 内置工具发现与“会话只读”排查

## 判断边界

“本轮还没有修改源码”描述工作进展；“会话只有读取工具”描述能力。前者不能证明后者。

必须分开核实三层：Core 当前注册哪些工具、客户端向当前会话暴露/允许哪些工具、具体本机操作是否被操作系统或本地审批拒绝。服务器注册表和全局历史成功事件不能证明另一个会话的权限。

`agentdock_context.tool_discovery` 与 `runtime_diagnostics.diagnostics.tool_discovery` 共用同一运行时注册表摘要。它包含注册数量及 `exec_command`、`file_edit`、`validation_run`、`task_manage`、`read_file`、`tool_catalog` 的注册状态。未启用分组会明确返回 `registered=false`。摘要不包含完整 Schema，不联网、不运行命令、不写入权限探针文件；客户端可见性和授权标为 `not_observable`，本机权限标为 `not_probed`。

## 调用顺序

先在客户端发现 AgentDock 的内置入口并读取 `agentdock_context`。参数没有展开时，可用下列目录请求取得单个完整定义：

```json
{"format":"mcp","name":"exec_command"}
```

或将 `name` 换成 `file_edit`。`group` 可继续作为额外的精确约束；工具未知、分组被禁用或分组不匹配时拒绝返回。这个请求只是检索定义，不启用工具、不授予权限，也不自动把入口安装到当前会话。

随后使用客户端提供的对应内置工具入口。`mcp_tool_search`、`mcp_tool_inspect`、`mcp_tool_call` 仅用于独立配置的动态 MCP 服务，不是内置命令/编辑工具的备用执行通道。执行工具使用 `cmd`，不能仅凭其他终端工具的经验猜测为 `command`。

客户端没有暴露入口时应明确报告“Core 已注册，但当前客户端未提供可调用入口”。真正发生拒绝时，应报告返回的具体错误和发生层次；不能靠改用 shell、动态 MCP 或降低审批来绕过拒绝。不要为证明权限而制造无关写操作，直接在用户已授权的工作中验证实际调用。文件修改仍保留版本护栏；冲突后重新读取。

## 元数据更新后生效

本改动增加了启动上下文输出字段及 `tool_catalog.name` 输入参数。源码修改不等于安装版更新；只有新 Core 实际运行后，才可向该 Core 请求新参数。再按客户端支持的流程刷新工具元数据，使用新会话验证。不要反复重启服务来替代客户端刷新，也不要把缓存仅列为可能因素时就宣布历史故障已定位。

OpenAI 的开发者文档要求在工具定义变化后刷新连接并新建会话；同时说明工具描述、服务器指引会影响工具选择。这里保留写工具的真实写入标记，不把写工具伪装成只读，也不放宽参数校验。

参考（2026-09-25 核对）：
- https://developers.openai.com/plugins/deploy/connect-chatgpt
- https://developers.openai.com/api/docs/guides/developer-mode

## 回归边界

覆盖注册表与分组过滤一致性、上下文/诊断一致性、未知客户端权限不可被伪造、完整单工具 Schema 导出、禁用工具/命名空间不可绕过、写入标记保持，以及真实 MCP stdio initialize、tools/list、tools/call 链路。

```sh
go test ./internal/app ./internal/mcp -count=1
```

这些测试验证产品可控制的注册、协议和指引，不证明所有模型在所有旧会话中都不会误判。macOS 菜单和任务管理属于独立的原生界面/任务存储测试范围；不可用工具发现回归代替点击验收。
