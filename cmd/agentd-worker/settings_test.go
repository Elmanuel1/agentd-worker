package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// unset clears keys for the test, whatever the shell running it holds.
func unset(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

// controller sets the controller's required settings.
func controller(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"ANTHROPIC_BASE_URL", "ANTHROPIC_ENVIRONMENT_ID", "ANTHROPIC_ENVIRONMENT_KEY", "AGENTD_HANDOFF_CERT",
		"AGENTD_HANDOFF_KEY", "AGENTD_HANDOFF_URL", "AGENTD_HANDOFF_CA", "AGENTD_SANDBOX_IMAGE", "AGENTD_TOOL_IMAGE",
	} {
		t.Setenv(k, "set")
	}
}

// TestHandoffWait holds SC-1's handoff wait: the backend's default, 120 s or 110 s on Fargate, unless
// one is set; an unknown backend is refused.
func TestHandoffWait(t *testing.T) {
	cases := []struct {
		launcher, set string
		want          time.Duration
		err           string
	}{
		{"docker", "", 120 * time.Second, ""},
		{"fargate", "", 110 * time.Second, ""},
		{"fargate", "90", 90 * time.Second, ""},
		{"docker", "two", 0, "AGENTD_HANDOFF_WAIT_SECONDS"},
		{"kubernetes", "", 0, "want docker or fargate"},
	}
	for _, c := range cases {
		t.Run(c.launcher+"/"+c.set, func(t *testing.T) {
			controller(t)
			t.Setenv("AGENTD_WORKER_LAUNCHER", c.launcher)
			unset(t, "AGENTD_HANDOFF_WAIT_SECONDS")
			if c.set != "" {
				t.Setenv("AGENTD_HANDOFF_WAIT_SECONDS", c.set)
			}
			var s ScaleSettings
			err := envconfig.Process("", &s)
			l, lerr := launcherNamed(s.Launcher)
			if err == nil {
				err = lerr
			}
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Errorf("error %v, want one naming %q", err, c.err)
				}
				return
			}
			if got := dispatchConfig(s, l).HandoffWait; err != nil || got != c.want {
				t.Errorf("handoff wait %s, %v; want %s", got, err, c.want)
			}
		})
	}
}

// TestSettings holds that a missing setting is named, defaults apply, and the key stays redacted (CV-4).
func TestSettings(t *testing.T) {
	unset(t, "ANTHROPIC_BASE_URL", "AGENTD_MAX_SANDBOXES", "AGENTD_RELEASE_INTERVAL_SECONDS", "AGENTD_RETENTION_DAYS",
		"AGENTD_REAP_INTERVAL_SECONDS", "AGENTD_STALE_AFTER_SECONDS")
	var missing ScaleSettings
	if err := envconfig.Process("", &missing); err == nil || !strings.Contains(err.Error(), "ANTHROPIC_BASE_URL") {
		t.Errorf("no settings: %v, want ANTHROPIC_BASE_URL named", err)
	}
	controller(t)
	t.Setenv("ANTHROPIC_ENVIRONMENT_KEY", "sk-live")
	var s ScaleSettings
	if err := envconfig.Process("", &s); err != nil {
		t.Fatal(err)
	}
	cfg, reap := dispatchConfig(s, launcher{}), s.ReapSettings.config()
	if cfg.MaxSandboxes != 100 || cfg.ReleaseInterval != 10*time.Minute || cfg.Retention != 0 || reap.Interval != time.Minute || reap.StaleAfter != 6*time.Minute {
		t.Errorf("defaults %+v, %+v", cfg, reap)
	}
	if s.Key.Reveal() != "sk-live" || strings.Contains(s.Key.String(), "sk-live") {
		t.Errorf("key %q reveals %q", s.Key, s.Key.Reveal())
	}
}
