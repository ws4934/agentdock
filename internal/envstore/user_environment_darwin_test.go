//go:build darwin

package envstore

import (
	"encoding/json"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestUserExecutableDirectoriesSharedMacOSContract(t *testing.T) {
	data, err := os.ReadFile("testdata/macos-user-path.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name        string
		Home        string
		Environment map[string]string
		Expected    []string
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			if got := userExecutableDirectories(tc.Home, tc.Environment); !reflect.DeepEqual(got, tc.Expected) {
				t.Fatalf("directories = %#v, want %#v", got, tc.Expected)
			}
		})
	}
}

func TestMinimalSystemEnvRestoresMacOSIdentityWithoutLeakingSecrets(t *testing.T) {
	account, err := user.LookupId(strconv.Itoa(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USER", "")
	t.Setenv("LOGNAME", "wrong-launcher-user")
	t.Setenv("SHELL", "")
	t.Setenv("PATH", "/usr/bin:/bin")
	for _, key := range []string{"VOLTA_HOME", "PNPM_HOME", "BUN_INSTALL"} {
		t.Setenv(key, "")
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "NODE_OPTIONS", "DYLD_INSERT_LIBRARIES", "AGENTDOCK_AUTH_TOKEN"} {
		t.Setenv(key, "must-not-inherit")
	}
	env := MinimalSystemEnv()
	if env["USER"] != account.Username || env["LOGNAME"] != account.Username {
		t.Fatalf("incorrect child identity: USER=%q LOGNAME=%q", env["USER"], env["LOGNAME"])
	}
	if env["HOME"] != home || env["SHELL"] != "/bin/zsh" {
		t.Fatal("HOME/SHELL baseline missing")
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "NODE_OPTIONS", "DYLD_INSERT_LIBRARIES", "AGENTDOCK_AUTH_TOKEN"} {
		if _, exists := env[key]; exists {
			t.Fatalf("unconfigured host variable %s leaked", key)
		}
	}
	before := Merge(env)
	CompleteUserEnvironment(env)
	if !reflect.DeepEqual(env, before) {
		t.Fatal("completion must be idempotent")
	}
}

func TestCompleteUserEnvironmentFillsMissingHome(t *testing.T) {
	account, err := user.LookupId(strconv.Itoa(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	CompleteUserEnvironment(env)
	if env["HOME"] != account.HomeDir {
		t.Fatalf("HOME = %q, want system user home", env["HOME"])
	}
}

func TestMinimalSystemEnvChildFindsVoltaWithoutLoginShell(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, ".volta", "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\nprintf VOLTA_NODE_OK\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("VOLTA_HOME", "")
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("USER", "")
	t.Setenv("LOGNAME", "root")
	env := MinimalSystemEnv()
	cmd := exec.Command("/bin/zsh", "-c", `printf '%s:%s:' "$USER" "$LOGNAME"; node`)
	cmd.Env = Format(env)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child failed: %v: %s", err, out)
	}
	if want := env["USER"] + ":" + env["LOGNAME"] + ":VOLTA_NODE_OK"; string(out) != want {
		t.Fatalf("child output = %q, want %q", out, want)
	}
}

func TestMinimalSystemEnvKeepsCustomToolRootsAndExplicitOverrides(t *testing.T) {
	root := t.TempDir()
	t.Setenv("VOLTA_HOME", filepath.Join(root, "Volta Data"))
	t.Setenv("PNPM_HOME", filepath.Join(root, "pnpm"))
	t.Setenv("BUN_INSTALL", filepath.Join(root, "bun"))
	env := MinimalSystemEnv()
	for key, suffix := range map[string]string{"VOLTA_HOME": "/bin", "PNPM_HOME": "", "BUN_INSTALL": "/bin"} {
		if env[key] != os.Getenv(key) || !strings.Contains(":"+env["PATH"]+":", ":"+env[key]+suffix+":") {
			t.Fatalf("custom %s was not propagated to the child", key)
		}
	}
	overrides := map[string]string{"PATH": "/explicit/bin", "HOME": "/explicit/home", "USER": "explicit-user", "LOGNAME": "explicit-user"}
	merged := Merge(env, overrides)
	for key, want := range overrides {
		if merged[key] != want {
			t.Fatalf("explicit %s was overridden", key)
		}
	}
}
