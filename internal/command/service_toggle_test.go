package command

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/lifecycle"
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

// --- helper for single-service toggle tests ---

// writeServiceProject writes a devbox project in a temp dir with a single
// service named "svc". svcYAML is the content of service.yml.
// Returns (configPath, baseDir).
func writeServiceProject(t *testing.T, svcYAML string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"),
		[]byte("project:\n  name: test\n  prefix: t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svcDir := filepath.Join(dir, "devbox", "services", "svc")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(svcYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "devbox.yml"), dir
}

func statePath(baseDir string) string {
	return filepath.Join(baseDir, journal.DefaultRelPath)
}

func readPending(t *testing.T, baseDir string) *journal.PendingApply {
	t.Helper()
	state, err := journal.Load(statePath(baseDir))
	if err != nil {
		t.Fatalf("load journal state: %v", err)
	}
	return state.Pending
}

// TestSingleToggle_PrintPlan_NoMutation verifies --print-plan makes no filesystem changes.
func TestSingleToggle_PrintPlan_NoMutation(t *testing.T) {
	configPath, baseDir := writeServiceProject(t, "type: app\ncontainer: c\n")
	localPath := filepath.Join(baseDir, "devbox", "local.yml")
	envPath := filepath.Join(baseDir, ".env")

	flags := &rootFlags{configPath: configPath}
	cmd := newServiceEnableCmd(flags)
	cmd.SetArgs([]string{"--print-plan", "svc"})
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Error("local.yml must not be written by --print-plan")
	}
	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		t.Error(".env must not be written by --print-plan")
	}
	if _, err := os.Stat(statePath(baseDir)); !os.IsNotExist(err) {
		t.Error("journal state must not be written by --print-plan")
	}
	if !strings.Contains(out.String(), "step") && !strings.Contains(out.String(), "Plan") &&
		!strings.Contains(out.String(), "No steps") {
		t.Errorf("expected plan output, got: %q", out.String())
	}
}

// TestSingleToggle_RequiresNone_NoPending verifies that a service with
// requires:none writes local.yml and .env but produces no pending state.
func TestSingleToggle_RequiresNone_NoPending(t *testing.T) {
	configPath, baseDir := writeServiceProject(t,
		"type: app\ncontainer: c\non_enable:\n  requires: none\n")
	localPath := filepath.Join(baseDir, "devbox", "local.yml")

	flags := &rootFlags{configPath: configPath}
	cmd := newServiceEnableCmd(flags)
	cmd.SetArgs([]string{"svc"})

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return false }

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// local.yml must be written
	data, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("local.yml not written: %v", err)
	}
	var local map[string]any
	if err := yaml.Unmarshal(data, &local); err != nil {
		t.Fatal(err)
	}
	svcMap, _ := local["services"].(map[string]any)
	if svcMap == nil {
		t.Fatal("local.yml missing services key")
	}
	entry, _ := svcMap["svc"].(map[string]any)
	if entry == nil || entry["enabled"] != true {
		t.Errorf("svc should be enabled=true, got %v", entry)
	}

	// No pending should be written.
	pending := readPending(t, baseDir)
	if pending != nil {
		t.Errorf("expected no pending state for requires:none, got %+v", pending)
	}
}

// TestSingleToggle_NonTTY_PendingRecorded verifies that in non-TTY without
// --apply, mutation is persisted and pending state is recorded.
func TestSingleToggle_NonTTY_PendingRecorded(t *testing.T) {
	configPath, baseDir := writeServiceProject(t, "type: app\ncontainer: c\n")
	localPath := filepath.Join(baseDir, "devbox", "local.yml")

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return false }

	flags := &rootFlags{configPath: configPath}
	cmd := newServiceEnableCmd(flags)
	cmd.SetArgs([]string{"svc"})
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// local.yml must be written.
	if _, err := os.Stat(localPath); err != nil {
		t.Fatalf("local.yml not written: %v", err)
	}

	// Pending must be recorded (default requires = restart).
	pending := readPending(t, baseDir)
	if pending == nil {
		t.Fatal("expected pending state to be recorded")
	}
	if pending.Find(journal.PendingRestart) == nil {
		t.Errorf("expected PendingRestart op, got %+v", pending)
	}

	// Hint must be printed.
	if !strings.Contains(out.String(), "--apply") {
		t.Errorf("expected '--apply' hint in output, got: %q", out.String())
	}
}

