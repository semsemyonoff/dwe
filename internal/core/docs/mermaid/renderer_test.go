package mermaid

import (
	"context"
	"errors"
	"testing"
)

func TestDisabled(t *testing.T) {
	r := Disabled{}
	_, err := r.Render(context.Background(), "graph LR", ThemeDark, 100)
	if !errors.Is(err, ErrRenderingDisabled) {
		t.Fatalf("Disabled.Render should return ErrRenderingDisabled, got %v", err)
	}
}

func TestChain(t *testing.T) {
	tests := []struct {
		name      string
		renderers []Renderer
		expectErr error
		expectPNG bool
	}{
		{
			name:      "empty chain",
			renderers: []Renderer{},
			expectErr: ErrRenderingDisabled,
			expectPNG: false,
		},
		{
			name:      "single disabled",
			renderers: []Renderer{Disabled{}},
			expectErr: ErrRenderingDisabled,
			expectPNG: false,
		},
		{
			name:      "first succeeds",
			renderers: []Renderer{&mockRenderer{png: []byte("PNG")}},
			expectErr: nil,
			expectPNG: true,
		},
		{
			name: "first unavailable, second succeeds",
			renderers: []Renderer{
				&mockRenderer{err: ErrMmdcNotAvailable},
				&mockRenderer{png: []byte("PNG")},
			},
			expectErr: nil,
			expectPNG: true,
		},
		{
			name: "first disabled, second unavailable, third succeeds",
			renderers: []Renderer{
				Disabled{},
				&mockRenderer{err: ErrMmdcNotAvailable},
				&mockRenderer{png: []byte("PNG")},
			},
			expectErr: nil,
			expectPNG: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chain := Chain(tt.renderers...)
			png, err := chain.Render(context.Background(), "graph LR", ThemeDark, 100)

			if !errors.Is(err, tt.expectErr) {
				t.Errorf("expected error %v, got %v", tt.expectErr, err)
			}

			if (png != nil) != tt.expectPNG {
				t.Errorf("expected PNG=%v, got %v", tt.expectPNG, png != nil)
			}
		})
	}
}
