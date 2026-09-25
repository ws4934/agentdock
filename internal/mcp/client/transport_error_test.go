package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	sdkjsonrpc "github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

func TestWrapSDKErrorClassifiesTransportRejected(t *testing.T) {
	client := &sdkProtocolClient{cfg: ServerConfig{Name: "demo"}}

	err := client.wrapSDKError("initialize MCP session", &sdkjsonrpc.Error{
		Code:    -32005,
		Message: "rejected by transport",
	})
	var mcpErr *Error
	if !errors.As(err, &mcpErr) {
		t.Fatalf("wrapSDKError() error = %T, want *Error", err)
	}
	if mcpErr.Code != "MCP_TRANSPORT_REJECTED" || !mcpErr.Retryable {
		t.Fatalf("wrapSDKError() = %#v, want retryable transport rejection", mcpErr)
	}
	if got := mcpErr.Details["rpc_code"]; got != int64(-32005) {
		t.Fatalf("rpc_code = %#v, want -32005", got)
	}
}

func TestWrapSDKErrorKeepsRemoteErrorsNonRetryable(t *testing.T) {
	client := &sdkProtocolClient{cfg: ServerConfig{Name: "demo"}}

	err := client.wrapSDKError("call MCP tool", &sdkjsonrpc.Error{
		Code:    -32602,
		Message: "invalid params",
	})
	var mcpErr *Error
	if !errors.As(err, &mcpErr) {
		t.Fatalf("wrapSDKError() error = %T, want *Error", err)
	}
	if mcpErr.Code != "MCP_REMOTE_ERROR" || mcpErr.Retryable {
		t.Fatalf("wrapSDKError() = %#v, want non-retryable remote error", mcpErr)
	}
}

func TestManagerTransportRejectedDoesNotPoisonServerStateOrRetryCall(t *testing.T) {
	var toolCallCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var request struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch request.Method {
		case "server/discover":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      request.ID,
				"error":   map[string]any{"code": -32601, "message": "Method not found"},
			})
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-transport")
			writeRPCResult(t, w, request.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "test", "version": "1.0.0"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "text/event-stream")
			payload, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      request.ID,
				"result": map[string]any{"tools": []map[string]any{{
					"name":        "echo",
					"description": "Echo supplied text",
					"inputSchema": map[string]any{
						"type":       "object",
						"required":   []string{"text"},
						"properties": map[string]any{"text": map[string]any{"type": "string"}},
					},
				}}},
			})
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", payload)
		case "tools/call":
			count := toolCallCount.Add(1)
			if count == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			arguments, _ := request.Params["arguments"].(map[string]any)
			writeRPCResult(t, w, request.ID, map[string]any{
				"content":           []map[string]any{{"type": "text", "text": arguments["text"]}},
				"structuredContent": map[string]any{"echo": arguments["text"]},
			})
		default:
			t.Errorf("unexpected method %q", request.Method)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if _, err := manager.Add(ServerConfig{
		Name:        "demo",
		Description: "Demo MCP server",
		Transport:   TransportStreamableHTTP,
		URL:         server.URL,
		Enabled:     true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Refresh(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}

	_, err = manager.Call(context.Background(), "demo:echo", map[string]any{"text": "first"})
	var mcpErr *Error
	if !errors.As(err, &mcpErr) || mcpErr.Code != "MCP_TRANSPORT_REJECTED" || mcpErr.Retryable || mcpErr.Details["do_not_replay"] != true {
		t.Fatalf("first Call() error = %#v, want non-replayable MCP_TRANSPORT_REJECTED", err)
	}
	if got := toolCallCount.Load(); got != 1 {
		t.Fatalf("tools/call count after rejected request = %d, want 1", got)
	}
	listed := manager.List()
	if len(listed) != 1 || listed[0].Status != "ready" || listed[0].LastError != "" {
		t.Fatalf("server summary after rejected request = %#v, want ready without last_error", listed)
	}

	result, err := manager.Call(context.Background(), "demo:echo", map[string]any{"text": "second"})
	if err != nil {
		t.Fatalf("second Call() error = %v", err)
	}
	structured, _ := result["structuredContent"].(map[string]any)
	if structured["echo"] != "second" {
		t.Fatalf("second Call() result = %#v", result)
	}
	if got := toolCallCount.Load(); got != 2 {
		t.Fatalf("tools/call count after second request = %d, want 2", got)
	}
}

func TestManagerRefreshFailureStillMarksServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.Method == "server/discover" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      request.ID,
				"error":   map[string]any{"code": -32601, "message": "Method not found"},
			})
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if _, err := manager.Add(ServerConfig{
		Name:        "demo",
		Description: "Demo MCP server",
		Transport:   TransportStreamableHTTP,
		URL:         server.URL,
		Enabled:     true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Refresh(context.Background(), "demo"); err == nil {
		t.Fatal("Refresh() succeeded, want transient initialization failure")
	}
	listed := manager.List()
	if len(listed) != 1 || listed[0].Status != "error" || listed[0].LastError == "" {
		t.Fatalf("server summary after refresh failure = %#v, want error with last_error", listed)
	}
}
