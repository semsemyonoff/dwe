package cmdbrowser

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"

	"devbox-cli/internal/config"
	"devbox-cli/internal/ui"
)

func TestPaletteConstructors_PickUpAppliedColors(t *testing.T) {
	// Snapshot current palette to avoid bleeding into other tests in this package.
	t.Cleanup(func() {
		ui.ApplyStyles(&config.StylesConfig{})
		// ApplyStyles with empty fields preserves defaults — restore by re-applying
		// the original hardcoded values explicitly.
		ui.ApplyStyles(&config.StylesConfig{Colors: config.StylesColors{
			FocusBorder:        "12",
			Description:        "8",
			TreeCount:          "8",
			TreeArrow:          "6",
			FilterMatch:        "12",
			PaginationActive:   "12",
			PaginationInactive: "8",
			Label:              "12",
			Enabled:            "2",
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
		Label:              "117",
		Enabled:            "78",
	}})

	cases := []struct {
		name  string
		style lipgloss.Style
		want  color.Color
	}{
		{"FocusBorder", paletteFocusBorder(), lipgloss.Color("203")},
		{"Description", paletteDescription(), lipgloss.Color("245")},
		{"TreeCount", paletteTreeCount(), lipgloss.Color("240")},
		{"TreeArrow", paletteTreeArrow(), lipgloss.Color("167")},
		{"FilterMatch", paletteFilterMatch(), lipgloss.Color("214")},
		{"PaginationActive", palettePaginationActive(), lipgloss.Color("210")},
		{"PaginationInactive", palettePaginationInactive(), lipgloss.Color("239")},
		{"Key", paletteKey(), lipgloss.Color("117")},
		{"Success", paletteSuccess(), lipgloss.Color("78")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.style.GetForeground(); got != tc.want {
				t.Errorf("foreground: got %v, want %v", got, tc.want)
			}
		})
	}
}
