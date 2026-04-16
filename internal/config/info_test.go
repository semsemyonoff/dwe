package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

const sampleInfoYML = `
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

func TestLoadInfoConfig(t *testing.T) {
	path := writeTempYML(t, sampleInfoYML)
	cfg, err := LoadInfoConfig(path)
	if err != nil {
		t.Fatalf("LoadInfoConfig: %v", err)
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

func TestLoadInfoConfig_notFound(t *testing.T) {
	_, err := LoadInfoConfig("/tmp/devbox-nonexistent-info.yml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func parseInfoIndent(t *testing.T, yamlStr string) InfoIndent {
	t.Helper()
	var item struct {
		Indent InfoIndent `yaml:"indent"`
	}
	if err := yaml.Unmarshal([]byte(yamlStr), &item); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	return item.Indent
}

func TestInfoIndent_notSet(t *testing.T) {
	h := parseInfoIndent(t, `{}`)
	if h.IsSet() {
		t.Error("IsSet() should be false when indent is omitted")
	}
	if h.Value() != 0 {
		t.Errorf("Value() = %d, want 0", h.Value())
	}
}

func TestInfoIndent_int(t *testing.T) {
	h := parseInfoIndent(t, `indent: 4`)
	if !h.IsSet() {
		t.Error("IsSet() should be true")
	}
	if h.Value() != 4 {
		t.Errorf("Value() = %d, want 4", h.Value())
	}
}

func TestInfoIndent_zero(t *testing.T) {
	h := parseInfoIndent(t, `indent: 0`)
	if !h.IsSet() {
		t.Error("IsSet() should be true for explicit 0")
	}
	if h.Value() != 0 {
		t.Errorf("Value() = %d, want 0", h.Value())
	}
}

func TestInfoIndent_invalidType(t *testing.T) {
	var item struct {
		Indent InfoIndent `yaml:"indent"`
	}
	for _, bad := range []string{`indent: "oops"`, `indent: false`, `indent: true`} {
		if err := yaml.Unmarshal([]byte(bad), &item); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}
