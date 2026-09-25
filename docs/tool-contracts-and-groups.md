# 工具分组、契约与结果交付

基于 `2236821` 的工具审查加固。统一注册表保留专用强类型工具，不把所有能力压成一个任意参数的万能入口。注册工具总数为 50，实际可用集合还受配置和可选服务开关约束。

## 分组与发现

| Group ID | 能力 |
| --- | --- |
| core | 核心上下文、工具目录与安全诊断 |
| files | 文件读取、精确续读、检索与版本约束编辑 |
| execution | 普通命令会话、托管 Job、验证、观察与控制 |
| project | 语义导航、工作副本和项目演进 |
| work | 任务进度、证据、冻结结果与显式展示 |
| integrations | 动态 MCP 和 Skill 包管理 |
| media | 图像查看及文件交付 |
| desktop | 本机授权的桌面观察与输入 |
| browser | 可选浏览器服务 |
| acp | 可选编程代理 |
| knowledge | 可选 Nexus 记忆、私密笔记和工作流 |

`tool_catalog` 是只读、无 UI 的目录工具。默认 `format=summary` 返回精简名称、标题、分组和只读/展示标记。`group` 可过滤一个已知组；`format=mcp` 显式返回完整 MCP 描述符；`format=openai` 导出 OpenAI namespace/function/tool_search 配置。输出带稳定的 `catalog_revision`。

MCP 描述符通过 `_meta["agentdock/group"]` 提供分类提示，最终仍按标准 MCP 工具列表暴露。自定义分类字段不保证 ChatGPT 自动显示层级或按组延迟加载；没有通过某次工具调用临时改变连接的工具集合。

OpenAI 导出将每组变成 namespace，函数保持原参数 Schema，`strict=false`；非 core 函数带 `defer_loading=true`，工具数组包含 tool_search。导出 JSON 的 `tools` 字段才是接入方使用的配置数组。API 使用方收到 namespace/name 后可调用 `Runtime.CallNamespaced`，它核对组归属和真实可用性，再进入同一 Runtime 校验与授权路径。

这项实现包含配置生成和命名空间分派，不包含对用户 OpenAI 账户发起真实 API 请求，也不声称当前 ChatGPT 连接已切换成 API 命名空间模式。

## 纯目录 CLI

安装包含本次提交的二进制后：

```sh
agentdock tools --format summary
agentdock tools --format summary --group files
agentdock tools --format mcp
agentdock tools --format openai > tool-catalog.json
```

CLI 读取启动环境并生成目录，不启动 Runtime、不创建任务状态目录、不连接外部 MCP、不改变工具开关。导出应使用与实际运行服务一致的环境，否则看到的是另一套配置的可用集合。

## 启动配置

| 环境变量 | 默认值 | 作用 |
| --- | --- | --- |
| `AGENTDOCK_TOOL_GROUPS` | 未设置 | 可选组的允许列表，逗号分隔；仍须满足 Browser/ACP/Nexus/Desktop 原有开关。core 始终保留；未知或重复组拒绝 |
| `AGENTDOCK_RESULT_TEXT_MODE` | `summary` | `summary` 为有界文本摘要；`json` 为原生工具提供完整结构化结果的 JSON 文本副本，供仅文本客户端兼容 |
| `AGENTDOCK_MAX_TOOL_RESULT_BYTES` | `16777216` | 单次工具结果的整体 JSON 预算；可设 4096 到 33554432 字节 |

例如仅暴露文件与执行组、同时保留核心目录/诊断，可在目标服务的启动环境设置 `AGENTDOCK_TOOL_GROUPS=files,execution`。配置在创建 Runtime 时固定，发现和调用都过滤；不能通过调用已隐藏工具、换 namespace 或读取目录重新启用。

分组控制的是工具暴露范围，不是文件系统沙箱或新的权限系统。命令执行、桌面本地批准、现有认证和可选能力边界保持独立。`exec_command` 本身有通用执行能力，不能把启用它后的分组列表当作操作系统安全隔离。

本次没有在 macOS 设置界面新增这些输入框，也没有修改用户当前的 `agentdock.env`。正式部署通过既有安装/设置流程更新启动环境；刷新 ChatGPT 工具定义并新开会话，不能指望旧会话缓存自动变化。

## 文件读取：精确、版本绑定的续读

`read_file` 和 `read_files` 的范围先校验，起始行必须为正，结束行不能早于起始行。内部切片也防御反向范围；处理器的意外 panic 会变成非重试的 `TOOL_PANIC`，不泄露 panic 内容、不记录为成功。

截断结果返回 `next_cursor`。游标包含文件完整读取版本、准确字节位置和原选择范围的结束行；长行未返回的后半段不会被跳过。续读请求应传同一个文件路径和 cursor，省略起止行。文件内容变化、换路径或游标畸形时拒绝，而不是移除版本护栏继续。

```json
{"path":"source.go","max_bytes":4096}
```

```json
{"path":"source.go","cursor":"<上次 next_cursor>","max_bytes":4096}
```

`next_start_line` 和 `next_start_byte` 仅供解释位置，优先使用完整 cursor。预算连一个 UTF-8 码点都放不下时返回明确错误，不能返回不前进的继续位置。批量请求仍为每段最多 400 行；超出该边界的游标须通过 read_file 继续。当前 WSL 文件适配没有实现精确游标，遇到要求时明确拒绝，不静默降级。

