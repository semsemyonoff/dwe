package render

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestLongestUnbreakableToken(t *testing.T) {
	tests := []struct {
		name string
		s    string
		wrap func(string, int) string
		want int
	}{
		{
			name: "nil wrap returns full width",
			s:    "unbreakable whole cell",
			wrap: nil,
			want: lipgloss.Width("unbreakable whole cell"),
		},
		{
			name: "empty string",
			s:    "",
			wrap: wrapText,
			want: 0,
		},
		{
			// Non-URL words hard-split via splitDisplayWidth same as paths, so
			// plain ASCII prose floors down to a single column of width 1.
			name: "prose without a long token floors to a single character",
			s:    "a short message",
			wrap: wrapText,
			want: 1,
		},
		{
			name: "prose with a long URL is pinned by the URL width",
			s:    "see https://github.com/hadolint/hadolint/wiki/DL3008 for details",
			wrap: wrapText,
			want: lipgloss.Width("https://github.com/hadolint/hadolint/wiki/DL3008"),
		},
		{
			// wrapPath hard-splits every segment via splitDisplayWidth — there is
			// no unsplittable token, so an ASCII path also floors to width 1.
			name: "path input via wrapPath floors to a single character",
			s:    "services/catalog/verylongsegmentnamethatcannotbesplit/Dockerfile",
			wrap: wrapPath,
			want: 1,
		},
		{
			// A full-width rune cannot be split below its own display width, so
			// the floor tracks rune width, not always 1.
			name: "wide runes floor at their own display width, not 1",
			s:    "日本語",
			wrap: wrapText,
			want: 2,
		},
		{
			name: "input already containing newlines floors like single-line prose",
			s:    "first line\nsecond much longer line here",
			wrap: wrapText,
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := longestUnbreakableToken(tt.s, tt.wrap)
			if got != tt.want {
				t.Errorf("longestUnbreakableToken(%q) = %d, want %d", tt.s, got, tt.want)
			}
		})
	}
}
