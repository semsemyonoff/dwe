package command

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"
)

func TestRunToolList_LipglossTable(t *testing.T) {
	cfg := makeServicesCfg(nil, config.ToolsConfig{
		"adminer": config.ToolConfig{
			Enabled:   false,
			Host:      "adminer.localhost",
			Port:      8080,
			Container: "adminer",
		},
		"redis_insight": config.ToolConfig{
			Enabled:   true,
			Host:      "redis.localhost",
			Port:      5540,
			Container: "redis_insight",
		},
		"mailpit": config.ToolConfig{
			Enabled:   true,
			Host:      "mail.localhost",
			Port:      8025,
			Container: "mailpit",
		},
	}, make(config.RuntimePorts), make(config.RuntimeHosts))

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
		"adminer": config.ToolConfig{
			Enabled:   true,
			Host:      "adminer.localhost",
			Port:      8080,
			Container: "adminer",
		},
	}, make(config.RuntimePorts), make(config.RuntimeHosts))

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
	cfg := makeServicesCfg(nil, config.ToolsConfig{
		"adminer":       config.ToolConfig{Enabled: false, Container: "adminer", Host: "adminer.localhost", Port: 8080},
		"redis_insight": config.ToolConfig{Enabled: false, Container: "redis_insight", Host: "redis.localhost", Port: 5540},
		"mailpit":       config.ToolConfig{Enabled: false, Container: "mailpit", Host: "mail.localhost", Port: 8025},
	}, make(config.RuntimePorts), make(config.RuntimeHosts))

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

func TestToolStatusCmd_SameOutputAsToolList(t *testing.T) {
	cfg := makeServicesCfg(nil, config.ToolsConfig{
		"adminer": config.ToolConfig{
			Enabled:   false,
			Container: "adminer",
			Host:      "adminer.localhost",
			Port:      8080,
		},
		"mailpit": config.ToolConfig{
			Enabled:   true,
			Container: "mailpit",
			Host:      "mail.localhost",
			Port:      8025,
		},
	}, make(config.RuntimePorts), make(config.RuntimeHosts))

	neverRunning := func(_, _ string) bool { return false }

	var listBuf, statusBuf bytes.Buffer
	if err := runToolList(render.NewWriter(&listBuf), cfg, neverRunning); err != nil {
		t.Fatalf("runToolList error: %v", err)
	}
	if err := runToolList(render.NewWriter(&statusBuf), cfg, neverRunning); err != nil {
		t.Fatalf("runToolList (status) error: %v", err)
	}

	if listBuf.String() != statusBuf.String() {
		t.Errorf("tools status output differs from tools list output\nlist:\n%s\nstatus:\n%s",
			listBuf.String(), statusBuf.String())
	}
}

func TestToolStatusCmd_Registered(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	parent := newToolCmd(flags)

	var found bool
	for _, sub := range parent.Commands() {
		if sub.Use == "status" {
			found = true
			break
		}
	}
	if !found {
		t.Error("tools status subcommand is not registered")
	}
}

