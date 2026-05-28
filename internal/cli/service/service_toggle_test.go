package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"devbox-cli/internal/cli/cmdctx"
	cmddeploy "devbox-cli/internal/cli/deploy"
	"devbox-cli/internal/core/project/config"
	localpkg "devbox-cli/internal/core/project/local"
	servicepkg "devbox-cli/internal/core/project/services"
	"devbox-cli/internal/core/ui"
	"devbox-cli/internal/core/workflow/deploy/journal"
	"devbox-cli/internal/core/workflow/lifecycle"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// writeTempServiceConfig creates a minimal devbox config in a temp dir and
// returns the path to devbox.yml. Services map: name → {required, enabled, container}.
func writeTempServiceConfig(t *testing.T, services map[string]struct {
	required  bool
	enabled   bool
	container string
}) string {
	t.Helper()
	// Same default as writeServiceProject — assume stack is running so the
	// pending/apply branches fire. Override per-test for stopped/probe-error
	// regressions.
	oldDetect := detectStackRunning
	t.Cleanup(func() { detectStackRunning = oldDetect })
	detectStackRunning = func(_ *config.DevboxConfig, _ string) (bool, error) { return true, nil }

	dir := t.TempDir()

	var devboxLines []string
	devboxLines = append(devboxLines, "project:")
	devboxLines = append(devboxLines, "  name: test")
	devboxLines = append(devboxLines, "  prefix: devbox")
	hasEnabled := false
	for name, spec := range services {
		if spec.enabled && !spec.required {
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
		if spec.required {
			svcLines = append(svcLines, "required: true")
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
		required  bool
		enabled   bool
		container string
	}{
		"second": {required: false, enabled: false},
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

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
	cmd := NewCmd("", flags)
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
		required  bool
		enabled   bool
		container string
	}{
		"main": {required: true, enabled: true},
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

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
	cmd := NewCmd("", flags)
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
		required  bool
		enabled   bool
		container string
	}{
		"main":   {required: true, enabled: false},
		"second": {required: false, enabled: false},
		"third":  {required: false, enabled: true},
	})

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	oldMS := runMultiSelect
	t.Cleanup(func() { runMultiSelect = oldMS })
	runMultiSelect = func(_ string, _ []ui.MultiSelectItem) (ui.MultiSelectResult, error) {
		return ui.MultiSelectResult{Kept: []string{"second"}, Locked: []string{"main"}}, nil
	}

	// Decline the apply prompt — this test only asserts local.yml mutation.
	oldPrompt := confirmApplyPrompt
	t.Cleanup(func() { confirmApplyPrompt = oldPrompt })
	confirmApplyPrompt = func() (bool, error) { return false, nil }

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
	cmd := NewCmd("", flags)
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
		required  bool
		enabled   bool
		container string
	}{
		"second": {required: false, enabled: false},
	})

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	oldMS := runMultiSelect
	t.Cleanup(func() { runMultiSelect = oldMS })
	runMultiSelect = func(_ string, _ []ui.MultiSelectItem) (ui.MultiSelectResult, error) {
		return ui.MultiSelectResult{}, ui.ErrCancelled
	}

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
	cmd := NewCmd("", flags)
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

func TestFormatServiceToggleLabel_DisabledPreservesVisibleText(t *testing.T) {
	active := servicepkg.Row{Name: "adminer", Type: "tool", Container: "adminer", Enabled: true}
	disabled := servicepkg.Row{Name: "adminer", Type: "tool", Container: "adminer", Enabled: false}

	activeLabel := formatServiceToggleLabel(active)
	disabledLabel := formatServiceToggleLabel(disabled)

	if stripANSI(activeLabel) != stripANSI(disabledLabel) {
		t.Fatalf("labels should differ only by style, got active=%q disabled=%q", activeLabel, disabledLabel)
	}
}

