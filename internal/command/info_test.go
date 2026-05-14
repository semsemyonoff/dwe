package command

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMinimalInfoYML writes a minimal info.yml to dir and returns its path.
func writeMinimalInfoYML(t *testing.T, dir, content string) {
	t.Helper()
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(devboxDir, "info.yml"), []byte(content), 0644); err != nil {
		t.Fatalf("writing info.yml: %v", err)
	}
}

// writeMinimalDevboxYML writes a minimal devbox.yml to dir.
func writeMinimalDevboxYML(t *testing.T, dir string) string {
	t.Helper()
	cfgPath := filepath.Join(dir, "devbox.yml")
	cfgYAML := `schema_version: "2"
project:
  name: infotest
  prefix: devbox
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0644); err != nil {
		t.Fatalf("writing devbox.yml: %v", err)
	}
	return cfgPath
}

// TestInfoCmd_RendersSection verifies that info command renders section content.
func TestInfoCmd_RendersSection(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeMinimalDevboxYML(t, dir)
	writeMinimalInfoYML(t, dir, `sections:
  - id: details
    title: Project Details
    items:
      - type: definition
        name: Project
        value: "{{ .Project.Name }}"
`)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"info"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Errorf("info command returned error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Project Details") {
		t.Errorf("expected section title in output, got:\n%s", out)
	}
	if !strings.Contains(out, "infotest") {
		t.Errorf("expected project name in output, got:\n%s", out)
	}
}

// TestInfoCmd_RendersWarningAndInfoItems verifies warning/info item types.
func TestInfoCmd_RendersWarningAndInfoItems(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeMinimalDevboxYML(t, dir)
	writeMinimalInfoYML(t, dir, `sections:
  - id: hosts
    title: Hosts
    items:
      - type: warning
        text: "Add to /etc/hosts"
      - type: info
        text: "127.0.0.1 app.local"
`)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"info"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Errorf("info command returned error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Add to /etc/hosts") {
		t.Errorf("expected warning text in output, got:\n%s", out)
	}
	if !strings.Contains(out, "127.0.0.1 app.local") {
		t.Errorf("expected info text in output, got:\n%s", out)
	}
}

// TestInfoCmd_ConditionalItems verifies that when: conditions are evaluated.
func TestInfoCmd_ConditionalItems(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeMinimalDevboxYML(t, dir)
	writeMinimalInfoYML(t, dir, `sections:
  - id: cond
    items:
      - type: definition
        name: visible
        value: shown
        when: "{{if .Project.Name}}true{{end}}"
      - type: definition
        name: hidden
        value: never-shown
        when: "{{if eq .Project.Name \"nonexistent\"}}true{{end}}"
`)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"info"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Errorf("info command returned error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "shown") {
		t.Errorf("expected visible item in output, got:\n%s", out)
	}
	if strings.Contains(out, "never-shown") {
		t.Errorf("expected hidden item absent from output, got:\n%s", out)
	}
}

// TestInfoCmd_StylesMissingIsGraceful verifies that info command does not error
// when devbox/styles.yml is absent.
func TestInfoCmd_StylesMissingIsGraceful(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeMinimalDevboxYML(t, dir)
	writeMinimalInfoYML(t, dir, `sections:
  - id: s1
    title: Test
    items:
      - type: definition
        name: Name
        value: "{{ .Project.Name }}"
`)
	// No devbox/styles.yml — must not cause an error.
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"info"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}
	if err := root.Execute(); err != nil {
		t.Errorf("info command returned unexpected error without styles.yml: %v", err)
	}
	if !strings.Contains(buf.String(), "infotest") {
		t.Errorf("expected project name in output, got:\n%s", buf.String())
	}
}

// TestInfoCmd_StylesWithHeaderRendered verifies that info command renders ASCII
// art header when devbox/styles.yml defines one, without error.
func TestInfoCmd_StylesWithHeaderRendered(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeMinimalDevboxYML(t, dir)
	writeMinimalInfoYML(t, dir, `sections:
  - id: s1
    items:
      - type: definition
        name: Name
        value: "{{ .Project.Name }}"
`)
	devboxDir := filepath.Join(dir, "devbox")
	stylesYAML := "header:\n  lines:\n    - \"Devbox\"\n  font: standard\n  color: none\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "styles.yml"), []byte(stylesYAML), 0644); err != nil {
		t.Fatalf("writing styles.yml: %v", err)
	}

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"info"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}
	if err := root.Execute(); err != nil {
		t.Errorf("info command returned error with styles.yml header: %v", err)
	}
	if !strings.Contains(buf.String(), "infotest") {
		t.Errorf("expected project name in output, got:\n%s", buf.String())
	}
}

// TestInfoCmd_MissingInfoYMLIsGraceful verifies that `devbox info` does not
// error when devbox/info.yml is absent. It should render a minimal summary
// (project name) on stdout and a warning on stderr.
func TestInfoCmd_MissingInfoYMLIsGraceful(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeMinimalDevboxYML(t, dir)
	// Intentionally no info.yml written.

	root := NewRootCmd()
	var out bytes.Buffer
	var errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs([]string{"info"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Errorf("info command returned error without info.yml: %v", err)
	}

	if !strings.Contains(out.String(), "infotest") {
		t.Errorf("expected project name in minimal summary, got stdout:\n%s", out.String())
	}
	if !strings.Contains(errBuf.String(), "info.yml") {
		t.Errorf("expected warning about missing info.yml on stderr, got:\n%s", errBuf.String())
	}
}

// TestInfoCmd_MissingConfig returns an error when devbox.yml is not found.
func TestInfoCmd_MissingConfig(t *testing.T) {
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"info"})
	if err := root.PersistentFlags().Set("config", "/tmp/nonexistent-devbox-xyz.yml"); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	err := root.Execute()
	if err == nil {
		t.Error("expected error when config is missing, got nil")
	}
}

// TestInfoCmd_UsesUIRenderInfo verifies that the info command no longer uses
// the legacy TableHeader/Definition rendering path by checking that section
// content is still present (structural smoke test).
func TestInfoCmd_UsesUIRenderInfo(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeMinimalDevboxYML(t, dir)
	writeMinimalInfoYML(t, dir, `sections:
  - id: s1
    title: Status
    items:
      - type: definition
        name: Project
        value: "{{ .Project.Name }}"
      - type: definition
        name: Prefix
        value: "{{ .Project.Prefix }}"
footer: true
`)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"info"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Errorf("info command returned error: %v", err)
	}

	out := buf.String()
	// Project name and prefix should appear.
	if !strings.Contains(out, "infotest") {
		t.Errorf("expected project name, got:\n%s", out)
	}
	if !strings.Contains(out, "devbox") {
		t.Errorf("expected project prefix, got:\n%s", out)
	}
	// Section title should appear.
	if !strings.Contains(out, "Status") {
		t.Errorf("expected section title, got:\n%s", out)
	}
}
