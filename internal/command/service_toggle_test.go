package command

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/ui"

	"gopkg.in/yaml.v3"
)

// writeTempServiceConfig creates a minimal devbox config in a temp dir and
// returns the path to devbox.yml. Services map: name → {mandatory, enabled}.
func writeTempServiceConfig(t *testing.T, services map[string]struct {
	mandatory bool
	enabled   bool
	container string
}) string {
	t.Helper()
	dir := t.TempDir()

	// devbox.yml: project block + enabled states (Enabled is computed from merge)
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

	// devbox/services.yml: Container and Mandatory
	if err := os.MkdirAll(filepath.Join(dir, "devbox"), 0o755); err != nil {
		t.Fatalf("mkdir devbox/: %v", err)
	}
	var svcLines []string
	svcLines = append(svcLines, "services:")
	for name, spec := range services {
		svcLines = append(svcLines, "  "+name+":")
		container := spec.container
		if container == "" {
			container = "app-" + name
		}
		svcLines = append(svcLines, "    container: "+container)
		if spec.mandatory {
			svcLines = append(svcLines, "    mandatory: true")
		}
	}
	servicesYML := strings.Join(svcLines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "devbox", "services.yml"), []byte(servicesYML), 0o644); err != nil {
		t.Fatalf("write services.yml: %v", err)
	}

	return filepath.Join(dir, "devbox.yml")
}

func TestServiceListCmd_NonTTY_NoMultiSelect(t *testing.T) {
	configPath := writeTempServiceConfig(t, map[string]struct {
		mandatory bool
		enabled   bool
		container string
	}{
		"main":   {mandatory: true, enabled: false},
		"second": {mandatory: false, enabled: false},
	})

	// Simulate non-TTY: IsInteractiveFn returns false.
	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return false }

	// runMultiSelect must not be called.
	oldMS := runMultiSelect
	t.Cleanup(func() { runMultiSelect = oldMS })
	runMultiSelect = func(_ string, _ []ui.MultiSelectItem) (ui.MultiSelectResult, error) {
		t.Fatal("runMultiSelect should not be called in non-TTY mode")
		return ui.MultiSelectResult{}, nil
	}

	flags := &rootFlags{configPath: configPath}
	cmd := newServiceListCmd(flags)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// local.yml must not be created.
	localPath := filepath.Join(filepath.Dir(configPath), "devbox", "local.yml")
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Error("local.yml should not have been created in non-TTY mode")
	}
}

func TestServiceListCmd_TTY_EnablesAndDisables(t *testing.T) {
	configPath := writeTempServiceConfig(t, map[string]struct {
		mandatory bool
		enabled   bool
		container string
	}{
		"main":   {mandatory: true, enabled: false},
		"second": {mandatory: false, enabled: false},
		"third":  {mandatory: false, enabled: true},
	})

	// Simulate TTY.
	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	// Fake multi-select: user enables "second", disables "third".
	oldMS := runMultiSelect
	t.Cleanup(func() { runMultiSelect = oldMS })
	runMultiSelect = func(_ string, _ []ui.MultiSelectItem) (ui.MultiSelectResult, error) {
		return ui.MultiSelectResult{Kept: []string{"second"}, Locked: []string{"main"}}, nil
	}

	flags := &rootFlags{configPath: configPath}
	cmd := newServiceListCmd(flags)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Read local.yml and verify the changes.
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

	// "second" should be enabled = true.
	secondEntry, _ := svcMap["second"].(map[string]any)
	if secondEntry == nil || secondEntry["enabled"] != true {
		t.Errorf("second should be enabled=true in local.yml, got %v", secondEntry)
	}

	// "third" should be enabled = false.
	thirdEntry, _ := svcMap["third"].(map[string]any)
	if thirdEntry == nil || thirdEntry["enabled"] != false {
		t.Errorf("third should be enabled=false in local.yml, got %v", thirdEntry)
	}
}

func TestServiceListCmd_TTY_NoChanges_NoWrites(t *testing.T) {
	configPath := writeTempServiceConfig(t, map[string]struct {
		mandatory bool
		enabled   bool
		container string
	}{
		"second": {mandatory: false, enabled: true},
	})

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	// Fake multi-select: user leaves second checked (no change).
	oldMS := runMultiSelect
	t.Cleanup(func() { runMultiSelect = oldMS })
	runMultiSelect = func(_ string, _ []ui.MultiSelectItem) (ui.MultiSelectResult, error) {
		return ui.MultiSelectResult{Kept: []string{"second"}, Locked: nil}, nil
	}

	flags := &rootFlags{configPath: configPath}
	cmd := newServiceListCmd(flags)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No changes → local.yml must not be created.
	localPath := filepath.Join(filepath.Dir(configPath), "devbox", "local.yml")
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Error("local.yml should not be created when no changes were made")
	}
}

// TestApplyServiceTogglesBatch_AllOrNothing verifies that when validation
// rejects any toggle in the batch, no partial state is written to local.yml.
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

	// "second" is valid; "main" is mandatory — batch must reject before writing.
	err = applyServiceTogglesBatch(configPath, cfg, []string{"second"}, []string{"main"})
	if err == nil {
		t.Fatal("expected error for mandatory toggle, got nil")
	}

	localPath := filepath.Join(filepath.Dir(configPath), "devbox", "local.yml")
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Error("local.yml must not be written when batch validation fails")
	}
}

func TestServiceListCmd_TTY_CancelNoWrites(t *testing.T) {
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

	// Fake multi-select: user presses Esc.
	oldMS := runMultiSelect
	t.Cleanup(func() { runMultiSelect = oldMS })
	runMultiSelect = func(_ string, _ []ui.MultiSelectItem) (ui.MultiSelectResult, error) {
		return ui.MultiSelectResult{}, ui.ErrCancelled
	}

	flags := &rootFlags{configPath: configPath}
	cmd := newServiceListCmd(flags)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("cancel should return nil, got: %v", err)
	}

	localPath := filepath.Join(filepath.Dir(configPath), "devbox", "local.yml")
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Error("local.yml should not be created after cancel")
	}
}
