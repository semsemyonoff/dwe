package command

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"
)

func makeServicesCfg(services map[string]config.ServiceConfig, tools config.ToolsConfig, ports config.RuntimePorts, hosts config.RuntimeHosts) *config.DevboxConfig {
	return &config.DevboxConfig{
		Services: services,
		Tools:    tools,
		Runtime: config.RuntimeConfig{
			Ports: ports,
			Hosts: hosts,
		},
	}
}

func TestBuildServiceRows_empty(t *testing.T) {
	cfg := makeServicesCfg(nil, config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{})
	rows := buildServiceRows(cfg)
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}

func TestBuildServiceRows_single(t *testing.T) {
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"main": {Type: "app", Dir: "./services/main", Container: "app-main"},
	}, config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{})

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
	if r.Dir != "./services/main" {
		t.Errorf("Dir = %q, want ./services/main", r.Dir)
	}
	if r.Container != "app-main" {
		t.Errorf("Container = %q, want app-main", r.Container)
	}
}

func TestBuildServiceRows_sortedByName(t *testing.T) {
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"worker": {Type: "worker", Dir: "./services/worker"},
		"api":    {Type: "app", Dir: "./services/api"},
		"main":   {Type: "app", Dir: "./services/main"},
	}, config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{})

	rows := buildServiceRows(cfg)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	names := []string{rows[0].Name, rows[1].Name, rows[2].Name}
	want := []string{"api", "main", "worker"}
	for i, n := range names {
		if n != want[i] {
			t.Errorf("rows[%d].Name = %q, want %q", i, n, want[i])
		}
	}
}

func TestBuildToolRows_allDisabled(t *testing.T) {
	cfg := makeServicesCfg(nil, config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{})
	rows := buildToolRows(cfg)
	if len(rows) != 3 {
		t.Fatalf("expected 3 tool rows, got %d", len(rows))
	}
	for _, r := range rows {
		if r.Enabled {
			t.Errorf("tool %q should be disabled", r.Name)
		}
	}
}

func TestBuildToolRows_someEnabled(t *testing.T) {
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

	rows := buildToolRows(cfg)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	adminer := rows[0]
	if adminer.Name != "adminer" {
		t.Errorf("rows[0].Name = %q, want adminer", adminer.Name)
	}
	if adminer.Enabled {
		t.Error("adminer should be disabled")
	}
	if adminer.Port != 8080 {
		t.Errorf("adminer.Port = %d, want 8080", adminer.Port)
	}
	if adminer.Host != "adminer.localhost" {
		t.Errorf("adminer.Host = %q, want adminer.localhost", adminer.Host)
	}

	ri := rows[1]
	if ri.Name != "redis_insight" {
		t.Errorf("rows[1].Name = %q, want redis_insight", ri.Name)
	}
	if !ri.Enabled {
		t.Error("redis_insight should be enabled")
	}
	if ri.Port != 5540 {
		t.Errorf("redis_insight.Port = %d, want 5540", ri.Port)
	}

	mp := rows[2]
	if mp.Name != "mailpit" {
		t.Errorf("rows[2].Name = %q, want mailpit", mp.Name)
	}
	if !mp.Enabled {
		t.Error("mailpit should be enabled")
	}
}

func TestRunServiceList_LipglossTable(t *testing.T) {
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"main":   {Type: "app", Container: "app-main", Mandatory: true},
		"second": {Type: "app", Container: "app-second", Enabled: false},
	}, config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{})

	neverRunning := func(_, _ string) bool { return false }

	var buf bytes.Buffer
	w := render.NewWriter(&buf)
	if err := runServiceList(w, cfg, neverRunning); err != nil {
		t.Fatalf("runServiceList error: %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		"NAME", "CONTAINER", "STATE", "RUNNING",
		"main", "app-main", "mandatory", "stopped",
		"second", "app-second", "disabled",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, out)
		}
	}
}

// --- Task 7: services enable/disable interactive selector ---

