package ui

import (
	"strings"
	"testing"
)

func TestRenderBrandHeader_Minimal(t *testing.T) {
	out := RenderBrandHeader(BrandHeader{Project: "devbox-myapp", Version: "v1.2.3"})
	if !strings.Contains(out, "Devbox") {
		t.Errorf("expected 'Devbox' in brand header, got:\n%s", out)
	}
	if !strings.Contains(out, "devbox-myapp") {
		t.Errorf("expected project in brand header, got:\n%s", out)
	}
	if !strings.Contains(out, "v1.2.3") {
		t.Errorf("expected version in brand header, got:\n%s", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("expected trailing newline, got %q", out)
	}
}

func TestRenderBrandHeader_AlwaysEmitsIdentityLine(t *testing.T) {
	// Empty Lines / empty Tagline must NOT suppress the identity line.
	out := RenderBrandHeader(BrandHeader{Project: "p", Version: "v"})
	if !strings.Contains(out, "Devbox") || !strings.Contains(out, "p") || !strings.Contains(out, "v") {
		t.Errorf("identity line missing when only project+version provided:\n%s", out)
	}
}

func TestRenderBrandHeader_WithTagline(t *testing.T) {
	out := RenderBrandHeader(BrandHeader{Project: "p", Version: "v", Tagline: "fast docker dev"})
	if !strings.Contains(out, "fast docker dev") {
		t.Errorf("expected tagline in brand header, got:\n%s", out)
	}
}

func TestRenderBrandHeader_NoTagline(t *testing.T) {
	out := RenderBrandHeader(BrandHeader{Project: "p", Version: "v"})
	// Without a tagline the body should be a single line + trailing newline.
	if n := strings.Count(strings.TrimRight(out, "\n"), "\n"); n != 0 {
		t.Errorf("expected single identity line without tagline, got %d extra newlines:\n%s", n, out)
	}
}

func TestRenderBrandHeader_WithASCIILines(t *testing.T) {
	out := RenderBrandHeader(BrandHeader{
		Project: "p",
		Version: "v",
		Lines:   []string{"Hi"},
		Font:    "standard",
	})
	// ASCII art for "Hi" in standard figlet font contains a recognisable 'H'
	// glyph; assert the rendered string is meaningfully longer than the bare
	// identity line.
	identity := RenderBrandHeader(BrandHeader{Project: "p", Version: "v"})
	if len(out) <= len(identity) {
		t.Errorf("expected ASCII block to extend output past identity line:\nidentity=%q\nfull=%q", identity, out)
	}
}

func TestRenderBrandHeader_AllSections(t *testing.T) {
	out := RenderBrandHeader(BrandHeader{
		Project: "demo",
		Version: "v9",
		Tagline: "dev env",
		Lines:   []string{"X"},
		Font:    "standard",
	})
	if !strings.Contains(out, "demo") || !strings.Contains(out, "v9") || !strings.Contains(out, "dev env") {
		t.Errorf("expected project, version and tagline in output:\n%s", out)
	}
}

func TestRenderBrandHeader_HasLogoMark(t *testing.T) {
	out := RenderBrandHeader(BrandHeader{Project: "p", Version: "v"})
	if !strings.Contains(out, "{") || !strings.Contains(out, "▪") || !strings.Contains(out, "}") {
		t.Errorf("expected logomark '{▪}' in brand header, got:\n%s", out)
	}
}

func TestRenderBrandHeader_EmptyProjectAndVersion(t *testing.T) {
	// Edge case: helper must still emit the 'Devbox' word even with no
	// project/version provided (caller hasn't loaded a config yet).
	out := RenderBrandHeader(BrandHeader{})
	if !strings.Contains(out, "Devbox") {
		t.Errorf("expected 'Devbox' even when project/version empty, got:\n%s", out)
	}
}
