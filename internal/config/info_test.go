package config

import (
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const sampleInfoYML = `
sections:
  - id: devbox_info
    items:
      - type: subgroup
        title: Devbox
        items:
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
      - type: subgroup
        title: Main
        items:
          - type: definition
            indent: 2
            name: URL
            value: '{{ appURL (index .Services "main").Host "web" (index .Services "app").Port "http" .Runtime.UseHTTPS }}'
      - type: subgroup
        title: Tools
        when: '{{ len .ToolServices }}'
        items:
          - type: definition
            indent: 2
            name: Adminer
            value: "adminer-url"
            when: '{{ (index .Services "adminer").Enabled }}'

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
	if len(devboxInfo.Items) != 1 {
		t.Fatalf("sections[0] items = %d, want 1 (subgroup)", len(devboxInfo.Items))
	}

	subgroup := devboxInfo.Items[0]
	if subgroup.Type != "subgroup" {
		t.Errorf("sections[0].items[0].type = %q, want subgroup", subgroup.Type)
	}
	if subgroup.Title != "Devbox" {
		t.Errorf("sections[0].items[0].title = %q, want Devbox", subgroup.Title)
	}
	if len(subgroup.Items) != 2 {
		t.Fatalf("subgroup items = %d, want 2", len(subgroup.Items))
	}

	stateItem := subgroup.Items[1]
	if stateItem.When != "{{ .State }}" {
		t.Errorf("state item when = %q", stateItem.When)
	}

	urls := cfg.Sections[1]
	if urls.Title != "URLs" {
		t.Errorf("sections[1].title = %q", urls.Title)
	}
	if len(urls.Items) != 2 {
		t.Fatalf("sections[1] items = %d, want 2 (subgroups)", len(urls.Items))
	}

	// The second subgroup (Tools) has a when: expression — verify it is parsed.
	toolsSubgroup := urls.Items[1]
	if toolsSubgroup.When != "{{ len .ToolServices }}" {
		t.Errorf("tools subgroup when = %q, want {{ len .ToolServices }}", toolsSubgroup.When)
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

func TestInfoIndent_negativeValue(t *testing.T) {
	var item struct {
		Indent InfoIndent `yaml:"indent"`
	}
	err := yaml.Unmarshal([]byte(`indent: -1`), &item)
	if err == nil {
		t.Fatal("expected error for negative indent value")
	}
	if !strings.Contains(err.Error(), "negative") {
		t.Errorf("error should mention 'negative': %v", err)
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
		name        string
		hideOnEmpty *bool
		want        bool
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
		name            string
		yaml            string
		wantType        string
		wantTitle       string
		wantItemsLen    int
		wantHideOnEmpty *bool
		wantDecorative  *bool
	}{
		{
			name: "basic subgroup",
			yaml: `type: subgroup
title: Tools
items:
  - type: info
    text: hello`,
			wantType:        "subgroup",
			wantTitle:       "Tools",
			wantItemsLen:    1,
			wantHideOnEmpty: nil,
			wantDecorative:  nil,
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
			wantType:        "subgroup",
			wantTitle:       "Services",
			wantItemsLen:    1,
			wantHideOnEmpty: ptrBool(false),
			wantDecorative:  ptrBool(true),
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
			wantType:        "subgroup",
			wantTitle:       "Outer",
			wantItemsLen:    1,
			wantHideOnEmpty: nil,
			wantDecorative:  nil,
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

func TestValidateInfoConfig_unknownType(t *testing.T) {
	tests := []struct {
		name        string
		unknownType string
	}{
		{name: "subheader is unknown", unknownType: "subheader"},
		{name: "made_up_type is unknown", unknownType: "made_up_type"},
		{name: "typo in type name", unknownType: "definitino"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			yml := fmt.Sprintf(`
sections:
  - id: test
    items:
      - type: %s
        text: oops
`, tt.unknownType)
			path := writeTempYML(t, yml)
			_, err := LoadInfoConfig(path)
			if err == nil {
				t.Fatal("expected error for unknown type")
			}
			if !strings.Contains(err.Error(), "unknown type") {
				t.Errorf("error should mention 'unknown type': %v", err)
			}
			if !strings.Contains(err.Error(), "valid types") {
				t.Errorf("error should list valid types: %v", err)
			}
		})
	}
}

func TestValidateInfoConfig_emptySubgroupItems(t *testing.T) {
	yml := `
sections:
  - id: test
    items:
      - type: subgroup
        title: Empty
        items: []
`
	path := writeTempYML(t, yml)
	_, err := LoadInfoConfig(path)
	if err == nil {
		t.Fatal("expected error for empty subgroup items")
	}
	if !strings.Contains(err.Error(), "subgroup must declare items") {
		t.Errorf("error should mention 'subgroup must declare items': %v", err)
	}
}

func TestValidateInfoConfig_nestedUnknownType(t *testing.T) {
	yml := `
sections:
  - id: test
    items:
      - type: subgroup
        title: Parent
        items:
          - type: bad_type
            text: oops
`
	path := writeTempYML(t, yml)
	_, err := LoadInfoConfig(path)
	if err == nil {
		t.Fatal("expected error for nested unknown type")
	}
	if !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("error should mention 'unknown type': %v", err)
	}
	// Should indicate it's nested
	if !strings.Contains(err.Error(), ".items[") {
		t.Errorf("error should include path with .items: %v", err)
	}
}

func TestValidateInfoConfig_validSubgroupRecursion(t *testing.T) {
	yml := `
sections:
  - id: test
    items:
      - type: subgroup
        title: Parent
        hide_on_empty: false
        decorative: true
        items:
          - type: subgroup
            title: Child
            items:
              - type: info
                text: nested content
          - type: definition
            name: DB
            value: postgres
`
	path := writeTempYML(t, yml)
	cfg, err := LoadInfoConfig(path)
	if err != nil {
		t.Fatalf("valid subgroup config should parse: %v", err)
	}

	if len(cfg.Sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(cfg.Sections))
	}

	parent := cfg.Sections[0].Items[0]
	if parent.Type != "subgroup" || parent.Title != "Parent" {
		t.Errorf("expected parent subgroup, got type=%q title=%q", parent.Type, parent.Title)
	}
	if !boolPtrsEqual(parent.HideOnEmpty, ptrBool(false)) {
		t.Errorf("expected hide_on_empty=false, got %v", parent.HideOnEmpty)
	}
	if !boolPtrsEqual(parent.Decorative, ptrBool(true)) {
		t.Errorf("expected decorative=true, got %v", parent.Decorative)
	}

	if len(parent.Items) != 2 {
		t.Fatalf("parent should have 2 items, got %d", len(parent.Items))
	}

	child := parent.Items[0]
	if child.Type != "subgroup" || child.Title != "Child" {
		t.Errorf("expected child subgroup, got type=%q title=%q", child.Type, child.Title)
	}
	if len(child.Items) != 1 {
		t.Fatalf("child should have 1 item, got %d", len(child.Items))
	}
}

func TestLoadInfoConfig_arbitraryToolKey(t *testing.T) {
	// Regression test: verify that an arbitrary tool key (e.g. elasticvue)
	// not in the hardcoded original set (adminer, redis_insight, mailpit)
	// can be referenced in templates with mixed-case syntax.
	yml := `
sections:
  - id: tools
    items:
      - type: subgroup
        title: Utilities
        items:
          - type: definition
            name: ElasticVue
            value: "elasticvue-dashboard"
            when: '{{ (index .Services "elasticvue").Enabled }}'
`
	path := writeTempYML(t, yml)
	cfg, err := LoadInfoConfig(path)
	if err != nil {
		t.Fatalf("LoadInfoConfig for arbitrary tool key: %v", err)
	}

	if len(cfg.Sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(cfg.Sections))
	}

	subgroup := cfg.Sections[0].Items[0]
	item := subgroup.Items[0]
	if item.When != `{{ (index .Services "elasticvue").Enabled }}` {
		t.Errorf("expected services template for arbitrary tool, got: %q", item.When)
	}
}
