package docstui

import (
	"context"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/docs"
	"github.com/semsemyonoff/dwe/internal/core/docs/mermaid"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

func TestNewModel(t *testing.T) {
	fsys := &testFS{
		files: map[string]string{
			"test.md": "test content",
		},
	}

	roots := []docs.DocRoot{
		{
			Name: "dwe",
			FS:   fsys,
		},
	}

	translator := i18n.NopTranslator{}
	// Use a no-op renderer for now
	renderer := &testRenderer{}

	m, err := NewModel(context.Background(), roots, "en", translator, renderer, 120, 30, "", "DWE · Documentation", "auto")
	if err != nil {
		t.Fatalf("NewModel failed: %v", err)
	}

	if m.Tree == nil {
		t.Error("Model.Tree should not be nil")
	}

	if m.Viewport == nil {
		t.Error("Model.Viewport should not be nil")
	}

	if m.StatusBar == nil {
		t.Error("Model.StatusBar should not be nil")
	}

}

func TestTranslationBanner(t *testing.T) {
	tests := []struct {
		name       string
		rootName   string
		locale     string
		sourceLang string
		stale      bool
		want       string // "" = no banner, "missing", or "outdated"
	}{
		{"dwe missing translation", "dwe", "ru", "en", false, "missing"},
		{"dwe outdated translation", "dwe", "ru", "ru", true, "outdated"},
		{"dwe exact translation", "dwe", "ru", "ru", false, ""},
		{"dwe english", "dwe", "en", "en", false, ""},
		{"project missing translation suppressed", "project", "ru", "en", false, ""},
		{"project outdated suppressed", "project", "ru", "ru", true, ""},
		{"project exact translation", "project", "ru", "ru", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := translationBanner(tt.rootName, tt.locale, tt.sourceLang, tt.stale)
			switch tt.want {
			case "":
				if got != "" {
					t.Errorf("translationBanner = %q, want no banner", got)
				}
			case "missing":
				if !strings.Contains(got, "Translation not available") {
					t.Errorf("translationBanner = %q, want the missing-translation banner", got)
				}
			case "outdated":
				if !strings.Contains(got, "outdated") {
					t.Errorf("translationBanner = %q, want the outdated-translation banner", got)
				}
			}
		})
	}
}

type testRenderer struct{}

func (tr *testRenderer) Render(ctx context.Context, src string, theme mermaid.Theme, width int) ([]byte, error) {
	return []byte("mermaid output"), nil
}

// Focus switching is owned by the tui.Frame and delivered to the plugin via
// tui.FocusChangedMsg; the live routing is covered by
// TestBrowser_FocusChangedMsgUpdatesActive and
// TestBrowser_FocusChangedMsgSwitchesNavRouting in plugin_test.go. The old
// assignment-only Model.FocusZone tautology was removed.
