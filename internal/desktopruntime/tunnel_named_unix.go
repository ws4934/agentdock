//go:build darwin || linux

package desktopruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"
)

var errNamedTunnelUnhealthy = errors.New("Named Tunnel 持续未连接 Cloudflare，交由服务管理器重新启动")

// cloudflared 进程存活不等于隧道可用。网络切换或代理 Fake-IP 映射更新后，
// 旧进程可能一直重试失效的边缘地址；持续未就绪时回收本次子进程，让 launchd /
// systemd 使用原有凭据重新启动并重新解析 DNS，不重启 Core 或修改用户代理设置。
func runNamedTunnel(ctx context.Context, manifest unixRuntimeManifest, root, token string, stdout, stderr io.Writer) error {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("分配 Tunnel 本机健康检查地址失败: %w", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return err
	}
	arguments, err := prepareCloudflaredTunnelArgs(root, "--metrics", address, "run")
	if err != nil {
		return err
	}
	command := exec.Command(manifest.CloudflaredBinary, arguments...)
	command.Env = append(os.Environ(), "TUNNEL_TOKEN="+token)
	command.Stdout, command.Stderr = stdout, stderr
	command.WaitDelay = 3 * time.Second
	err = superviseNamedTunnel(ctx, command, "http://"+address+"/ready", 10*time.Second, 2*time.Minute)
	if errors.Is(err, errNamedTunnelUnhealthy) {
		fmt.Fprintln(stderr, "AgentDock: Named Tunnel 连续 2 分钟未就绪，重新建立连接（保留现有 Token）。")
	}
	return err
}

type tunnelRecoveryWindow struct {
	unhealthySince time.Time
}

func (window *tunnelRecoveryWindow) expired(now time.Time, ready bool, timeout time.Duration) bool {
	if ready {
		window.unhealthySince = time.Time{}
		return false
	}
	if window.unhealthySince.IsZero() {
		window.unhealthySince = now
	}
	return now.Sub(window.unhealthySince) >= timeout
}

func superviseNamedTunnel(ctx context.Context, command *exec.Cmd, readinessURL string, interval, timeout time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	stop := func(reason error) error {
		// 只回收由本次启动获得的进程句柄；不按进程名杀其他 Tunnel。
		_ = command.Process.Kill()
		<-wait
		return reason
	}
	// 探针固定使用 loopback，不能继承 HTTP_PROXY 或跟随重定向。
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	window := tunnelRecoveryWindow{}
	for {
		select {
		case err := <-wait:
			return err
		case <-ctx.Done():
			return stop(ctx.Err())
		case <-ticker.C:
			ready := probeNamedTunnelReadiness(ctx, client, readinessURL)
			if ctx.Err() != nil {
				return stop(ctx.Err())
			}
			if window.expired(time.Now(), ready, timeout) {
				return stop(errNamedTunnelUnhealthy)
			}
		}
	}
}

func probeNamedTunnelReadiness(ctx context.Context, client *http.Client, endpoint string) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false
	}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false
	}
	var payload struct {
		ReadyConnections int `json:"readyConnections"`
	}
	// 部分边缘连接断开不需要重启：至少一条连接可用就交给 cloudflared 自行恢复。
	return json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&payload) == nil && payload.ReadyConnections > 0
}
