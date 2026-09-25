package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRedirectCannotForwardFixedCredential(t *testing.T) {
	hits := make(chan string, 2)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits <- r.Header.Get("Authorization") }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(target.URL, "127.0.0.1", "localhost", 1), 307)
	}))
	defer source.Close()
	headers := http.Header{"Authorization": []string{"Bearer SYNTHETIC_ONLY"}}
	// 即使调用方忘记设置重定向策略，传输层的同源校验仍阻止转发。
	client := &http.Client{Transport: headerRoundTripper{headers: headers, origin: origin(source.URL)}, Timeout: time.Second}
	if response, err := client.Get(source.URL); err == nil {
		response.Body.Close()
		t.Fatal("cross-origin redirect accepted")
	}
	select {
	case h := <-hits:
		t.Fatalf("redirect reached target with header %q", h)
	default:
	}
	client.CheckRedirect = noRedirect
	response, err := client.Get(source.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 307 {
		t.Fatal("redirect followed")
	}
}
func TestOriginNormalization(t *testing.T) {
	if origin("https://example.test/x") != origin("https://EXAMPLE.test:443/y") {
		t.Fatal("default port mismatch")
	}
	for _, u := range []string{"http://example.test", "https://example.test:444", "https://other.test", "https://user:pass@example.test"} {
		if origin(u) == origin("https://example.test") {
			t.Fatalf("unsafe origin %s", u)
		}
	}
}
func TestResponseLimitRejectsOverflowNotEOF(t *testing.T) {
	r := &responseLimit{ReadCloser: io.NopCloser(strings.NewReader("123456")), remaining: 5}
	data, err := io.ReadAll(r)
	if string(data) != "12345" || err == nil {
		t.Fatalf("%q %v", data, err)
	}
}
func TestCancelledLookupDoesNotWaitForServer(t *testing.T) {
	state := &serverState{tools: map[string]Tool{"safe": {Name: "safe"}}}
	manager := &Manager{servers: map[string]ServerConfig{"fixture": {Name: "fixture", Enabled: true, TimeoutMS: 1000}}, states: map[string]*serverState{"fixture": state}}
	state.mu.Lock()
	defer state.mu.Unlock()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	done := make(chan error, 1)
	go func() { _, err := manager.ensureTools(ctx, "fixture"); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled lookup blocked")
	}
}
func TestQueueDeadlineAndGlobalLockIndependence(t *testing.T) {
	state := &serverState{}
	m := &Manager{servers: map[string]ServerConfig{"fixture": {Name: "fixture", Enabled: true, TimeoutMS: 30}}, states: map[string]*serverState{"fixture": state}}
	state.mu.Lock()
	defer state.mu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := m.ensureTools(t.Context(), "fixture"); done <- err }()
	// Waiting for one server must never hold m.mu and block a registry mutation.
	global := make(chan struct{})
	go func() { m.mu.Lock(); m.mu.Unlock(); close(global) }()
	select {
	case <-global:
	case <-time.After(time.Second):
		t.Fatal("server wait held global lock")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("queue excluded from timeout")
	}
}
