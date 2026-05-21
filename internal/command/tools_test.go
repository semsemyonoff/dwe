package command

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/ui"
)

// --- pickToolToEnable / pickToolToDisable ---

func neverToolToggleFn(t *testing.T) selectToggleFn {
	return func(title string, items []ui.SelectorItem) (int, error) {
		t.Helper()
		t.Errorf("selector should not have been called, items=%d", len(items))
		return -1, fmt.Errorf("selector unexpectedly called")
	}
}

func alwaysToolToggleFn(idx int) selectToggleFn {
	return func(title string, items []ui.SelectorItem) (int, error) {
		if idx < 0 || idx >= len(items) {
			return -1, fmt.Errorf("index %d out of range for %d items", idx, len(items))
		}
		return idx, nil
	}
}

func TestPickToolToEnable_NoneDisabled_ReturnsError(t *testing.T) {
	cfg := makeServicesCfg(nil, config.ToolsConfig{
		"adminer": config.ToolConfig{Enabled: true, Container: "adminer", Host: "h", Port: 1},
	}, config.RuntimePorts{}, config.RuntimeHosts{})

	_, err := pickToolToEnable(cfg, neverToolToggleFn(t))
	if err == nil {
		t.Fatal("expected error when no disabled tools, got nil")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("error should mention 'disabled', got: %v", err)
	}
}

func TestPickToolToEnable_SelectorPicksSecond(t *testing.T) {
	cfg := makeServicesCfg(nil, config.ToolsConfig{
		"adminer":       config.ToolConfig{Enabled: false, Container: "adminer", Host: "h", Port: 1},
		"mailpit":       config.ToolConfig{Enabled: false, Container: "mailpit", Host: "h", Port: 2},
		"redis_insight": config.ToolConfig{Enabled: false, Container: "redis_insight", Host: "h", Port: 3},
	}, config.RuntimePorts{}, config.RuntimeHosts{})

	name, err := pickToolToEnable(cfg, alwaysToolToggleFn(1))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "mailpit" {
		t.Errorf("expected 'mailpit' from index 1, got %q", name)
	}
}

func TestPickToolToDisable_NoneEnabled_ReturnsError(t *testing.T) {
	cfg := makeServicesCfg(nil, make(config.ToolsConfig), config.RuntimePorts{}, config.RuntimeHosts{})

	_, err := pickToolToDisable(cfg, neverToolToggleFn(t))
	if err == nil {
		t.Fatal("expected error when no enabled tools, got nil")
	}
}

func TestPickToolToEnable_CancelPropagates(t *testing.T) {
	cfg := makeServicesCfg(nil, config.ToolsConfig{
		"adminer": config.ToolConfig{Enabled: false, Container: "adminer", Host: "h", Port: 1},
	}, config.RuntimePorts{}, config.RuntimeHosts{})
	cancelSelector := func(title string, items []ui.SelectorItem) (int, error) {
		return -1, ui.ErrCancelled
	}
	_, err := pickToolToEnable(cfg, cancelSelector)
	if !errors.Is(err, ui.ErrCancelled) {
		t.Errorf("expected ErrCancelled, got: %v", err)
	}
}

func TestToolEnableCmd_Args(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newToolEnableCmd(flags)
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("2 args should fail")
	}
}

func TestToolDisableCmd_Args(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newToolDisableCmd(flags)
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("2 args should fail")
	}
}

func TestSetToolEnabled_UnknownToolReturnsErrorMentioningAvailable(t *testing.T) {
	configPath := writeTempToolConfig(t, map[string]bool{
		"adminer": false,
		"mailpit": true,
	})
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	err = setToolEnabled(io.Discard, cfg, configPath, "nonexistent_tool", true)
	if err == nil {
		t.Fatal("expected error for unknown tool, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "nonexistent_tool") {
		t.Errorf("error should mention the unknown tool name, got: %q", msg)
	}
}

func TestToolNameSet_DerivedFromConfig(t *testing.T) {
	cfg := makeServicesCfg(nil, config.ToolsConfig{
		"elasticvue": config.ToolConfig{Enabled: false},
		"adminer":    config.ToolConfig{Enabled: true},
	}, config.RuntimePorts{}, config.RuntimeHosts{})

	set := toolNameSet(cfg)
	if !set["elasticvue"] || !set["adminer"] {
		t.Errorf("expected both tools in set, got %v", set)
	}
	if len(set) != 2 {
		t.Errorf("expected 2 tools, got %d", len(set))
	}
}

// --- Task 5: bare `tools` group ---

func TestToolsGroup_NoStatusOrListSubcommands(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	parent := newToolCmd(flags)
	for _, sub := range parent.Commands() {
		if sub.Use == "status" || sub.Use == "list" {
			t.Errorf("tools group should not register %q subcommand", sub.Use)
		}
	}
}

func TestToolsGroup_NoArgs(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newToolCmd(flags)
	if err := cmd.Args(cmd, []string{"foo"}); err == nil {
		t.Error("tools with positional arg should fail Args validation")
	}
}
