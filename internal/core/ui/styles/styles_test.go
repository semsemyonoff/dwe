package styles

import (
	"image/color"
	"strings"
	"testing"

	huh "charm.land/huh/v2"
	lipglossv2 "charm.land/lipgloss/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"devbox-cli/internal/core/project/config"
)

// forceTruecolor pins the v1 lipgloss color profile to TrueColor for the
// duration of the test. ANSI render-output assertions need a deterministic
// profile because lipgloss otherwise downgrades to Ascii in a non-TTY harness
// (which strips all escapes).
func forceTruecolor(t *testing.T) {
	t.Helper()
	saved := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(saved) })
}

// snapshotPalette captures the current resolved palette + separator and
// reinstalls it via t.Cleanup. Tests that drive ApplyStyles or
// lipgloss.SetHasDarkBackground must wrap themselves in this helper so they
// do not bleed state into sibling tests in the same package — none of these
// tests use t.Parallel for the same reason.
func snapshotPalette(t *testing.T) {
	t.Helper()
	savedAccent, savedSuccess, savedWarning := resolvedAccent, resolvedSuccess, resolvedWarning
	savedDanger, savedMuted, savedBorder, savedText := resolvedDanger, resolvedMuted, resolvedBorder, resolvedText
	savedSep := DefSep
	savedDark := lipgloss.HasDarkBackground()
	savedTheme := HuhTheme
	t.Cleanup(func() {
		lipgloss.SetHasDarkBackground(savedDark)
		rebuildSemanticStyles(config.StylesColors{
			Accent:  savedAccent,
			Success: savedSuccess,
			Warning: savedWarning,
			Danger:  savedDanger,
			Muted:   savedMuted,
			Border:  savedBorder,
			Text:    savedText,
		})
		DefSep = savedSep
		HuhTheme = savedTheme
	})
}

// resetStyles re-initialises the palette to the built-in defaults for the
// current dark/light mode. Used by older sibling tests in this package that
// pre-date snapshotPalette.
func resetStyles() {
	rebuildSemanticStyles(config.StylesColors{})
	DefSep = "—"
}

func TestApplyStyles_Nil(t *testing.T) {
	snapshotPalette(t)
	ApplyStyles(nil)
	if DefSep != "—" {
		t.Errorf("nil cfg must not change DefSep: got %q", DefSep)
	}
}

