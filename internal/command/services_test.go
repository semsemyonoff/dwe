package command

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/ui"
)

// testTool is the legacy tool shape used pre-unification, converted into a
// ServiceConfig with Type=tool by makeServicesCfg.
type testTool struct {
	Enabled   bool
	Container string
	Host      string
	Port      int
}

func makeServicesCfg(services map[string]config.ServiceConfig, tools map[string]testTool, _ any, _ any) *config.DevboxConfig {
	merged := make(map[string]config.ServiceConfig, len(services)+len(tools))
	maps.Copy(merged, services)
	for k, v := range tools {
		svc := config.ServiceConfig{
			Type:      config.ServiceTypeTool,
			Container: v.Container,
			Enabled:   v.Enabled,
		}
		if v.Port != 0 {
			svc.Ports = map[string]int{"main": v.Port}
		}
		if v.Host != "" {
			svc.Hosts = map[string]string{"main": v.Host}
		}
		merged[k] = svc
	}
	return &config.DevboxConfig{Services: merged}
}

func TestBuildServiceRows_empty(t *testing.T) {
	cfg := makeServicesCfg(nil, map[string]testTool{}, nil, nil)
	rows := buildServiceRows(cfg)
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}

func TestBuildServiceRows_single(t *testing.T) {
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"main": {Type: "app", Dir: "./services/main", Container: "app-main"},
	}, map[string]testTool{}, nil, nil)

	rows := buildServiceRows(cfg)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.Name != "main" {
		t.Errorf("Name = %q, want main", r.Name)
	}
	if r.Type != "app" {
		t.Errorf("Type = %q, want app", r.Type)
	}
}

func TestBuildServiceRows_sortedByTypeThenName(t *testing.T) {
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"worker":  {Type: "worker"},
		"api":     {Type: "app"},
		"main":    {Type: "app"},
		"mailpit": {Type: "tool"},
		"adminer": {Type: "tool"},
		"db":      {Type: "infra"},
	}, map[string]testTool{}, nil, nil)

	rows := buildServiceRows(cfg)
	if len(rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(rows))
	}
	want := []string{"api", "main", "adminer", "mailpit", "worker"}
	for i, w := range want {
		if rows[i].Name != w {
			t.Errorf("rows[%d].Name = %q, want %q", i, rows[i].Name, w)
		}
	}
}

func TestBuildServiceRows_includesTools(t *testing.T) {
	cfg := makeServicesCfg(nil, map[string]testTool{
		"adminer": {Enabled: false, Container: "adminer", Host: "h", Port: 1},
		"mailpit": {Enabled: false, Container: "mailpit", Host: "h", Port: 2},
	}, nil, nil)
	rows := buildServiceRows(cfg)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	for _, r := range rows {
		if r.Type != "tool" {
			t.Errorf("row %q: expected type=tool, got %q", r.Name, r.Type)
		}
	}
}

// --- enable/disable selector helpers ---

func neverToggleFn(t *testing.T) selectToggleFn {
	return func(title string, items []ui.SelectorItem) (int, error) {
		t.Helper()
		t.Errorf("selector should not have been called, items=%d", len(items))
		return -1, fmt.Errorf("selector unexpectedly called")
	}
}

func alwaysToggleFn(idx int) selectToggleFn {
	return func(title string, items []ui.SelectorItem) (int, error) {
		if idx < 0 || idx >= len(items) {
			return -1, fmt.Errorf("index %d out of range for %d items", idx, len(items))
		}
		return idx, nil
	}
}

func TestPickServiceToEnable_NoDisabled_ReturnsError(t *testing.T) {
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"main":   {Type: "app", Mandatory: true},
		"second": {Type: "app", Enabled: true},
	}, map[string]testTool{}, nil, nil)

	_, err := pickServiceToEnable(cfg, neverToggleFn(t))
	if err == nil {
		t.Fatal("expected error when no disabled services, got nil")
	}
}

func TestPickServiceToEnable_SelectorPicksByIndex(t *testing.T) {
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"alpha": {Type: "app", Enabled: false},
		"beta":  {Type: "app", Enabled: false},
	}, map[string]testTool{}, nil, nil)

	name, err := pickServiceToEnable(cfg, alwaysToggleFn(1))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "beta" {
		t.Errorf("expected 'beta' from index 1, got %q", name)
	}
}

