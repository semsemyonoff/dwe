package cmdbrowser

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"

	"devbox-cli/internal/config"
	"devbox-cli/internal/ui"
)

func TestPaletteConstructors_PickUpAppliedColors(t *testing.T) {
	// Snapshot current palette to avoid bleeding into other tests.
	t.Cleanup(func() {
		ui.ApplyStyles(&config.StylesConfig{})
	})

	ui.ApplyStyles(&config.StylesConfig{Colors: config.StylesColors{
		Accent:  "#AABBCC",
		Success: "#11AA22",
		Muted:   "#445566",
	}})

	cases := []struct {
		name  string
		style lipgloss.Style
		want  color.Color
	}{
		{"FocusBorder/Accent", paletteFocusBorder(), lipgloss.Color("#AABBCC")},
		{"Description/Muted", paletteDescription(), lipgloss.Color("#445566")},
		{"TreeCount/Muted", paletteTreeCount(), lipgloss.Color("#445566")},
		{"TreeArrow/Muted", paletteTreeArrow(), lipgloss.Color("#445566")},
		{"FilterMatch/Accent", paletteFilterMatch(), lipgloss.Color("#AABBCC")},
		{"PaginationActive/Accent", palettePaginationActive(), lipgloss.Color("#AABBCC")},
		{"PaginationInactive/Muted", palettePaginationInactive(), lipgloss.Color("#445566")},
		{"Key/Accent", paletteKey(), lipgloss.Color("#AABBCC")},
		{"Success", paletteSuccess(), lipgloss.Color("#11AA22")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.style.GetForeground(); got != tc.want {
				t.Errorf("foreground: got %v, want %v", got, tc.want)
			}
		})
	}
}