func TestToolStatusCmd_ContainsExpectedColumns(t *testing.T) {
	cfg := makeServicesCfg(nil, config.ToolsConfig{
		"adminer": config.ToolConfig{
			Enabled:   true,
			Container: "adminer",
			Host:      "adminer.localhost",
			Port:      8080,
		},
	}, make(config.RuntimePorts), make(config.RuntimeHosts))

	neverRunning := func(_, _ string) bool { return false }

	var buf bytes.Buffer
	w := render.NewWriter(&buf)
	if err := runToolList(w, cfg, neverRunning); err != nil {
		t.Fatalf("runToolList error: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"NAME", "HOST", "PORT", "STATE", "RUNNING", "adminer", "adminer.localhost", "8080"} {
		if !strings.Contains(out, want) {
			t.Errorf("tools status output missing %q\nfull output:\n%s", want, out)
		}
	}
}

// --- Task 8: tools enable/disable interactive selector ---

// neverToolToggleFn returns a selectToggleFn that fails the test if called.
func neverToolToggleFn(t *testing.T) selectToggleFn {
	return func(title string, items []ui.SelectorItem) (int, error) {
		t.Helper()
		t.Errorf("selector should not have been called, but was called with %d items", len(items))
		return -1, fmt.Errorf("selector unexpectedly called")
	}
}

// alwaysToolToggleFn returns a selectToggleFn that always picks the item at idx.
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
		"adminer":       config.ToolConfig{Enabled: true, Container: "adminer", Host: "adminer.localhost", Port: 8080},
		"redis_insight": config.ToolConfig{Enabled: true, Container: "redis_insight", Host: "redis.localhost", Port: 5540},
		"mailpit":       config.ToolConfig{Enabled: true, Container: "mailpit", Host: "mail.localhost", Port: 8025},
	}, config.RuntimePorts{}, config.RuntimeHosts{})

	_, err := pickToolToEnable(cfg, neverToolToggleFn(t))
	if err == nil {
		t.Fatal("expected error when no disabled tools, got nil")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("error should mention 'disabled', got: %v", err)
	}
}

