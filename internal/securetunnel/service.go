// Package securetunnel adapts a user-configured official OpenAI tunnel-client.
// It never provisions accounts/tunnels, stores API keys, rewrites official client
// profiles, or treats local readiness as proof of remote MCP delivery.
package securetunnel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/uvwt/agentdock/internal/fs/atomicfile"
	"github.com/uvwt/agentdock/internal/fs/processlock"
	"github.com/uvwt/agentdock/internal/fs/securepath"
	"github.com/uvwt/agentdock/internal/tool/command/session"
)

type Config struct {
	SchemaVersion int    `json:"schema_version"`
	Executable    string `json:"executable"`
	BinarySHA256  string `json:"binary_sha256"`
	Profile       string `json:"profile"`
	ReadinessURL  string `json:"readiness_url,omitempty"`
}
type Service struct {
	root string
	env  func(map[string]string) ([]string, error)
}

func New(home string, env func(map[string]string) ([]string, error)) *Service {
	return &Service{root: filepath.Join(home, "secure-tunnel"), env: env}
}
func DefaultHome() string {
	if home := os.Getenv("AGENTDOCK_HOME"); home != "" {
		return home
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agentdock")
}

var profileName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func ReadinessURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/readyz" || u.Port() == "" {
		return errors.New("readiness_url must be an explicit loopback HTTP port and /readyz, without credentials")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return errors.New("readiness_url must use a literal loopback IP")
	}
	return nil
}
func binaryHash(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("tunnel-client executable must be an absolute path")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 128<<20 {
		return "", errors.New("invalid tunnel-client executable")
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, (128<<20)+1))
	if err != nil || n != info.Size() {
		return "", errors.New("tunnel-client changed while hashing")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func (s *Service) Load() (Config, error) {
	path := filepath.Join(s.root, "config.json")
	info, err := os.Lstat(path)
	if err != nil {
		return Config{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16384 {
		return Config{}, errors.New("invalid secure-tunnel configuration file")
	}
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()
	var cfg Config
	if err = json.NewDecoder(io.LimitReader(file, 16385)).Decode(&cfg); err != nil {
		return cfg, err
	}
	if cfg.SchemaVersion != 1 || !profileName.MatchString(cfg.Profile) || !filepath.IsAbs(cfg.Executable) || len(cfg.BinarySHA256) != 64 {
		return cfg, errors.New("invalid secure-tunnel configuration")
	}
	if err = ReadinessURL(cfg.ReadinessURL); err != nil {
		return cfg, err
	}
	return cfg, nil
}
func (s *Service) Configure(ctx context.Context, cfg Config) error {
	if !profileName.MatchString(cfg.Profile) {
		return errors.New("invalid official client profile name")
	}
	if err := ReadinessURL(cfg.ReadinessURL); err != nil {
		return err
	}
	path, err := filepath.EvalSymlinks(cfg.Executable)
	if err != nil {
		return err
	}
	cfg.Executable = path
	digest, err := binaryHash(path)
	if err != nil {
		return err
	}
	cfg.BinarySHA256 = digest
	cfg.SchemaVersion = 1
	if err = os.MkdirAll(s.root, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(s.root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("secure-tunnel state must be a real directory")
	}
	if err = securepath.EnsurePrivate(s.root); err != nil {
		return err
	}
	deadline, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	guard, err := processlock.Acquire(deadline, filepath.Join(s.root, "config.lock"))
	if err != nil {
		return err
	}
	defer guard.Release()
	// Reconfiguration while active would mislabel an existing client instance.
	owner, available, err := processlock.TryAcquire(filepath.Join(s.root, "run.lock"))
	if err != nil {
		return err
	}
	if !available {
		return errors.New("stop the secure tunnel before reconfiguring")
	}
	defer owner.Release()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(s.root, "config.json"), data, 0600)
}
func (s *Service) Status(ctx context.Context, probe bool) map[string]any {
	result := map[string]any{"provider": "openai_secure_mcp_tunnel", "configured": false, "status": "not_configured", "remote_delivery": "unknown", "credentials_included": false, "account_provisioning": "external_official_workflow"}
	cfg, err := s.Load()
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			result["status"] = "invalid_configuration"
		}
		return result
	}
	result["configured"] = true
	result["profile"] = cfg.Profile
	result["status"] = "configured"
	result["local_readiness"] = "not_probed"
	hash, err := binaryHash(cfg.Executable)
	if err != nil {
		result["status"] = "client_unavailable"
		return result
	}
	if hash != cfg.BinarySHA256 {
		result["status"] = "client_identity_changed"
		return result
	}
	owner, available, err := processlock.TryAcquire(filepath.Join(s.root, "run.lock"))
	if err != nil {
		result["status"] = "owner_unknown"
		return result
	}
	if available {
		_ = owner.Release()
	} else {
		result["status"] = "running_remote_unconfirmed"
	}
	if probe && cfg.ReadinessURL != "" {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.ReadinessURL, nil)
		if err != nil {
			return result
		}
		transport := &http.Transport{Proxy: nil}
		defer transport.CloseIdleConnections()
		client := http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, err := client.Do(request)
		if err != nil {
			result["local_readiness"] = "unavailable"
		} else {
			_ = response.Body.Close()
			result["local_readiness"] = "not_ready"
			result["readiness_http_status"] = response.StatusCode
			if response.StatusCode == 200 {
				result["local_readiness"] = "ready_identity_unverified"
			}
		}
	}
	return result
}
func (s *Service) checked() (Config, error) {
	cfg, err := s.Load()
	if err != nil {
		return cfg, err
	}
	digest, err := binaryHash(cfg.Executable)
	if err != nil {
		return cfg, err
	}
	if digest != cfg.BinarySHA256 {
		return cfg, errors.New("official client binary changed; review and configure it again")
	}
	return cfg, nil
}
func (s *Service) Doctor(ctx context.Context) (map[string]any, error) {
	cfg, err := s.checked()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	env, err := s.env(nil)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(cfg.Executable, "doctor", "--profile", cfg.Profile, "--explain")
	cmd.Env = env
	code, err := session.RunOwned(ctx, cmd, io.Discard, io.Discard)
	// Official doctor output may contain credentials or configuration. Never return it.
	return map[string]any{"provider": "openai_secure_mcp_tunnel", "profile": cfg.Profile, "doctor_completed": err == nil && code == 0, "exit_code": code, "raw_output_included": false, "remote_delivery": "unknown"}, nil
}
func (s *Service) Run(ctx context.Context, stdout, stderr io.Writer) error {
	// Claim the same owner lock as Configure before reading the pinned identity.
	owner, available, err := processlock.TryAcquire(filepath.Join(s.root, "run.lock"))
	if err != nil {
		return err
	}
	if !available {
		return errors.New("secure tunnel already has an owner")
	}
	defer owner.Release()
	cfg, err := s.checked()
	if err != nil {
		return err
	}
	env, err := s.env(nil)
	if err != nil {
		return err
	}
	command := exec.Command(cfg.Executable, "run", "--profile", cfg.Profile)
	command.Env = env
	// Credentials remain with the official profile/environment. No header, token or
	// complete official output is copied into AgentDock's persisted logs.
	_, _ = fmt.Fprintln(stdout, "AgentDock: starting configured Secure MCP Tunnel profile; remote delivery remains unconfirmed.")
	code, err := session.RunOwned(ctx, command, io.Discard, io.Discard)
	if err != nil {
		return fmt.Errorf("Secure MCP Tunnel client exited (code %d)", code)
	}
	return nil
}
