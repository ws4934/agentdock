package securetunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) >= 2 && (os.Args[1] == "doctor" || os.Args[1] == "run") {
		if os.Getenv("AGENTDOCK_AUTH_TOKEN") != "" || os.Getenv("TUNNEL_TOKEN") != "" {
			os.Exit(8)
		}
		if len(os.Args) < 4 || os.Args[2] != "--profile" || os.Args[3] != "test-profile" {
			os.Exit(9)
		}
		fmt.Println("PRIVATE-CLIENT-OUTPUT-DO-NOT-RETURN")
		if os.Args[1] == "run" {
			time.Sleep(500 * time.Millisecond)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestOptionalConfigurationAndCredentialBoundary(t *testing.T) {
	t.Setenv("AGENTDOCK_AUTH_TOKEN", "CORE-SECRET")
	t.Setenv("TUNNEL_TOKEN", "CF-SECRET")
	s := New(t.TempDir(), ClientEnvironment)
	if status := s.Status(t.Context(), false); status["configured"] != false {
		t.Fatal("unconfigured tunnel claimed ready")
	}
	binary, _ := os.Executable()
	cfg := Config{Executable: binary, Profile: "test-profile"}
	if err := s.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	report, err := s.Doctor(t.Context())
	if err != nil || report["doctor_completed"] != true {
		t.Fatalf("%#v %v", report, err)
	}
	encoded, _ := json.Marshal(report)
	if strings.Contains(string(encoded), "PRIVATE-CLIENT") {
		t.Fatal("raw doctor output leaked")
	}
	var stdout, stderr bytes.Buffer
	if err = s.Run(t.Context(), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String()+stderr.String(), "PRIVATE-CLIENT") {
		t.Fatal("client secret leaked")
	}
	data, _ := os.ReadFile(filepath.Join(s.root, "config.json"))
	for _, secret := range []string{"CORE-SECRET", "CF-SECRET"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("credential was persisted")
		}
	}
}
func TestReadinessIsLoopbackOnlyAndNotRemoteDelivery(t *testing.T) {
	for _, bad := range []string{"https://example.com/readyz", "http://localhost:8080/readyz", "http://127.0.0.1:8080/other", "http://user:pass@127.0.0.1:8080/readyz", "http://127.0.0.1:8080/readyz?secret=x", "http://10.0.0.1:80/readyz"} {
		if ReadinessURL(bad) == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" || r.Header.Get("Authorization") != "" {
			t.Error("probe leaked auth or incorrect path")
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	s := New(t.TempDir(), ClientEnvironment)
	binary, _ := os.Executable()
	if err := s.Configure(t.Context(), Config{Executable: binary, Profile: "test-profile", ReadinessURL: server.URL + "/readyz"}); err != nil {
		t.Fatal(err)
	}
	status := s.Status(t.Context(), true)
	if status["local_readiness"] != "ready_identity_unverified" || status["remote_delivery"] != "unknown" {
		t.Fatalf("%#v", status)
	}
}
func TestClientIdentityPinAndRunCancellation(t *testing.T) {
	s := New(t.TempDir(), ClientEnvironment)
	binary, _ := os.Executable()
	if err := s.Configure(t.Context(), Config{Executable: binary, Profile: "test-profile"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
	defer cancel()
	var out bytes.Buffer
	started := time.Now()
	if err := s.Run(ctx, &out, &out); err == nil || time.Since(started) > 3*time.Second {
		t.Fatalf("cancel failed: %v", err)
	}
	cfg, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.BinarySHA256 = strings.Repeat("0", 64)
	b, _ := json.Marshal(cfg)
	_ = os.WriteFile(filepath.Join(s.root, "config.json"), b, 0600)
	if _, err = s.Doctor(t.Context()); err == nil {
		t.Fatal("changed client identity was used")
	}
	if s.Status(t.Context(), false)["status"] != "client_identity_changed" {
		t.Fatal("changed binary status missing")
	}
}