// neverToggleFn returns a selectToggleFn that fails the test if called.
func neverToggleFn(t *testing.T) selectToggleFn {
	return func(title string, items []ui.SelectorItem) (int, error) {
		t.Helper()
		t.Errorf("selector should not have been called, but was called with %d items", len(items))
		return -1, fmt.Errorf("selector unexpectedly called")
	}
}

// alwaysToggleFn returns a selectToggleFn that always picks the item at idx.
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
		"main":   {Type: "app", Container: "app-main", Mandatory: true},
		"second": {Type: "app", Container: "app-second", Enabled: true},
	}, config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{})

	_, err := pickServiceToEnable(cfg, neverToggleFn(t))
	if err == nil {
		t.Fatal("expected error when no disabled services, got nil")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("error should mention 'disabled', got: %v", err)
	}
}

func TestPickServiceToEnable_SingleDisabled_ShowsSelector(t *testing.T) {
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"main":   {Type: "app", Container: "app-main", Mandatory: true},
		"second": {Type: "app", Container: "app-second", Enabled: false},
	}, config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{})

	selectorCalled := false
	sel := func(title string, items []ui.SelectorItem) (int, error) {
		selectorCalled = true
		if len(items) != 1 {
			t.Errorf("expected 1 item in selector, got %d", len(items))
		}
		return 0, nil
	}

	name, err := pickServiceToEnable(cfg, sel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !selectorCalled {
		t.Error("selector should have been called even for a single item")
	}
	if name != "second" {
		t.Errorf("expected 'second', got %q", name)
	}
}

func TestPickServiceToEnable_MultipleDisabled_CallsSelector(t *testing.T) {
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"main":  {Type: "app", Container: "app-main", Mandatory: true},
		"alpha": {Type: "app", Container: "app-alpha", Enabled: false},
		"beta":  {Type: "app", Container: "app-beta", Enabled: false},
	}, config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{})

	selectorCalled := false
	var seenItems []ui.SelectorItem
	sel := func(title string, items []ui.SelectorItem) (int, error) {
		selectorCalled = true
		seenItems = items
		return 0, nil
	}

	name, err := pickServiceToEnable(cfg, sel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !selectorCalled {
		t.Error("selector should have been called for multiple disabled services")
	}
	if len(seenItems) != 2 {
		t.Errorf("selector should see 2 items, got %d", len(seenItems))
	}
	// mandatory service must not appear
	for _, item := range seenItems {
		if item.Label == "main" {
			t.Error("mandatory service 'main' should not appear in enable selector")
		}
		if item.Status != "disabled" {
			t.Errorf("item %q status should be 'disabled', got %q", item.Label, item.Status)
		}
	}
	if name == "" {
		t.Error("expected non-empty service name from selector")
	}
}

func TestPickServiceToEnable_SelectorPicksSecond(t *testing.T) {
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"alpha": {Type: "app", Container: "app-alpha", Enabled: false},
		"beta":  {Type: "app", Container: "app-beta", Enabled: false},
	}, config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{})

	// sorted: alpha(0), beta(1)
	name, err := pickServiceToEnable(cfg, alwaysToggleFn(1))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "beta" {
		t.Errorf("expected 'beta' from index 1, got %q", name)
	}
}

func TestPickServiceToDisable_NoEnabled_ReturnsError(t *testing.T) {
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"main":   {Type: "app", Container: "app-main", Mandatory: true},
		"second": {Type: "app", Container: "app-second", Enabled: false},
	}, config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{})

	_, err := pickServiceToDisable(cfg, neverToggleFn(t))
	if err == nil {
		t.Fatal("expected error when no enabled optional services, got nil")
	}
	if !strings.Contains(err.Error(), "enabled") {
		t.Errorf("error should mention 'enabled', got: %v", err)
	}
}

