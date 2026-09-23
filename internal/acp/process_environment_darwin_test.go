//go:build darwin

package acp

import (
	"context"
	"encoding/json"
	"os"
	"os/user"
	"strconv"
	"strings"
	"testing"
)

func TestACPProcessKeepsMacOSIdentityAndProfileOverrides(t *testing.T) {
	account, err := user.LookupId(strconv.Itoa(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USER", "")
	t.Setenv("LOGNAME", "root")
	t.Setenv("VOLTA_HOME", "")
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("ANTHROPIC_API_KEY", "must-not-inherit")
	for _, override := range []bool{false, true} {
		env := map[string]string{"GO_ACP_USER_ENV_HELPER": "1"}
		if override {
			env["USER"] = "profile-user"
			env["LOGNAME"] = "profile-user"
			env["PATH"] = "/profile/bin"
		}
		process, err := startAgentProcess(context.Background(), AgentSpec{
			Name: "env-helper", Command: os.Args[0], Args: []string{"-test.run=^TestACPUserEnvironmentHelper$"}, Environment: env,
		}, t.TempDir(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		values, ok := process.initialize.AgentCapabilities["testEnvironment"].(map[string]any)
		_ = process.Close()
		if !ok {
			t.Fatal("helper did not report child environment")
		}
		want := account.Username
		if override {
			want = "profile-user"
		}
		if values["USER"] != want || values["LOGNAME"] != want {
			t.Fatal("ACP lost identity or profile override")
		}
		if values["secretLeaked"] != false {
			t.Fatal("unconfigured provider credential leaked")
		}
		path, _ := values["PATH"].(string)
		if override {
			if path != "/profile/bin" {
				t.Fatal("ACP profile PATH override lost")
			}
		} else if !strings.Contains(path, os.Getenv("HOME")+"/.volta/bin") {
			t.Fatal("ACP child missing Volta")
		}
	}
}

func TestACPUserEnvironmentHelper(t *testing.T) {
	if os.Getenv("GO_ACP_USER_ENV_HELPER") != "1" {
		return
	}
	decoder, encoder := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	for {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := decoder.Decode(&request); err != nil {
			os.Exit(0)
		}
		if request.Method != "initialize" {
			continue
		}
		err := encoder.Encode(map[string]any{
			"jsonrpc": "2.0", "id": request.ID,
			"result": map[string]any{
				"protocolVersion": ProtocolVersion,
				"agentInfo":       AgentInfo{Name: "environment-helper"},
				"agentCapabilities": map[string]any{"testEnvironment": map[string]any{
					"USER": os.Getenv("USER"), "LOGNAME": os.Getenv("LOGNAME"), "PATH": os.Getenv("PATH"),
					"secretLeaked": os.Getenv("ANTHROPIC_API_KEY") != "",
				}},
			},
		})
		if err != nil {
			os.Exit(2)
		}
	}
}
