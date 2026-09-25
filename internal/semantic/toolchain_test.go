package semantic

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCachedGoSelectionRejectsSpoofedToolchain(t *testing.T) {
	cache := t.TempDir()
	fake := filepath.Join(cache, "golang.org", "toolchain@v0.0.1-go9.99.0."+runtime.GOOS+"-"+runtime.GOARCH, "bin")
	if err := os.MkdirAll(fake, 0700); err != nil {
		t.Fatal(err)
	}
	name := "go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(fake, name), []byte("not a Go distribution"), 0700); err != nil {
		t.Fatal(err)
	}
	root, v := cachedGoRoot(cache, "original-root", "go1.26.5")
	if root != "original-root" || v != "go1.26.5" {
		t.Fatal("invalid cached executable selected")
	}
}

func TestLocalGoEnvironmentCannotDownloadMissingToolchain(t *testing.T) {
	env := os.Environ()
	for k, v := range offlineEnvironment() {
		env = append(env, k+"="+v)
	}
	selected, v := localGoEnvironment(t.Context(), env)
	if len(selected) == 0 || v == "" {
		t.Skip("no locally usable Go executable")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.invalid/offline\n\ngo 9.99.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	name := "go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	command := exec.CommandContext(t.Context(), filepath.Join(selected["GOROOT"], "bin", name), "list", ".")
	command.Dir = root
	for k, v := range selected {
		env = append(env, k+"="+v)
	}
	command.Env = env
	if err := command.Run(); err == nil {
		t.Fatal("unsupported future toolchain was accepted")
	}
}
