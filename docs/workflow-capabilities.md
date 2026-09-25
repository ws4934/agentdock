# 可靠执行、验证证据与交付（0.8.5）

本版本在现有 Runtime、命令执行器、任务系统、MCP、文件事务和反馈组件上扩展能力，不引入第二套 Agent 循环、数据库、Plugin 协议或远程任务调度服务。设计参考了 WebCodex 的执行/观察分离、验证新鲜度和交付投影；本实现使用 AgentDock 现有 Go 架构。

## 能力与入口

| 需求 | 入口 | 主要边界 |
| --- | --- | --- |
| 非交互长任务跨 Core 退出继续 | `exec_command` 的 `execution_mode=managed` | 显式启用；保留旧 auto/sync/async 语义 |
| 独立日志观察、终态和验证新鲜度 | `job_observe` | 自带字节 offset，不消费其他观察者的游标 |
| 取消、归档或显式放弃未知执行 | `job_control` | `abandon` 必须证明原执行已结束，永不重放、永不标记成功 |
| 机器生成验证记录 | `validation_run` | Go JSON、JUnit、普通进程三种适配 |
| 合并源码读取／搜索后读上下文 | `read_files` / `search_and_read` | 数量、行数、总输入与输出预算 |
| 读取版本约束的编辑 | `read_file` / `file_edit` | 版本绑定完整内容和规范路径，冲突不得移除护栏 |
| 当前结果和不可变交付 | `work_result_read` / `work_result_freeze` | 仅数据；任务、Job、源码、产物明确关联 |
| 显式可视化交付 | `work_result_show` | 单独创建卡片；不要在中间检查点重复展示 |
| 脱敏分层诊断 | `runtime_diagnostics` / `diagnostic_export` | 元数据白名单，不导出配置、凭据、命令正文或项目文件 |
| 原生 Go 语义导航 | `code_navigate` | 只读 gopls；不自动下载依赖／工具链 |
| 显式独立工作副本 | `worktree_manage` | exact commit、detached worktree；不是进程沙箱 |
| 认证连接内文件交付 | `file_publish delivery=private` | 不生成公网 URL，MCP ResourceLink，单资源最多64 MiB |
| 可选私网接入 | macOS 控制面板或 `agentdock secure-tunnel` | 使用用户已配置的官方客户端，不自动开通远程账户／隧道 |

## 1. 可恢复的托管 Job

```json
{
  "cmd": "go test -race ./...",
  "workdir": "/absolute/project",
  "execution_mode": "managed",
  "request_id": "task-123-regression-1",
  "task_id": "tsk_...",
  "title": "后端回归",
  "timeout_ms": 900000
}
```

返回的是执行回执，不是命令成功。后续使用 `job_observe` 的 `status`、`logs`、`evidence` 或 `list`，不再用 Core 内存会话的 `session_id`。`task_id` 可选；提供时必须是当前任务系统中存在的任务。

- 一个 request_id 对应一次提交意图。断线恢复重复提交同一意图时使用原键，得到同一个 Job。新的验证轮次使用新键。定义、工作目录或有效环境变更会产生幂等冲突，而不是偷偷再次执行。
- 独立 supervisor 使用现有进程组／Job Object 控制执行进程树。Core 正常关闭或提交进程退出，不自动取消显式 managed Job；machine reboot/supervisor 丢失不能伪造恢复，返回 `outcome_unknown`。
- 持久化文件在 `<AGENTDOCK_HOME>/jobs`。环境值不落盘；私有请求文件会包含 argv/命令，**不要把凭据放进命令正文**。目录只允许当前操作系统用户访问。
- 运行上限8个，单 Job 超时最多24小时，每个流保留前8 MiB。超过预算继续排空管道并报告 `dropped_bytes`，不让输出堵住子进程。
- 日志返回 `offset`、`next_offset`、保留／总字节数与截断标志。游标是字节位置；无效 UTF-8 或跨字符切片以 `encoding=utf-8-lossy` 明确标注，原始保留文件不变。
- 取消是请求，只有终态回执能证明结束；不按进程名广泛杀进程，不通过可能复用的 PID 恢复所有权。
- 归档仅删除已确认结束且无所有者的日志／请求文件，保留幂等墓碑。上限10000条记录；没有自动重放、自动清除未知任务或主机重启后自动执行。
- managed 不接受 PTY、stdin 或 WSL 定制执行上下文。交互任务继续使用现有会话接口。