## 会话：读取不再消费结果

普通 Session 与 managed Job 保持不同寿命：Session 依赖当前 Core，Job 由独立 supervisor 执行。但两者都不能因为观察者读取一次就丢失回执。

`session_observe action=status` 使用调用者独立维护的 `stdout_offset`、`stderr_offset`，返回各自的 `stdout_next_offset`、`stderr_next_offset`。省略游标从当前保留起点读取；重复同一游标可找回同一段输出。完成、取消、kill、同步执行的 Session 回执均保留，不在第一次终态读取后删除。

普通 Session 最长保留一小时，也受已存在的 128 个保留会话上限和新增的 64 MiB 已完成输出总预算约束；仅淘汰最旧的已完成会话，不终止活动命令。命令结束和存入会话时会执行内存限额检查，不依赖用户持续轮询。

stdout/stderr 共享一次读取的原始字节预算。历史输出被淘汰、游标超出已保留区间时返回 `SESSION_OUTPUT_CURSOR` 与实际保留/总字节位置，不静默跳跃。所有游标都是原始字节位置；UTF-8 无效或跨字符切片会标记 `utf-8-lossy` 并提供对应的 `stdout_base64`/`stderr_base64` 原始字节。managed Job 同类情况提供 `data_base64`，可无损恢复。

## 动态 MCP：可见性、取消与安全

HTTP 客户端默认拒绝重定向。认证头注入层另外核对配置目标与请求目标的 scheme/host/effective-port，跨源或含嵌入凭据的目标被拒绝。即使上层误启用重定向，也不能把固定认证头重新注入到其他来源。

上游结果级 `_meta` 在客户端转换处移除，线协议投影再防御一次；不会被提升为模型可见的 structuredContent，也不会合并进 AgentDock 自己的 UI 元数据。不修改上游对象。普通业务结果中的非隐藏字段仍保留。

服务锁和注册表等待可以响应 context 取消；队列时间计入请求期限，不持有全局注册锁等待单个服务。替换/关闭服务先取消其生命周期，再关闭连接。实际 tools/call 发生网络/超时失败时结果可能未知，因此标注不自动重试，不用重放写操作猜测恢复；这与可重试的初始化/发现错误分类分开。

上游 HTTP 解压后响应和 stdio 单条消息有 16 MiB 上限；工具目录最多 2048 项、总定义 4 MiB、单项 256 KiB。超限报错，不把截断 JSON 当作正常结果。每个阶段仍保留原有参数校验与最小环境策略。

## 动作契约与结果契约

必要参数由相应能力包的 Schema 声明，不只放在说明文字里。文件 move、worktree create、Job logs、JUnit argv、Skill install、Task get 等动作缺少必要字段会在执行前拒绝。

所有 50 个注册工具的成功输出 Schema 均拒绝空对象；关键字段按动作或结果分支要求。Runtime 也验证实际成功结果，而非只在测试中检查。契约不符/原始结构化结果超过 32 MiB 时返回 `OUTPUT_CONTRACT_VIOLATION`，明确提示副作用可能已发生、不要重放。

共享协议包的 ACP 概览仍描述旧的单 agent 结构，本地适配到实际的 default_profile 和 profiles 列表；不修改 Go module cache，不用允许任意字段掩盖不匹配。Schema 构造顺序固定，避免目录哈希随 map 遍历变化。

## 整体结果预算与文本兼容

`summary` 模式完整结构化数据保存在 structuredContent，原生文本为最多 1024 字节的导航摘要；`json` 模式显式复制完整 JSON 到文本，供仅文本消费方使用。两种模式都受整体预算约束。动态 MCP 的正文/图像/音频/资源仍只在外层 content 放一份，不为兼容模式机械复制图像。

最终预算覆盖 structuredContent、content、图像和本地元数据。可序列化且不超过 64 MiB 的超额结果会保存为一小时有效的私有不可变 JSON 资源，返回 `RESULT_DEFERRED` 和资源引用；外层不是成功回执，须读取资源判断原始结果。资源含完整原输出、原 isError、版本和游标，不生成公开下载 URL，不重跑工具。保存失败或超出可保存上限则明确报告交付失败，仍不允许重放副作用。

这不是无限传输承诺，也不保证当前 ChatGPT 为任意大结果自动物化。接入方必须支持认证资源读取，或使用较小的读取范围、日志片段与专用分页工具。

## Job 历史分页与归档

`job_observe action=list` 新增 `cursor`，返回 `next_cursor`、`has_more`、`partial`。游标绑定项目状态根和 task 过滤条件，使用时间/ID 键继续，不依赖不断变化的数组下标。列表仍是观察，不是数据库快照。

只缓存终态目录摘要；活动/未知/损坏记录每次重读，目录和 record.json 的时间/大小变化使缓存失效。最终选中条目仍通过 Status 读取真实结果。目录元数据扫描仍为 O(N)，冷启动也要加载记录；没有声称一次分页恒定时间。

归档后将幂等回执及状态原子移至 `jobs/tombstones/<两位分片>/<job_id>`。原 ID 和 request_id 恢复行为保留，重启后同请求不会再次执行；墓碑不再占用 10000 条近期历史上限。旧版本留在根目录的 archived 记录可再次显式 archive 完成迁移。归档不会自动删除幂等身份，磁盘空间仍应由操作者规划。
