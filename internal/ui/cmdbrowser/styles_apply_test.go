package cmdbrowser

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"

	"devbox-cli/internal/config"
	"devbox-cli/internal/ui"
)

// applyPaletteOverride installs a palette where each accessor returns a distinct
// 256-color index, so render-level assertions can search for the exact ANSI
// escape that the configured color emits. Restores defaults via t.Cleanup.
func applyPaletteOverride(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		ui.ApplyStyles(&config.StylesConfig{Colors: config.StylesColors{
			FocusBorder:        "12",
			Description:        "8",
			TreeCount:          "8",
			TreeArrow:          "6",
			FilterMatch:        "12",
			PaginationActive:   "12",
			PaginationInactive: "8",
		}})
	})
	ui.ApplyStyles(&config.StylesConfig{Colors: config.StylesColors{
		FocusBorder:        "203",
		Description:        "245",
		TreeCount:          "240",
		TreeArrow:          "167",
		FilterMatch:        "214",
		PaginationActive:   "210",
		PaginationInactive: "239",
	}})
}

func TestApplyListStyles_PopulatesPaletteFields(t *testing.T) {
	applyPaletteOverride(t)

	l := list.New(nil, &cmdDelegate{}, 80, 20)
	applyListStyles(&l)

	cases := []struct {
		name string
		got  color.Color
		want color.Color
	}{
		{"ActivePaginationDot", l.Styles.ActivePaginationDot.GetForeground(), lipgloss.Color("210")},
		{"InactivePaginationDot", l.Styles.InactivePaginationDot.GetForeground(), lipgloss.Color("239")},
		{"DefaultFilterCharacterMatch", l.Styles.DefaultFilterCharacterMatch.GetForeground(), lipgloss.Color("214")},
		{"NoItems", l.Styles.NoItems.GetForeground(), lipgloss.Color("245")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("foreground: got %v, want %v", tc.got, tc.want)
			}
		})
	}
}

func TestApplyListStyles_RendersConfiguredAnsi(t *testing.T) {
	applyPaletteOverride(t)

	l := list.New(nil, &cmdDelegate{}, 80, 20)
	applyListStyles(&l)

	// Render the active pagination dot and look for ANSI fg=210.
	out := l.Styles.ActivePaginationDot.Render(paginationDotGlyph)
	want := "\x1b[38;5;210m"
	if !strings.Contains(out, want) {
		t.Fatalf("ActivePaginationDot render missing %q ANSI escape; got %q", want, out)
	}
}

func TestApplyItemStyles_PopulatesPaletteFields(t *testing.T) {
	applyPaletteOverride(t)

	s := list.NewDefaultItemStyles(true)
	applyItemStyles(&s)

	cases := []struct {
		name string
		got  color.Color
		want color.Color
	}{
		{"NormalDesc", s.NormalDesc.GetForeground(), lipgloss.Color("245")},
		{"SelectedDesc", s.SelectedDesc.GetForeground(), lipgloss.Color("245")},
		{"DimmedDesc", s.DimmedDesc.GetForeground(), lipgloss.Color("245")},
		{"FilterMatch", s.FilterMatch.GetForeground(), lipgloss.Color("214")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("foreground: got %v, want %v", tc.got, tc.want)
			}
		})
	}
}

func TestApplyHelpStyles_PopulatesPaletteFields(t *testing.T) {
	applyPaletteOverride(t)

	hm := help.New()
	applyHelpStyles(&hm.Styles)

	cases := []struct {
		name string
		got  color.Color
		want color.Color
	}{
		{"ShortKey", hm.Styles.ShortKey.GetForeground(), lipgloss.Color("167")},
		{"FullKey", hm.Styles.FullKey.GetForeground(), lipgloss.Color("167")},
		{"ShortDesc", hm.Styles.ShortDesc.GetForeground(), lipgloss.Color("245")},
		{"FullDesc", hm.Styles.FullDesc.GetForeground(), lipgloss.Color("245")},
		{"ShortSeparator", hm.Styles.ShortSeparator.GetForeground(), lipgloss.Color("245")},
		{"FullSeparator", hm.Styles.FullSeparator.GetForeground(), lipgloss.Color("245")},
		{"Ellipsis", hm.Styles.Ellipsis.GetForeground(), lipgloss.Color("245")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("foreground: got %v, want %v", tc.got, tc.want)
			}
		})
	}
}

func TestApplyHelpStyles_RendersConfiguredAnsi(t *testing.T) {
	applyPaletteOverride(t)

	hm := help.New()
	applyHelpStyles(&hm.Styles)

	binding := key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help"))
	out := hm.ShortHelpView([]key.Binding{binding})

	// Key color should be present.
	wantKey := "\x1b[38;5;167m"
	if !strings.Contains(out, wantKey) {
		t.Errorf("ShortHelpView missing key color %q ANSI escape; got %q", wantKey, out)
	}
	// Desc color should be present.
	wantDesc := "\x1b[38;5;245m"
	if !strings.Contains(out, wantDesc) {
		t.Errorf("ShortHelpView missing desc color %q ANSI escape; got %q", wantDesc, out)
	}
}

func TestApplyViewportStyles_PopulatesPaletteFields(t *testing.T) {
	applyPaletteOverride(t)

	vp := viewport.New(viewport.WithWidth(40), viewport.WithHeight(10))
	applyViewportStyles(&vp)

	if got, want := vp.Style.GetForeground(), lipgloss.Color("245"); got != want {
		t.Errorf("Style foreground: got %v, want %v", got, want)
	}
	if got, want := vp.HighlightStyle.GetForeground(), lipgloss.Color("214"); got != want {
		t.Errorf("HighlightStyle foreground: got %v, want %v", got, want)
	}
}

func TestNewModel_AppliesListAndHelpStyles(t *testing.T) {
	applyPaletteOverride(t)

	items := []Item{{ID: "alpha", Description: "first", Type: "shell"}}
	m := newModel("title", items, DefaultOptions(), 120, 30)

	if got, want := m.list.Styles.ActivePaginationDot.GetForeground(), lipgloss.Color("210"); got != want {
		t.Errorf("list.Styles.ActivePaginationDot: got %v, want %v", got, want)
	}
	if got, want := m.help.Styles.ShortKey.GetForeground(), lipgloss.Color("167"); got != want {
		t.Errorf("help.Styles.ShortKey: got %v, want %v", got, want)
	}

	// Render-level: the help footer must include the configured key ANSI escape.
	footer := m.renderHelpFooter()
	wantKey := "\x1b[38;5;167m"
	if !strings.Contains(footer, wantKey) {
		t.Errorf("renderHelpFooter missing key color %q; got %q", wantKey, footer)
	}
}

func TestNewInspectState_AppliesViewportStyles(t *testing.T) {
	applyPaletteOverride(t)

	st := newInspectState(80, 20, "some content", 0)
	if got, want := st.vp.HighlightStyle.GetForeground(), lipgloss.Color("214"); got != want {
		t.Errorf("HighlightStyle foreground: got %v, want %v", got, want)
	}
}
