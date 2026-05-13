package ui

import (
	"strings"
	"testing"

	"devbox-cli/internal/config"
)

func makeInfoConfig(sections []config.InfoSection) *config.InfoConfig {
	return &config.InfoConfig{Sections: sections}
}

func TestRenderInfo_SectionTitle(t *testing.T) {
	infoCfg := makeInfoConfig([]config.InfoSection{
		{ID: "s1", Title: "Project Info", Items: nil},
	})
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Project Info") {
		t.Errorf("expected section title in output, got:\n%s", out)
	}
}

func TestRenderInfo_DefinitionItem(t *testing.T) {
	infoCfg := makeInfoConfig([]config.InfoSection{
		{
			ID:    "s1",
			Title: "Details",
			Items: []config.InfoItem{
				{Type: "definition", Name: "project", Value: "myapp"},
			},
		},
	})
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "project") {
		t.Errorf("expected definition key in output, got:\n%s", out)
	}
	if !strings.Contains(out, "myapp") {
		t.Errorf("expected definition value in output, got:\n%s", out)
	}
}

func TestRenderInfo_DefinitionWithIcon(t *testing.T) {
	infoCfg := makeInfoConfig([]config.InfoSection{
		{
			ID: "s1",
			Items: []config.InfoItem{
				{Type: "definition", Name: "url", Value: "http://app.local", Icon: "🌐"},
			},
		},
	})
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "🌐") {
		t.Errorf("expected icon in output, got:\n%s", out)
	}
	if !strings.Contains(out, "http://app.local") {
		t.Errorf("expected value in output, got:\n%s", out)
	}
}

func TestRenderInfo_WarningItem(t *testing.T) {
	infoCfg := makeInfoConfig([]config.InfoSection{
		{
			ID: "s1",
			Items: []config.InfoItem{
				{Type: "warning", Text: "something is wrong"},
			},
		},
	})
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "something is wrong") {
		t.Errorf("expected warning text in output, got:\n%s", out)
	}
}

func TestRenderInfo_InfoItem(t *testing.T) {
	infoCfg := makeInfoConfig([]config.InfoSection{
		{
			ID: "s1",
			Items: []config.InfoItem{
				{Type: "info", Text: "helpful hint"},
			},
		},
	})
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "helpful hint") {
		t.Errorf("expected info text in output, got:\n%s", out)
	}
}

func TestRenderInfo_ConditionalItem_Show(t *testing.T) {
	infoCfg := makeInfoConfig([]config.InfoSection{
		{
			ID: "s1",
			Items: []config.InfoItem{
				{Type: "definition", Name: "env", Value: "prod", When: "{{if .Project.Name}}true{{end}}"},
			},
		},
	})
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
	}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "prod") {
		t.Errorf("expected conditional item shown, got:\n%s", out)
	}
}

func TestRenderInfo_ConditionalItem_Hide(t *testing.T) {
	infoCfg := makeInfoConfig([]config.InfoSection{
		{
			ID: "s1",
			Items: []config.InfoItem{
				{Type: "definition", Name: "env", Value: "prod", When: "{{if .Project.Name}}true{{end}}"},
			},
		},
	})
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: ""},
	}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "prod") {
		t.Errorf("expected conditional item hidden, got:\n%s", out)
	}
}

func TestRenderInfo_TemplateInValue(t *testing.T) {
	infoCfg := makeInfoConfig([]config.InfoSection{
		{
			ID: "s1",
			Items: []config.InfoItem{
				{Type: "definition", Name: "name", Value: "{{.Project.Name}}"},
			},
		},
	})
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "testproject"},
	}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "testproject") {
		t.Errorf("expected template value evaluated, got:\n%s", out)
	}
}

func TestRenderInfo_Footer(t *testing.T) {
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{ID: "s1", Title: "Test"},
		},
		Footer: true,
	}
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Footer adds a separator line. The output should have multiple lines.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Errorf("expected multiple lines with footer, got:\n%s", out)
	}
}

func TestRenderInfo_MultipleSection(t *testing.T) {
	infoCfg := makeInfoConfig([]config.InfoSection{
		{ID: "s1", Title: "First"},
		{ID: "s2", Title: "Second"},
	})
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "First") || !strings.Contains(out, "Second") {
		t.Errorf("expected both section titles, got:\n%s", out)
	}
}

