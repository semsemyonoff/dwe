package docstui

import (
	"context"
	"errors"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/docs"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

func makeTestOpts() Options {
	return Options{
		Roots:      []docs.DocRoot{{Name: "dwe", FS: &testFS{files: map[string]string{"a.md": "# A\n"}}}},
		Locale:     "en",
		Translator: i18n.NopTranslator{},
		Renderer:   &testRenderer{},
		Title:      "Test",
	}
}

func TestRunMapsErrTooNarrow(t *testing.T) {
	// Swap the runDocsTUI seam to return tui.ErrTooNarrow.
	orig := runDocsTUI
	t.Cleanup(func() { runDocsTUI = orig })
	runDocsTUI = func(p tui.Plugin, opts tui.RunOptions) (any, error) {
		return nil, tui.ErrTooNarrow
	}

	err := Run(context.Background(), makeTestOpts())
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if errors.Is(err, tui.ErrTooNarrow) {
		t.Error("ErrTooNarrow should have been replaced with a clean message, not returned directly")
	}
	if errors.Is(err, tui.ErrNotTTY) {
		t.Error("got ErrNotTTY, expected clean message for ErrTooNarrow")
	}
}

func TestRunPassesThroughErrNotTTY(t *testing.T) {
	orig := runDocsTUI
	t.Cleanup(func() { runDocsTUI = orig })
	runDocsTUI = func(p tui.Plugin, opts tui.RunOptions) (any, error) {
		return nil, tui.ErrNotTTY
	}

	err := Run(context.Background(), makeTestOpts())
	if !errors.Is(err, tui.ErrNotTTY) {
		t.Errorf("expected ErrNotTTY to be passed through, got %v", err)
	}
}

func TestRunReturnsNilOnCleanExit(t *testing.T) {
	orig := runDocsTUI
	t.Cleanup(func() { runDocsTUI = orig })
	runDocsTUI = func(p tui.Plugin, opts tui.RunOptions) (any, error) {
		return nil, nil
	}

	if err := Run(context.Background(), makeTestOpts()); err != nil {
		t.Errorf("expected nil on clean exit, got %v", err)
	}
}

func TestRunMmdcNoticeSetOnModel(t *testing.T) {
	const notice = "mmdc is not installed, so Mermaid diagrams cannot render."
	opts := makeTestOpts()
	opts.MmdcMissingNotice = notice

	var gotNotice string
	orig := runDocsTUI
	t.Cleanup(func() { runDocsTUI = orig })
	runDocsTUI = func(p tui.Plugin, opts tui.RunOptions) (any, error) {
		b, ok := p.(*browser)
		if !ok {
			return nil, nil
		}
		gotNotice = b.MmdcMissingNotice
		return nil, nil
	}

	_ = Run(context.Background(), opts)
	if gotNotice != notice {
		t.Errorf("MmdcMissingNotice = %q, want %q", gotNotice, notice)
	}
}

func TestRunMermaidThemeSeam(t *testing.T) {
	const theme = "dark"
	opts := makeTestOpts()
	opts.MermaidTheme = "auto"

	orig := mermaidThemeResolverFn
	t.Cleanup(func() { mermaidThemeResolverFn = orig })
	mermaidThemeResolverFn = func(_ string) string { return theme }

	var gotTheme string
	origRun := runDocsTUI
	t.Cleanup(func() { runDocsTUI = origRun })
	runDocsTUI = func(p tui.Plugin, opts tui.RunOptions) (any, error) {
		b, ok := p.(*browser)
		if !ok {
			return nil, nil
		}
		gotTheme = b.Theme
		return nil, nil
	}

	_ = Run(context.Background(), opts)
	if gotTheme != theme {
		t.Errorf("Theme = %q, want %q", gotTheme, theme)
	}
}
