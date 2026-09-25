package semantic

import (
	"context"
	"debug/buildinfo"
	"encoding/json"
	"go/version"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Offline Go selection reuses already installed toolchains only. In particular,
// GOTOOLCHAIN=auto plus GOSUMDB=off may reject even an extracted cached toolchain.
// Query go env outside the project, then select a verified local executable and
// keep GOTOOLCHAIN=local so neither gopls nor Go can download a toolchain.
func localGoEnvironment(ctx context.Context, env []string) (map[string]string, string) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "env", "-json", "GOMODCACHE", "GOROOT", "GOVERSION")
	command.Dir = os.TempDir()
	command.Env = env
	command.Stderr = io.Discard
	out := &limitedEnvOutput{}
	command.Stdout = out
	if command.Run() != nil || out.overflow {
		return nil, ""
	}
	var info struct{ GOMODCACHE, GOROOT, GOVERSION string }
	if json.Unmarshal(out.data, &info) != nil || !version.IsValid(info.GOVERSION) {
		return nil, ""
	}
	selected, current := info.GOROOT, info.GOVERSION
	// Explicit GOROOT is the operator's choice; do not replace it with a cache.
	explicitRoot := false
	path := ""
	for _, item := range env {
		if strings.HasPrefix(item, "GOROOT=") && strings.TrimPrefix(item, "GOROOT=") != "" {
			explicitRoot = true
		}
		if strings.HasPrefix(item, "PATH=") {
			path = strings.TrimPrefix(item, "PATH=")
		}
	}
	if !explicitRoot {
		selected, current = cachedGoRoot(info.GOMODCACHE, selected, current)
	}
	if selected == "" {
		return nil, current
	}
	return map[string]string{"GOROOT": selected, "PATH": filepath.Join(selected, "bin") + string(os.PathListSeparator) + path}, current
}

type limitedEnvOutput struct {
	data     []byte
	overflow bool
}

func (w *limitedEnvOutput) Write(p []byte) (int, error) {
	n := len(p)
	remain := 32768 - len(w.data)
	if n > remain {
		w.overflow = true
		p = p[:max(0, remain)]
	}
	w.data = append(w.data, p...)
	return n, nil
}

func cachedGoRoot(cache, selected, current string) (string, string) {
	dir, err := os.Open(filepath.Join(cache, "golang.org"))
	if err != nil {
		return selected, current
	}
	defer dir.Close()
	entries, err := dir.ReadDir(128)
	if err != nil && err != io.EOF {
		return selected, current
	}
	suffix := "." + runtime.GOOS + "-" + runtime.GOARCH
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || !strings.HasPrefix(name, "toolchain@v0.0.1-go") || !strings.HasSuffix(name, suffix) {
			continue
		}
		v := strings.TrimSuffix(strings.TrimPrefix(name, "toolchain@v0.0.1-"), suffix)
		if !version.IsValid(v) || strings.Contains(v, "rc") || strings.Contains(v, "beta") || version.Compare(v, current) <= 0 {
			continue
		}
		root := filepath.Join(cache, "golang.org", name)
		executable := filepath.Join(root, "bin", "go")
		if runtime.GOOS == "windows" {
			executable += ".exe"
		}
		stat, err := os.Stat(executable)
		if err != nil || !stat.Mode().IsRegular() {
			continue
		}
		build, err := buildinfo.ReadFile(executable)
		if err != nil || build.GoVersion != v || build.Path != "cmd/go" {
			continue
		}
		selected, current = root, v
	}
	return selected, current
}
