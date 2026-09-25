# AgentDock 审查问题修复记录

基线：`7f0ff86ece931fd2dc1d042c635f7131da059f38`。修复分支：`fix/review-hardening-20260924`。范围为已复现的审查问题及跨平台验证中发现的关联编译缺陷，不把既有测试全绿解释为没有其他潜在问题。

## 完成的修改

| 项目 | 实现 | 主要回归 |
| --- | --- | --- |
| 回滚覆盖并发写入 | 原子 no-replace 恢复；先撤回再检查；保留可能仍被外部 FD 写入的恢复 inode | `internal/tool/file/rollback_safety_test.go` |
| JUnit 矛盾报告假通过 | 单根、逐层 tests/failures/errors/skipped 对账；畸形报告不形成通过证据 | `internal/jobrun/junit_test.go` |
| 半条 Job 记录阻塞 | 私有暂存目录完整写入后发布；独立初始回执；损坏项隔离并标记 partial | `internal/jobrun/reconcile_test.go` |
| 未知 Job 永久占用 | 持锁显式 abandon，依据专属执行组/启动代际；保留幂等墓碑和未知结果 | `internal/jobrun/reconcile_test.go` |
| 旧记录缺少进程身份 | 首次保存恢复观察但拒绝释放；实际启动代际变化后须再次显式 abandon | `internal/jobrun/recovery_legacy_test.go` |
| worktree 身份错判 | 核对公共 Git 目录、独立管理目录和 .git 双向指针，而非只看路径存在 | `internal/worktree/identity_test.go` |
| 卡片累积 | 数据工具移除描述符和结果 UI 绑定；新增共享校验的 work_result_show | `internal/mcp/response_budget_test.go` |
| 销毁不释放执行环境 | 协议回复后清理并导航到固定 about:blank；清除 Store 快照和订阅 | `internal/mcpapps/browser_test.go`、`memory_test.go` |
| 响应重复正文/图片 | structuredContent 完整保留、文本有界摘要；动态 content 仅保留一份 | `internal/mcp/response_budget_test.go`、前端投影测试 |
| Windows 32 位构建溢出 | 完整 uint32 窗口/显示 ID 的 schema 使用 int64 边界 | `internal/tool/contract/wide_integer_test.go`、完整 386 二进制编译 |

Windows 的受控执行先以 suspended 状态创建新子进程，加入命名 Job Object 后再恢复。查询/恢复只观察明确的所属对象，不按名称杀进程，不用可能复用的 PID 重新取得所有权。原生 Windows 生命周期与结构布局回归已加入 CI；本机交叉编译不替代 Windows 运行验收。

## 内存与线协议结果

当前 UI 仍使用官方 SDK 和原有 Preact 外观，不宣称单张活动卡片的 SDK 已显著变小。主要改进是高频操作不创建卡片，以及销毁整个执行环境而不只解绑监听器。

隔离 Chrome、合成数据、每次采样前强制 GC：100 张活动工作结果卡片 691.262 MiB；teardown 后仍保留 100 个 iframe 为 18.281 MiB；移除后 0.576 MiB；三轮创建/移除后 0.595 MiB。原审查的同类保留 iframe 销毁场景仍约 689.5 MiB。

普通结果的源码、日志、错误、revision 和游标仍在完整 structuredContent 中。文本摘要不是全文。动态上游 content 转移至外层标准 content，保留所有块及原始顺序，并用 content_location 指明位置，不修改上游对象。旧的仅文本消费方须适配结构化结果。

## 操作及恢复边界

不自动重跑不确定执行，不把 abandon 当作成功或覆盖保证，不自动重启用户主机。没有足够进程证据时继续保留名额。对旧版本无身份记录的恢复，测试使用临时目录模拟启动代际，未在用户设备上真的执行重启。

失败回滚生成的 `.agentdock-rollback-*/withdrawn` 是有意保留的恢复文件，可能承接仍打开的外部文件描述符；只有确认恢复后才由操作者清理。正常成功编辑不创建此类文件。

未安装、未覆盖 App Bundle、未修改运行环境、未重启当前 Core/Tunnel、未修改 Cloudflare 或签名配置。历史 ChatGPT 响应不被改写，源码验证不代表当前网页或当前安装版已经改变。使用新版本还需要安装、刷新工具元数据并新建会话。

## 验证记录

最终实际结果：

| 检查 | 结果 |
| --- | --- |
| 全仓库 `go test -race ./... -count=1` | 1073 个顶层测试通过，1 个真实 ACP 集成用例未启用而跳过；无失败 |
| 真实 gopls | 文档符号、定义、引用、悬浮、诊断、工作区符号通过，已纳入最终全仓库运行 |
| 前端 TypeScript / 单元测试 | 类型检查通过，15 项单元测试通过 |
| 生成文件一致性 | `npm run check:generated` 通过 |
| 隔离 Chrome 行为和内存 | 两套测试连续各运行 3 次，全部通过；每轮清理临时用户目录成功 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过 |
| 整包编译 | macOS arm64；Windows amd64/arm64/386；Linux amd64/arm64 均通过 |
| Windows 测试编译 | process/jobrun/worktree/desktopruntime 四包在三种架构共 12 次编译通过；不等同 Windows 实机测试 |
| 源码一致性 | 受检 Go/前端/CI 等 813 个源码及生成文件，测试前后内容哈希一致 |

受检文件清单摘要：`5fcbd68b56bfe703114ec0cfe28ee3eee39ffc67cf7a15978300d9ef200be1ff`。这不是文件系统隔离或完整覆盖保证，Markdown 验收记录不参与该摘要。

最终三轮的进度提交 p95 分别为 2.0、4.3、1.9 ms；100 张活动卡片约 691.2–691.4 MiB，销毁但保留 iframe 后 18.281–18.283 MiB；每轮三次创建/移除后约 0.594–0.603 MiB。大正文的线协议样例由 270296 字节降到 130226 字节，原文、revision 和游标保持完整。

保留两次发现问题的原始失败记录，不以重试通过掩盖原因：首次 Windows/386 整包编译发现 uint32 边界 int 溢出，已修正并复验；首次最终浏览器回归发现 Chrome 强制退出与临时用户目录清理的竞争，改为先优雅关闭并等待资源清理后，连续三轮行为/内存回归通过。未放宽原内存和延迟阈值。

本机原始验证目录：`/private/tmp/agentdock-fix-20260924.cEjKip`。目录位于项目之外；用户的生产状态目录未用于故障注入。
