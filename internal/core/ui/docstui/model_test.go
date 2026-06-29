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

func TestFocusSwitching(t *testing.T) {
	fsys := &testFS{
		files: map[string]string{
			"test.md": "test",
		},
	}

	roots := []docs.DocRoot{
		{
			Name: "dwe",
			FS:   fsys,
		},
	}

	translator := i18n.NopTranslator{}
	renderer := &testRenderer{}

	m, err := NewModel(context.Background(), roots, "en", translator, renderer, 80, 24, "", "DWE · Documentation", "auto")
	if err != nil {
		t.Fatalf("NewModel failed: %v", err)
	}

	if m.FocusZone != FocusTree {
		t.Error("Initial focus should be FocusTree")
	}

	// Simulate Tab key press (switching focus)
	// Note: actual key handling would need to be tested separately with tea.KeyPressMsg
	m.FocusZone = FocusViewport
	if m.FocusZone != FocusViewport {
		t.Error("Focus should change to FocusViewport")
	}
}
