package command

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/ui"

	"gopkg.in/yaml.v3"
)

// writeTempServiceConfig creates a minimal devbox config in a temp dir and
// returns the path to devbox.yml. Services map: name → {mandatory, enabled, container}.
func writeTempServiceConfig(t *testing.T, services map[string]struct {
	mandatory bool
	enabled   bool
	container string
}) string {
	t.Helper()
	dir := t.TempDir()

	var devboxLines []string
	devboxLines = append(devboxLines, "project:")
	devboxLines = append(devboxLines, "  name: test")
	devboxLines = append(devboxLines, "  prefix: devbox")
	hasEnabled := false
	for name, spec := range services {
		if spec.enabled && !spec.mandatory {
			if !hasEnabled {
				devboxLines = append(devboxLines, "services:")
				hasEnabled = true
			}
			devboxLines = append(devboxLines, "  "+name+":")
			devboxLines = append(devboxLines, "    enabled: true")
		}
	}
	devboxYML := strings.Join(devboxLines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"), []byte(devboxYML), 0o644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(dir, "devbox"), 0o755); err != nil {
		t.Fatalf("mkdir devbox/: %v", err)
	}
	for name, spec := range services {
		svcDir := filepath.Join(dir, "devbox", "services", name)
		if err := os.MkdirAll(svcDir, 0o755); err != nil {
			t.Fatalf("mkdir services/%s: %v", name, err)
		}
		container := spec.container
		if container == "" {
			container = "app-" + name
		}
		var svcLines []string
		svcLines = append(svcLines, "type: app")
		svcLines = append(svcLines, "container: "+container)
		if spec.mandatory {
			svcLines = append(svcLines, "mandatory: true")
		}
		content := strings.Join(svcLines, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(content), 0o644); err != nil {
			t.Fatalf("write services/%s/service.yml: %v", name, err)
		}
	}

	return filepath.Join(dir, "devbox.yml")
}

func TestServicesToggle_NonTTY_ReturnsInteractiveRequired(t *testing.T) {
	configPath := writeTempServiceConfig(t, map[string]struct {
		mandatory bool
		enabled   bool
		container string
	}{
		"second": {mandatory: false, enabled: false},
	})

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return false }

	oldMS := runMultiSelect
	t.Cleanup(func() { runMultiSelect = oldMS })
	runMultiSelect = func(_ string, _ []ui.MultiSelectItem) (ui.MultiSelectResult, error) {
		t.Fatal("runMultiSelect should not be called in non-TTY mode")
		return ui.MultiSelectResult{}, nil
	}

	flags := &rootFlags{configPath: configPath}
	cmd := newServiceCmd(flags)
	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("expected non-TTY error, got nil")
	}
	if !errors.Is(err, ErrInteractiveRequired) {
		t.Errorf("expected ErrInteractiveRequired, got: %v", err)
	}
	if !strings.Contains(err.Error(), "devbox status") {
		t.Errorf("error should hint at 'devbox status', got: %v", err)
	}
}

func TestServicesToggle_AllMandatory_ReturnsError(t *testing.T) {
	configPath := writeTempServiceConfig(t, map[string]struct {
		mandatory bool
		enabled   bool
		container string
	}{
		"main": {mandatory: true, enabled: true},
	})

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	oldMS := runMultiSelect
	t.Cleanup(func() { runMultiSelect = oldMS })
	runMultiSelect = func(_ string, _ []ui.MultiSelectItem) (ui.MultiSelectResult, error) {
		t.Fatal("runMultiSelect should not be called when all mandatory")
		return ui.MultiSelectResult{}, nil
	}

	flags := &rootFlags{configPath: configPath}
	cmd := newServiceCmd(flags)
	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error when all services mandatory, got nil")
	}
	if !strings.Contains(err.Error(), "nothing to toggle") {
		t.Errorf("error should say 'nothing to toggle', got: %v", err)
	}
}

func TestServicesToggle_TTY_EnablesAndDisables(t *testing.T) {
	configPath := writeTempServiceConfig(t, map[string]struct {
		mandatory bool
		enabled   bool
		container string
	}{
		"main":   {mandatory: true, enabled: false},
		"second": {mandatory: false, enabled: false},
		"third":  {mandatory: false, enabled: true},
	})

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	oldMS := runMultiSelect
	t.Cleanup(func() { runMultiSelect = oldMS })
	runMultiSelect = func(_ string, _ []ui.MultiSelectItem) (ui.MultiSelectResult, error) {
		return ui.MultiSelectResult{Kept: []string{"second"}, Locked: []string{"main"}}, nil
	}

	flags := &rootFlags{configPath: configPath}
	cmd := newServiceCmd(flags)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	localPath := filepath.Join(filepath.Dir(configPath), "devbox", "local.yml")
	data, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("local.yml not written: %v", err)
	}
	var local map[string]any
	if err := yaml.Unmarshal(data, &local); err != nil {
		t.Fatalf("unmarshal local.yml: %v", err)
	}
	svcMap, _ := local["services"].(map[string]any)
	if svcMap == nil {
		t.Fatal("local.yml missing services key")
	}
	secondEntry, _ := svcMap["second"].(map[string]any)
	if secondEntry == nil || secondEntry["enabled"] != true {
		t.Errorf("second should be enabled=true, got %v", secondEntry)
	}
	thirdEntry, _ := svcMap["third"].(map[string]any)
	if thirdEntry == nil || thirdEntry["enabled"] != false {
		t.Errorf("third should be enabled=false, got %v", thirdEntry)
	}
}

