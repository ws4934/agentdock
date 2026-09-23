//go:build darwin

package command

import (
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"testing"
)

func TestCommandAndInternalEnvironmentsKeepMacOSUserIdentity(t *testing.T) {
	account, err := user.LookupId(strconv.Itoa(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("USER", "")
	t.Setenv("LOGNAME", "root")
	t.Setenv("VOLTA_HOME", "")
	t.Setenv("PATH", "/usr/bin:/bin")
	svc, _ := newCommandTestService(t)
	for _, internal := range []bool{false, true} {
		var env []string
		if internal {
			env, err = svc.InternalCommandEnv(nil)
		} else {
			env, err = svc.CommandEnv("", nil)
		}
		if err != nil {
			t.Fatal(err)
		}
		values := commandEnvValues(env)
		if values["USER"] != account.Username || values["LOGNAME"] != account.Username {
			t.Fatal("command lost user identity")
		}
		if !strings.Contains(values["PATH"], values["HOME"]+"/.volta/bin") {
			t.Fatal("command missing Volta PATH")
		}
		cmd := exec.Command("/bin/zsh", "-c", `printf '%s:%s' "$USER" "$LOGNAME"`)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil || string(out) != account.Username+":"+account.Username {
			t.Fatalf("child identity: %s (%v)", out, err)
		}
	}
}

func TestCommandMacOSIdentityStillAllowsExplicitOverrides(t *testing.T) {
	svc, _ := newCommandTestService(t)
	want := map[string]string{"USER": "explicit", "LOGNAME": "explicit", "HOME": "/custom/home", "PATH": "/custom/bin"}
	env, err := svc.CommandEnv("", want)
	if err != nil {
		t.Fatal(err)
	}
	values := commandEnvValues(env)
	for key, value := range want {
		if values[key] != value {
			t.Fatalf("explicit %s was overridden", key)
		}
	}
}
