# macOS Tunnel 与控制状态栏稳定性

## 2026-09-24 排查结果

排查基线为 `d3bf998`，运行环境为 macOS 27.0 / arm64、AgentDock 0.8.3、cloudflared 2026.8.3。本次只修改源码并执行隔离验证，没有替换 `/Applications/AgentDock.app`，没有重启实际 Core/Tunnel，没有修改代理、Token、系统权限或证书。

### 固定域名 Tunnel 返回 530

故障日志反复出现 QUIC 超时和 `there are no free edge addresses left to resolve to`。旧进程反复连接 `198.18.0.5` / `198.18.0.67`；08:02:41 UTC 发生的已有重启之后，08:02:48—49 注册到 `198.18.0.120` / `198.18.0.222`。此次只读 DNS 查询返回后两个地址；公网 `/healthz` 和本地 cloudflared `/ready` 均返回 200，后者报告两条可用连接。

证据指向代理 Fake-IP / 网络切换之后旧边缘地址持续重试。尚未在实际网络上主动制造故障复现，也没有确认具体代理应用或映射更新触发原因。所查故障片段没有 Token 无效的认证错误；不能把“重输 Token 并重启后恢复”直接认定为 Token 丢失或失效。

原 Named Tunnel 启动器只等待子进程退出。进程仍活着却没有连接时，launchd 的 KeepAlive 无法触发重建。界面普通 Core 重启也不等于 Tunnel 重启。

本次修复：

- `internal/desktopruntime/tunnel_named_unix.go` 为本次 Named Tunnel 分配独立 loopback metrics 地址，每 10 秒读取 `/ready`。不使用 HTTP 代理、不跟随重定向，响应解码限制 4 KiB。
- 自第一次失败探针起连续两分钟无可用连接，回收并等待本次子进程退出，再以错误结束 supervisor。已有 launchd KeepAlive / systemd Restart=on-failure 负责重新启动，并复用原配置凭据、重新解析 DNS。不重启 Core，不按进程名结束其他 Tunnel。
- 至少一条连接可用即重置故障计时；短暂断线和部分连接降级由 cloudflared 自行恢复。停用/取消会回收当前子进程，不自行创建另一份后台服务。
- 此恢复逻辑处理 connector 断线，不修复 DNS 路由、Cloudflare 域名配置、源站故障或持续网络封锁。没有强制协议切换，也没有修改 Quick Tunnel / Windows 路径。
- 公网检查器只在 530 响应体中出现明确错误标记时提取 1xxx 代码，分别提示 1033 隧道未连接、1016 源站 DNS 错误；没有代码时保留一般 HTTP 530，不展示远端 HTML。

### 更新后再次请求系统权限

实际安装的 App 和 Core 均为 `Signature=adhoc`、`TeamIdentifier=not set`，designated requirement 为具体二进制的 `cdhash`。本机 `security find-identity -v -p codesigning` 返回零个有效身份。

这不是通过保存一个“已授权”标志能解决的问题。macOS 会检查新版本是否满足旧授权记录的代码要求；ad-hoc 的身份跟随构建内容变化。同名 App、相同版本号或 Bundle ID 都不能替代稳定签名身份。

`packaging/macos/build-app.sh` 已支持 `AGENTDOCK_CODESIGN_IDENTITY` 和可选 keychain，并为嵌套 Helper 与外层 App 使用稳定 identifier。后续应持续使用同一合适证书和一致的签名方式；开发签名与分发签名之间切换也需要核对 designated requirement。对外分发的 Developer ID / 公证流程不能由本机 ad-hoc 签名替代。

本次未生成证书、调整钥匙串信任或修改 TCC 数据库，也没有弱化代码要求。当前缺少签名身份，因此尚不能验证真实的证书签名跨版本授权保持；从 ad-hoc 切换到稳定身份后可能仍需要一次本机重新授权。详情见 `macos-permission-recovery.md`。

### 桌面控制图标常驻

只读桌面状态为 `idle`、空 session/task、`active_operations=0`，不是持续执行桌面输入。旧可见性判断把历史 session ID 和持久 `trusted_applications` 当作显示依据。

新增状态可见性规则：空闲且没有桌面任务时隐藏控制图标和残留面板；正在运行、暂停、停止清理中、清理失败、待审批、尚有操作或仍持有桌面任务时保留入口。持久信任本身不再保持第二个图标。主 AgentDock 菜单提供相同的“已信任的应用”撤销入口，不能通过隐藏图标丢失撤销能力。没有桌面任务时禁用“结束并释放桌面任务”。

监视器心跳仍在运行；没有关闭用户审批、暂停、紧急停止或失败清理保护。

## 验证

验证日志保留在 Git 忽略的 `dist/validation/macos-stability/`。

- 修改前：`go test ./internal/desktopruntime ./scripts/test` 通过。
- 修改后：`go test -race ./internal/desktopruntime`、`go test ./...` 通过，退出码均为 0。
- 新增八个 Go 顶层测试，覆盖连续失败计时/恢复重置、部分连接健康、格式/长度限制、重定向拒绝、无响应探针取消、自身子进程回收、退出码保留、原有凭据环境传递且不出现在参数中。
- Swift 回归覆盖闲置/活动/安全状态、闲置信任撤销入口、530 响应体识别及原有界面/停止故障注入/预览/权限诊断测试。
- `scripts/test/test-macos-app.sh` 完整通过，退出码 0：Swift 界面与故障注入回归、当前 arm64 架构 App 编译、DMG/ZIP 构建、校验和、嵌套代码签名验证、解包/只读挂载内容一致性均通过。测试使用 ad-hoc 签名，payload 中的 cloudflared 是测试替身，不是可交付安装包；不能将此结果当作证书签名跨版本授权验证。
- `git diff --check` 通过。

代码层验证不等于已在用户实际代理故障环境或跨版本 TCC 迁移中验收。本次没有主动中断当前连接来制造公网 530。

## 官方依据

- Cloudflare 530 与响应体 1xxx 错误：https://developers.cloudflare.com/support/troubleshooting/http-status-codes/cloudflare-5xx-errors/error-530/
- Cloudflare 1033 与 connector 健康状态：https://developers.cloudflare.com/support/troubleshooting/http-status-codes/cloudflare-1xxx-errors/error-1033/
- Cloudflare Tunnel 连接排障：https://developers.cloudflare.com/tunnel/troubleshooting/
- Apple TN3127：跨版本代码要求与 ad-hoc 身份：https://developer.apple.com/documentation/technotes/tn3127-inside-code-signing-requirements
- Mihomo Fake-IP 地址范围（仅解释地址特征，不据此确定用户代理产品）：https://wiki.metacubex.one/config/dns/