func TestServicesToggle_TTY_CancelNoWrites(t *testing.T) {
	configPath := writeTempServiceConfig(t, map[string]struct {
		mandatory bool
		enabled   bool
		container string
	}{
		"second": {mandatory: false, enabled: false},
	})

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	oldMS := runMultiSelect
	t.Cleanup(func() { runMultiSelect = oldMS })
	runMultiSelect = func(_ string, _ []ui.MultiSelectItem) (ui.MultiSelectResult, error) {
		return ui.MultiSelectResult{}, ui.ErrCancelled
	}

	flags := &rootFlags{configPath: configPath}
	cmd := newServiceCmd(flags)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("cancel should return nil, got: %v", err)
	}
	localPath := filepath.Join(filepath.Dir(configPath), "devbox", "local.yml")
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Error("local.yml should not be created after cancel")
	}
}

var ansiSeqRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func stripANSI(s string) string {
	return ansiSeqRe.ReplaceAllString(s, "")
}

func TestFormatServiceToggleLabel_DisabledPreservesVisibleText(t *testing.T) {
	active := serviceRow{Name: "adminer", Type: "tool", Container: "adminer", Enabled: true}
	disabled := serviceRow{Name: "adminer", Type: "tool", Container: "adminer", Enabled: false}

	activeLabel := formatServiceToggleLabel(active)
	disabledLabel := formatServiceToggleLabel(disabled)

	if stripANSI(activeLabel) != stripANSI(disabledLabel) {
		t.Fatalf("labels should differ only by style, got active=%q disabled=%q", activeLabel, disabledLabel)
	}
}

func TestFormatServiceToggleLabel_MandatoryLooksActive(t *testing.T) {
	active := serviceRow{Name: "main", Type: "app", Container: "app-main", Enabled: true}
	mandatory := serviceRow{Name: "main", Type: "app", Container: "app-main", Mandatory: true, Enabled: false}

	if got, want := formatServiceToggleLabel(mandatory), formatServiceToggleLabel(active); got != want {
		t.Errorf("mandatory service should render as active, got %q, want %q", got, want)
	}
}

// TestServicesToggle_MixedTypes verifies the unified toggle iterates manageable
// services (app + tool + optional infra) and omits mandatory infra because
// mandatory services are always on.
func TestServicesToggle_MixedTypes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"), []byte("project:\n  name: t\n  prefix: t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "devbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"main":    "type: app\ncontainer: app-main\nmandatory: true\n",
		"adminer": "type: tool\ncontainer: adminer\nports:\n  web: 8080\n",
		"db":      "type: infra\ncontainer: db\nmandatory: true\n",
	} {
		svcDir := filepath.Join(dir, "devbox", "services", name)
		if err := os.MkdirAll(svcDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	oldMS := runMultiSelect
	t.Cleanup(func() { runMultiSelect = oldMS })
	var seenKeys []string
	var seenItems []ui.MultiSelectItem
	runMultiSelect = func(_ string, items []ui.MultiSelectItem) (ui.MultiSelectResult, error) {
		seenItems = append(seenItems, items...)
		for _, item := range items {
			seenKeys = append(seenKeys, item.Key)
		}
		return ui.MultiSelectResult{Kept: []string{"adminer"}, Locked: []string{"main"}}, nil
	}

	flags := &rootFlags{configPath: filepath.Join(dir, "devbox.yml")}
	cmd := newServiceCmd(flags)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantOrder := []string{"main", "adminer"}
	if !slices.Equal(seenKeys, wantOrder) {
		t.Errorf("unified toggle order = %v, want %v", seenKeys, wantOrder)
	}
	for _, want := range wantOrder {
		if !slices.Contains(seenKeys, want) {
			t.Errorf("unified toggle should include %q (any type), got %v", want, seenKeys)
		}
	}
	for _, item := range seenItems {
		if item.Label == stripANSI(item.Label) || item.Description == stripANSI(item.Description) {
			t.Errorf("multi-select option text should keep per-field ANSI styling, got label=%q description=%q", item.Label, item.Description)
		}
		if strings.Contains(item.Label, "\x1b[0m") || strings.Contains(item.Description, "\x1b[0m") {
			t.Errorf("multi-select option text must not use full ANSI reset because huh owns dynamic state styling, got label=%q description=%q", item.Label, item.Description)
		}
		visible := stripANSI(item.Label + " " + item.Description)
		if !strings.Contains(visible, item.Key) {
			t.Errorf("visible item should include service name %q, got %q", item.Key, visible)
		}
		wantContainer := map[string]string{
			"main":    "app-main",
			"adminer": "adminer",
		}[item.Key]
		if !strings.Contains(visible, wantContainer) {
			t.Errorf("visible item should include container %q, got %q", wantContainer, visible)
		}
		if strings.Contains(visible, "container=") {
			t.Errorf("visible item should not include container= prefix, got %q", visible)
		}
		if !strings.Contains(visible, "[") || !strings.Contains(visible, "]") {
			t.Errorf("visible item should include a type badge, got %q", visible)
		}
	}
}

func TestPickServiceToEnable_TypeSortedAndDecorated(t *testing.T) {
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"adminer": {Type: config.ServiceTypeTool, Container: "adminer", Enabled: false},
		"api":     {Type: config.ServiceTypeApp, Container: "app-api", Enabled: false},
		"varnish": {Type: config.ServiceTypeInfra, Container: "varnish", Enabled: false},
		"db":      {Type: config.ServiceTypeInfra, Container: "db", Mandatory: true, Enabled: false},
	}, map[string]testTool{}, nil, nil)

	var labels []string
	var descriptions []string
	selector := func(title string, items []ui.SelectorItem) (int, error) {
		for _, item := range items {
			labels = append(labels, stripANSI(item.Label))
			descriptions = append(descriptions, stripANSI(item.Description))
		}
		return 0, nil
	}

	name, err := pickServiceToEnable(cfg, selector)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "api" {
		t.Errorf("expected first type-sorted candidate api, got %q", name)
	}
	if !slices.Equal(labels, []string{"api", "adminer", "varnish"}) {
		t.Errorf("selector labels = %v, want [api adminer varnish]", labels)
	}
	if !strings.Contains(descriptions[0], "[app]") ||
		!strings.Contains(descriptions[1], "[tool]") ||
		!strings.Contains(descriptions[2], "[infra]") {
		t.Errorf("selector descriptions should include type badges, got %v", descriptions)
	}
}

