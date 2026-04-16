package command

import (
	"bytes"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
)

func TestRunToolList_LipglossTable(t *testing.T) {
	cfg := makeServicesCfg(nil, config.ToolsConfig{
		Adminer:      config.ToolConfig{Enabled: false},
		RedisInsight: config.ToolConfig{Enabled: true},
		Mailpit:      config.ToolConfig{Enabled: true},
	}, config.RuntimePorts{
		Adminer:      8080,
		RedisInsight: 5540,
		Mailpit:      8025,
	}, config.RuntimeHosts{
		Adminer:      "adminer.localhost",
		RedisInsight: "redis.localhost",
		Mailpit:      "mail.localhost",
	})

	neverRunning := func(_, _ string) bool { return false }

	var buf bytes.Buffer
	w := render.NewWriter(&buf)
	if err := runToolList(w, cfg, neverRunning); err != nil {
		t.Fatalf("runToolList error: %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		"NAME", "HOST", "PORT", "STATE", "RUNNING",
		"adminer", "adminer.localhost", "8080", "disabled",
		"redis_insight", "redis.localhost", "5540", "enabled", "stopped",
		"mailpit", "mail.localhost", "8025", "enabled", "stopped",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestRunToolList_EnabledRunning(t *testing.T) {
	cfg := makeServicesCfg(nil, config.ToolsConfig{
		Adminer: config.ToolConfig{Enabled: true},
	}, config.RuntimePorts{
		Adminer: 8080,
	}, config.RuntimeHosts{
		Adminer: "adminer.localhost",
	})

	alwaysRunning := func(_, _ string) bool { return true }

	var buf bytes.Buffer
	w := render.NewWriter(&buf)
	if err := runToolList(w, cfg, alwaysRunning); err != nil {
		t.Fatalf("runToolList error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "running") {
		t.Errorf("enabled running tool should show 'running'\nfull output:\n%s", out)
	}
}

func TestRunToolList_AllDisabled(t *testing.T) {
	cfg := makeServicesCfg(nil, config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{})

	neverRunning := func(_, _ string) bool { return false }

	var buf bytes.Buffer
	w := render.NewWriter(&buf)
	if err := runToolList(w, cfg, neverRunning); err != nil {
		t.Fatalf("runToolList error: %v", err)
	}

	out := buf.String()
	// All tools should show disabled state
	if !strings.Contains(out, "disabled") {
		t.Errorf("output should contain 'disabled' for all-disabled tools\nfull output:\n%s", out)
	}
	// No tool should show stopped or running (disabled tools show —)
	if strings.Contains(out, "stopped") {
		t.Errorf("disabled tools should not show 'stopped'\nfull output:\n%s", out)
	}
}
