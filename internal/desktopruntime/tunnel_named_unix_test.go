//go:build darwin || linux

package desktopruntime

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTunnelRecoveryWindowRequiresContinuousFailure(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	window := tunnelRecoveryWindow{}
	for _, step := range []struct {
		seconds int
		ready   bool
		want    bool
	}{
		{0, false, false}, {119, false, false}, {120, false, true},
		{121, true, false}, {122, false, false}, {241, false, false},
		{242, false, true}, {243, true, false}, {1000, true, false},
	} {
		if got := window.expired(start.Add(time.Duration(step.seconds)*time.Second), step.ready, 2*time.Minute); got != step.want {
			t.Fatalf("at %ds ready=%v: expired=%v, want %v", step.seconds, step.ready, got, step.want)
		}
	}
}

func TestProbeNamedTunnelReadiness(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
		ready  bool
	}{
		{"healthy", 200, `{"readyConnections":4}`, true},
		{"degraded but serving", 200, `{"readyConnections":1}`, true},
		{"no connection", 200, `{"readyConnections":0}`, false},
		{"unavailable", 503, `{"readyConnections":0}`, false},
		{"status takes precedence", 503, `{"readyConnections":4}`, false},
		{"not a readiness endpoint", 200, `{"ok":true}`, false},
		{"malformed", 200, `not json`, false},
		{"bounded body", 200, `{"readyConnections":` + strings.Repeat(" ", 4096) + `4}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/ready" {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			if got := probeNamedTunnelReadiness(t.Context(), server.Client(), server.URL+"/ready"); got != tt.ready {
				t.Fatalf("ready=%v, want %v", got, tt.ready)
			}
		})
	}
}

func TestSuperviseNamedTunnelReapsUnhealthyProcess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	command := exec.Command("/bin/sleep", "30")
	err := superviseNamedTunnel(ctx, command, server.URL+"/ready", 5*time.Millisecond, 30*time.Millisecond)
	if !errors.Is(err, errNamedTunnelUnhealthy) {
		t.Fatalf("got %v", err)
	}
	if command.ProcessState == nil {
		t.Fatal("unhealthy child was not reaped")
	}
}

func TestSuperviseNamedTunnelKeepsHealthyProcessUntilCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"readyConnections":1}`)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	command := exec.Command("/bin/sleep", "30")
	err := superviseNamedTunnel(ctx, command, server.URL+"/ready", 5*time.Millisecond, 30*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("healthy child restarted: %v", err)
	}
	if command.ProcessState == nil {
		t.Fatal("cancelled child was not reaped")
	}
}

func TestSuperviseNamedTunnelDoesNotFollowRedirects(t *testing.T) {
	var redirected atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			http.Redirect(w, r, "/other-service", http.StatusFound)
			return
		}
		redirected.Store(true)
		_, _ = io.WriteString(w, `{"readyConnections":4}`)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err := superviseNamedTunnel(ctx, exec.Command("/bin/sleep", "30"), server.URL+"/ready", 5*time.Millisecond, 30*time.Millisecond)
	if !errors.Is(err, errNamedTunnelUnhealthy) || redirected.Load() {
		t.Fatalf("redirect followed=%v, err=%v", redirected.Load(), err)
	}
}

func TestSuperviseNamedTunnelCancelsStalledProbe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	command := exec.Command("/bin/sleep", "30")
	err := superviseNamedTunnel(ctx, command, server.URL+"/ready", time.Millisecond, time.Minute)
	if !errors.Is(err, context.DeadlineExceeded) || command.ProcessState == nil {
		t.Fatalf("cancellation did not reap child: %v", err)
	}
}

func TestSuperviseNamedTunnelPropagatesProcessFailure(t *testing.T) {
	command := exec.Command("/bin/sh", "-c", "exit 7")
	err := superviseNamedTunnel(t.Context(), command, "http://127.0.0.1:1/ready", time.Second, time.Minute)
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 7 {
		t.Fatalf("lost child exit status: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	command = exec.Command("/bin/sleep", "30")
	if err := superviseNamedTunnel(ctx, command, "", time.Second, time.Minute); !errors.Is(err, context.Canceled) || command.Process != nil {
		t.Fatalf("started a process after cancellation: %v", err)
	}

	command = exec.Command(filepath.Join(t.TempDir(), "missing-cloudflared"))
	if err := superviseNamedTunnel(t.Context(), command, "", time.Second, time.Minute); err == nil {
		t.Fatal("missing executable accepted")
	}
}

func TestRunNamedTunnelUsesPrivateMetricsAndExistingCredential(t *testing.T) {
	root := t.TempDir()
	argumentsFile := filepath.Join(root, "arguments")
	t.Setenv("AGENTDOCK_TEST_ARGUMENTS", argumentsFile)
	t.Setenv("TUNNEL_TOKEN", "stale-test-value")
	binary := filepath.Join(root, "fake-cloudflared")
	script := "#!/bin/sh\n[ \"$TUNNEL_TOKEN\" = 'saved-test-value' ] || exit 11\nprintf '%s\\n' \"$@\" > \"$AGENTDOCK_TEST_ARGUMENTS\"\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	err := runNamedTunnel(t.Context(), unixRuntimeManifest{CloudflaredBinary: binary}, root, "saved-test-value", io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(argumentsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(args) != 7 || args[0] != "--config" || args[2] != "tunnel" || args[3] != "--no-autoupdate" || args[4] != "--metrics" || args[6] != "run" {
		t.Fatalf("unexpected arguments: %q", args)
	}
	host, port, err := net.SplitHostPort(args[5])
	if err != nil || host != "127.0.0.1" || port == "0" || port == "" {
		t.Fatalf("metrics must use an allocated loopback port: %q", args[5])
	}
	if strings.Contains(string(data), "saved-test-value") {
		t.Fatal("credential exposed in command arguments")
	}
}
