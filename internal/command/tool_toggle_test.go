package command

import (
	"errors"
	"fmt"
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

	toolDefs := map[string]struct {
		container string
		host      string
		port      int
	}{
		"adminer":       {container: "adminer", host: "adminer.localhost", port: 8080},
		"redis_insight": {container: "redis_insight", host: "redis.localhost", port: 5540},
		"mailpit":       {container: "mailpit", host: "mail.localhost", port: 8025},
	}
	toolOrder := []string{"adminer", "redis_insight", "mailpit"}

	var lines []string
	lines = append(lines, "project:")
	lines = append(lines, "  name: test")
	lines = append(lines, "  prefix: devbox")
	lines = append(lines, "runtime:")
	lines = append(lines, "  ports:")
	lines = append(lines, "    app: 3000")
	for _, name := range toolOrder {
		lines = append(lines, "    "+name+": "+fmt.Sprint(toolDefs[name].port))
	}
	lines = append(lines, "  hosts:")
	lines = append(lines, "    main: localhost")
	for _, name := range toolOrder {
		lines = append(lines, "    "+name+": "+toolDefs[name].host)
	}
	lines = append(lines, "tools:")
	for _, name := range toolOrder {
		lines = append(lines, "  "+name+":")
		if tools[name] {
			lines = append(lines, "    enabled: true")
		} else {
			lines = append(lines, "    enabled: false")
		}
	}

	devboxYML := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"), []byte(devboxYML), 0o644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "devbox"), 0o755); err != nil {
		t.Fatalf("mkdir devbox/: %v", err)
	}

	var toolsLines []string
	toolsLines = append(toolsLines, "tools:")
	for _, name := range toolOrder {
		toolsLines = append(toolsLines, "  "+name+":")
		toolsLines = append(toolsLines, "    container: "+toolDefs[name].container)
	}
	toolsYML := strings.Join(toolsLines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "devbox", "tools.yml"), []byte(toolsYML), 0o644); err != nil {
		t.Fatalf("write tools.yml: %v", err)
	}

	return filepath.Join(dir, "devbox.yml")
}

func TestToolsToggle_NonTTY_ReturnsInteractiveRequired(t *testing.T) {
	configPath := writeTempToolConfig(t, map[string]bool{"adminer": false})

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
	cmd := newToolCmd(flags)
	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("expected non-TTY error, got nil")
	}
	if !errors.Is(err, ErrInteractiveRequired) {
		t.Errorf("expected ErrInteractiveRequired, got: %v", err)
	}
	if !strings.Contains(err.Error(), "devbox status tools") {
		t.Errorf("error should hint at 'devbox status tools', got: %v", err)
	}
}

func TestToolsToggle_TTY_EnablesAndDisables(t *testing.T) {
	configPath := writeTempToolConfig(t, map[string]bool{
		"adminer":       false,
		"redis_insight": false,
		"mailpit":       true,
	})

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	oldMS := runMultiSelect
	t.Cleanup(func() { runMultiSelect = oldMS })
	runMultiSelect = func(_ string, _ []ui.MultiSelectItem) (ui.MultiSelectResult, error) {
		return ui.MultiSelectResult{Kept: []string{"adminer"}, Locked: nil}, nil
	}

	flags := &rootFlags{configPath: configPath}
	cmd := newToolCmd(flags)
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
	toolsMap, _ := local["tools"].(map[string]any)
	if toolsMap == nil {
		t.Fatal("local.yml missing tools key")
	}
	adminerEntry, _ := toolsMap["adminer"].(map[string]any)
	if adminerEntry == nil || adminerEntry["enabled"] != true {
		t.Errorf("adminer should be enabled=true, got %v", adminerEntry)
	}
	mailpitEntry, _ := toolsMap["mailpit"].(map[string]any)
	if mailpitEntry == nil || mailpitEntry["enabled"] != false {
		t.Errorf("mailpit should be enabled=false, got %v", mailpitEntry)
	}
}

func TestToolsToggle_TTY_CancelNoWrites(t *testing.T) {
	configPath := writeTempToolConfig(t, map[string]bool{"adminer": false})

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	oldMS := runMultiSelect
	t.Cleanup(func() { runMultiSelect = oldMS })
	runMultiSelect = func(_ string, _ []ui.MultiSelectItem) (ui.MultiSelectResult, error) {
		return ui.MultiSelectResult{}, ui.ErrCancelled
	}

	flags := &rootFlags{configPath: configPath}
	cmd := newToolCmd(flags)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("cancel should return nil, got: %v", err)
	}
	localPath := filepath.Join(filepath.Dir(configPath), "devbox", "local.yml")
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Error("local.yml should not be created after cancel")
	}
}

func TestToolsToggle_EmptyTools_ReturnsError(t *testing.T) {
	// Tools config with no tools at all → "no tools configured"
	dir := t.TempDir()
	devboxYML := `project:
  name: test
  prefix: devbox
`
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"), []byte(devboxYML), 0o644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "devbox"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	oldInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = oldInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	flags := &rootFlags{configPath: filepath.Join(dir, "devbox.yml")}
	cmd := newToolCmd(flags)
	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error when no tools to toggle, got nil")
	}
	if !strings.Contains(err.Error(), "no tools configured") {
		t.Errorf("error should say 'no tools configured', got: %v", err)
	}
}