// TestSingleToggle_TTY_No_PendingRecorded verifies that TTY + user types 'n'
// leaves mutation persisted and pending recorded.
func TestSingleToggle_TTY_No_PendingRecorded(t *testing.T) {
	configPath, baseDir := writeServiceProject(t, "type: app\ncontainer: c\n")
	localPath := filepath.Join(baseDir, "devbox", "local.yml")

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	flags := &rootFlags{configPath: configPath}
	cmd := newServiceEnableCmd(flags)
	cmd.SetArgs([]string{"svc"})
	cmd.SetIn(strings.NewReader("n\n"))

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(localPath); err != nil {
		t.Fatalf("local.yml not written: %v", err)
	}

	pending := readPending(t, baseDir)
	if pending == nil {
		t.Fatal("expected pending state to be recorded when user declines apply")
	}
	if pending.Find(journal.PendingRestart) == nil {
		t.Errorf("expected PendingRestart op, got %+v", pending)
	}
}

// TestSingleToggle_Apply_Success verifies --apply clears pending on success.
func TestSingleToggle_Apply_Success(t *testing.T) {
	configPath, baseDir := writeServiceProject(t, "type: app\ncontainer: c\n")

	oldRunRestart := singleToggleRunRestart
	t.Cleanup(func() { singleToggleRunRestart = oldRunRestart })
	singleToggleRunRestart = func(_ lifecycle.RunContext) error { return nil }

	flags := &rootFlags{configPath: configPath}
	cmd := newServiceEnableCmd(flags)
	cmd.SetArgs([]string{"--apply", "svc"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Apply succeeded → pending must be cleared.
	pending := readPending(t, baseDir)
	if pending != nil && pending.Find(journal.PendingRestart) != nil {
		t.Errorf("pending restart should be cleared after successful apply, got %+v", pending)
	}
}

// TestSingleToggle_Apply_Failure verifies --apply failure leaves mutation and
// pending in place.
func TestSingleToggle_Apply_Failure(t *testing.T) {
	configPath, baseDir := writeServiceProject(t, "type: app\ncontainer: c\n")

	restartErr := fmt.Errorf("restart failed: injected error")
	oldRunRestart := singleToggleRunRestart
	t.Cleanup(func() { singleToggleRunRestart = oldRunRestart })
	singleToggleRunRestart = func(_ lifecycle.RunContext) error { return restartErr }

	flags := &rootFlags{configPath: configPath}
	cmd := newServiceEnableCmd(flags)
	cmd.SetArgs([]string{"--apply", "svc"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error from failed apply, got nil")
	}

	// Mutation must persist.
	localPath := filepath.Join(baseDir, "devbox", "local.yml")
	if _, err := os.Stat(localPath); err != nil {
		t.Fatal("local.yml must remain after --apply failure")
	}

	// Pending must still be recorded.
	pending := readPending(t, baseDir)
	if pending == nil || pending.Find(journal.PendingRestart) == nil {
		t.Errorf("pending must remain after --apply failure, got %+v", pending)
	}
}

// TestSingleToggle_TTY_Yes_ClearsPending verifies TTY + user types 'y' executes
// the plan and clears pending on success.
func TestSingleToggle_TTY_Yes_ClearsPending(t *testing.T) {
	configPath, baseDir := writeServiceProject(t, "type: app\ncontainer: c\n")

	oldRunRestart := singleToggleRunRestart
	t.Cleanup(func() { singleToggleRunRestart = oldRunRestart })
	singleToggleRunRestart = func(_ lifecycle.RunContext) error { return nil }

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	flags := &rootFlags{configPath: configPath}
	cmd := newServiceEnableCmd(flags)
	cmd.SetArgs([]string{"svc"})
	cmd.SetIn(strings.NewReader("y\n"))

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pending := readPending(t, baseDir)
	if pending != nil && pending.Find(journal.PendingRestart) != nil {
		t.Errorf("pending restart should be cleared after TTY yes + successful apply, got %+v", pending)
	}
}

// TestSingleToggle_RollbackOnBuildTogglePlanError verifies that when
// buildTogglePlan fails (e.g. ErrUnknownToggleRequires), local.yml and .env
// are restored to their pre-toggle state and no pending is written.
func TestSingleToggle_RollbackOnBuildTogglePlanError(t *testing.T) {
	// Service with an unknown requires value — buildTogglePlan will reject it.
	configPath, baseDir := writeServiceProject(t,
		"type: app\ncontainer: c\non_enable:\n  requires: bad_value\n")

	localPath := filepath.Join(baseDir, "devbox", "local.yml")
	envPath := filepath.Join(baseDir, ".env")

	// Pre-create local.yml referencing the actual "svc" service so that the
	// config reload in step 2 still succeeds.
	origLocalContent := []byte("services:\n  svc:\n    enabled: false\n")
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localPath, origLocalContent, 0o600); err != nil {
		t.Fatal(err)
	}

	flags := &rootFlags{configPath: configPath}
	cmd := newServiceEnableCmd(flags)
	cmd.SetArgs([]string{"svc"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown requires value, got nil")
	}
	if !errors.Is(err, ErrUnknownToggleRequires) {
		t.Errorf("expected ErrUnknownToggleRequires, got: %v", err)
	}

	// local.yml must be restored to original.
	got, readErr := os.ReadFile(localPath)
	if readErr != nil {
		t.Fatalf("local.yml missing after rollback: %v", readErr)
	}
	if !bytes.Equal(got, origLocalContent) {
		t.Errorf("local.yml not restored: got %q, want %q", got, origLocalContent)
	}

	// .env must not exist (wasn't there before).
	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		t.Error(".env should not exist after rollback when it was absent before")
	}

	// No pending state.
	pending := readPending(t, baseDir)
	if pending != nil {
		t.Errorf("no pending should be written after rollback, got %+v", pending)
	}
}

// TestSingleToggle_RollbackEnvAbsent verifies that when .env did not exist
// before the toggle, rollback removes it (not leaves an empty file).
func TestSingleToggle_RollbackEnvAbsent(t *testing.T) {
	configPath, baseDir := writeServiceProject(t,
		"type: app\ncontainer: c\non_enable:\n  requires: bad_value\n")
	envPath := filepath.Join(baseDir, ".env")

	flags := &rootFlags{configPath: configPath}
	cmd := newServiceEnableCmd(flags)
	cmd.SetArgs([]string{"svc"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}

	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		t.Error(".env must be removed by rollback when it was absent before; must not be left as empty file")
	}
}

// TestSingleToggle_RollbackLocalYmlAbsent verifies that when local.yml did not
// exist before the toggle, rollback removes it.
func TestSingleToggle_RollbackLocalYmlAbsent(t *testing.T) {
	configPath, baseDir := writeServiceProject(t,
		"type: app\ncontainer: c\non_enable:\n  requires: bad_value\n")
	localPath := filepath.Join(baseDir, "devbox", "local.yml")

	flags := &rootFlags{configPath: configPath}
	cmd := newServiceEnableCmd(flags)
	cmd.SetArgs([]string{"svc"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error")
	}

	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Error("local.yml must be removed by rollback when it was absent before")
	}
}

// TestSingleToggle_RollbackOnAddPendingOpsFailure verifies that an injected
// write failure in step 5 (AddPendingOps) causes local.yml and .env to be
// restored.
func TestSingleToggle_RollbackOnAddPendingOpsFailure(t *testing.T) {
	configPath, baseDir := writeServiceProject(t, "type: app\ncontainer: c\n")
	localPath := filepath.Join(baseDir, "devbox", "local.yml")
	envPath := filepath.Join(baseDir, ".env")

	// Pre-create local.yml referencing the actual "svc" service so the config
	// reload in step 2 still succeeds and we reach the step-5 failure.
	origLocalContent := []byte("services:\n  svc:\n    enabled: false\n")
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localPath, origLocalContent, 0o600); err != nil {
		t.Fatal(err)
	}

	injectedErr := fmt.Errorf("injected AddPendingOps failure")
	oldAdd := singleToggleAddPendingOps
	t.Cleanup(func() { singleToggleAddPendingOps = oldAdd })
	singleToggleAddPendingOps = func(_ string, _ []journal.PendingOp, _ string) error {
		return injectedErr
	}

	flags := &rootFlags{configPath: configPath}
	cmd := newServiceEnableCmd(flags)
	cmd.SetArgs([]string{"svc"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error from AddPendingOps failure, got nil")
	}

	// local.yml must be restored.
	got, readErr := os.ReadFile(localPath)
	if readErr != nil {
		t.Fatalf("local.yml missing: %v", readErr)
	}
	if !bytes.Equal(got, origLocalContent) {
		t.Errorf("local.yml not restored: got %q, want %q", got, origLocalContent)
	}

	// .env must not exist (was absent before).
	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		t.Error(".env must not exist after rollback when it was absent before")
	}

	// Journal must be unchanged.
	pending := readPending(t, baseDir)
	if pending != nil {
		t.Errorf("no pending should be written after rollback, got %+v", pending)
	}
}

// TestSingleToggle_AllRequiresNone_NoPendingRecord verifies that when all
// contributors are RequiresNone, AddPendingOps is called with an empty slice
// (no-op) and no journal record is created.
func TestSingleToggle_AllRequiresNone_NoPendingRecord(t *testing.T) {
	configPath, baseDir := writeServiceProject(t,
		"type: app\ncontainer: c\non_enable:\n  requires: none\n")

	// Track whether AddPendingOps was called.
	var addCalled bool
	oldAdd := singleToggleAddPendingOps
	t.Cleanup(func() { singleToggleAddPendingOps = oldAdd })
	singleToggleAddPendingOps = func(path string, ops []journal.PendingOp, hash string) error {
		addCalled = true
		if len(ops) != 0 {
			return fmt.Errorf("expected empty ops for requires:none, got %v", ops)
		}
		return journal.AddPendingOps(path, ops, hash)
	}

	flags := &rootFlags{configPath: configPath}
	cmd := newServiceEnableCmd(flags)
	cmd.SetArgs([]string{"svc"})

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return false }

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !addCalled {
		t.Error("AddPendingOps must be called even for requires:none (empty-slice no-op semantics)")
	}

	// No journal record should be created.
	pending := readPending(t, baseDir)
	if pending != nil {
		t.Errorf("no pending state should be created for all-RequiresNone, got %+v", pending)
	}
}