func TestApplyStyles_Empty_ResolvesDefaultsForDarkBackground(t *testing.T) {
	snapshotPalette(t)
	lipgloss.SetHasDarkBackground(true)
	ApplyStyles(&config.StylesConfig{})

	cases := []struct {
		name string
		got  func() string
		want string
	}{
		{"Accent", ColorAccent, defaultAccent.Dark},
		{"Success", ColorSuccess, defaultSuccess.Dark},
		{"Warning", ColorWarning, defaultWarning.Dark},
		{"Danger", ColorDanger, defaultDanger.Dark},
		{"Muted", ColorMuted, defaultMuted.Dark},
		{"Border", ColorBorder, defaultBorder.Dark},
		{"Text", ColorText, defaultText.Dark},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.got(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestApplyStyles_Empty_ResolvesDefaultsForLightBackground(t *testing.T) {
	snapshotPalette(t)
	lipgloss.SetHasDarkBackground(false)
	ApplyStyles(&config.StylesConfig{})

	cases := []struct {
		name string
		got  func() string
		want string
	}{
		{"Accent", ColorAccent, defaultAccent.Light},
		{"Success", ColorSuccess, defaultSuccess.Light},
		{"Warning", ColorWarning, defaultWarning.Light},
		{"Danger", ColorDanger, defaultDanger.Light},
		{"Muted", ColorMuted, defaultMuted.Light},
		{"Border", ColorBorder, defaultBorder.Light},
		{"Text", ColorText, defaultText.Light},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.got(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestApplyStyles_UserOverride_AppliesRegardlessOfMode(t *testing.T) {
	snapshotPalette(t)
	cfg := &config.StylesConfig{Colors: config.StylesColors{
		Accent:  "#FF0000",
		Success: "#00FF00",
		Warning: "#FFFF00",
		Danger:  "#FF00FF",
		Muted:   "#888888",
		Border:  "#CCCCCC",
		Text:    "#111111",
	}}

	for _, dark := range []bool{true, false} {
		lipgloss.SetHasDarkBackground(dark)
		ApplyStyles(cfg)
		assertions := []struct {
			name string
			got  func() string
			want string
		}{
			{"Accent", ColorAccent, "#FF0000"},
			{"Success", ColorSuccess, "#00FF00"},
			{"Warning", ColorWarning, "#FFFF00"},
			{"Danger", ColorDanger, "#FF00FF"},
			{"Muted", ColorMuted, "#888888"},
			{"Border", ColorBorder, "#CCCCCC"},
			{"Text", ColorText, "#111111"},
		}
		for _, tc := range assertions {
			if got := tc.got(); got != tc.want {
				t.Errorf("dark=%v %s: got %q want %q", dark, tc.name, got, tc.want)
			}
		}
	}
}

func TestApplyStyles_PartialOverride_DefaultsFillRest(t *testing.T) {
	snapshotPalette(t)
	lipgloss.SetHasDarkBackground(true)
	ApplyStyles(&config.StylesConfig{Colors: config.StylesColors{Accent: "#ABCDEF"}})
	if ColorAccent() != "#ABCDEF" {
		t.Errorf("accent override not applied: %q", ColorAccent())
	}
	if ColorMuted() != defaultMuted.Dark {
		t.Errorf("muted should fall back to dark default: got %q", ColorMuted())
	}
	if ColorSuccess() != defaultSuccess.Dark {
		t.Errorf("success should fall back to dark default: got %q", ColorSuccess())
	}
}

func TestApplyStyles_Separator(t *testing.T) {
	snapshotPalette(t)
	ApplyStyles(&config.StylesConfig{Separator: ":"})
	if DefSep != ":" {
		t.Errorf("expected DefSep to be ':', got %q", DefSep)
	}
}

func TestStyleText_EmptyConfig_NoForegroundAnsi(t *testing.T) {
	snapshotPalette(t)
	forceTruecolor(t)
	ApplyStyles(&config.StylesConfig{}) // text override stays empty
	out := styleText.Render("plain")
	// NoColor{} path must NOT emit a foreground SGR (\x1b[3X... or 38;5; / 38;2;).
	if strings.Contains(out, "\x1b[38;") {
		t.Errorf("styleText with empty text emitted a foreground escape: %q", out)
	}
}

func TestStyleText_UserOverride_EmitsForegroundAnsi(t *testing.T) {
	snapshotPalette(t)
	forceTruecolor(t)
	ApplyStyles(&config.StylesConfig{Colors: config.StylesColors{Text: "#FF8800"}})
	out := styleText.Render("colored")
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("styleText with explicit text= should emit ANSI: %q", out)
	}
	if ColorText() != "#FF8800" {
		t.Errorf("ColorText: got %q, want %q", ColorText(), "#FF8800")
	}
}

func TestApplyStyles_AccessorReturnsConfiguredHex(t *testing.T) {
	snapshotPalette(t)
	ApplyStyles(&config.StylesConfig{Colors: config.StylesColors{
		Accent:  "#010203",
		Success: "#040506",
		Warning: "#070809",
		Danger:  "#0A0B0C",
		Muted:   "#0D0E0F",
		Border:  "#101112",
		Text:    "#131415",
	}})
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"Accent", ColorAccent(), "#010203"},
		{"Success", ColorSuccess(), "#040506"},
		{"Warning", ColorWarning(), "#070809"},
		{"Danger", ColorDanger(), "#0A0B0C"},
		{"Muted", ColorMuted(), "#0D0E0F"},
		{"Border", ColorBorder(), "#101112"},
		{"Text", ColorText(), "#131415"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}

func TestStyleHelpers_NonEmpty(t *testing.T) {
	snapshotPalette(t)
	cases := []struct {
		name string
		fn   func(string) string
	}{
		{"RenderEnabled", RenderEnabled},
		{"RenderPartial", RenderPartial},
		{"RenderStopped", RenderStopped},
		{"StyleKey", StyleKey},
		{"StyleGroup", StyleGroup},
		{"StyleSectionTitle", StyleSectionTitle},
		{"StyleSubheader", StyleSubheader},
		{"StyleMuted", StyleMuted},
		{"StyleInfo", StyleInfo},
		{"StyleFailed", StyleFailed},
		{"StyleWarning", StyleWarning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := tc.fn("test")
			if out == "" {
				t.Errorf("%s returned empty string", tc.name)
			}
		})
	}
}

func TestServiceInactiveStyle_DimmedAndNotBold(t *testing.T) {
	snapshotPalette(t)
	st := styleInactiveService("app")
	if !st.GetFaint() {
		t.Error("inactive service style should be faint")
	}
	if st.GetBold() {
		t.Error("inactive service style should not be bold")
	}
}

func TestServiceOptionStyles_TruecolorForegroundOnly(t *testing.T) {
	snapshotPalette(t)
	ApplyStyles(&config.StylesConfig{Colors: config.StylesColors{Accent: "#112233"}})
	out := StyleServiceOptionName("app", "main")
	if out == "main" {
		t.Fatal("expected ANSI styling")
	}
	if !strings.Contains(out, "\x1b[38;2;17;34;51m") {
		t.Errorf("expected truecolor accent fg, got %q", out)
	}
	if strings.Contains(out, "\x1b[0m") {
		t.Errorf("option styles must not emit full reset, got %q", out)
	}
	if !strings.Contains(out, "\x1b[39m") {
		t.Errorf("option styles should reset foreground only, got %q", out)
	}
}

func TestTermWidth_ReturnsPositive(t *testing.T) {
	w := TermWidth()
	if w <= 0 {
		t.Errorf("expected positive terminal width, got %d", w)
	}
}

// --- huh theme tests (migrated from internal/core/ui/huh_test.go) ---

func TestThemeNonNilByDefault(t *testing.T) {
	th := Theme()
	if th == nil {
		t.Fatal("Theme() returned nil before ApplyStyles")
	}
}

func TestThemeNonNilAfterApplyStylesNil(t *testing.T) {
	original := HuhTheme
	t.Cleanup(func() { HuhTheme = original })

	ApplyStyles(nil)
	th := Theme()
	if th == nil {
		t.Fatal("Theme() returned nil after ApplyStyles(nil)")
	}
}

func TestThemeReturnsNonNilStyles(t *testing.T) {
	original := HuhTheme
	t.Cleanup(func() { HuhTheme = original })

	ApplyStyles(nil)
	th := Theme()

	light := th.Theme(false)
	if light == nil {
		t.Fatal("Theme().Theme(false) returned nil")
	}

	dark := th.Theme(true)
	if dark == nil {
		t.Fatal("Theme().Theme(true) returned nil")
	}
}

func TestDefaultThemeMultiSelectStateStyles(t *testing.T) {
	original := HuhTheme
	t.Cleanup(func() { HuhTheme = original })
	resetStyles()
	HuhTheme = huh.ThemeFunc(func(isDark bool) *huh.Styles {
		s := huh.ThemeBase(isDark)
		ApplyFormGlyphs(s)
		applyMultiSelectStateStyles(s, resolvedSuccess, resolvedMuted)
		return s
	})

	styles := Theme().Theme(false)
	if !styles.Focused.SelectedOption.GetBold() {
		t.Error("selected multi-select options should be bold")
	}
	if styles.Focused.SelectedOption.GetFaint() {
		t.Error("selected multi-select options should not be faint")
	}
	if styles.Focused.UnselectedOption.GetBold() {
		t.Error("unselected multi-select options should not be bold")
	}
	if !styles.Focused.UnselectedOption.GetFaint() {
		t.Error("unselected multi-select options should be faint")
	}
}

func TestApplyStylesPaletteReflected(t *testing.T) {
	original := HuhTheme
	t.Cleanup(func() { HuhTheme = original })

	cfg := &config.StylesConfig{
		Colors: config.StylesColors{
			Accent: "#123456",
		},
	}
	ApplyStyles(cfg)

	styles := Theme().Theme(false)
	got := styles.Focused.Title.GetForeground()
	want := lipglossv2.Color("#123456")
	if got != want {
		t.Errorf("Focused.Title foreground = %v (%T), want %v (%T)", got, got, want, want)
	}
}

func TestApplyStylesGroupTitlePaletteReflected(t *testing.T) {
	original := HuhTheme
	t.Cleanup(func() { HuhTheme = original })

	cfg := &config.StylesConfig{
		Colors: config.StylesColors{
			Accent: "#123456",
		},
	}
	ApplyStyles(cfg)

	styles := Theme().Theme(false)
	got := styles.Group.Title.GetForeground()
	want := lipglossv2.Color("#123456")
	if got != want {
		t.Errorf("Group.Title foreground = %v (%T), want %v (%T)", got, got, want, want)
	}
}

func TestBuildPaletteApplierNilNoOp(t *testing.T) {
	apply := BuildPaletteApplier()
	s := huh.ThemeBase(false)
	// Should not panic.
	apply(s)
}

func TestBuildPaletteApplierEmptyColorsNoOp(t *testing.T) {
	apply := BuildPaletteApplier()
	s := huh.ThemeBase(false)
	// Calling apply with an empty config should not change any foreground colors
	// on fields whose color comes from ThemeBase (they should stay as zero/default).
	apply(s)
	// The test verifies the applier runs without panicking.
}

func TestBuildPaletteApplierAllFields(t *testing.T) {
	c := &config.StylesConfig{Colors: config.StylesColors{
		Accent:  "#012345",
		Success: "#011223",
		Danger:  "#993311",
		Muted:   "#445566",
	}}
	ApplyStyles(c)

	apply := BuildPaletteApplier()
	s := huh.ThemeBase(false)
	apply(s)

	accent := lipglossv2.Color("#012345")
	success := lipglossv2.Color("#011223")
	danger := lipglossv2.Color("#993311")
	muted := lipglossv2.Color("#445566")

	tests := []struct {
		name string
		got  color.Color
		want color.Color
	}{
		{"Focused.Title", s.Focused.Title.GetForeground(), accent},
		{"Group.Title", s.Group.Title.GetForeground(), accent},
		{"Focused.Description", s.Focused.Description.GetForeground(), muted},
		{"Group.Description", s.Group.Description.GetForeground(), muted},
		{"Focused.SelectSelector", s.Focused.SelectSelector.GetForeground(), accent},
		{"Focused.MultiSelectSelector", s.Focused.MultiSelectSelector.GetForeground(), accent},
		{"Focused.Option", s.Focused.Option.GetForeground(), accent},
		{"Blurred.Title", s.Blurred.Title.GetForeground(), muted},
		{"Blurred.Description", s.Blurred.Description.GetForeground(), muted},
		{"Focused.UnselectedOption", s.Focused.UnselectedOption.GetForeground(), muted},
		{"Focused.SelectedOption", s.Focused.SelectedOption.GetForeground(), success},
		{"Focused.SelectedPrefix", s.Focused.SelectedPrefix.GetForeground(), success},
		{"Focused.ErrorIndicator", s.Focused.ErrorIndicator.GetForeground(), danger},
		{"Focused.ErrorMessage", s.Focused.ErrorMessage.GetForeground(), danger},
		{"Focused.NextIndicator", s.Focused.NextIndicator.GetForeground(), accent},
		{"Focused.PrevIndicator", s.Focused.PrevIndicator.GetForeground(), accent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %v (%T), want %v (%T)", tt.got, tt.got, tt.want, tt.want)
			}
		})
	}

	if !s.Focused.SelectedOption.GetBold() || s.Focused.SelectedOption.GetFaint() {
		t.Error("focused selected option should be bold and not faint")
	}
	if s.Focused.UnselectedOption.GetBold() || !s.Focused.UnselectedOption.GetFaint() {
		t.Error("focused unselected option should be faint and not bold")
	}
}
