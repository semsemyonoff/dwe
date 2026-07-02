package docstui

import (
	"context"
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

type testRenderer struct{}

func (tr *testRenderer) Render(ctx context.Context, src string, theme mermaid.Theme, width int) ([]byte, error) {
	return []byte("mermaid output"), nil
}

// Focus switching is owned by the tui.Frame and delivered to the plugin via
// tui.FocusChangedMsg; the live routing is covered by
// TestBrowser_FocusChangedMsgUpdatesActive and
// TestBrowser_FocusChangedMsgSwitchesNavRouting in plugin_test.go. The old
// assignment-only Model.FocusZone tautology was removed.