func TestPickServiceToDisable_MandatoryNotShown(t *testing.T) {
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"main":   {Type: "app", Mandatory: true, Enabled: true},
		"second": {Type: "app", Enabled: true},
		"third":  {Type: "app", Enabled: true},
	}, map[string]testTool{}, nil, nil)

	var seenLabels []string
	sel := func(title string, items []ui.SelectorItem) (int, error) {
		for _, item := range items {
			seenLabels = append(seenLabels, item.Label)
		}
		return 0, nil
	}

	_, err := pickServiceToDisable(cfg, sel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, label := range seenLabels {
		if label == "main" {
			t.Errorf("mandatory service 'main' should not be in disable selector, got %v", seenLabels)
		}
	}
}

func TestServiceEnableCmd_Args(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newServiceEnableCmd(flags)
	if err := cmd.Args(cmd, []string{}); err != nil {
		t.Errorf("0 args should be valid, got: %v", err)
	}
	if err := cmd.Args(cmd, []string{"x"}); err != nil {
		t.Errorf("1 arg should be valid, got: %v", err)
	}
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("2 args should fail")
	}
}

func TestServiceDisableCmd_Args(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newServiceDisableCmd(flags)
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("2 args should fail")
	}
}

// --- Task 5 additions ---

// TestServicesGroup_NoStatusOrListSubcommands ensures the removed subcommands
// are not registered.
func TestServicesGroup_NoStatusOrListSubcommands(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	parent := newServiceCmd(flags)
	for _, sub := range parent.Commands() {
		if sub.Use == "status" || sub.Use == "list" {
			t.Errorf("services group should not register %q subcommand", sub.Use)
		}
	}
}

// TestServicesGroup_NoArgs ensures the bare command rejects positional args.
func TestServicesGroup_NoArgs(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newServiceCmd(flags)
	if err := cmd.Args(cmd, []string{"foo"}); err == nil {
		t.Error("services with positional arg should fail Args validation")
	}
}

// TestServiceEnableCmd_MandatoryWarn ensures enabling a mandatory service is a
// no-op + warning (not an error).
func TestServiceEnableCmd_MandatoryWarn(t *testing.T) {
	configPath := writeTempServiceConfig(t, map[string]struct {
		mandatory bool
		enabled   bool
		container string
	}{
		"main": {mandatory: true, enabled: false},
	})
	flags := &rootFlags{configPath: configPath}
	cmd := newServiceEnableCmd(flags)
	var stderr strings.Builder
	cmd.SetErr(&stderr)
	if err := cmd.RunE(cmd, []string{"main"}); err != nil {
		t.Fatalf("enable mandatory should be no-op + warning, got: %v", err)
	}
	if !strings.Contains(stderr.String(), "already mandatory") {
		t.Errorf("expected 'already mandatory' warning, got: %q", stderr.String())
	}
}

// TestServiceDisableCmd_MandatoryError ensures disabling a mandatory service
// returns an error.
func TestServiceDisableCmd_MandatoryError(t *testing.T) {
	configPath := writeTempServiceConfig(t, map[string]struct {
		mandatory bool
		enabled   bool
		container string
	}{
		"main": {mandatory: true, enabled: true},
	})
	flags := &rootFlags{configPath: configPath}
	cmd := newServiceDisableCmd(flags)
	err := cmd.RunE(cmd, []string{"main"})
	if err == nil {
		t.Fatal("expected error disabling mandatory service, got nil")
	}
	if !strings.Contains(err.Error(), "mandatory") {
		t.Errorf("error should mention 'mandatory', got: %v", err)
	}
}

// TestPickServiceToEnable_CancelPropagates ensures ErrCancelled flows back.
func TestPickServiceToEnable_CancelPropagates(t *testing.T) {
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"alpha": {Type: "app", Enabled: false},
	}, map[string]testTool{}, nil, nil)
	cancelSelector := func(title string, items []ui.SelectorItem) (int, error) {
		return -1, ui.ErrCancelled
	}
	_, err := pickServiceToEnable(cfg, cancelSelector)
	if !errors.Is(err, ui.ErrCancelled) {
		t.Errorf("expected ErrCancelled, got: %v", err)
	}
}
