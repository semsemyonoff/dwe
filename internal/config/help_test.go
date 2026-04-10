package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

const sampleHelpYML = `
header:
  ascii:
    lines:
      - "Welcome to"
      - "Devbox 2.0"
    font: standard
    color: blue

sections:
  - id: devbox_info
    items:
      - type: subheader
        text: Devbox
      - type: definition
        indent: 2
        name: Project
        value: "{{ .Project.FullName }}"
      - type: definition
        indent: 2
        name: State
        value: "{{ .State }}"
        when: "{{ .State }}"

  - id: urls
    title: URLs
    items:
      - type: subheader
        text: Main
      - type: definition
        indent: 2
        name: URL
        value: "{{ appURL .Runtime.Hosts.Main .Runtime.Ports.App .Runtime.UseHTTPS }}"
      - type: subheader
        text: Tools
        when: "{{ .Tools.AnyEnabled }}"
      - type: definition
        indent: 2
        name: Adminer
        value: "adminer-url"
        when: "{{ .Tools.Adminer.Enabled }}"

footer: true
`

func TestLoadHelpConfig(t *testing.T) {
	path := writeTempYML(t, sampleHelpYML)
	cfg, err := LoadHelpConfig(path)
	if err != nil {
		t.Fatalf("LoadHelpConfig: %v", err)
	}

	if cfg.Header.ASCII.Color != "blue" {
		t.Errorf("header.ascii.color = %q", cfg.Header.ASCII.Color)
	}
	if cfg.Header.ASCII.Font != "standard" {
		t.Errorf("header.ascii.font = %q", cfg.Header.ASCII.Font)
	}
	if len(cfg.Header.ASCII.Lines) != 2 {
		t.Errorf("header.ascii.lines count = %d, want 2", len(cfg.Header.ASCII.Lines))
	}
	if cfg.Header.ASCII.Lines[0] != "Welcome to" {
		t.Errorf("header.ascii.lines[0] = %q", cfg.Header.ASCII.Lines[0])
	}
	if !cfg.Footer {
		t.Error("footer should be true")
	}
	if len(cfg.Sections) != 2 {
		t.Fatalf("sections count = %d, want 2", len(cfg.Sections))
	}

	devboxInfo := cfg.Sections[0]
	if devboxInfo.ID != "devbox_info" {
		t.Errorf("sections[0].id = %q", devboxInfo.ID)
	}
	if devboxInfo.Title != "" {
		t.Errorf("sections[0].title should be empty, got %q", devboxInfo.Title)
	}
	if len(devboxInfo.Items) != 3 {
		t.Fatalf("sections[0] items = %d, want 3", len(devboxInfo.Items))
	}

	stateItem := devboxInfo.Items[2]
	if stateItem.When != "{{ .State }}" {
		t.Errorf("state item when = %q", stateItem.When)
	}

	urls := cfg.Sections[1]
	if urls.Title != "URLs" {
		t.Errorf("sections[1].title = %q", urls.Title)
	}
	if len(urls.Items) != 4 {
		t.Fatalf("sections[1] items = %d, want 4", len(urls.Items))
	}
}

func TestLoadHelpConfig_notFound(t *testing.T) {
	_, err := LoadHelpConfig("/tmp/devbox-nonexistent-help.yml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func parseIndent(t *testing.T, yamlStr string) HelpIndent {
	t.Helper()
	var item struct {
		Indent HelpIndent `yaml:"indent"`
	}
	if err := yaml.Unmarshal([]byte(yamlStr), &item); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	return item.Indent
}

func TestHelpIndent_notSet(t *testing.T) {
	h := parseIndent(t, `{}`)
	if h.IsSet() {
		t.Error("IsSet() should be false when indent is omitted")
	}
	if h.Value() != 0 {
		t.Errorf("Value() = %d, want 0", h.Value())
	}
}

func TestHelpIndent_int(t *testing.T) {
	h := parseIndent(t, `indent: 4`)
	if !h.IsSet() {
		t.Error("IsSet() should be true")
	}
	if h.Value() != 4 {
		t.Errorf("Value() = %d, want 4", h.Value())
	}
}

func TestHelpIndent_zero(t *testing.T) {
	h := parseIndent(t, `indent: 0`)
	if !h.IsSet() {
		t.Error("IsSet() should be true for explicit 0")
	}
	if h.Value() != 0 {
		t.Errorf("Value() = %d, want 0", h.Value())
	}
}

func TestHelpIndent_invalidType(t *testing.T) {
	var item struct {
		Indent HelpIndent `yaml:"indent"`
	}
	for _, bad := range []string{`indent: "oops"`, `indent: false`, `indent: true`} {
		if err := yaml.Unmarshal([]byte(bad), &item); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}
