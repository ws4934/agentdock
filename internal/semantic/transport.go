// Package semantic exposes a bounded, read-only subset of the Go LSP. Each
// query owns a fresh server; no stale server workspace survives source changes.
package semantic

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	processcontrol "github.com/uvwt/agentdock/internal/process"
)

const maxFrame = 8 << 20

type packet struct {
	Version string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	message := e.Message
	if len(message) > 512 {
		message = message[:512]
	}
	return fmt.Sprintf("LSP %d: %s", e.Code, message)
}

type rpcClient struct {
	input       io.WriteCloser
	output      io.ReadCloser
	packets     chan packet
	done        chan struct{}
	wait        chan error
	closed      chan struct{}
	stopOnce    sync.Once
	controller  *processcontrol.Controller
	mu          sync.Mutex
	sequence    int
	options     map[string]any
	rootURI     string
	diagnostics map[string]diagnosticNotice
}
type diagnosticNotice struct {
	URI         string            `json:"uri"`
	Version     *int              `json:"version"`
	Diagnostics []json.RawMessage `json:"diagnostics"`
}

func startClient(ctx context.Context, cmd *exec.Cmd, rootURI string, options map[string]any) (*rpcClient, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return nil, err
	}
	cmd.Stderr = io.Discard
	processcontrol.Configure(cmd)
	if err = cmd.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		return nil, err
	}
	controller, err := processcontrol.Attach(cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	client := &rpcClient{input: input, output: output, packets: make(chan packet, 16), done: make(chan struct{}), closed: make(chan struct{}), wait: make(chan error, 1), controller: controller, options: options, rootURI: rootURI, diagnostics: map[string]diagnosticNotice{}}
	go func() { client.readLoop(); close(client.done) }()
	go func() { err := cmd.Wait(); _ = controller.Close(); client.wait <- err }()
	go func() {
		select {
		case <-ctx.Done():
			client.close()
		case <-client.closed:
		}
	}()
	return client, nil
}
func (c *rpcClient) close() {
	c.stopOnce.Do(func() { close(c.closed); _ = c.controller.Terminate(); _ = c.input.Close(); _ = c.output.Close() })
}
func (c *rpcClient) readLoop() {
	reader := bufio.NewReaderSize(c.output, 16384)
	for {
		size := -1
		headers := 0
		for {
			line, err := reader.ReadSlice('\n')
			if err != nil {
				return
			}
			headers += len(line)
			if headers > 8192 {
				return
			}
			value := strings.TrimSpace(string(line))
			if value == "" {
				break
			}
			key, text, ok := strings.Cut(value, ":")
			if !ok {
				return
			}
			if strings.EqualFold(key, "Content-Length") {
				if size != -1 {
					return
				}
				size, err = strconv.Atoi(strings.TrimSpace(text))
				if err != nil {
					return
				}
			}
		}
		if size < 1 || size > maxFrame {
			return
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(reader, data); err != nil {
			return
		}
		var p packet
		if json.Unmarshal(data, &p) != nil || p.Version != "2.0" {
			return
		}
		select {
		case c.packets <- p:
		case <-c.closed:
			return
		}
	}
}
func (c *rpcClient) send(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) > maxFrame {
		return errors.New("LSP frame exceeds bound")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err = fmt.Fprintf(c.input, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	for len(data) > 0 {
		n, err := c.input.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
func (c *rpcClient) notify(method string, params any) error {
	return c.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}
func (c *rpcClient) incoming(p packet) error {
	if p.Method == "textDocument/publishDiagnostics" {
		var notice diagnosticNotice
		if json.Unmarshal(p.Params, &notice) == nil && len(c.diagnostics) < 128 && len(notice.Diagnostics) <= 2000 {
			c.diagnostics[notice.URI] = notice
		}
		return nil
	}
	if len(p.ID) == 0 {
		return nil
	}
	var result any
	switch p.Method {
	case "workspace/configuration":
		var params struct {
			Items []json.RawMessage `json:"items"`
		}
		if json.Unmarshal(p.Params, &params) != nil || len(params.Items) > 32 {
			return errors.New("invalid LSP configuration request")
		}
		values := make([]any, len(params.Items))
		for i := range values {
			values[i] = c.options
		}
		result = values
	case "workspace/workspaceFolders":
		result = []map[string]any{{"uri": c.rootURI, "name": "project"}}
	case "window/workDoneProgress/create", "client/registerCapability", "client/unregisterCapability":
		result = map[string]any{}
	case "window/showMessageRequest":
		result = nil
	case "workspace/applyEdit":
		result = map[string]any{"applied": false, "failureReason": "AgentDock semantic tools are read-only"}
	default:
		return c.send(map[string]any{"jsonrpc": "2.0", "id": p.ID, "error": rpcError{Code: -32601, Message: "Method not available in read-only client"}})
	}
	return c.send(map[string]any{"jsonrpc": "2.0", "id": p.ID, "result": result})
}
func (c *rpcClient) next(ctx context.Context) (packet, error) {
	select {
	case p := <-c.packets:
		return p, nil
	default:
	}
	select {
	case p := <-c.packets:
		return p, nil
	case <-ctx.Done():
		return packet{}, ctx.Err()
	case <-c.done:
		return packet{}, errors.New("language server disconnected")
	}
}
func (c *rpcClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.sequence++
	id := c.sequence
	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for {
		p, err := c.next(ctx)
		if err != nil {
			return nil, err
		}
		if p.Method == "" && string(p.ID) == strconv.Itoa(id) {
			if p.Error != nil {
				return nil, p.Error
			}
			return p.Result, nil
		}
		if err = c.incoming(p); err != nil {
			return nil, err
		}
	}
}
func (c *rpcClient) waitDiagnostics(ctx context.Context, uri string, version int) (json.RawMessage, bool, error) {
	for {
		if notice, ok := c.diagnostics[uri]; ok && notice.Version != nil && *notice.Version == version {
			data, err := json.Marshal(notice.Diagnostics)
			return data, true, err
		}
		p, err := c.next(ctx)
		if err != nil {
			return nil, false, err
		}
		if err = c.incoming(p); err != nil {
			return nil, false, err
		}
	}
}
