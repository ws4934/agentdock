//go:build darwin

package envstore

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

// CompleteUserEnvironment 补齐 macOS 宿主子进程的非敏感用户环境。
// ACP、动态 MCP 和命令工具共用此基线；Profile、Skill 和单次请求的显式覆盖应在其后应用。
// 不读取 shell 启动脚本，不复制 Provider 凭据或任意宿主环境变量。
func CompleteUserEnvironment(env map[string]string) {
	// 不能把继承的 USER/LOGNAME 当作身份事实；它们可能缺失或来自旧启动器。
	// 按有效 UID 查询系统用户数据库，不改变进程权限，也不读取 Keychain 凭据。
	if account, err := user.LookupId(strconv.Itoa(os.Geteuid())); err == nil {
		env["USER"] = account.Username
		env["LOGNAME"] = account.Username
		if env["HOME"] == "" {
			env["HOME"] = account.HomeDir
		}
	}
	for _, key := range []string{"SHELL", "VOLTA_HOME", "PNPM_HOME", "BUN_INSTALL"} {
		if env[key] == "" {
			if value := os.Getenv(key); filepath.IsAbs(value) {
				env[key] = value
			}
		}
	}
	if env["SHELL"] == "" {
		env["SHELL"] = "/bin/zsh"
	}
	env["PATH"] = strings.Join(userExecutableDirectories(env["HOME"], env), ":")
}

// 与 ACPConfiguration.swift 的 searchDirectories 保持同一顺序：稳定用户入口、
// 已配置 PATH、工具链目录、Homebrew、系统兜底。保留 shim 路径，不解析成版本化 target。
func userExecutableDirectories(home string, env map[string]string) []string {
	var candidates []string
	if filepath.IsAbs(home) {
		candidates = append(candidates, filepath.Join(home, ".local", "bin"))
	}
	candidates = append(candidates, filepath.SplitList(env["PATH"])...)
	appendToolHome := func(key string, suffix string, defaults ...string) {
		if root := env[key]; filepath.IsAbs(root) {
			candidates = append(candidates, filepath.Join(root, suffix))
		} else if filepath.IsAbs(home) {
			for _, entry := range defaults {
				candidates = append(candidates, filepath.Join(home, entry))
			}
		}
	}
	appendToolHome("VOLTA_HOME", "bin", ".volta/bin")
	appendToolHome("PNPM_HOME", "", "Library/pnpm", ".local/share/pnpm")
	appendToolHome("BUN_INSTALL", "bin", ".bun/bin")
	candidates = append(candidates, "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin")

	seen := make(map[string]bool, len(candidates))
	directories := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		// 不把空项或相对路径变成当前工作目录中的可执行文件搜索入口。
		if !filepath.IsAbs(candidate) {
			continue
		}
		candidate = filepath.Clean(candidate)
		if !seen[candidate] {
			seen[candidate] = true
			directories = append(directories, candidate)
		}
	}
	return directories
}