### 崩溃与未知记录的恢复

新提交先在私有 `.admission-*` 目录完整写入请求、不可变 `receipt.json` 和可变 `record.json`，然后原子发布目录；发布前不会启动 supervisor。可变记录损坏时只恢复初始身份，不恢复成功结论。完全损坏项在列表中明确标记 `state_unavailable` / partial，不再使无关任务查询和提交一并失败。未知项仍保守占用并发名额。

`job_control action=abandon` 持有准入锁和对应 owner 锁，在以下证据之一成立时，写入 `abandoned` 终态并释放名额：从未启动命令；已记录的操作系统启动代际结束；或已记录的专属进程组/Windows 命名 Job Object 已不存在或无活动成员。不能根据 PID 名称、进程退出码猜测，也没有“强制成功”开关。

缺少旧进程身份的遗留记录不会被直接放行。第一次显式 `abandon` 保存 `recovery-observation.json` 并返回 `PROCESS_EXIT_UNPROVEN`，名额仍保留；只有用户自行安排系统重启，后续显式调用观察到不同启动代际，才可恢复。重复请求或仅重启 Core 都不能提供这项证据。没有稳定 OS 启动标识时继续拒绝。工具不自动重启主机。

`abandoned` 表示放弃未知结果，不证明操作成功、测试通过或副作用被撤销；原请求键和证据仍保留，重用 request_id 不会执行第二次。受控进程组不是任意命令的安全沙箱，主动脱离该组的外部服务仍应独立管理。

## 2. 结构化验证与源码新鲜度

```json
{
  "request_id": "task-123-go-tests-1",
  "adapter": "go_test",
  "workdir": "/absolute/project",
  "task_id": "tsk_...",
  "packages": ["./..."],
  "race": true
}
```

Go 适配执行 `go test -json -count=1`，按流记录实际测试终态、失败、跳过以及不完整报告。零测试、全部跳过、不完整报告不作为测试通过的证明。`process` 只报告 `process_passed`，不会编造测试数量。

JUnit 使用原生 `argv`，其中恰有一个 `{report}` 占位符，例如自有测试脚本的输出路径参数。平台为每个执行创建全新的私有报告路径，不消费项目里遗留的旧报告。报告限制4 MiB、有界深度／测试数量，拒绝 DTD、多根与截断 XML；逐层核对 `testsuite` 和 `testsuites` 声明的 tests/failures/errors/skipped。声明与实际节点矛盾、重复计数属性或混合结果标记都不能成为通过证据。

验证前后按完整文件字节观察源码。省略 `source_paths` 时要求准确 Git 根目录，覆盖 tracked 与未忽略的 untracked 文件；显式路径只证明该范围。预算为20000文件／256 MiB，无法完整观察就不能报完整证据。构建输出应放在 Git 忽略目录或项目外。

`job_observe action=evidence` 重新观察源码，返回 current / stale / unproven / not_available。验证失败、测试未执行、进程失败、取消、超时和未知终态分别保留，不用“预期失败”覆盖真正失败。

**这是前后源码观察，不是文件系统快照隔离，也不是测试覆盖保证。** 验证期间又修改源码、改后恢复、外部依赖变化或未包含的路径，不能凭此声称整个工程永远正确。原 `task_manage.final_review` 仍是人工／模型声明；机器证据独立展示，不由 verified 字符串生成。

## 3. 批量读取与编辑护栏

`read_files.requests` 最多16个，每段最多400行；相同文件、相同 revision、重叠或间距不超过20行的范围会合并，返回 `request_indexes`。默认总输出256 KiB、最多1 MiB；总读取预算128 MiB。逐项失败、编码限制、截断和继续行号明确返回。

`search_and_read` 最多16个匹配，有界上下文，搜索结果与阅读块去重；搜索和读取不是原子事务，`read_revision` 只对应返回的源码，不声称早先搜索仍是同一瞬间。

