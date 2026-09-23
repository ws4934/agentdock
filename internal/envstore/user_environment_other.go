//go:build !darwin

package envstore

// CompleteUserEnvironment 在非 macOS 平台保留现有服务/容器环境策略。
func CompleteUserEnvironment(_ map[string]string) {}
