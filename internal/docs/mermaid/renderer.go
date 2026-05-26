package mermaid

import (
	"context"
	"errors"
)

// Sentinel errors returned by mermaid renderers.
var (
	ErrRenderingDisabled = errors.New("mermaid rendering disabled")
	ErrMmdcNotAvailable  = errors.New("mmdc not available on PATH")
	ErrMmdcRequired      = errors.New("mmdc required but not available")
	ErrMmdcVersionProbe  = errors.New("could not determine mmdc version")
)

// Theme controls the color theme passed to mmdc.
type Theme string

// Theme constants for the two supported rendering modes.
const (
	ThemeDark  Theme = "dark"
	ThemeLight Theme = "light"
)

// Renderer renders mermaid source to PNG bytes.
type Renderer interface {
	// Render returns PNG bytes or an error.
	// Width is part of the cache key so callers vary it per render.
	Render(ctx context.Context, src string, theme Theme, width int) ([]byte, error)
}

// Disabled always returns ErrRenderingDisabled.
type Disabled struct{}

// Render implements Renderer and always returns ErrRenderingDisabled.
func (d Disabled) Render(_ context.Context, _ string, _ Theme, _ int) ([]byte, error) {
	return nil, ErrRenderingDisabled
}

// Chain returns the first non-nil renderer that succeeds, or the last renderer's error.
// If all renderers return a *nil* output (shouldn't happen) or the list is empty,
// returns ErrRenderingDisabled.
func Chain(renderers ...Renderer) Renderer {
	return &chainedRenderer{renderers: renderers}
}

type chainedRenderer struct {
	renderers []Renderer
}

func (c *chainedRenderer) Render(ctx context.Context, src string, theme Theme, width int) ([]byte, error) {
	if len(c.renderers) == 0 {
		return nil, ErrRenderingDisabled
	}

	var lastErr error
	for _, r := range c.renderers {
		if r == nil {
			continue
		}
		png, err := r.Render(ctx, src, theme, width)
		if err != nil {
			lastErr = err
			// Special handling for "not available" errors: try the next renderer
			if errors.Is(err, ErrRenderingDisabled) || errors.Is(err, ErrMmdcNotAvailable) {
				continue
			}
			// Other errors (timeout, syntax, etc.) stop the chain
			return nil, err
		}
		if png != nil {
			return png, nil
		}
	}

	// All renderers exhausted; return the last error or ErrRenderingDisabled
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, ErrRenderingDisabled
}
