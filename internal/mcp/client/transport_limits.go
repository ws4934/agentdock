package client

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const maxRemoteMessageBytes int64 = 16 << 20
const maxRemoteCatalogBytes = 4 << 20
const maxRemoteTools = 2048

func origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return strings.ToLower(u.Scheme+"://"+u.Hostname()) + ":" + port
}
func noRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

// 限制解压后的响应体，达到上限必须报错，不能把截断 JSON 当成正常 EOF。
type responseLimit struct {
	io.ReadCloser
	remaining int64
}

func (r *responseLimit) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		var probe [1]byte
		n, err := r.ReadCloser.Read(probe[:])
		if n > 0 {
			return 0, errors.New("MCP_RESPONSE_TOO_LARGE")
		}
		return 0, err
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.ReadCloser.Read(p)
	r.remaining -= int64(n)
	return n, err
}

// stdio 的每条 JSON-RPC 消息以换行分帧；总进程寿命不限，单帧有明确上限。
type frameLimit struct {
	io.ReadCloser
	used     int64
	exceeded bool
}

func (r *frameLimit) Read(p []byte) (int, error) {
	if r.exceeded {
		return 0, errors.New("MCP_RESPONSE_TOO_LARGE")
	}
	n, err := r.ReadCloser.Read(p)
	for i, b := range p[:n] {
		r.used++
		if r.used > maxRemoteMessageBytes {
			r.exceeded = true
			return i, errors.New("MCP_RESPONSE_TOO_LARGE")
		}
		if b == '\n' {
			r.used = 0
		}
	}
	return n, err
}
