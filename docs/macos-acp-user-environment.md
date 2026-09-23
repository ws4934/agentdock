# macOS ACP 用户环境修复

## 问题与根因

现象是终端里的 `claude auth status` 已登录，但通过 AgentDock 执行同一命令或启动 Claude ACP 时得到 `Authentication required`。另一个现象是 Volta 已安装 `claude-agent-acp`，桌面设置却显示未安装，CLI hook 还可能报 `node: command not found`。

这三个状态必须区分：GUI/Core 进程的原始环境、AgentDock 筛选后的子进程环境、用户交互式终端环境。本次复现中，GUI 和 Core 的 `USER`、`LOGNAME` 均正确；不是后台服务换了用户，也不是登录凭据不存在。`envstore.MinimalSystemEnv` 和命令工具自己的环境白名单没有保留用户身份字段，真正的丢失发生在创建子进程时。缺少身份字段的 shell/CLI 又可能推导出错误的登录名。

因此，单独修改 launch-core、重装 Claude、重新登录或仅给 ACP 填绝对路径都不能完整解决问题。

## 修复范围

`envstore.CompleteUserEnvironment` 作为 macOS 子进程环境的共享补全入口，用于 ACP、动态 stdio MCP、命令工具及其内部命令。

- 根据进程有效 UID 查询系统用户数据库，补齐 `USER`、`LOGNAME`；`HOME` 缺失时补系统用户目录，已有的 HOME 保留，`SHELL` 缺失时补 `/bin/zsh`。
- 只补非敏感用户/工具链信息，不读取 Keychain 中的凭据，不无条件继承宿主环境，不执行 `.zshrc` 等登录脚本。
- 保留 Profile、Skill、宿主显式映射和单次调用环境覆盖的既有优先级，Linux/Windows 策略不变。
- GUI 中的 ACP 自动发现与运行时采用相同的路径顺序，通过同一份 JSON fixture 校验 Go/Swift 契约。

默认路径顺序为：`~/.local/bin`、已有 PATH 中的绝对目录、Volta、pnpm、Bun、Homebrew 和系统目录。Volta 支持 `VOLTA_HOME/bin`，默认 `~/.volta/bin`；pnpm 支持 `PNPM_HOME`，默认兼顾 `~/Library/pnpm` 和 `~/.local/share/pnpm`；Bun 支持 `BUN_INSTALL/bin`，默认 `~/.bun/bin`。目录去重并忽略空项、相对项，不把当前工作目录加入默认搜索范围。

已经在 PATH 中选定的 nvm/fnm 等运行时保持其顺序，不扫描版本目录并随意挑选版本。自定义工具链目录必须由 Core 启动环境提供，或通过已有的显式环境映射配置；仅在另一个终端 export 不会改变已运行的 GUI/Core。

Volta 等管理器的稳定 shim 路径继续原样保存，不转换为版本化目标文件。已有自定义 ACP 配置不需要删除或重建。

## 回归验证

自动化覆盖以下层次：

1. `internal/envstore`：缺失/污染的用户身份、HOME/SHELL 补全、默认与自定义工具目录、空/相对/重复路径、秘密环境变量不泄露、显式覆盖与幂等性；真实 shell 子进程可找到临时 Volta node 入口。
2. `internal/tool/command`：外部和内部命令均得到正确身份，命令级显式覆盖仍然生效。
3. `internal/acp`：经真实 ACP 进程启动与 initialize 握手，在 Adapter 进程内确认身份、PATH、环境隔离和 Profile 覆盖。
4. macOS Swift：与 Go 共用 `internal/envstore/testdata/macos-user-path.json`；覆盖默认/自定义 Volta shim、断链、pnpm/Bun Node 入口及自定义 ACP 路径。

完整检查使用 `go test -race ./...`、`go vet ./...`、脚本治理检查与 `scripts/test/test-macos-app.sh`。长命令应在 tmux detached session 中执行并读取日志和退出码。

实际 Claude 验证必须经过新编译的 AgentDock Core 的 `exec_command`、`acp_session` 与 `acp_prompt`，不能只用额外手工注入 USER/PATH 的独立 Node 客户端替代。测试使用隔离的 AgentDock 状态目录和空工作目录，沿用用户已有的 Provider 登录态，不复制或打印认证凭据。

## 本次实测结果（2026-09-22）

- 定向 Go 回归、全量 `go test -race ./...`（47 个含测试包）、`go vet ./...`、脚本治理检查全部通过。
- `scripts/test/test-macos-app.sh` 完整通过，包含 Swift 配置/路径回归及 App、ZIP、DMG 签名与载荷检查。
- 新 Core 以 `USER` 为空、`LOGNAME=root`、PATH 仅系统目录的环境启动，通过真实 MCP `exec_command` 自动恢复正确用户身份、找到 Volta Node，`claude auth status` 返回 `loggedIn=true`。
- 通过新 Core 的 `acp_session` / `acp_prompt`，Claude ACP 0.81.0 完成 protocol v1 握手和会话创建；第一轮返回 `ACP_OK`，第二轮正确复述上一轮临时验证码，两轮均 `stop_reason=end_turn`，会话关闭成功。
- 没有手工向子进程补身份变量，没有导出或注入 Provider 登录凭据，没有改动用户现有 ACP Profile。

## 生效与排查

源代码提交不会替换已经运行的 AgentDock.app。需要安装包含新 Core 和 GUI 的完整 App 后重启；不要只改一份独立 CLI 二进制而继续使用 App 内旧 Helper。

安装后，在 AgentDock 中重新验证 `claude auth status` 和真实 ACP prompt。内置 Claude 现在应能发现默认 Volta 安装；继续使用原来的自定义绝对路径也可。

如果终端已登录而 AgentDock 仍报认证错误，先对比实际运行用户、HOME、USER、LOGNAME 和子进程 PATH；仅有一条认证失败日志不足以证明用户未登录。不要在排查输出中打印 Provider Token、Keychain 密码或完整敏感环境。
