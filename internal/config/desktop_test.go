package config

import "testing"

func TestDesktopOptInEnvironment(t *testing.T) {
	for _, value := range []string{"false", "true"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("AGENTDOCK_DESKTOP_ENABLED", value)
			cfg, err := FromEnv()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.DesktopEnabled != (value == "true") {
				t.Fatalf("enabled=%v", cfg.DesktopEnabled)
			}
		})
	}
	t.Setenv("AGENTDOCK_DESKTOP_ENABLED", "invalid")
	if _, err := FromEnv(); err == nil {
		t.Fatal("invalid boolean accepted")
	}
}
