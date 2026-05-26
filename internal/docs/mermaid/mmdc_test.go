package mermaid

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMmdcRenderer(t *testing.T) {
	// Use the fake-mmdc.sh script for testing.
	testdataDir := filepath.Join("testdata")
	fakeMMDC := filepath.Join(testdataDir, "fake-mmdc.sh")

	// Make sure fake-mmdc.sh exists and is executable.
	if _, err := os.Stat(fakeMMDC); err != nil {
		t.Skipf("fake-mmdc.sh not found at %s: %v", fakeMMDC, err)
	}
	os.Chmod(fakeMMDC, 0o755)

	renderer := NewMmdc(fakeMMDC, false)

	ctx := context.Background()
	png, err := renderer.Render(ctx, "graph LR\n  A --> B", ThemeDark, 100)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	// Verify we got PNG bytes (should be a valid PNG header).
	if len(png) < 8 {
		t.Errorf("PNG too short: %d bytes", len(png))
	}
	if !isPNGSignature(png) {
		t.Errorf("output is not a valid PNG")
	}
}

func TestMmdcVersionProbe(t *testing.T) {
	testdataDir := filepath.Join("testdata")
	fakeMMDC := filepath.Join(testdataDir, "fake-mmdc.sh")

	if _, err := os.Stat(fakeMMDC); err != nil {
		t.Skipf("fake-mmdc.sh not found: %v", err)
	}
	os.Chmod(fakeMMDC, 0o755)

	version := probeMmdcVersion(fakeMMDC)
	if version == "" {
		t.Errorf("version should not be empty")
	}
	if version == "unknown" {
		t.Logf("version probe returned 'unknown' (acceptable)")
	}
}

func TestMmdcNotFound(t *testing.T) {
	renderer := NewMmdc("/nonexistent/mmdc", false)

	ctx := context.Background()
	_, err := renderer.Render(ctx, "graph LR", ThemeDark, 100)
	if !errors.Is(err, ErrMmdcNotAvailable) {
		t.Errorf("expected ErrMmdcNotAvailable, got %v", err)
	}
}

func TestMmdcNotFoundStrict(t *testing.T) {
	renderer := NewMmdc("/nonexistent/mmdc", true)

	ctx := context.Background()
	_, err := renderer.Render(ctx, "graph LR", ThemeDark, 100)
	if !errors.Is(err, ErrMmdcRequired) {
		t.Errorf("expected ErrMmdcRequired, got %v", err)
	}
}

func isPNGSignature(data []byte) bool {
	if len(data) < 8 {
		return false
	}
	// PNG signature: 89 50 4E 47 0D 0A 1A 0A
	return data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 &&
		data[4] == 0x0D && data[5] == 0x0A && data[6] == 0x1A && data[7] == 0x0A
}
