package securetunnel

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"github.com/uvwt/agentdock/internal/envstore"
	"io"
	"os"
	"sort"
	"strings"
)

// ClientEnvironment has an explicit credential boundary. AgentDock auth/Tunnel
// secrets are not forwarded. The official client can use its own saved profile.
func ClientEnvironment(_ map[string]string) ([]string, error) {
	allowed := map[string]bool{"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true, "TMPDIR": true, "TMP": true, "TEMP": true, "SYSTEMROOT": true, "WINDIR": true, "COMSPEC": true, "USERPROFILE": true, "APPDATA": true, "LOCALAPPDATA": true, "CONTROL_PLANE_API_KEY": true, "HTTPS_PROXY": true, "HTTP_PROXY": true, "NO_PROXY": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true}
	values := map[string]string{}
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok && allowed[strings.ToUpper(key)] {
			values[key] = value
		}
	}
	envstore.CompleteUserEnvironment(values)
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(values))
	for _, key := range keys {
		env = append(env, key+"="+values[key])
	}
	return env, nil

}
func Command(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: agentdock secure-tunnel <configure|status|validate|doctor|run> [options]")
	}
	flags := flag.NewFlagSet("agentdock secure-tunnel "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	home := flags.String("home", DefaultHome(), "AgentDock state home")
	var executable, profile, readiness *string
	var probe *bool
	if args[0] == "configure" {
		executable = flags.String("executable", "", "Absolute official tunnel-client binary")
		profile = flags.String("profile", "", "Existing official client profile")
		readiness = flags.String("readiness-url", "", "Optional loopback HTTP /readyz endpoint")
	}
	if args[0] == "status" {
		probe = flags.Bool("probe", false, "Probe configured local readiness without credentials")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("unexpected secure-tunnel argument")
	}
	service := New(*home, ClientEnvironment)
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	switch args[0] {
	case "configure":
		if err := service.Configure(ctx, Config{Executable: *executable, Profile: *profile, ReadinessURL: *readiness}); err != nil {
			return err
		}
		return encoder.Encode(service.Status(ctx, false))
	case "status":
		return encoder.Encode(service.Status(ctx, *probe))
	case "validate":
		if _, err := service.checked(); err != nil {
			return err
		}
		return encoder.Encode(map[string]any{"configured": true, "remote_delivery": "unknown"})
	case "doctor":
		result, err := service.Doctor(ctx)
		if err != nil {
			return err
		}
		return encoder.Encode(result)
	case "run":
		return service.Run(ctx, stdout, stderr)
	default:
		return errors.New("unsupported secure-tunnel action")
	}
}