func TestFormatServiceToggleLabel_MandatoryLooksActive(t *testing.T) {
	active := servicepkg.Row{Name: "main", Type: "app", Container: "app-main", Enabled: true}
	mandatory := servicepkg.Row{Name: "main", Type: "app", Container: "app-main", Mandatory: true, Enabled: false}

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
		"main":    "type: app\ncontainer: app-main\nrequired: true\n",
		"adminer": "type: tool\ncontainer: adminer\nports:\n  web: 8080\n",
		"db":      "type: infra\ncontainer: db\nrequired: true\n",
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

	// Stack-running override + decline apply prompt — this test asserts the
	// multi-select item ordering, not the apply behavior.
	oldDetect := detectStackRunning
	t.Cleanup(func() { detectStackRunning = oldDetect })
	detectStackRunning = func(_ *config.DevboxConfig, _ string) (bool, error) { return true, nil }
	oldPrompt := confirmApplyPrompt
	t.Cleanup(func() { confirmApplyPrompt = oldPrompt })
	confirmApplyPrompt = func() (bool, error) { return false, nil }

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "devbox.yml")}
	cmd := NewCmd("", flags)
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
		"db":      {Type: config.ServiceTypeInfra, Container: "db", Required: true, Enabled: false},
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
	if !slices.Equal(labels, []string{"📦 api", "🔧 adminer", "🧱 varnish"}) {
		t.Errorf("selector labels = %v, want [📦 api 🔧 adminer 🧱 varnish]", labels)
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
	if err := os.WriteFile(filepath.Join(svcDir1, "service.yml"), []byte("type: infra\ncontainer: db\nrequired: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "devbox.yml")}
	cmd := newServiceEnableCmd(flags)
	var stderr strings.Builder
	cmd.SetErr(&stderr)
	if err := cmd.RunE(cmd, []string{"db"}); err != nil {
		t.Fatalf("enable mandatory infra should be no-op + warning, got: %v", err)
	}
	if !strings.Contains(stderr.String(), "already required") {
		t.Errorf("expected 'already required' warning, got: %q", stderr.String())
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

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "devbox.yml")}
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
	// Default to "stack running" for toggle tests so the existing pending /
	// hook assertions remain valid. Individual tests that exercise the
	// stack-not-running path must override this seam themselves.
	oldDetect := detectStackRunning
	t.Cleanup(func() { detectStackRunning = oldDetect })
	detectStackRunning = func(_ *config.DevboxConfig, _ string) (bool, error) { return true, nil }

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

// TestSingleToggle_StackNotRunning_NoApply_RecordsPendingSkipsHooks verifies
// that when the stack is not currently running AND the user did not pass
// --apply, the toggle records pending (so devbox status reminds the user) and
// does NOT auto-run hooks/restart. local.yml is updated.
func TestSingleToggle_StackNotRunning_NoApply_RecordsPendingSkipsHooks(t *testing.T) {
	configPath, baseDir := writeServiceProject(t, "type: app\ncontainer: c\n")

	detectStackRunning = func(_ *config.DevboxConfig, _ string) (bool, error) { return false, nil }

	restartCalled := false
	oldRunRestart := singleToggleRunRestart
	t.Cleanup(func() { singleToggleRunRestart = oldRunRestart })
	singleToggleRunRestart = func(_ lifecycle.RunContext) error {
		restartCalled = true
		return nil
	}

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return false }

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
	cmd := newServiceEnableCmd(flags)
	cmd.SetArgs([]string{"svc"})
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	localPath := filepath.Join(baseDir, "devbox", "local.yml")
	if _, err := os.Stat(localPath); err != nil {
		t.Fatalf("local.yml must be written: %v", err)
	}
	if p := readPending(t, baseDir); p == nil || p.Find(journal.PendingRestart) == nil {
		t.Errorf("pending must be recorded so devbox status shows the deferred work, got %+v", p)
	}
	if restartCalled {
		t.Error("RunRestart must NOT auto-run when stack is stopped and --apply was not passed")
	}
	if !strings.Contains(out.String(), "stack is not running") {
		t.Errorf("expected info message about stack not running, got: %q", out.String())
	}
}

// TestSingleToggle_StackNotRunning_Apply_StillExecutes verifies that an
// explicit --apply is honored even when the stack probe reports stopped.
// The apply step itself (deploy/restart) brings containers up.
func TestSingleToggle_StackNotRunning_Apply_StillExecutes(t *testing.T) {
	configPath, _ := writeServiceProject(t, "type: app\ncontainer: c\n")

	detectStackRunning = func(_ *config.DevboxConfig, _ string) (bool, error) { return false, nil }

	restartCalled := false
	oldRunRestart := singleToggleRunRestart
	t.Cleanup(func() { singleToggleRunRestart = oldRunRestart })
	singleToggleRunRestart = func(_ lifecycle.RunContext) error {
		restartCalled = true
		return nil
	}

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
	cmd := newServiceEnableCmd(flags)
	cmd.SetArgs([]string{"--apply", "svc"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !restartCalled {
		t.Error("explicit --apply must execute the plan even when stack is stopped")
	}
}

// TestSingleToggle_StackProbeError_ProceedsAsRunning verifies that a probe
// failure (docker missing / daemon down) does NOT collapse to "stopped". The
// toggle proceeds as if running: pending is recorded and --apply executes.
func TestSingleToggle_StackProbeError_ProceedsAsRunning(t *testing.T) {
	configPath, _ := writeServiceProject(t, "type: app\ncontainer: c\n")

	// Override the helper's default (true) with a probe-error sentinel.
	detectStackRunning = func(_ *config.DevboxConfig, _ string) (bool, error) {
		return false, fmt.Errorf("docker daemon unreachable")
	}

	restartCalled := false
	oldRunRestart := singleToggleRunRestart
	t.Cleanup(func() { singleToggleRunRestart = oldRunRestart })
	singleToggleRunRestart = func(_ lifecycle.RunContext) error {
		restartCalled = true
		return nil
	}

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
	cmd := newServiceEnableCmd(flags)
	cmd.SetArgs([]string{"--apply", "svc"})
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !restartCalled {
		t.Error("RunRestart must be invoked when probe fails (probe error must NOT silently skip apply)")
	}

	if !strings.Contains(stderr.String(), "could not probe stack state") {
		t.Errorf("expected probe warning on stderr, got: %q", stderr.String())
	}
}

// TestSingleToggle_StackProbeError_PendingStillRecorded verifies that when the
// probe fails AND the user did not pass --apply, the deferred pending entry is
// still written (probe failure must not silently drop the deferred work).
func TestSingleToggle_StackProbeError_PendingStillRecorded(t *testing.T) {
	configPath, baseDir := writeServiceProject(t, "type: app\ncontainer: c\n")

	detectStackRunning = func(_ *config.DevboxConfig, _ string) (bool, error) {
		return false, fmt.Errorf("docker daemon unreachable")
	}

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return false }

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
	cmd := newServiceEnableCmd(flags)
	cmd.SetArgs([]string{"svc"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pending := readPending(t, baseDir)
	if pending == nil || pending.Find(journal.PendingRestart) == nil {
		t.Errorf("expected PendingRestart to be recorded under probe error, got %+v", pending)
	}
}

// TestSingleToggle_PrintPlan_NoMutation verifies --print-plan makes no filesystem changes.
func TestSingleToggle_PrintPlan_NoMutation(t *testing.T) {
	configPath, baseDir := writeServiceProject(t, "type: app\ncontainer: c\n")
	localPath := filepath.Join(baseDir, "devbox", "local.yml")
	envPath := filepath.Join(baseDir, ".env")

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
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

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
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

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
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

	oldPrompt := confirmApplyPrompt
	t.Cleanup(func() { confirmApplyPrompt = oldPrompt })
	confirmApplyPrompt = func() (bool, error) { return false, nil }

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
	cmd := newServiceEnableCmd(flags)
	cmd.SetArgs([]string{"svc"})

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

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
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

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
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

	oldPrompt := confirmApplyPrompt
	t.Cleanup(func() { confirmApplyPrompt = oldPrompt })
	confirmApplyPrompt = func() (bool, error) { return true, nil }

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
	cmd := newServiceEnableCmd(flags)
	cmd.SetArgs([]string{"svc"})

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

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
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

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
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

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
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

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
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

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
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

	oldDetect := detectStackRunning
	t.Cleanup(func() { detectStackRunning = oldDetect })
	detectStackRunning = func(_ *config.DevboxConfig, _ string) (bool, error) { return true, nil }

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "devbox.yml")}
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

// TestBuildContributors_DeployOrRestart_ResolvedFromJournal verifies that
// the deploy-or-restart resolution in buildContributors matches the one in
// buildTogglePlan — otherwise the pending op recorded would not match the
// apply step the user is told to run.
func TestBuildContributors_DeployOrRestart_ResolvedFromJournal(t *testing.T) {
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"never": {Type: config.ServiceTypeApp, Container: "n",
			OnEnable: &config.ServiceToggleHooks{Requires: config.RequiresDeployOrRestart}},
		"already": {Type: config.ServiceTypeApp, Container: "a",
			OnEnable: &config.ServiceToggleHooks{Requires: config.RequiresDeployOrRestart}},
	}, map[string]testTool{}, nil, nil)

	toggles := []ToggleAction{
		{Service: "never", Direction: DirectionEnable},
		{Service: "already", Direction: DirectionEnable},
	}

	contributors := buildContributors(cfg, toggles, map[string]bool{"already": true})

	got := make(map[string]config.ToggleRequires, len(contributors))
	for _, c := range contributors {
		got[c.Service] = c.Requires
	}
	if got["never"] != config.RequiresDeploy {
		t.Errorf("deploy-or-restart on never-deployed must resolve to RequiresDeploy, got %q", got["never"])
	}
	if got["already"] != config.RequiresRestart {
		t.Errorf("deploy-or-restart on already-deployed must resolve to RequiresRestart, got %q", got["already"])
	}

	// The pending ops slice must reflect the resolved kinds — never as deploy
	// (the service has no journal record) and already as restart.
	ops := buildPendingOpsFromContributors(contributors)
	var sawDeploy, sawRestart bool
	for _, op := range ops {
		switch op.Kind {
		case journal.PendingDeploy:
			sawDeploy = true
			if len(op.Services) != 1 || op.Services[0] != "never" {
				t.Errorf("deploy op must target [never], got %v", op.Services)
			}
		case journal.PendingRestart:
			sawRestart = true
		}
	}
	if !sawDeploy {
		t.Error("expected a PendingDeploy op for the never-deployed service")
	}
	if !sawRestart {
		t.Error("expected a PendingRestart op for the already-deployed service")
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

	contributors := buildContributors(cfg, toggles, nil)
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

// --- multi-toggle (devbox services) tests ---

// writeMultiServiceProject creates a project with named services in the
// per-folder layout. svcContents maps service name to service.yml content.
// deployNames lists services that should also have a deploy.yml (phases: []).
func writeMultiServiceProject(t *testing.T, svcContents map[string]string, deployNames []string) (configPath, baseDir string) {
	t.Helper()
	// Same default as writeServiceProject: stack is "running" so the toggle
	// pending/hook assertions still fire. Override in individual tests when needed.
	oldDetect := detectStackRunning
	t.Cleanup(func() { detectStackRunning = oldDetect })
	detectStackRunning = func(_ *config.DevboxConfig, _ string) (bool, error) { return true, nil }

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"),
		[]byte("project:\n  name: test\n  prefix: t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, content := range svcContents {
		svcDir := filepath.Join(dir, "devbox", "services", name)
		if err := os.MkdirAll(svcDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range deployNames {
		svcDir := filepath.Join(dir, "devbox", "services", name)
		if err := os.MkdirAll(svcDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(svcDir, "deploy.yml"), []byte("phases: []\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "devbox.yml"), dir
}

// injectMultiToggleSeams replaces the multi-toggle apply seams for the duration
// of the test, recording calls. Returns (callLog, restore).
type multiToggleCallLog struct {
	deployOpts   []cmddeploy.Opts
	restartCalls int
}

func injectMultiToggleSeams(t *testing.T, deployErr, restartErr error) *multiToggleCallLog {
	t.Helper()
	log := &multiToggleCallLog{}
	oldDeploy := multiToggleRunDeploy
	oldRestart := multiToggleRunRestart
	t.Cleanup(func() {
		multiToggleRunDeploy = oldDeploy
		multiToggleRunRestart = oldRestart
	})
	multiToggleRunDeploy = func(_ context.Context, _ *cobra.Command, _ *cmdctx.RootFlags, opts cmddeploy.Opts) error {
		log.deployOpts = append(log.deployOpts, opts)
		return deployErr
	}
	multiToggleRunRestart = func(_ lifecycle.RunContext) error {
		log.restartCalls++
		return restartErr
	}
	return log
}

// TestMultiToggle_PrintPlan_NoMutation verifies that --print-plan makes no
// filesystem changes and prints a plan.
func TestMultiToggle_PrintPlan_NoMutation(t *testing.T) {
	configPath, baseDir := writeMultiServiceProject(t,
		map[string]string{
			"alpha": "type: app\ncontainer: a\n",
			"beta":  "type: app\ncontainer: b\n",
		}, nil)
	localPath := filepath.Join(baseDir, "devbox", "local.yml")
	envPath := filepath.Join(baseDir, ".env")

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	oldMS := runMultiSelect
	t.Cleanup(func() { runMultiSelect = oldMS })
	runMultiSelect = func(_ string, _ []ui.MultiSelectItem) (ui.MultiSelectResult, error) {
		return ui.MultiSelectResult{Kept: []string{"alpha"}, Locked: nil}, nil
	}

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
	cmd := NewCmd("", flags)
	cmd.SetArgs([]string{"--print-plan"})
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
	outStr := out.String()
	if !strings.Contains(outStr, "step") && !strings.Contains(outStr, "Plan") &&
		!strings.Contains(outStr, "No steps") {
		t.Errorf("expected plan output, got: %q", outStr)
	}
}

// TestMultiToggle_AllNone_NoPending verifies that all-none services produce no
// pending state and no apply work.
func TestMultiToggle_AllNone_NoPending(t *testing.T) {
	configPath, baseDir := writeMultiServiceProject(t,
		map[string]string{
			"alpha": "type: app\ncontainer: a\non_enable:\n  requires: none\n",
			"beta":  "type: app\ncontainer: b\non_enable:\n  requires: none\n",
		}, nil)

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	oldMS := runMultiSelect
	t.Cleanup(func() { runMultiSelect = oldMS })
	// TUI keeps alpha+beta enabled (enabling both from disabled → all-enable).
	runMultiSelect = func(_ string, _ []ui.MultiSelectItem) (ui.MultiSelectResult, error) {
		return ui.MultiSelectResult{Kept: []string{"alpha", "beta"}, Locked: nil}, nil
	}

	var addCalled bool
	oldAdd := multiToggleAddPendingOps
	t.Cleanup(func() { multiToggleAddPendingOps = oldAdd })
	multiToggleAddPendingOps = func(path string, ops []journal.PendingOp, hash string) error {
		addCalled = true
		return journal.AddPendingOps(path, ops, hash)
	}

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
	cmd := NewCmd("", flags)
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !addCalled {
		t.Error("multiToggleAddPendingOps must be called even for all-none (empty-slice no-op)")
	}
	pending := readPending(t, baseDir)
	if pending != nil {
		t.Errorf("no pending should be created for all-RequiresNone, got %+v", pending)
	}
}

// TestMultiToggle_MixedRestartDeploy_Apply verifies a mixed restart+deploy batch
// with --apply: RunDeploy called once (with deploy contributor), then RunRestart.
func TestMultiToggle_MixedRestartDeploy_Apply(t *testing.T) {
	// "ada" requires deploy (has deploy.yml); "bob" requires restart (no deploy.yml).
	configPath, baseDir := writeMultiServiceProject(t,
		map[string]string{
			"ada": "type: app\ncontainer: ada\non_enable:\n  requires: deploy\n",
			"bob": "type: app\ncontainer: bob\non_enable:\n  requires: restart\n",
		}, []string{"ada"})

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	oldMS := runMultiSelect
	t.Cleanup(func() { runMultiSelect = oldMS })
	runMultiSelect = func(_ string, _ []ui.MultiSelectItem) (ui.MultiSelectResult, error) {
		return ui.MultiSelectResult{Kept: []string{"ada", "bob"}, Locked: nil}, nil
	}

	callLog := injectMultiToggleSeams(t, nil, nil)

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
	cmd := NewCmd("", flags)
	cmd.SetArgs([]string{"--apply"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Deploy must be called once with exactly ["ada"].
	if len(callLog.deployOpts) != 1 {
		t.Fatalf("expected 1 RunDeploy call, got %d", len(callLog.deployOpts))
	}
	if !slices.Equal(callLog.deployOpts[0].Services, []string{"ada"}) {
		t.Errorf("RunDeploy called with services=%v, want [ada]", callLog.deployOpts[0].Services)
	}

	// Restart must be called once.
	if callLog.restartCalls != 1 {
		t.Errorf("expected 1 RunRestart call, got %d", callLog.restartCalls)
	}

	// Pending must be cleared on success.
	pending := readPending(t, baseDir)
	if pending != nil && (pending.Find(journal.PendingDeploy) != nil || pending.Find(journal.PendingRestart) != nil) {
		t.Errorf("pending must be cleared after successful --apply, got %+v", pending)
	}
}

// TestMultiToggle_MixedBatchPartialFailure verifies that when deploy succeeds
// but restart fails, pending stays intact.
func TestMultiToggle_MixedBatchPartialFailure(t *testing.T) {
	configPath, baseDir := writeMultiServiceProject(t,
		map[string]string{
			"ada": "type: app\ncontainer: ada\non_enable:\n  requires: deploy\n",
			"bob": "type: app\ncontainer: bob\non_enable:\n  requires: restart\n",
		}, []string{"ada"})

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	oldMS := runMultiSelect
	t.Cleanup(func() { runMultiSelect = oldMS })
	runMultiSelect = func(_ string, _ []ui.MultiSelectItem) (ui.MultiSelectResult, error) {
		return ui.MultiSelectResult{Kept: []string{"ada", "bob"}, Locked: nil}, nil
	}

	restartErr := fmt.Errorf("restart failed: injected")
	injectMultiToggleSeams(t, nil, restartErr)

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
	cmd := NewCmd("", flags)
	cmd.SetArgs([]string{"--apply"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error from restart failure, got nil")
	}

	// Pending must remain intact after partial failure.
	pending := readPending(t, baseDir)
	if pending == nil {
		t.Fatal("pending must remain after partial apply failure")
	}
	if pending.Find(journal.PendingDeploy) == nil {
		t.Error("deploy op must remain in pending after partial failure")
	}
	if pending.Find(journal.PendingRestart) == nil {
		t.Error("restart op must remain in pending after partial failure")
	}
}

// TestMultiToggle_AllDeploy_Apply verifies that two deploy-requiring services
// result in a single RunDeploy call with both service names.
func TestMultiToggle_AllDeploy_Apply(t *testing.T) {
	configPath, baseDir := writeMultiServiceProject(t,
		map[string]string{
			"ada": "type: app\ncontainer: ada\non_enable:\n  requires: deploy\n",
			"bob": "type: app\ncontainer: bob\non_enable:\n  requires: deploy\n",
		}, []string{"ada", "bob"})

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	oldMS := runMultiSelect
	t.Cleanup(func() { runMultiSelect = oldMS })
	runMultiSelect = func(_ string, _ []ui.MultiSelectItem) (ui.MultiSelectResult, error) {
		return ui.MultiSelectResult{Kept: []string{"ada", "bob"}, Locked: nil}, nil
	}

	callLog := injectMultiToggleSeams(t, nil, nil)

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
	cmd := NewCmd("", flags)
	cmd.SetArgs([]string{"--apply"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(callLog.deployOpts) != 1 {
		t.Fatalf("expected 1 RunDeploy call for all-deploy batch, got %d", len(callLog.deployOpts))
	}
	// Services must be sorted alphabetically.
	got := callLog.deployOpts[0].Services
	if !slices.Equal(got, []string{"ada", "bob"}) {
		t.Errorf("RunDeploy services=%v, want [ada bob]", got)
	}
	if callLog.restartCalls != 0 {
		t.Errorf("RunRestart should not be called for all-deploy batch, got %d calls", callLog.restartCalls)
	}

	// Pending cleared on success.
	pending := readPending(t, baseDir)
	if pending != nil && pending.Find(journal.PendingDeploy) != nil {
		t.Errorf("pending deploy op must be cleared on success, got %+v", pending)
	}
}

// TestMultiToggle_AtomicPendingWriteRegression verifies that an injected failure
// in multiToggleAddPendingOps restores local.yml and .env.
func TestMultiToggle_AtomicPendingWriteRegression(t *testing.T) {
	configPath, baseDir := writeMultiServiceProject(t,
		map[string]string{
			"ada": "type: app\ncontainer: ada\non_enable:\n  requires: deploy\n",
			"bob": "type: app\ncontainer: bob\non_enable:\n  requires: restart\n",
		}, []string{"ada"})
	localPath := filepath.Join(baseDir, "devbox", "local.yml")
	envPath := filepath.Join(baseDir, ".env")

	// Pre-create local.yml so rollback restores bytes rather than removes.
	origLocal := []byte("services:\n  ada:\n    enabled: false\n  bob:\n    enabled: false\n")
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localPath, origLocal, 0o600); err != nil {
		t.Fatal(err)
	}

	injectedErr := fmt.Errorf("injected AddPendingOps failure")
	oldAdd := multiToggleAddPendingOps
	t.Cleanup(func() { multiToggleAddPendingOps = oldAdd })
	multiToggleAddPendingOps = func(_ string, _ []journal.PendingOp, _ string) error {
		return injectedErr
	}

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	oldMS := runMultiSelect
	t.Cleanup(func() { runMultiSelect = oldMS })
	runMultiSelect = func(_ string, _ []ui.MultiSelectItem) (ui.MultiSelectResult, error) {
		return ui.MultiSelectResult{Kept: []string{"ada", "bob"}, Locked: nil}, nil
	}

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
	cmd := NewCmd("", flags)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error from AddPendingOps failure, got nil")
	}

	// local.yml must be restored to original bytes.
	got, readErr := os.ReadFile(localPath)
	if readErr != nil {
		t.Fatalf("local.yml missing after rollback: %v", readErr)
	}
	if !bytes.Equal(got, origLocal) {
		t.Errorf("local.yml not restored: got %q, want %q", got, origLocal)
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

// TestMultiToggle_DeclineApply_PendingRecorded verifies that when the user
// declines the apply prompt, mutation and pending are both persisted.
func TestMultiToggle_DeclineApply_PendingRecorded(t *testing.T) {
	configPath, baseDir := writeMultiServiceProject(t,
		map[string]string{
			"ada": "type: app\ncontainer: ada\non_enable:\n  requires: deploy\n",
			"bob": "type: app\ncontainer: bob\non_enable:\n  requires: restart\n",
		}, []string{"ada"})
	localPath := filepath.Join(baseDir, "devbox", "local.yml")

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	oldMS := runMultiSelect
	t.Cleanup(func() { runMultiSelect = oldMS })
	runMultiSelect = func(_ string, _ []ui.MultiSelectItem) (ui.MultiSelectResult, error) {
		return ui.MultiSelectResult{Kept: []string{"ada", "bob"}, Locked: nil}, nil
	}

	oldPrompt := confirmApplyPrompt
	t.Cleanup(func() { confirmApplyPrompt = oldPrompt })
	confirmApplyPrompt = func() (bool, error) { return false, nil }

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
	cmd := NewCmd("", flags)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// local.yml must be written.
	if _, err := os.Stat(localPath); err != nil {
		t.Fatalf("local.yml must be written even when user declines: %v", err)
	}

	// Both pending ops must be recorded.
	pending := readPending(t, baseDir)
	if pending == nil {
		t.Fatal("expected pending state to be recorded when user declines apply")
	}
	if pending.Find(journal.PendingDeploy) == nil {
		t.Error("expected PendingDeploy op when user declines")
	}
	if pending.Find(journal.PendingRestart) == nil {
		t.Error("expected PendingRestart op when user declines")
	}
}

// TestMultiToggle_RequiresNoneAndRestart verifies that a batch mixing none and
// restart produces only a single restart apply step and a single restart pending op.
func TestMultiToggle_RequiresNoneAndRestart(t *testing.T) {
	configPath, baseDir := writeMultiServiceProject(t,
		map[string]string{
			"alpha": "type: app\ncontainer: a\non_enable:\n  requires: none\n",
			"beta":  "type: app\ncontainer: b\non_enable:\n  requires: restart\n",
		}, nil)

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	oldMS := runMultiSelect
	t.Cleanup(func() { runMultiSelect = oldMS })
	runMultiSelect = func(_ string, _ []ui.MultiSelectItem) (ui.MultiSelectResult, error) {
		return ui.MultiSelectResult{Kept: []string{"alpha", "beta"}, Locked: nil}, nil
	}

	callLog := injectMultiToggleSeams(t, nil, nil)

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
	cmd := NewCmd("", flags)
	cmd.SetArgs([]string{"--apply"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callLog.restartCalls != 1 {
		t.Errorf("expected 1 RunRestart call, got %d", callLog.restartCalls)
	}
	if len(callLog.deployOpts) != 0 {
		t.Errorf("expected no RunDeploy calls, got %d", len(callLog.deployOpts))
	}

	// Pending cleared on success.
	pending := readPending(t, baseDir)
	if pending != nil && pending.Find(journal.PendingRestart) != nil {
		t.Errorf("pending restart must be cleared after success, got %+v", pending)
	}
}

// TestBatchServiceConfigHash verifies determinism regardless of name order.
func TestBatchServiceConfigHash(t *testing.T) {
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"a": {Type: config.ServiceTypeApp, Container: "ca"},
			"b": {Type: config.ServiceTypeApp, Container: "cb"},
		},
	}
	deploys := map[string]*config.ServiceDeployConfig{}

	h1 := batchServiceConfigHash(cfg, deploys, "a", "b")
	h2 := batchServiceConfigHash(cfg, deploys, "b", "a")
	if h1 != h2 {
		t.Errorf("batchServiceConfigHash must be order-independent: %q vs %q", h1, h2)
	}

	// Single service.
	h3 := batchServiceConfigHash(cfg, deploys, "a")
	h4 := batchServiceConfigHash(cfg, deploys, "a")
	if h3 != h4 {
		t.Errorf("batchServiceConfigHash must be stable: %q vs %q", h3, h4)
	}

	// Different services must produce different hashes.
	if h3 == h1 {
		t.Error("hash of [a] should differ from hash of [a,b]")
	}
}

// TestApplyServiceToggles_MandatoryRejected verifies that ApplyServiceTogglesToYAML
// rejects an attempt to disable a mandatory service.
func TestApplyServiceToggles_MandatoryRejected(t *testing.T) {
	configPath := writeTempServiceConfig(t, map[string]struct {
		required  bool
		enabled   bool
		container string
	}{
		"main":   {required: true, enabled: false},
		"second": {required: false, enabled: false},
	})

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	baseDir := filepath.Dir(configPath)
	localPath := filepath.Join(baseDir, "devbox", "local.yml")
	local, err := localpkg.LoadLocalYAML(localPath)
	if err != nil {
		t.Fatalf("load local.yml: %v", err)
	}

	if err := localpkg.ApplyServiceTogglesToYAML(cfg, local, []string{"second"}, []string{"main"}); err == nil {
		t.Fatal("expected error for mandatory toggle, got nil")
	}
}