func TestRenderInfo_TemplateError(t *testing.T) {
	infoCfg := makeInfoConfig([]config.InfoSection{
		{
			ID: "s1",
			Items: []config.InfoItem{
				{Type: "definition", Name: "k", Value: "{{.Invalid.Field.Path}}"},
			},
		},
	})
	cfg := &config.DevboxConfig{}
	_, err := RenderInfo(cfg, infoCfg)
	if err == nil {
		t.Error("expected error from invalid template, got nil")
	}
}

func TestRenderSectionTitle_NonEmpty(t *testing.T) {
	resetStyles()
	out := RenderSectionTitle("My Section")
	if out == "" {
		t.Error("expected non-empty output for non-empty title")
	}
}

func TestRenderSectionTitle_Empty(t *testing.T) {
	resetStyles()
	out := RenderSectionTitle("")
	if out == "" {
		t.Error("expected separator line for empty title")
	}
}

func TestRenderSubheader_ReturnsNonEmpty(t *testing.T) {
	resetStyles()
	out := RenderSubheader("sub")
	if out == "" {
		t.Error("expected non-empty subheader output")
	}
}

func TestRenderDefinition_Basic(t *testing.T) {
	resetStyles()
	out := RenderDefinition("key", "value", 0, "")
	if !strings.Contains(out, "key") {
		t.Errorf("expected key in definition output, got %q", out)
	}
}

func TestWordWrap_ShortText(t *testing.T) {
	out := wordWrap("hello", 80)
	if len(out) != 1 || out[0] != "hello" {
		t.Errorf("short text should be unchanged: %v", out)
	}
}

func TestWordWrap_ZeroWidth(t *testing.T) {
	out := wordWrap("hello world", 0)
	if len(out) != 1 || out[0] != "hello world" {
		t.Errorf("zero width should return text unchanged: %v", out)
	}
}

func TestWordWrap_BreaksAtWordBoundary(t *testing.T) {
	out := wordWrap("one two three four five", 10)
	if len(out) < 2 {
		t.Errorf("expected multiple lines for long text, got %v", out)
	}
	for _, line := range out {
		if len([]rune(line)) > 10 {
			t.Errorf("line exceeds width: %q", line)
		}
	}
}

func TestWordWrap_NoSpaces(t *testing.T) {
	out := wordWrap("abcdefghij", 5)
	if len(out) < 2 {
		t.Errorf("expected split of word without spaces: %v", out)
	}
}

func TestRenderInfo_HideOnEmpty_AllFiltered_True(t *testing.T) {
	t.Parallel()
	// All items filtered out, hide_on_empty: true → section title and all items absent
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:          "hidden",
				Title:       "Should Be Hidden",
				HideOnEmpty: true,
				Items: []config.InfoItem{
					{Type: "definition", Name: "HIDDEN_KEY_SENTINEL", Value: "HIDDEN_VAL_SENTINEL", When: "{{if false}}yes{{end}}"},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "Should Be Hidden") {
		t.Errorf("expected section title hidden, got:\n%s", out)
	}
	if strings.Contains(out, "HIDDEN_KEY_SENTINEL") || strings.Contains(out, "HIDDEN_VAL_SENTINEL") {
		t.Errorf("expected all items hidden, got:\n%s", out)
	}
}

func TestRenderInfo_HideOnEmpty_AllFiltered_False(t *testing.T) {
	t.Parallel()
	// All items filtered out, hide_on_empty: false (legacy) → section title rendered
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:          "visible",
				Title:       "Should Show Title",
				HideOnEmpty: false,
				Items: []config.InfoItem{
					{Type: "definition", Name: "FILTERED_KEY_SENTINEL", Value: "FILTERED_VAL_SENTINEL", When: "{{if false}}yes{{end}}"},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Should Show Title") {
		t.Errorf("expected section title visible with hide_on_empty=false, got:\n%s", out)
	}
	if strings.Contains(out, "FILTERED_VAL_SENTINEL") {
		t.Errorf("expected filtered item value absent with hide_on_empty=false, got:\n%s", out)
	}
}

