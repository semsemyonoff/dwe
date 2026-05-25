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

// applyPaletteOverride installs a palette where accent and muted are distinct
// 256-color indices, so render-level assertions can search for the exact ANSI
// escape that the configured color emits. Restores defaults via t.Cleanup.
//
// Under the 7-token semantic palette, every cmdbrowser slot resolves to either
// ColorAccent() or ColorMuted(); these are the only two distinguishable
// foregrounds in this surface.
func applyPaletteOverride(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		ui.ApplyStyles(&config.StylesConfig{})
	})
	ui.ApplyStyles(&config.StylesConfig{Colors: config.StylesColors{
		Accent:  "167",
		Muted:   "245",
		Success: "78",
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
		{"ActivePaginationDot", l.Styles.ActivePaginationDot.GetForeground(), lipgloss.Color("167")},
		{"InactivePaginationDot", l.Styles.InactivePaginationDot.GetForeground(), lipgloss.Color("245")},
		{"DefaultFilterCharacterMatch", l.Styles.DefaultFilterCharacterMatch.GetForeground(), lipgloss.Color("167")},
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

	out := l.Styles.ActivePaginationDot.Render(paginationDotGlyph)
	want := "\x1b[38;5;167m"
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
		{"FilterMatch", s.FilterMatch.GetForeground(), lipgloss.Color("167")},
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

	wantKey := "\x1b[38;5;167m"
	if !strings.Contains(out, wantKey) {
		t.Errorf("ShortHelpView missing key color %q ANSI escape; got %q", wantKey, out)
	}
	wantDesc := "\x1b[38;5;245m"
	if !strings.Contains(out, wantDesc) {
		t.Errorf("ShortHelpView missing desc color %q ANSI escape; got %q", wantDesc, out)
	}
}

func TestApplyViewportStyles_PopulatesPaletteFields(t *testing.T) {
	applyPaletteOverride(t)

	vp := viewport.New(viewport.WithWidth(40), viewport.WithHeight(10))
	applyViewportStyles(&vp)

	// vp.Style intentionally has no foreground — see applyViewportStyles
	// docstring. bubbles/viewport wraps every visible line with Style.Render,
	// so any foreground here would bleed onto unstyled inspect-content
	// segments (definition value bodies use lipgloss.NoColor to inherit the
	// terminal default).
	if got := vp.Style.GetForeground(); got != (lipgloss.NoColor{}) {
		t.Errorf("Style foreground: got %v, want NoColor", got)
	}
	if got, want := vp.HighlightStyle.GetForeground(), lipgloss.Color("167"); got != want {
		t.Errorf("HighlightStyle foreground: got %v, want %v", got, want)
	}
}

func TestNewModel_AppliesListAndHelpStyles(t *testing.T) {
	applyPaletteOverride(t)

	items := []Item{{ID: "alpha", Description: "first", Type: "shell"}}
	m := newModel("title", items, DefaultOptions(), 120, 30)

	if got, want := m.list.Styles.ActivePaginationDot.GetForeground(), lipgloss.Color("167"); got != want {
		t.Errorf("list.Styles.ActivePaginationDot: got %v, want %v", got, want)
	}
	if got, want := m.help.Styles.ShortKey.GetForeground(), lipgloss.Color("167"); got != want {
		t.Errorf("help.Styles.ShortKey: got %v, want %v", got, want)
	}

	footer := m.renderHelpFooter(120)
	wantKey := "\x1b[38;5;167m"
	if !strings.Contains(footer, wantKey) {
		t.Errorf("renderHelpFooter missing key color %q; got %q", wantKey, footer)
	}
}

func TestNewInspectState_AppliesViewportStyles(t *testing.T) {
	applyPaletteOverride(t)

	st := newInspectState(80, 20, func(int) string { return "some content" }, 0)
	if got, want := st.vp.HighlightStyle.GetForeground(), lipgloss.Color("167"); got != want {
		t.Errorf("HighlightStyle foreground: got %v, want %v", got, want)
	}
}
