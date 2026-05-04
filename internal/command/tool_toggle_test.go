package command

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/ui"

	"gopkg.in/yaml.v3"
)

// writeTempToolConfig creates a minimal devbox config in a temp dir and
// returns the path to devbox.yml. tools map: name → enabled.
func writeTempToolConfig(t *testing.T, tools map[string]bool) string {
	t.Helper()
	dir := t.TempDir()

	var lines []string
	lines = append(lines, "project:")
	lines = append(lines, "  name: test")
	lines = append(lines, "  prefix: devbox")
	lines = append(lines, "tools:")
	for _, name := range []string{"adminer", "redis_insight", "mailpit"} {
		lines = append(lines, "  "+name+":")
		if tools[name] {
			lines = append(lines, "    enabled: true")
		}
	}

	devboxYML := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"), []byte(devboxYML), 0o644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "devbox"), 0o755); err != nil {
		t.Fatalf("mkdir devbox/: %v", err)
	}

	return filepath.Join(dir, "devbox.yml")
}

func TestToolListCmd_NonTTY_NoMultiSelect(t *testing.T) {
	configPath := writeTempToolConfig(t, map[string]bool{
		"adminer": false,
		"mailpit": true,
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
	cmd := newToolListCmd(flags)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// local.yml must not be created.
	localPath := filepath.Join(filepath.Dir(configPath), "devbox", "local.yml")
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Error("local.yml should not have been created in non-TTY mode")
	}
}

func TestToolListCmd_TTY_EnablesAndDisables(t *testing.T) {
	configPath := writeTempToolConfig(t, map[string]bool{
		"adminer":       false,
		"redis_insight": false,
		"mailpit":       true,
	})

	// Simulate TTY.
	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	// Fake multi-select: user enables adminer, disables mailpit.
	oldMS := runMultiSelect
	t.Cleanup(func() { runMultiSelect = oldMS })
	runMultiSelect = func(_ string, _ []ui.MultiSelectItem) (ui.MultiSelectResult, error) {
		return ui.MultiSelectResult{Kept: []string{"adminer"}, Locked: nil}, nil
	}

	flags := &rootFlags{configPath: configPath}
	cmd := newToolListCmd(flags)
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

	toolsMap, _ := local["tools"].(map[string]any)
	if toolsMap == nil {
		t.Fatal("local.yml missing tools key")
	}

	// "adminer" should be enabled = true.
	adminerEntry, _ := toolsMap["adminer"].(map[string]any)
	if adminerEntry == nil || adminerEntry["enabled"] != true {
		t.Errorf("adminer should be enabled=true in local.yml, got %v", adminerEntry)
	}

	// "mailpit" should be enabled = false.
	mailpitEntry, _ := toolsMap["mailpit"].(map[string]any)
	if mailpitEntry == nil || mailpitEntry["enabled"] != false {
		t.Errorf("mailpit should be enabled=false in local.yml, got %v", mailpitEntry)
	}
}

func TestToolListCmd_TTY_NoChanges_NoWrites(t *testing.T) {
	configPath := writeTempToolConfig(t, map[string]bool{
		"adminer": true,
	})

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	// Fake multi-select: user leaves adminer checked (no change).
	oldMS := runMultiSelect
	t.Cleanup(func() { runMultiSelect = oldMS })
	runMultiSelect = func(_ string, _ []ui.MultiSelectItem) (ui.MultiSelectResult, error) {
		return ui.MultiSelectResult{Kept: []string{"adminer"}, Locked: nil}, nil
	}

	flags := &rootFlags{configPath: configPath}
	cmd := newToolListCmd(flags)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No changes → local.yml must not be created.
	localPath := filepath.Join(filepath.Dir(configPath), "devbox", "local.yml")
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Error("local.yml should not be created when no changes were made")
	}
}

func TestToolListCmd_TTY_CancelNoWrites(t *testing.T) {
	configPath := writeTempToolConfig(t, map[string]bool{
		"adminer": false,
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
	cmd := newToolListCmd(flags)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("cancel should return nil, got: %v", err)
	}

	localPath := filepath.Join(filepath.Dir(configPath), "devbox", "local.yml")
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Error("local.yml should not be created after cancel")
	}
}
