package render

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/semsemyonoff/devbox/internal/core/project/config"
)

func makeInfoConfig(sections []config.InfoSection) *config.InfoConfig {
	return &config.InfoConfig{Sections: sections}
}

func TestRenderInfo_SectionTitle(t *testing.T) {
	infoCfg := makeInfoConfig([]config.InfoSection{
		{ID: "s1", Title: "Project Info", Items: nil},
	})
	cfg := &config.DevboxConfig{}
	out, err := Info(cfg, infoCfg)
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
	out, err := Info(cfg, infoCfg)
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
	out, err := Info(cfg, infoCfg)
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
	out, err := Info(cfg, infoCfg)
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
	out, err := Info(cfg, infoCfg)
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
	out, err := Info(cfg, infoCfg)
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
	out, err := Info(cfg, infoCfg)
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
	out, err := Info(cfg, infoCfg)
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
	out, err := Info(cfg, infoCfg)
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
	out, err := Info(cfg, infoCfg)
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
	_, err := Info(cfg, infoCfg)
	if err == nil {
		t.Error("expected error from invalid template, got nil")
	}
}

func TestRenderSectionTitle_NonEmpty(t *testing.T) {
	resetStyles()
	out := SectionTitle("My Section")
	if out == "" {
		t.Error("expected non-empty output for non-empty title")
	}
}

func TestRenderSectionTitle_Empty(t *testing.T) {
	resetStyles()
	out := SectionTitle("")
	if out == "" {
		t.Error("expected separator line for empty title")
	}
}

func TestRenderSubheader_ReturnsNonEmpty(t *testing.T) {
	resetStyles()
	out := Subheader("sub")
	if out == "" {
		t.Error("expected non-empty subheader output")
	}
}

func TestRenderDefinition_Basic(t *testing.T) {
	resetStyles()
	out := Definition("key", "value", 0, "")
	if !strings.Contains(out, "key") {
		t.Errorf("expected key in definition output, got %q", out)
	}
}