`read_file` 和批量读取返回 `read_revision`，随后可传给 `file_edit.expected_read_revision`。覆盖移动还必须提供目标 revision；目标必须原本不存在时使用 `absent`。结构化多文件 patch 使用 `expected_revisions`，一旦要求护栏就必须为全部受影响路径提供预期状态；标准 unified diff 或 WSL 不支持该护栏时明确拒绝，不静默降级。备份恢复使用原子 no-replace，不能在 Lstat 后再覆盖 rename。失败回滚先撤回到私有目录再检查；并发写入和仍可能被外部文件描述符修改的撤回 inode 均保留，错误返回恢复路径。`.agentdock-rollback-*/withdrawn` 是失败现场的恢复文件，不自动删除；确认恢复后再由操作者清理。正常成功的编辑不会创建这类回滚保留文件。

## 4. 工作结果与冻结交付

`work_result_read` 接受 `task_id`、`workdir`、可选 Job/产物 ID 和源码范围；最多32个Job、8个产物。Job 必须属于同一个任务和准确工作目录。输出独立展示机器验证、当前源码、变更路径、交付引用与人工／模型验收。

完成任务后，使用 `work_result_freeze` 并传入 `request_id` 和刚观察到的 `expected_source_revision`。冻结前重新检查源码和任务版本，拒绝活动任务、未确认执行或不完整 Job 列表。相同冻结键恢复同一份 JSON，读取历史结果不会悄悄刷新为当前工作区。

冻结记录不等于“所有测试通过”：允许保存明确的历史失败或无验证交付，但不能伪装成功。它保存元数据与引用，并不延长原产物到期时间，也不是源代码或产物的永久备份。

`work_result_read` 与 `work_result_freeze` 不生成卡片。需要可视化时显式调用 `work_result_show`，使用与 read 相同的任务/源码选择。第九类工作结果组件默认无轮询；只有用户点击才调用固定的只读 `work_result_read`。旧回包不能覆盖新宿主结果，错任务回包拒绝，失败时保留旧观察并标注非最新。冻结结果没有刷新按钮。

## 5. 诊断中心

macOS 主菜单新增“连接诊断”，通过经过认证的 loopback Runtime API 读取白名单模型，不继承系统代理，不跟随重定向。关闭／重开时丢弃旧回包，可导出当前可见的安全文本报告。

工具层诊断报告 Core、认证配置、公网入口、Tunnel、托管Job、响应交付及UI渲染等层。未观察的环节保持 unknown；本机 healthz、进程存活或某次 RPC 完成不能证明 ChatGPT 已收到并显示结果。

`diagnostic_export` 仅产生 `diagnostic-report.json`、`diagnostic-report.md`、`activity-safe.json` 三个文件。事件环最多128条，仅含随机关联ID、工具名、时间、成功状态与有界错误码，不含参数、返回体、任意进程日志、环境、凭据或源码。导出默认走私有认证资源。

## 6. 只读 gopls 与可选 worktree

`code_navigate` 支持 status、document_symbols、definition、references、hover、diagnostics、workspace_symbols。位置使用1起始行号、0起始UTF-16字符偏移。结果仅保留当前项目内的路径，依赖可以被分析但项目外结果会被省略并计数。

提供 `AGENTDOCK_GOPLS_EXECUTABLE_PATH` 可固定已安装客户端；macOS 含 gopls 的包也会自动发现 Core 同目录的 Helper。没有就返回 unavailable，不自动下载安装。本次安装包携带 gopls v0.23.0，需要本机可用的 Go 工具链；运行时不下载依赖，禁用 GOPACKAGESDRIVER 与隐式网络访问。若本机默认 Go 太旧，可复用模块缓存中已安装的较新稳定工具链；显式 GOROOT 优先，不触发工具链下载。

每次查询独立启动一个语言服务器并有限并发，返回后回收；这是避免旧工作区状态的保守实现，大项目冷启动可能较慢。最大查询时限30秒，结果数量、帧大小、单文件大小均有限制。无法完成或结果截断会明确标注。gopls v0.23.0 的部分 pull diagnostics 回复缺少 report kind：保留真实诊断，但 `diagnostics_complete=false` / unknown，不能把它当成“无错误”证明。

`worktree_manage create` 必须提供本地 exact 40位 commit 与稳定 request_id，创建 detached worktree，不 fetch，不切换原分支、不复制未提交改动。禁用 Git hooks、smudge/process 过滤器及子模块自动递归。list 只返回持久化注册，当前状态须用 status 核对；status 还比较 Git 公共目录、独立管理目录和 .git 双向指针，不能只凭路径仍存在就判定 ready；不自动合并、删除、重放不确定创建或清理用户变更。隔离的是代码改动，**不是任意 shell 权限**。