func TestPickServiceToDisable_SingleEnabled_ShowsSelector(t *testing.T) {
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"main":   {Type: "app", Container: "app-main", Mandatory: true},
		"second": {Type: "app", Container: "app-second", Enabled: true},
	}, config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{})

	selectorCalled := false
	sel := func(title string, items []ui.SelectorItem) (int, error) {
		selectorCalled = true
		if len(items) != 1 {
			t.Errorf("expected 1 item in selector, got %d", len(items))
		}
		return 0, nil
	}

	name, err := pickServiceToDisable(cfg, sel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !selectorCalled {
		t.Error("selector should have been called even for a single item")
	}
	if name != "second" {
		t.Errorf("expected 'second', got %q", name)
	}
}

func TestPickServiceToDisable_MultipleEnabled_CallsSelector(t *testing.T) {
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"main":  {Type: "app", Container: "app-main", Mandatory: true},
		"alpha": {Type: "app", Container: "app-alpha", Enabled: true},
		"beta":  {Type: "app", Container: "app-beta", Enabled: true},
	}, config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{})

	selectorCalled := false
	var seenItems []ui.SelectorItem
	sel := func(title string, items []ui.SelectorItem) (int, error) {
		selectorCalled = true
		seenItems = items
		return 0, nil
	}

	name, err := pickServiceToDisable(cfg, sel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !selectorCalled {
		t.Error("selector should have been called for multiple enabled services")
	}
	if len(seenItems) != 2 {
		t.Errorf("selector should see 2 items, got %d", len(seenItems))
	}
	// mandatory service must not appear
	for _, item := range seenItems {
		if item.Label == "main" {
			t.Error("mandatory service 'main' should not appear in disable selector")
		}
		if item.Status != "enabled" {
			t.Errorf("item %q status should be 'enabled', got %q", item.Label, item.Status)
		}
	}
	if name == "" {
		t.Error("expected non-empty service name from selector")
	}
}

func TestPickServiceToDisable_MandatoryNotShown(t *testing.T) {
	// Even if mandatory service has no explicit enabled=false, it must be excluded.
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"main":   {Type: "app", Container: "app-main", Mandatory: true, Enabled: true},
		"second": {Type: "app", Container: "app-second", Enabled: true},
		"third":  {Type: "app", Container: "app-third", Enabled: true},
	}, config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{})

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

func TestServiceEnableCmd_ArgsOptional(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newServiceEnableCmd(flags)
	// MaximumNArgs(1): 0 args is valid
	if err := cmd.Args(cmd, []string{}); err != nil {
		t.Errorf("enable: 0 args should be valid, got: %v", err)
	}
	// 1 arg is valid
	if err := cmd.Args(cmd, []string{"second"}); err != nil {
		t.Errorf("enable: 1 arg should be valid, got: %v", err)
	}
	// 2 args should fail
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("enable: 2 args should fail, got nil")
	}
}

func TestServiceDisableCmd_ArgsOptional(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newServiceDisableCmd(flags)
	if err := cmd.Args(cmd, []string{}); err != nil {
		t.Errorf("disable: 0 args should be valid, got: %v", err)
	}
	if err := cmd.Args(cmd, []string{"second"}); err != nil {
		t.Errorf("disable: 1 arg should be valid, got: %v", err)
	}
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("disable: 2 args should fail, got nil")
	}
}

func TestServiceEnableCmd_UseField(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newServiceEnableCmd(flags)
	if !strings.Contains(cmd.Use, "[service]") {
		t.Errorf("enable Use should show [service] as optional, got %q", cmd.Use)
	}
}

func TestServiceDisableCmd_UseField(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newServiceDisableCmd(flags)
	if !strings.Contains(cmd.Use, "[service]") {
		t.Errorf("disable Use should show [service] as optional, got %q", cmd.Use)
	}
}

func TestRunServiceList_EnabledRunning(t *testing.T) {
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"main": {Type: "app", Container: "app-main", Enabled: true},
	}, config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{})

	alwaysRunning := func(_, _ string) bool { return true }

	var buf bytes.Buffer
	w := render.NewWriter(&buf)
	if err := runServiceList(w, cfg, alwaysRunning); err != nil {
		t.Fatalf("runServiceList error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "running") {
		t.Errorf("enabled running service should show 'running'\nfull output:\n%s", out)
	}
}
