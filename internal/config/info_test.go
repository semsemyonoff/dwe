package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

const sampleInfoYML = `
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

func TestInfoSection_hideOnEmpty(t *testing.T) {
	testCases := []struct {
		name     string
		yaml     string
		wantHide bool
	}{
		{
			name: "hide_on_empty true",
			yaml: `id: test
hide_on_empty: true`,
			wantHide: true,
		},
		{
			name: "hide_on_empty false",
			yaml: `id: test
hide_on_empty: false`,
			wantHide: false,
		},
		{
			name:     "hide_on_empty omitted defaults to false",
			yaml:     `id: test`,
			wantHide: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var section InfoSection
			if err := yaml.Unmarshal([]byte(tc.yaml), &section); err != nil {
				t.Fatalf("yaml.Unmarshal: %v", err)
			}
			if section.HideOnEmpty != tc.wantHide {
				t.Errorf("HideOnEmpty = %v, want %v", section.HideOnEmpty, tc.wantHide)
			}
		})
	}
}

func parseInfoItem(t *testing.T, yamlStr string) InfoItem {
	t.Helper()
	var item InfoItem
	if err := yaml.Unmarshal([]byte(yamlStr), &item); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	return item
}

func TestInfoItem_IsDecorative(t *testing.T) {
	tests := []struct {
		name       string
		itemType   string
		decorative *bool
		want       bool
	}{
		// Type defaults (decorative == nil)
		{name: "info with nil decorative", itemType: "info", decorative: nil, want: false},
		{name: "warning with nil decorative", itemType: "warning", decorative: nil, want: false},
		{name: "definition with nil decorative", itemType: "definition", decorative: nil, want: false},
		{name: "separator with nil decorative", itemType: "separator", decorative: nil, want: true},
		{name: "subgroup with nil decorative", itemType: "subgroup", decorative: nil, want: false},

		// Explicit override to true
		{name: "info with decorative true", itemType: "info", decorative: ptrBool(true), want: true},
		{name: "warning with decorative true", itemType: "warning", decorative: ptrBool(true), want: true},
		{name: "separator with decorative true", itemType: "separator", decorative: ptrBool(true), want: true},

		// Explicit override to false
		{name: "separator with decorative false", itemType: "separator", decorative: ptrBool(false), want: false},
		{name: "info with decorative false", itemType: "info", decorative: ptrBool(false), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			item := InfoItem{
				Type:       tt.itemType,
				Decorative: tt.decorative,
			}
			if got := item.IsDecorative(); got != tt.want {
				t.Errorf("IsDecorative() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInfoItem_SubgroupHideOnEmpty(t *testing.T) {
	tests := []struct {
		name         string
		hideOnEmpty  *bool
		want         bool
	}{
		{name: "nil defaults to true", hideOnEmpty: nil, want: true},
		{name: "explicit true", hideOnEmpty: ptrBool(true), want: true},
		{name: "explicit false", hideOnEmpty: ptrBool(false), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			item := InfoItem{HideOnEmpty: tt.hideOnEmpty}
			if got := item.SubgroupHideOnEmpty(); got != tt.want {
				t.Errorf("SubgroupHideOnEmpty() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInfoItem_SubgroupYAMLRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		wantType string
		wantTitle string
		wantItemsLen int
		wantHideOnEmpty *bool
		wantDecorative *bool
	}{
		{
			name: "basic subgroup",
			yaml: `type: subgroup
title: Tools
items:
  - type: info
    text: hello`,
			wantType: "subgroup",
			wantTitle: "Tools",
			wantItemsLen: 1,
			wantHideOnEmpty: nil,
			wantDecorative: nil,
		},
		{
			name: "subgroup with hide_on_empty and decorative",
			yaml: `type: subgroup
title: Services
items:
  - type: definition
    name: DB
    value: postgres
hide_on_empty: false
decorative: true`,
			wantType: "subgroup",
			wantTitle: "Services",
			wantItemsLen: 1,
			wantHideOnEmpty: ptrBool(false),
			wantDecorative: ptrBool(true),
		},
		{
			name: "nested subgroup",
			yaml: `type: subgroup
title: Outer
items:
  - type: subgroup
    title: Inner
    items:
      - type: info
        text: nested`,
			wantType: "subgroup",
			wantTitle: "Outer",
			wantItemsLen: 1,
			wantHideOnEmpty: nil,
			wantDecorative: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			item := parseInfoItem(t, tt.yaml)

			if item.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", item.Type, tt.wantType)
			}
			if item.Title != tt.wantTitle {
				t.Errorf("Title = %q, want %q", item.Title, tt.wantTitle)
			}
			if len(item.Items) != tt.wantItemsLen {
				t.Errorf("Items len = %d, want %d", len(item.Items), tt.wantItemsLen)
			}
			if !boolPtrsEqual(item.HideOnEmpty, tt.wantHideOnEmpty) {
				t.Errorf("HideOnEmpty = %v, want %v", item.HideOnEmpty, tt.wantHideOnEmpty)
			}
			if !boolPtrsEqual(item.Decorative, tt.wantDecorative) {
				t.Errorf("Decorative = %v, want %v", item.Decorative, tt.wantDecorative)
			}
		})
	}
}

func ptrBool(b bool) *bool {
	return &b
}

func boolPtrsEqual(a, b *bool) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}