func TestServiceEnableCmd_MandatoryInfraWarn(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"), []byte("project:\n  name: t\n  prefix: t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "devbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	svcDir1 := filepath.Join(dir, "devbox", "services", "db")
	if err := os.MkdirAll(svcDir1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svcDir1, "service.yml"), []byte("type: infra\ncontainer: db\nmandatory: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	flags := &rootFlags{configPath: filepath.Join(dir, "devbox.yml")}
	cmd := newServiceEnableCmd(flags)
	var stderr strings.Builder
	cmd.SetErr(&stderr)
	if err := cmd.RunE(cmd, []string{"db"}); err != nil {
		t.Fatalf("enable mandatory infra should be no-op + warning, got: %v", err)
	}
	if !strings.Contains(stderr.String(), "already mandatory") {
		t.Errorf("expected 'already mandatory' warning, got: %q", stderr.String())
	}
}

func TestServiceEnableCmd_OptionalInfraEnables(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"), []byte("project:\n  name: t\n  prefix: t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "devbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	svcDir2 := filepath.Join(dir, "devbox", "services", "varnish")
	if err := os.MkdirAll(svcDir2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svcDir2, "service.yml"), []byte("type: infra\ncontainer: varnish\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	flags := &rootFlags{configPath: filepath.Join(dir, "devbox.yml")}
	cmd := newServiceEnableCmd(flags)
	if err := cmd.RunE(cmd, []string{"varnish"}); err != nil {
		t.Fatalf("enabling optional infra service should succeed, got: %v", err)
	}

	localPath := filepath.Join(dir, "devbox", "local.yml")
	data, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("local.yml not written: %v", err)
	}
	var local map[string]any
	if err := yaml.Unmarshal(data, &local); err != nil {
		t.Fatalf("unmarshal local.yml: %v", err)
	}
	svcMap, _ := local["services"].(map[string]any)
	if svcMap == nil {
		t.Fatal("local.yml missing services key")
	}
	varnishEntry, _ := svcMap["varnish"].(map[string]any)
	if varnishEntry == nil || varnishEntry["enabled"] != true {
		t.Errorf("varnish should be enabled=true, got %v", varnishEntry)
	}
}

// TestApplyServiceTogglesBatch_AllOrNothing verifies that batch validation
// rejects mandatory toggles without writing partial state.
func TestApplyServiceTogglesBatch_AllOrNothing(t *testing.T) {
	configPath := writeTempServiceConfig(t, map[string]struct {
		mandatory bool
		enabled   bool
		container string
	}{
		"main":   {mandatory: true, enabled: false},
		"second": {mandatory: false, enabled: false},
	})

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	err = applyServiceTogglesBatch(configPath, cfg, []string{"second"}, []string{"main"})
	if err == nil {
		t.Fatal("expected error for mandatory toggle, got nil")
	}

	localPath := filepath.Join(filepath.Dir(configPath), "devbox", "local.yml")
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Error("local.yml must not be written when batch validation fails")
	}
}