func TestPickToolToEnable_SingleDisabled_ShowsSelector(t *testing.T) {
	cfg := makeServicesCfg(nil, config.ToolsConfig{
		"adminer":       config.ToolConfig{Enabled: false, Container: "adminer", Host: "adminer.localhost", Port: 8080},
		"redis_insight": config.ToolConfig{Enabled: true, Container: "redis_insight", Host: "redis.localhost", Port: 5540},
		"mailpit":       config.ToolConfig{Enabled: true, Container: "mailpit", Host: "mail.localhost", Port: 8025},
	}, config.RuntimePorts{}, config.RuntimeHosts{})

	selectorCalled := false
	sel := func(title string, items []ui.SelectorItem) (int, error) {
		selectorCalled = true
		if len(items) != 1 {
			t.Errorf("expected 1 item in selector, got %d", len(items))
		}
		return 0, nil
	}

	name, err := pickToolToEnable(cfg, sel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !selectorCalled {
		t.Error("selector should have been called even for a single item")
	}
	if name != "adminer" {
		t.Errorf("expected 'adminer', got %q", name)
	}
}

func TestPickToolToEnable_MultipleDisabled_CallsSelector(t *testing.T) {
	cfg := makeServicesCfg(nil, config.ToolsConfig{
		"adminer":       config.ToolConfig{Enabled: false, Container: "adminer", Host: "adminer.localhost", Port: 8080},
		"redis_insight": config.ToolConfig{Enabled: false, Container: "redis_insight", Host: "redis.localhost", Port: 5540},
		"mailpit":       config.ToolConfig{Enabled: true, Container: "mailpit", Host: "mail.localhost", Port: 8025},
	}, config.RuntimePorts{}, config.RuntimeHosts{})

	selectorCalled := false
	var seenItems []ui.SelectorItem
	sel := func(title string, items []ui.SelectorItem) (int, error) {
		selectorCalled = true
		seenItems = items
		return 0, nil
	}

	name, err := pickToolToEnable(cfg, sel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !selectorCalled {
		t.Error("selector should have been called for multiple disabled tools")
	}
	if len(seenItems) != 2 {
		t.Errorf("selector should see 2 items, got %d", len(seenItems))
	}
	for _, item := range seenItems {
		if item.Label == "mailpit" {
			t.Error("enabled tool 'mailpit' should not appear in enable selector")
		}
		if item.Status != "disabled" {
			t.Errorf("item %q status should be 'disabled', got %q", item.Label, item.Status)
		}
	}
	if name == "" {
		t.Error("expected non-empty tool name from selector")
	}
}

func TestPickToolToEnable_SelectorPicksSecond(t *testing.T) {
	// buildToolRows returns tools sorted by name: adminer, mailpit, redis_insight (all disabled)
	cfg := makeServicesCfg(nil, config.ToolsConfig{
		"adminer":       config.ToolConfig{Enabled: false, Container: "adminer", Host: "adminer.localhost", Port: 8080},
		"mailpit":       config.ToolConfig{Enabled: false, Container: "mailpit", Host: "mail.localhost", Port: 8025},
		"redis_insight": config.ToolConfig{Enabled: false, Container: "redis_insight", Host: "redis.localhost", Port: 5540},
	}, make(config.RuntimePorts), make(config.RuntimeHosts))

	// index 1 → mailpit (second in sorted order)
	name, err := pickToolToEnable(cfg, alwaysToolToggleFn(1))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "mailpit" {
		t.Errorf("expected 'mailpit' from index 1, got %q", name)
	}
}

func TestPickToolToDisable_NoneEnabled_ReturnsError(t *testing.T) {
	cfg := makeServicesCfg(nil, make(config.ToolsConfig), make(config.RuntimePorts), make(config.RuntimeHosts))

	_, err := pickToolToDisable(cfg, neverToolToggleFn(t))
	if err == nil {
		t.Fatal("expected error when no enabled tools, got nil")
	}
	if !strings.Contains(err.Error(), "enabled") {
		t.Errorf("error should mention 'enabled', got: %v", err)
	}
}

func TestPickToolToDisable_SingleEnabled_ShowsSelector(t *testing.T) {
	cfg := makeServicesCfg(nil, config.ToolsConfig{
		"adminer":       config.ToolConfig{Enabled: false, Container: "adminer", Host: "adminer.localhost", Port: 8080},
		"redis_insight": config.ToolConfig{Enabled: false, Container: "redis_insight", Host: "redis.localhost", Port: 5540},
		"mailpit":       config.ToolConfig{Enabled: true, Container: "mailpit", Host: "mail.localhost", Port: 8025},
	}, config.RuntimePorts{}, config.RuntimeHosts{})

	selectorCalled := false
	sel := func(title string, items []ui.SelectorItem) (int, error) {
		selectorCalled = true
		if len(items) != 1 {
			t.Errorf("expected 1 item in selector, got %d", len(items))
		}
		return 0, nil
	}

	name, err := pickToolToDisable(cfg, sel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !selectorCalled {
		t.Error("selector should have been called even for a single item")
	}
	if name != "mailpit" {
		t.Errorf("expected 'mailpit', got %q", name)
	}
}

func TestPickToolToDisable_MultipleEnabled_CallsSelector(t *testing.T) {
	cfg := makeServicesCfg(nil, config.ToolsConfig{
		"adminer":       config.ToolConfig{Enabled: true, Container: "adminer", Host: "adminer.localhost", Port: 8080},
		"redis_insight": config.ToolConfig{Enabled: true, Container: "redis_insight", Host: "redis.localhost", Port: 5540},
		"mailpit":       config.ToolConfig{Enabled: false, Container: "mailpit", Host: "mail.localhost", Port: 8025},
	}, config.RuntimePorts{}, config.RuntimeHosts{})

	selectorCalled := false
	var seenItems []ui.SelectorItem
	sel := func(title string, items []ui.SelectorItem) (int, error) {
		selectorCalled = true
		seenItems = items
		return 0, nil
	}

	name, err := pickToolToDisable(cfg, sel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !selectorCalled {
		t.Error("selector should have been called for multiple enabled tools")
	}
	if len(seenItems) != 2 {
		t.Errorf("selector should see 2 items, got %d", len(seenItems))
	}
	for _, item := range seenItems {
		if item.Label == "mailpit" {
			t.Error("disabled tool 'mailpit' should not appear in disable selector")
		}
		if item.Status != "enabled" {
			t.Errorf("item %q status should be 'enabled', got %q", item.Label, item.Status)
		}
	}
	if name == "" {
		t.Error("expected non-empty tool name from selector")
	}
}

func TestToolEnableCmd_ArgsOptional(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newToolEnableCmd(flags)
	// MaximumNArgs(1): 0 args is valid
	if err := cmd.Args(cmd, []string{}); err != nil {
		t.Errorf("enable: 0 args should be valid, got: %v", err)
	}
	// 1 arg is valid
	if err := cmd.Args(cmd, []string{"adminer"}); err != nil {
		t.Errorf("enable: 1 arg should be valid, got: %v", err)
	}
	// 2 args should fail
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("enable: 2 args should fail, got nil")
	}
}

func TestToolDisableCmd_ArgsOptional(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newToolDisableCmd(flags)
	if err := cmd.Args(cmd, []string{}); err != nil {
		t.Errorf("disable: 0 args should be valid, got: %v", err)
	}
	if err := cmd.Args(cmd, []string{"adminer"}); err != nil {
		t.Errorf("disable: 1 arg should be valid, got: %v", err)
	}
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("disable: 2 args should fail, got nil")
	}
}

func TestToolEnableCmd_UseField(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newToolEnableCmd(flags)
	if !strings.Contains(cmd.Use, "[tool]") {
		t.Errorf("enable Use should show [tool] as optional, got %q", cmd.Use)
	}
}

func TestToolDisableCmd_UseField(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newToolDisableCmd(flags)
	if !strings.Contains(cmd.Use, "[tool]") {
		t.Errorf("disable Use should show [tool] as optional, got %q", cmd.Use)
	}
}

// TestPickToolToEnable_SelectorReturnsErrCancelled_Propagated verifies that
// ErrCancelled from the selector propagates through pickToolToEnable.
func TestPickToolToEnable_SelectorReturnsErrCancelled_Propagated(t *testing.T) {
	cfg := makeServicesCfg(nil, config.ToolsConfig{
		"adminer": config.ToolConfig{Enabled: false, Container: "adminer", Host: "adminer.localhost", Port: 8080},
	}, make(config.RuntimePorts), make(config.RuntimeHosts))
	cancelSelector := func(title string, items []ui.SelectorItem) (int, error) {
		return -1, ui.ErrCancelled
	}
	_, err := pickToolToEnable(cfg, cancelSelector)
	if err == nil {
		t.Fatal("expected ErrCancelled to propagate, got nil")
	}
	if !errors.Is(err, ui.ErrCancelled) {
		t.Errorf("expected errors.Is(err, ui.ErrCancelled), got: %v", err)
	}
}

// TestToolNameSet_ArbitraryToolName verifies that toolNameSet derives tool names from config,
// not from a hardcoded list. This test ensures that a tool like elasticvue (not in the
// original hardcoded set) is included when declared in the config.
func TestToolNameSet_ArbitraryToolName(t *testing.T) {
	cfg := makeServicesCfg(nil, config.ToolsConfig{
		"elasticvue": config.ToolConfig{Enabled: false, Container: "elasticvue", Host: "elasticvue.localhost", Port: 8044},
		"adminer":    config.ToolConfig{Enabled: true, Container: "adminer", Host: "adminer.localhost", Port: 8080},
	}, make(config.RuntimePorts), make(config.RuntimeHosts))

	set := toolNameSet(cfg)

	if !set["elasticvue"] {
		t.Error("elasticvue should be in toolNameSet")
	}
	if !set["adminer"] {
		t.Error("adminer should be in toolNameSet")
	}
	if len(set) != 2 {
		t.Errorf("expected 2 tools, got %d", len(set))
	}
}

// TestToolNameSet_EmptyConfig verifies that toolNameSet returns an empty set when
// no tools are declared in the config.
func TestToolNameSet_EmptyConfig(t *testing.T) {
	cfg := makeServicesCfg(nil, make(config.ToolsConfig), make(config.RuntimePorts), make(config.RuntimeHosts))

	set := toolNameSet(cfg)

	if len(set) != 0 {
		t.Errorf("expected empty set, got %d tools", len(set))
	}
}
