package command

import (
	"errors"
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
	var svcLines []string
	svcLines = append(svcLines, "services:")
	for name, spec := range services {
		svcLines = append(svcLines, "  "+name+":")
		svcLines = append(svcLines, "    type: app")
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
	if !strings.Contains(err.Error(), "devbox status services") {
		t.Errorf("error should hint at 'devbox status services', got: %v", err)
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