## 7. 私有文件与可选 OpenAI Secure MCP Tunnel

`file_publish` 增加 `delivery=private`。私有产物不生成签名公网 URL，即使伪造一个形式正确的公网 URL 也拒绝读取。通过 MCP `artifact://agentdock/<id>` ResourceLink 交给宿主读取，验证有效期、完整性、大小，单资源限制64 MiB。超过限制明确返回不可用，不能让模型循环搬运 Base64。

读取复用当前 MCP 连接的权限：HTTP 应配置 Bearer/OAuth，stdio 属于显式信任的本地进程。它不是多租户逐文件 ACL；若用户自己关闭整个 HTTP 服务认证，private 不会凭空增加独立认证层。宿主是否能物化 ResourceLink 是客户端能力，不以工具返回成功冒充客户端下载成功。

可选 Secure MCP Tunnel 使用用户单独安装、已完成账户／工作区关联的官方 `tunnel-client`：

```sh
"/Applications/AgentDock.app/Contents/Helpers/agentdock" secure-tunnel configure \
  --executable /absolute/path/tunnel-client --profile existing-profile
"/Applications/AgentDock.app/Contents/Helpers/agentdock" secure-tunnel doctor
```

随后可在 macOS 的公网访问选项中选择 OpenAI Secure MCP Tunnel。官方 profile 必须指向本机 MCP，并正确配置本机服务的认证；AgentDock 不关闭本机 Bearer 认证、不把 CF/AgentDock 密钥转交给客户端。profile/API凭据、远程权限和账户开通仍由官方流程管理。

适配器固定客户端 SHA-256；文件改变需要重新明确配置。run 与 configure 用同一所有权锁，不误标已有客户端。doctor 的原始输出不写入诊断报告。可选 loopback `/readyz` 只说明本机端点响应，不确认远程投递。没有客户端或profile时是未配置，不假装已连接。

本轮不会为用户创建收费资源、上传凭据或切换现有 CF 通道。官方服务要求与宿主支持见 https://developers.openai.com/api/docs/guides/secure-mcp-tunnels 。完整的真实云端联通验收需要用户已授权的官方配置，隔离测试不替代该步骤。

## 测试与发布

- 执行器：实际提交进程退出后的Job存活与终态、同request_id只执行一次、并发、取消、独立游标、容量和错误状态。
- 验证：实际 `go test -json` 的通过／失败／零测试、JUnit、源码变化、未知／不完整报告。
- 文件／项目：范围合并、预算、版本冲突、多文件事务、恶意Git过滤器和worktree范围。
- MCP：真实HTTP认证门禁、ResourceLink与二进制读取、私有公网拒绝、导出文件白名单。
- gopls：受控协议进程测试和指定真实客户端的导航／编译错误诊断。
- 前端：九视图、只读刷新、冻结不可刷新、跨任务/旧回包、主题、小屏、安全与生命周期；生成文件按哈希可复现。
- 原生macOS：诊断投影、四种Tunnel配置、全App编译、可选gopls载荷与签名封装。

安装包使用原有自签证书，更新源码与打包不等于安装。安装后确认App/Core版本一致，并在ChatGPT刷新AgentDock工具元数据，重新开启会话后再使用新增工具。托管Job跨进程测试不等于已在当前生产服务上主动重启或重装；本轮不替换当前运行实例。

## MCP 响应与客户端升级

完整源码、日志、错误和分页/版本护栏保存在 `structuredContent`；`content` 的普通文本是最多 1024 字节的状态摘要，不再重复整份 pretty JSON。动态 MCP 的正文/图片/音频/资源在外层标准 `content` 中保留一次，`result.content_location=mcp.content` 标明位置，上游结构化结果和未知元数据保持原样。消费者必须读取结构化结果，不能把摘要当成文件全文；只消费文本的旧客户端需要适配这一明确的线协议投影。

更新后刷新客户端工具定义，并新开会话核对 `work_result_show` 和 `job_control.abandon`。已有历史响应不会被改写。此次修改不等于已安装或已在真实 ChatGPT 宿主中验收。