func TestRenderInfo_HideOnEmpty_Mixed(t *testing.T) {
	t.Parallel()
	// One section hidden, one visible
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:          "hidden",
				Title:       "Hidden Section",
				HideOnEmpty: true,
				Items: []config.InfoItem{
					{Type: "definition", Name: "hidden_k", Value: "hidden_v", When: "{{if false}}no{{end}}"},
				},
			},
			{
				ID:          "visible",
				Title:       "Visible Section",
				HideOnEmpty: true,
				Items: []config.InfoItem{
					{Type: "definition", Name: "visible_k", Value: "visible_v"},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "Hidden Section") {
		t.Errorf("expected hidden section absent, got:\n%s", out)
	}
	if !strings.Contains(out, "Visible Section") {
		t.Errorf("expected visible section present, got:\n%s", out)
	}
	if !strings.Contains(out, "visible_v") {
		t.Errorf("expected visible item content, got:\n%s", out)
	}
	if strings.Contains(out, "hidden_v") {
		t.Errorf("expected hidden item absent, got:\n%s", out)
	}
}

func TestRenderInfo_Footer_NoSectionsRendered(t *testing.T) {
	t.Parallel()
	// All sections hidden, footer: true → footer suppressed
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:          "hidden",
				Title:       "Will Be Hidden",
				HideOnEmpty: true,
				Items: []config.InfoItem{
					{Type: "definition", Name: "k", Value: "v", When: "{{if false}}no{{end}}"},
				},
			},
		},
		Footer: true,
	}
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Output should be empty (no sections, no footer)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty output when all sections hidden with footer=true, got:\n%s", out)
	}
}

func TestRenderInfo_Footer_SectionRendered(t *testing.T) {
	t.Parallel()
	// At least one section rendered, footer: true → footer rendered
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:    "s1",
				Title: "Section",
				Items: []config.InfoItem{
					{Type: "definition", Name: "k", Value: "v"},
				},
			},
		},
		Footer: true,
	}
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Errorf("expected multiple lines (section + footer), got:\n%s", out)
	}
}

func TestRenderInfo_BareWarningCountsAsContent(t *testing.T) {
	t.Parallel()
	// Section with one filtered definition item + one unfiltered warning (no explicit decorative)
	// Warning's decorative default is false, so it counts as content.
	// → section rendered because warning counts as content
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:          "mixed",
				Title:       "Mixed Section",
				HideOnEmpty: true,
				Items: []config.InfoItem{
					{Type: "definition", Name: "k", Value: "FILTERED_VALUE", When: "{{if false}}no{{end}}"},
					{Type: "warning", Text: "important warning"},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Mixed Section") {
		t.Errorf("expected section title because warning counts as content, got:\n%s", out)
	}
	if !strings.Contains(out, "important warning") {
		t.Errorf("expected warning in output, got:\n%s", out)
	}
	if strings.Contains(out, "FILTERED_VALUE") {
		t.Errorf("expected filtered definition item absent, got:\n%s", out)
	}
}

func TestRenderInfo_DecorativeTrueWarningHidesSection(t *testing.T) {
	t.Parallel()
	// decorative: true on warning + hide_on_empty: true on section, when it's the only survivor
	// → section is fully hidden because warning is decorative and doesn't count as content
	decorativeTrue := true
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:          "decorative-only",
				Title:       "Decorative Only",
				HideOnEmpty: true,
				Items: []config.InfoItem{
					{Type: "warning", Text: "decorative warning", Decorative: &decorativeTrue},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "Decorative Only") {
		t.Errorf("expected section title hidden, got:\n%s", out)
	}
	if strings.Contains(out, "decorative warning") {
		t.Errorf("expected decorative warning hidden, got:\n%s", out)
	}
}

