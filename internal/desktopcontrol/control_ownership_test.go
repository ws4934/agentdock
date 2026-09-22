//go:build !windows

package desktopcontrol

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestActiveControlSocketCannotBeReplaced(t *testing.T) {
	root, rootErr := os.MkdirTemp("/tmp", "adctl-")
	if rootErr != nil {
		t.Fatal(rootErr)
	}
	defer os.RemoveAll(root)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, root, func(context.Context, Request) (any, error) { return map[string]bool{"original": true}, nil })
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var result map[string]bool
		if e := Call(t.Context(), root, "ping", nil, &result); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("original endpoint not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	otherCtx, otherCancel := context.WithCancel(t.Context())
	defer otherCancel()
	attempt := make(chan error, 1)
	go func() {
		attempt <- Serve(otherCtx, root, func(context.Context, Request) (any, error) { return nil, nil })
	}()
	select {
	case e := <-attempt:
		if e == nil || !strings.Contains(e.Error(), "already in use") {
			t.Fatalf("unexpected second server result: %v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("second server replaced a live control socket")
	}
	var result map[string]bool
	if e := Call(t.Context(), root, "ping", nil, &result); e != nil || !result["original"] {
		t.Fatalf("original monitor/stop channel disrupted: %v %v", result, e)
	}
	cancel()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
func TestStaleControlSocketCanBeRecovered(t *testing.T) {
	root, rootErr := os.MkdirTemp("/tmp", "adctl-")
	if rootErr != nil {
		t.Fatal(rootErr)
	}
	defer os.RemoveAll(root)
	listener, e := net.ListenUnix("unix", &net.UnixAddr{Name: endpointPath(root), Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	listener.SetUnlinkOnClose(false)
	if e = listener.Close(); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, root, func(context.Context, Request) (any, error) { return true, nil }) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var result bool
		if e := Call(t.Context(), root, "ping", nil, &result); e == nil && result {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stale socket was not recovered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