// TestRenderDefinitionAt_WrapsToExplicitWidth guards the inspect viewport
// pathway: a value rendered for a sub-region (here 60 cells) must word-wrap
// to that width regardless of the actual terminal width. Without this the
// inspect viewport clipped long values on the right edge.
func TestRenderDefinitionAt_WrapsToExplicitWidth(t *testing.T) {
	resetStyles()
	long := "Restore an OpenSearch snapshot archive: unpack into a temp dir, into the container's restore-repo location, register the repo, resolve the requested indices from the manifest, DROP the matching live indices, restore via the Snapshot Restore API."
	out := DefinitionAt("description", long, 2, "", 60)
	maxW := 0
	for line := range strings.SplitSeq(stripANSI(out), "\n") {
		if w := utf8.RuneCountInString(line); w > maxW {
			maxW = w
		}
	}
	if maxW > 60 {
		t.Errorf("max line width %d exceeds requested 60", maxW)
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
	out, err := Info(cfg, infoCfg)
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
	out, err := Info(cfg, infoCfg)
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
	out, err := Info(cfg, infoCfg)
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
	out, err := Info(cfg, infoCfg)
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
	out, err := Info(cfg, infoCfg)
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
	out, err := Info(cfg, infoCfg)
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
	out, err := Info(cfg, infoCfg)
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
	// decorative: false on separator → separator alone keeps section visible.
	// Only item is the separator; if decorative=false is not respected, the section would be hidden.
	decorativeFalse := false
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:          "sep-decorative-false",
				Title:       "Sep Only Section",
				HideOnEmpty: true,
				Items: []config.InfoItem{
					{Type: "separator", Decorative: &decorativeFalse},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	out, err := Info(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Section should render because the separator (decorative=false) counts as content.
	if !strings.Contains(out, "Sep Only Section") {
		t.Errorf("expected section title rendered (separator with decorative=false counts as content), got:\n%s", out)
	}
}

func TestRenderInfo_DecorativeFalseSeparatorTitleless(t *testing.T) {
	t.Parallel()
	// Titleless section with hide_on_empty=true and a single non-decorative separator.
	// The separator renders to "" but decorative=false means it counts as content,
	// so the section must report rendered=true even though the output string is blank.
	decorativeFalse := false
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:          "titleless-sep",
				Title:       "",
				HideOnEmpty: true,
				Items: []config.InfoItem{
					{Type: "separator", Decorative: &decorativeFalse},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	_, err := Info(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The section has real content (non-decorative separator), so the footer
	// should appear (footer presence signals at least one section rendered).
	// Re-run with Footer=true to detect whether rendered=true propagated.
	infoCfg.Footer = true
	out, err := Info(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Footer is only added when sb.Len() > 0, which requires at least one rendered section.
	if out == "" {
		t.Errorf("expected non-empty output (non-decorative separator must keep section visible), got empty string")
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
	out, err := Info(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Section should be hidden (decorative separator doesn't count as content).
	if strings.Contains(out, "Only Decorative Sep") {
		t.Errorf("expected section hidden (decorative separator only), got:\n%s", out)
	}
}

func TestRenderInfo_HideOnEmpty_NilItems(t *testing.T) {
	t.Parallel()
	// Section with HideOnEmpty=true and no items (nil) — should be suppressed entirely.
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{ID: "empty", Title: "Empty Section", HideOnEmpty: true, Items: nil},
		},
	}
	cfg := &config.DevboxConfig{}
	out, err := Info(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "Empty Section") {
		t.Errorf("expected empty section hidden with hide_on_empty=true, got:\n%s", out)
	}
}

func TestRenderInfo_SubgroupWhenFalse(t *testing.T) {
	t.Parallel()
	// when: on the subgroup item itself evaluates to false — subgroup absent from output.
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:    "s1",
				Title: "Parent Section",
				Items: []config.InfoItem{
					{
						Type:  "subgroup",
						Title: "Filtered Subgroup",
						When:  "{{if false}}yes{{end}}",
						Items: []config.InfoItem{
							{Type: "definition", Name: "k", Value: "v"},
						},
					},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	out, err := Info(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "Filtered Subgroup") {
		t.Errorf("expected subgroup hidden by when:false, got:\n%s", out)
	}
}

func TestRenderInfo_SubgroupWhenTrue(t *testing.T) {
	t.Parallel()
	// when: on the subgroup item itself evaluates to true — subgroup renders.
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:    "s1",
				Title: "Parent Section",
				Items: []config.InfoItem{
					{
						Type:  "subgroup",
						Title: "Visible Subgroup",
						When:  "{{if true}}yes{{end}}",
						Items: []config.InfoItem{
							{Type: "definition", Name: "k", Value: "v"},
						},
					},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	out, err := Info(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Visible Subgroup") {
		t.Errorf("expected subgroup rendered by when:true, got:\n%s", out)
	}
}

func TestRenderInfo_SubgroupWhenInvalidExpr(t *testing.T) {
	t.Parallel()
	// Invalid when: expression on subgroup item — error propagated.
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID: "s1",
				Items: []config.InfoItem{
					{
						Type:  "subgroup",
						Title: "Bad Subgroup",
						When:  "{{.Invalid.Field}}",
						Items: []config.InfoItem{
							{Type: "definition", Name: "k", Value: "v"},
						},
					},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{}
	_, err := Info(cfg, infoCfg)
	if err == nil {
		t.Error("expected error from invalid when: on subgroup, got nil")
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
	out, err := Info(cfg, infoCfg)
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
	out, err := Info(cfg, infoCfg)
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
	out, err := Info(cfg, infoCfg)
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
	out, err := Info(cfg, infoCfg)
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
	out, err := Info(cfg, infoCfg)
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
	out, err := Info(cfg, infoCfg)
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
	out, err := Info(cfg, infoCfg)
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
	out, err := Info(cfg, infoCfg)
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
	_, err := Info(cfg, infoCfg)
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
	_, err := Info(cfg, infoCfg)
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
	_, err := Info(cfg, infoCfg)
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
	_, err := Info(cfg, infoCfg)
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
	out, err := Info(cfg, infoCfg)
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
	_, err := Info(cfg, infoCfg)
	if err == nil {
		t.Error("expected error from invalid when: expression, got nil")
	}
}

// TestRenderInfo_AutoURLs_Integration tests that auto-urls items render through the dispatch.
func TestRenderInfo_AutoURLs_Integration(t *testing.T) {
	t.Parallel()
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:    "urls",
				Title: "URLs",
				Items: []config.InfoItem{
					{
						Type: "auto-urls",
						SourceAutoURLsSpec: &config.AutoURLsSpec{
							Include: []string{"app", "tool"},
						},
					},
				},
			},
		},
	}
	// Create a simple app service with hosts and ports
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"myapp": {
				Type:    "app",
				Enabled: true,
				Icon:    "📦",
				Hosts: map[string]string{
					"web": "myapp.local",
				},
				Ports: map[string]int{
					"http": 8080,
				},
				Info: config.ServiceInfoBlock{
					Title:       "My App",
					PrimaryHost: "web",
					PrimaryPort: "http",
				},
			},
		},
		Runtime: config.RuntimeConfig{UseHTTPS: false},
	}

	out, err := Info(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "URLs") {
		t.Errorf("expected URLs section title, got:\n%s", out)
	}
	if !strings.Contains(out, "localhost:8080") {
		t.Errorf("expected direct URL in output, got:\n%s", out)
	}
}

// TestRenderInfo_AutoHosts_Integration tests that auto-hosts items render through the dispatch.
func TestRenderInfo_AutoHosts_Integration(t *testing.T) {
	t.Parallel()
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:    "hosts",
				Title: "Hosts",
				Items: []config.InfoItem{
					{
						Type: "auto-hosts",
						SourceAutoHostsSpec: &config.AutoHostsSpec{
							Include: []string{"app", "tool"},
							IP:      "127.0.0.1",
						},
					},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"myapp": {
				Type:    "app",
				Enabled: true,
				Hosts: map[string]string{
					"web": "myapp.local",
				},
			},
		},
	}

	out, err := Info(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Hosts") {
		t.Errorf("expected Hosts section title, got:\n%s", out)
	}
	if !strings.Contains(out, "myapp.local") {
		t.Errorf("expected hostname in output, got:\n%s", out)
	}
}

// TestRenderInfo_AutoURLs_When_Hidden tests that when: condition hides an auto-urls item.
func TestRenderInfo_AutoURLs_When_Hidden(t *testing.T) {
	t.Parallel()
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:    "urls",
				Title: "URLs",
				Items: []config.InfoItem{
					{
						Type:               "auto-urls",
						When:               "{{if false}}show{{end}}",
						SourceAutoURLsSpec: &config.AutoURLsSpec{},
					},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"myapp": {
				Type:  "app",
				Hosts: map[string]string{"web": "myapp.local"},
			},
		},
	}

	out, err := Info(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The item is hidden by when: condition, so it won't appear
	if strings.Contains(out, "myapp.local") {
		t.Errorf("expected auto-urls hidden by when: condition, got:\n%s", out)
	}
}

// TestRenderBlock_HideOnEmpty_AutoBlock tests that hide_on_empty: true collapses when auto-block returns empty.
func TestRenderBlock_HideOnEmpty_AutoBlock(t *testing.T) {
	t.Parallel()
	// An auto-urls block with all services hidden returns "".
	// With hide_on_empty: true and only an auto-block (decorative=true), the section should be hidden.
	decorativeTrue := true
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:          "urls",
				Title:       "URLs (should collapse)",
				HideOnEmpty: true,
				Items: []config.InfoItem{
					{
						Type:               "auto-urls",
						Decorative:         &decorativeTrue,
						SourceAutoURLsSpec: &config.AutoURLsSpec{Hide: []string{"app", "tool"}},
					},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"app1": {Type: "app", Hosts: map[string]string{"web": "app1.local"}},
		},
	}

	out, err := Info(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Section title should be hidden since auto-urls returns "" and the auto-block is decorative
	if strings.Contains(out, "URLs (should collapse)") {
		t.Errorf("expected section title hidden when only content is decorative empty auto-block, got:\n%s", out)
	}
}

// TestRenderBlock_AutoBlock_EmptyServices tests that renderers handle empty services map gracefully.
func TestRenderBlock_AutoBlock_EmptyServices(t *testing.T) {
	t.Parallel()
	infoCfg := &config.InfoConfig{
		Sections: []config.InfoSection{
			{
				ID:    "urls",
				Title: "URLs",
				Items: []config.InfoItem{
					{
						Type:               "auto-urls",
						SourceAutoURLsSpec: &config.AutoURLsSpec{},
					},
				},
			},
		},
	}
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{}, // empty
	}

	out, err := Info(cfg, infoCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should not panic; the renderer returns "" for empty services
	if strings.Contains(out, "myapp") || strings.Contains(out, "localhost") {
		t.Errorf("expected no service output with empty services, got:\n%s", out)
	}
}

func TestRenderInfo_DefaultConfig(t *testing.T) {
	t.Parallel()
	// Default config has hide_on_empty: true on both sections.
	// With no services, auto-blocks render "" → sections collapse.
	infoCfg := config.DefaultInfoConfig()
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{},
	}

	out, err := Info(cfg, infoCfg)
	if err != nil {
		t.Fatalf("Info with default config returned error: %v", err)
	}

	// With no services, both sections collapse — output should be empty (or just whitespace).
	if strings.Contains(out, "URLs") {
		t.Errorf("URLs section should be hidden with no services, got:\n%s", out)
	}
	if strings.Contains(out, "Hosts") {
		t.Errorf("Hosts section should be hidden with no services, got:\n%s", out)
	}
	if strings.Contains(out, "Please, add these to your /etc/hosts:") {
		t.Errorf("warning text should be hidden with no services, got:\n%s", out)
	}
}

func TestRenderInfo_DefaultConfig_WithServices(t *testing.T) {
	t.Parallel()
	// With a service that has info.title and hosts, sections should render.
	infoCfg := config.DefaultInfoConfig()
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"web": {
				Type:    config.ServiceTypeApp,
				Enabled: true,
				Hosts:   map[string]string{"http": "web.local"},
				Ports:   map[string]int{"http": 8080},
				Info:    config.ServiceInfoBlock{Title: "Web"},
			},
		},
	}

	out, err := Info(cfg, infoCfg)
	if err != nil {
		t.Fatalf("Info with service returned error: %v", err)
	}

	if !strings.Contains(out, "URLs") {
		t.Errorf("expected URLs section header, got:\n%s", out)
	}
	if !strings.Contains(out, "Hosts") {
		t.Errorf("expected Hosts section header, got:\n%s", out)
	}
	if !strings.Contains(out, "Please, add these to your /etc/hosts:") {
		t.Errorf("expected warning text in Hosts section, got:\n%s", out)
	}
}