// TestSingleToggle_DisableFlow verifies the basic disable flow.
func TestSingleToggle_DisableFlow(t *testing.T) {
	// Write a project with svc enabled.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"),
		[]byte("project:\n  name: t\n  prefix: t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svcDir := filepath.Join(dir, "devbox", "services", "svc")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svcDir, "service.yml"),
		[]byte("type: app\ncontainer: c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pre-enable svc in local.yml.
	localPath := filepath.Join(dir, "devbox", "local.yml")
	if err := os.WriteFile(localPath,
		[]byte("services:\n  svc:\n    enabled: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return false }

	flags := &rootFlags{configPath: filepath.Join(dir, "devbox.yml")}
	cmd := newServiceDisableCmd(flags)
	cmd.SetArgs([]string{"svc"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("local.yml not found: %v", err)
	}
	var local map[string]any
	if err := yaml.Unmarshal(data, &local); err != nil {
		t.Fatal(err)
	}
	svcMap, _ := local["services"].(map[string]any)
	entry, _ := svcMap["svc"].(map[string]any)
	if entry == nil || entry["enabled"] != false {
		t.Errorf("svc should be enabled=false after disable, got %v", entry)
	}

	// Pending must be recorded (disable also defaults to restart).
	pending := readPending(t, dir)
	if pending == nil || pending.Find(journal.PendingRestart) == nil {
		t.Errorf("expected PendingRestart after disable, got %+v", pending)
	}
}

// TestBuildContributors verifies contributor derivation from toggle actions.
func TestBuildContributors(t *testing.T) {
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"a": {Type: config.ServiceTypeApp, Container: "a",
			OnEnable: &config.ServiceToggleHooks{Requires: config.RequiresDeploy}},
		"b": {Type: config.ServiceTypeApp, Container: "b",
			OnEnable: &config.ServiceToggleHooks{Requires: config.RequiresRestart}},
		"c": {Type: config.ServiceTypeApp, Container: "c",
			OnEnable: &config.ServiceToggleHooks{Requires: config.RequiresNone}},
		"d": {Type: config.ServiceTypeApp, Container: "d"}, // no hooks → defaults to restart
	}, map[string]testTool{}, nil, nil)

	toggles := []ToggleAction{
		{Service: "a", Direction: DirectionEnable},
		{Service: "b", Direction: DirectionEnable},
		{Service: "c", Direction: DirectionEnable},
		{Service: "d", Direction: DirectionEnable},
	}

	contributors := buildContributors(cfg, toggles)
	if len(contributors) != 4 {
		t.Fatalf("expected 4 contributors, got %d", len(contributors))
	}
	want := map[string]config.ToggleRequires{
		"a": config.RequiresDeploy,
		"b": config.RequiresRestart,
		"c": config.RequiresNone,
		"d": config.RequiresRestart, // default
	}
	for _, c := range contributors {
		if got := c.Requires; got != want[c.Service] {
			t.Errorf("contributor %q: requires=%v, want %v", c.Service, got, want[c.Service])
		}
	}
}

// TestBuildPendingOpsFromContributors verifies op collapsing.
func TestBuildPendingOpsFromContributors(t *testing.T) {
	tests := []struct {
		name         string
		contributors []Contributor
		wantKinds    []journal.PendingKind
	}{
		{
			name: "all none → empty ops",
			contributors: []Contributor{
				{Service: "a", Requires: config.RequiresNone},
			},
		},
		{
			name: "all restart → one restart op",
			contributors: []Contributor{
				{Service: "a", Requires: config.RequiresRestart},
				{Service: "b", Requires: config.RequiresRestart},
			},
			wantKinds: []journal.PendingKind{journal.PendingRestart},
		},
		{
			name: "all deploy → one deploy op",
			contributors: []Contributor{
				{Service: "a", Requires: config.RequiresDeploy},
				{Service: "b", Requires: config.RequiresDeploy},
			},
			wantKinds: []journal.PendingKind{journal.PendingDeploy},
		},
		{
			name: "mixed → deploy then restart",
			contributors: []Contributor{
				{Service: "a", Requires: config.RequiresDeploy},
				{Service: "b", Requires: config.RequiresRestart},
			},
			wantKinds: []journal.PendingKind{journal.PendingDeploy, journal.PendingRestart},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ops := buildPendingOpsFromContributors(tc.contributors)
			if len(ops) != len(tc.wantKinds) {
				t.Fatalf("got %d ops, want %d: %v", len(ops), len(tc.wantKinds), ops)
			}
			for i, op := range ops {
				if op.Kind != tc.wantKinds[i] {
					t.Errorf("op[%d].Kind=%v, want %v", i, op.Kind, tc.wantKinds[i])
				}
			}
			// Deploy op services must be sorted.
			for _, op := range ops {
				if op.Kind == journal.PendingDeploy && len(op.Services) > 1 {
					sorted := make([]string, len(op.Services))
					copy(sorted, op.Services)
					if !slices.Equal(op.Services, sorted) {
						t.Errorf("deploy op services not sorted: %v", op.Services)
					}
				}
			}
		})
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