func TestRenderInfo_DecorativeFalseSeparatorCountsAsContent(t *testing.T) {
	t.Parallel()
	// decorative: false on separator → separator counts as content AND section renders.
	// Test: section with a definition AND a non-decorative separator.
	// The definition makes it clear the section rendered.
	decorativeFalse := false
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:          "sep-decorative-false",
				Title:       "With Separator",
				HideOnEmpty: true,
				Items: []config.InfoItem{
					{Type: "definition", Name: "key", Value: "value"},
					{Type: "separator", Decorative: &decorativeFalse},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Section should render because separator counts as content.
	if !strings.Contains(out, "With Separator") {
		t.Errorf("expected section title (separator counted as content), got:\n%s", out)
	}
	if !strings.Contains(out, "value") {
		t.Errorf("expected definition value, got:\n%s", out)
	}
}

func TestRenderInfo_DecorativeTrueSeparatorHidesSection(t *testing.T) {
	t.Parallel()
	// Contrast: decorative: true on separator makes it NOT count as content.
	// Section with only a decorative separator should be hidden.
	decorativeTrue := true
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:          "sep-decorative-true",
				Title:       "Only Decorative Sep",
				HideOnEmpty: true,
				Items: []config.InfoItem{
					{Type: "separator", Decorative: &decorativeTrue},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Section should be hidden (decorative separator doesn't count as content).
	if strings.Contains(out, "Only Decorative Sep") {
		t.Errorf("expected section hidden (decorative separator only), got:\n%s", out)
	}
}

func TestRenderInfo_SubgroupAllItemsFiltered(t *testing.T) {
	t.Parallel()
	// Subgroup with all items filtered by when: AND hide_on_empty default (true)
	// → subgroup absent from output AND parent section does not count it
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:          "with-subgroup",
				Title:       "Section with Subgroup",
				HideOnEmpty: true,
				Items: []config.InfoItem{
					{
						Type:  "subgroup",
						Title: "Filtered Subgroup",
						Items: []config.InfoItem{
							{Type: "definition", Name: "k", Value: "v", When: "{{if false}}no{{end}}"},
						},
					},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Section should be hidden because its only child (subgroup) didn't render.
	if strings.Contains(out, "Section with Subgroup") {
		t.Errorf("expected section hidden when subgroup empty, got:\n%s", out)
	}
	if strings.Contains(out, "Filtered Subgroup") {
		t.Errorf("expected subgroup title absent, got:\n%s", out)
	}
}

func TestRenderInfo_SubgroupHideOnEmptyFalseWithTitle(t *testing.T) {
	t.Parallel()
	// Subgroup with hide_on_empty: false, non-empty title, zero surviving items, decorative default (false)
	// → subgroup renders (title only) AND parent counts it as content
	decorativeFalse := false
	hideOnEmptyFalse := false
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:          "with-subgroup",
				Title:       "Section",
				HideOnEmpty: true,
				Items: []config.InfoItem{
					{
						Type:        "subgroup",
						Title:       "Empty Subgroup",
						HideOnEmpty: &hideOnEmptyFalse,
						Decorative:  &decorativeFalse,
						Items: []config.InfoItem{
							{Type: "definition", Name: "k", Value: "v", When: "{{if false}}no{{end}}"},
						},
					},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Section should render because subgroup (with decorative: false) is counted as content.
	if !strings.Contains(out, "Section") {
		t.Errorf("expected section title, got:\n%s", out)
	}
	// Subgroup title should appear.
	if !strings.Contains(out, "Empty Subgroup") {
		t.Errorf("expected subgroup title, got:\n%s", out)
	}
}

func TestRenderInfo_SubgroupHideOnEmptyFalseDecorativeTrue(t *testing.T) {
	t.Parallel()
	// Same as above but with decorative: true
	// → subgroup still renders (title only), but parent does NOT count it as content
	decorativeTrue := true
	hideOnEmptyFalse := false
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:          "with-subgroup",
				Title:       "Section",
				HideOnEmpty: true,
				Items: []config.InfoItem{
					{
						Type:        "subgroup",
						Title:       "Decorative Empty Subgroup",
						HideOnEmpty: &hideOnEmptyFalse,
						Decorative:  &decorativeTrue,
						Items: []config.InfoItem{
							{Type: "definition", Name: "k", Value: "v", When: "{{if false}}no{{end}}"},
						},
					},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Section should be hidden because decorative subgroup doesn't count as content.
	if strings.Contains(out, "Section") {
		t.Errorf("expected section hidden when decorative subgroup is only content, got:\n%s", out)
	}
	// Subgroup title should be absent.
	if strings.Contains(out, "Decorative Empty Subgroup") {
		t.Errorf("expected subgroup title absent, got:\n%s", out)
	}
}

func TestRenderInfo_SubgroupTitlelessWithAllChildrenFiltered(t *testing.T) {
	t.Parallel()
	// Subgroup with empty title, hide_on_empty: false, all children filtered
	// → subgroup produces empty output → rendered=false → subgroup absent from parent's output
	hideOnEmptyFalse := false
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:          "with-subgroup",
				Title:       "Section",
				HideOnEmpty: true,
				Items: []config.InfoItem{
					{
						Type:        "subgroup",
						Title:       "", // no title
						HideOnEmpty: &hideOnEmptyFalse,
						Items: []config.InfoItem{
							{Type: "definition", Name: "k", Value: "v", When: "{{if false}}no{{end}}"},
						},
					},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Section should be hidden because subgroup (with no title and no content) renders empty.
	if strings.Contains(out, "Section") {
		t.Errorf("expected section hidden when titleless subgroup is empty, got:\n%s", out)
	}
}

func TestRenderInfo_SubgroupTitlelessWithSurvivingContent(t *testing.T) {
	t.Parallel()
	// Subgroup with empty title and surviving content items
	// → subgroup renders (no title, body only) and parent counts it as content
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:          "with-subgroup",
				Title:       "Section",
				HideOnEmpty: true,
				Items: []config.InfoItem{
					{
						Type:  "subgroup",
						Title: "", // no title
						Items: []config.InfoItem{
							{Type: "definition", Name: "key", Value: "val"},
						},
					},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Section should render.
	if !strings.Contains(out, "Section") {
		t.Errorf("expected section title, got:\n%s", out)
	}
	// Definition should appear (from titleless subgroup).
	if !strings.Contains(out, "val") {
		t.Errorf("expected definition value from titleless subgroup, got:\n%s", out)
	}
}

func TestRenderInfo_SubgroupWithContentItem(t *testing.T) {
	t.Parallel()
	// Subgroup with at least one surviving content item
	// → subgroup rendered; parent counts it as content
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:    "with-subgroup",
				Title: "Main",
				Items: []config.InfoItem{
					{
						Type:  "subgroup",
						Title: "Tools",
						Items: []config.InfoItem{
							{Type: "definition", Name: "tool1", Value: "v1"},
							{Type: "definition", Name: "tool2", Value: "v2"},
						},
					},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Main") {
		t.Errorf("expected section title, got:\n%s", out)
	}
	if !strings.Contains(out, "Tools") {
		t.Errorf("expected subgroup title, got:\n%s", out)
	}
	if !strings.Contains(out, "v1") {
		t.Errorf("expected first tool value, got:\n%s", out)
	}
	if !strings.Contains(out, "v2") {
		t.Errorf("expected second tool value, got:\n%s", out)
	}
}

func TestRenderInfo_NestedSubgroup(t *testing.T) {
	t.Parallel()
	// Nested subgroup: inner subgroup empty → outer counts no contribution;
	// inner has content → outer renders; section renders.
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:    "nested",
				Title: "Nested",
				Items: []config.InfoItem{
					{
						Type:  "subgroup",
						Title: "Outer",
						Items: []config.InfoItem{
							{
								Type:  "subgroup",
								Title: "Inner",
								Items: []config.InfoItem{
									{Type: "definition", Name: "k", Value: "v"},
								},
							},
						},
					},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Nested") {
		t.Errorf("expected section title, got:\n%s", out)
	}
	if !strings.Contains(out, "Outer") {
		t.Errorf("expected outer subgroup title, got:\n%s", out)
	}
	if !strings.Contains(out, "Inner") {
		t.Errorf("expected inner subgroup title, got:\n%s", out)
	}
	if !strings.Contains(out, "v") {
		t.Errorf("expected nested definition value, got:\n%s", out)
	}
}

func TestRenderInfo_SubgroupTitleTemplate(t *testing.T) {
	t.Parallel()
	// Subgroup title goes through tpl.Render before styling
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:    "s1",
				Title: "Main",
				Items: []config.InfoItem{
					{
						Type:  "subgroup",
						Title: "Tools: {{.Project.Name}}",
						Items: []config.InfoItem{
							{Type: "definition", Name: "tool", Value: "value"},
						},
					},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
	}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Tools: myapp") {
		t.Errorf("expected subgroup title with template evaluated, got:\n%s", out)
	}
}

func TestRenderInfo_ErrorPropagation_SubgroupTitleTemplate(t *testing.T) {
	t.Parallel()
	// Bad template in subgroup title → error propagates with location
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID: "s1",
				Items: []config.InfoItem{
					{
						Type:  "subgroup",
						Title: "{{.Invalid.Field}}",
						Items: []config.InfoItem{
							{Type: "definition", Name: "k", Value: "v"},
						},
					},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	_, err := RenderInfo(cfg, infoCfg)
	if err == nil {
		t.Error("expected error from bad subgroup title template, got nil")
	}
	if !strings.Contains(err.Error(), "subgroup") {
		t.Errorf("expected error message to mention subgroup, got: %v", err)
	}
}

func TestRenderInfo_ErrorPropagation_NestedItemWhen(t *testing.T) {
	t.Parallel()
	// Bad expression in a nested item's when: (item inside subgroup)
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID: "s1",
				Items: []config.InfoItem{
					{
						Type:  "subgroup",
						Title: "Sub",
						Items: []config.InfoItem{
							{Type: "definition", Name: "k", Value: "v", When: "{{.Invalid.Field}}"},
						},
					},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	_, err := RenderInfo(cfg, infoCfg)
	if err == nil {
		t.Error("expected error from bad nested when: expression, got nil")
	}
	// Error should include path hint.
	errStr := err.Error()
	if !strings.Contains(errStr, "items[0]") && !strings.Contains(errStr, "subgroup") {
		t.Errorf("expected error message to include path, got: %v", err)
	}
}

func TestRenderInfo_ErrorPropagation_NestedItemValue(t *testing.T) {
	t.Parallel()
	// Bad template in a nested item's value (definition inside subgroup)
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID: "s1",
				Items: []config.InfoItem{
					{
						Type:  "subgroup",
						Title: "Sub",
						Items: []config.InfoItem{
							{Type: "definition", Name: "k", Value: "{{.Invalid.Field}}"},
						},
					},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	_, err := RenderInfo(cfg, infoCfg)
	if err == nil {
		t.Error("expected error from bad nested item value template, got nil")
	}
	if !strings.Contains(err.Error(), "definition") {
		t.Errorf("expected error message to mention definition, got: %v", err)
	}
}

func TestRenderInfo_ErrorPropagation_NestedItemText(t *testing.T) {
	t.Parallel()
	// Bad template in a nested item's text (info inside subgroup)
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID: "s1",
				Items: []config.InfoItem{
					{
						Type:  "subgroup",
						Title: "Sub",
						Items: []config.InfoItem{
							{Type: "info", Text: "{{.Invalid.Field}}"},
						},
					},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	_, err := RenderInfo(cfg, infoCfg)
	if err == nil {
		t.Error("expected error from bad nested item text template, got nil")
	}
	if !strings.Contains(err.Error(), "info") {
		t.Errorf("expected error message to mention info, got: %v", err)
	}
}

func TestRenderInfo_HideOnEmpty_SeparatorOnly(t *testing.T) {
	t.Parallel()
	// Section with only a separator item and hide_on_empty: true → section hidden.
	// Separators produce no visible output and must not prevent hide_on_empty from firing.
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:          "sep-only",
				Title:       "Separator Only",
				HideOnEmpty: true,
				Items: []config.InfoItem{
					{Type: "separator"},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	out, err := RenderInfo(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "Separator Only") {
		t.Errorf("expected section hidden (separator-only + hide_on_empty), got:\n%s", out)
	}
}

func TestRenderInfo_WhenEvaluationError_Propagates(t *testing.T) {
	t.Parallel()
	// Section with an item whose when: expression causes an error
	// → error propagates (regression guard)
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID: "error",
				Items: []config.InfoItem{
					{Type: "definition", Name: "k", Value: "v", When: "{{.Invalid.Field}}"},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	_, err := RenderInfo(cfg, infoCfg)
	if err == nil {
		t.Error("expected error from invalid when: expression, got nil")
	}
}
