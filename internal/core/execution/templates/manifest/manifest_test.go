package manifest_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/core/execution/templates/manifest"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yml")
	writeFile(t, path, "render:\n  - {from: a.tmpl, to: a}\n")

	m, err := manifest.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Render) != 1 || m.Render[0].From != "a.tmpl" || m.Render[0].To != "a" {
		t.Fatalf("unexpected manifest: %+v", m)
	}
}

func TestLoad_MissingFile_BothSentinelsInChain(t *testing.T) {
	_, err := manifest.Load(filepath.Join(t.TempDir(), "missing.yml"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, manifest.ErrManifestMissing) {
		t.Errorf("expected ErrManifestMissing in chain, got %v", err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected os.ErrNotExist in chain, got %v", err)
	}
	if !strings.Contains(err.Error(), "missing.yml") {
		t.Errorf("error should mention path: %v", err)
	}
}

func TestLoad_UnknownField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yml")
	writeFile(t, path, "render:\n  - {from: a.tmpl, to: a}\nbogus: true\n")

	_, err := manifest.Load(path)
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
	if !strings.Contains(err.Error(), "manifest.yml") {
		t.Errorf("error should contain file path: %v", err)
	}
}

func TestLoad_MalformedYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yml")
	writeFile(t, path, "render: [::bogus")

	_, err := manifest.Load(path)
	if err == nil {
		t.Fatal("expected YAML error")
	}
}

func TestValidateShape_EmptyRejected(t *testing.T) {
	err := manifest.ValidateShape(&manifest.File{}, t.TempDir(), "test")
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected empty-manifest error, got %v", err)
	}
}

func TestValidateShape_DuplicateTo(t *testing.T) {
	m := &manifest.File{Render: []manifest.RenderEntry{
		{From: "a.tmpl", To: "x"},
		{From: "b.tmpl", To: "x"},
	}}
	err := manifest.ValidateShape(m, t.TempDir(), "test")
	if err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("expected duplicate-to error, got %v", err)
	}
}

func TestValidateShape_EscapingTo(t *testing.T) {
	m := &manifest.File{Render: []manifest.RenderEntry{
		{From: "a.tmpl", To: "../escape"},
	}}
	err := manifest.ValidateShape(m, t.TempDir(), "test")
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("expected escape error, got %v", err)
	}
}

func TestValidateShape_DanglingSymlinkTarget(t *testing.T) {
	m := &manifest.File{
		Render:   []manifest.RenderEntry{{From: "a.tmpl", To: "a"}},
		Symlinks: []manifest.SymlinkEntry{{Link: "ln", To: "nope"}},
	}
	err := manifest.ValidateShape(m, t.TempDir(), "test")
	if err == nil || !strings.Contains(err.Error(), "does not reference") {
		t.Fatalf("expected dangling-target error, got %v", err)
	}
}

func TestValidateShape_EscapingSymlink(t *testing.T) {
	m := &manifest.File{
		Render:   []manifest.RenderEntry{{From: "a.tmpl", To: "a"}},
		Symlinks: []manifest.SymlinkEntry{{Link: "../bad", To: "a"}},
	}
	err := manifest.ValidateShape(m, t.TempDir(), "test")
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("expected symlink escape error, got %v", err)
	}
}

func TestValidateShape_SymlinkLinkCollidesWithRenderDest(t *testing.T) {
	m := &manifest.File{
		Render:   []manifest.RenderEntry{{From: "a.tmpl", To: "a"}, {From: "b.tmpl", To: "b"}},
		Symlinks: []manifest.SymlinkEntry{{Link: "a", To: "b"}},
	}
	err := manifest.ValidateShape(m, t.TempDir(), "test")
	if err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("expected link-render collision error, got %v", err)
	}
}

func TestValidateShape_NoFSAccess_PassesWithoutFromFiles(t *testing.T) {
	// ValidateShape must not touch disk for `from` files.
	m := &manifest.File{Render: []manifest.RenderEntry{
		{From: "does-not-exist.tmpl", To: "out"},
	}}
	if err := manifest.ValidateShape(m, t.TempDir(), "test"); err != nil {
		t.Fatalf("ValidateShape touched disk or rejected ok manifest: %v", err)
	}
}

func TestValidateSources_OK(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.tmpl")
	writeFile(t, src, "x")

	m := &manifest.File{Render: []manifest.RenderEntry{{From: "a.tmpl", To: "a"}}}
	resolve := func(rel string) (string, bool, error) {
		return filepath.Join(dir, rel), false, nil
	}
	if err := manifest.ValidateSources(m, resolve, "test"); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestValidateSources_Missing(t *testing.T) {
	m := &manifest.File{Render: []manifest.RenderEntry{{From: "a.tmpl", To: "a"}}}
	resolve := func(rel string) (string, bool, error) {
		return "", false, os.ErrNotExist
	}
	err := manifest.ValidateSources(m, resolve, "test")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateSources_SymlinkRejected(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.tmpl")
	writeFile(t, real, "x")
	link := filepath.Join(dir, "link.tmpl")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	m := &manifest.File{Render: []manifest.RenderEntry{{From: "link.tmpl", To: "a"}}}
	resolve := func(rel string) (string, bool, error) {
		return filepath.Join(dir, rel), false, nil
	}
	err := manifest.ValidateSources(m, resolve, "test")
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestValidateSourcesWith_SinkReceivesOverrides(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.tmpl"), "x")
	writeFile(t, filepath.Join(dir, "b.tmpl"), "x")

	m := &manifest.File{Render: []manifest.RenderEntry{
		{From: "a.tmpl", To: "a"},
		{From: "b.tmpl", To: "b"},
	}}
	resolve := func(rel string) (string, bool, error) {
		return filepath.Join(dir, rel), rel == "a.tmpl", nil
	}
	var got []string
	sink := func(rel string, fromOverride bool) {
		if fromOverride {
			got = append(got, rel)
		}
	}
	if err := manifest.ValidateSourcesWith(m, resolve, sink, "test"); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(got) != 1 || got[0] != "a.tmpl" {
		t.Fatalf("expected sink to receive a.tmpl override, got %v", got)
	}
}

func TestValidatePackName(t *testing.T) {
	cases := []struct {
		name    string
		wantErr bool
	}{
		{"default", false},
		{"my-pack", false},
		{"pack_2", false},
		{"P1", false},
		{"", true},
		{"../etc", true},
		{"pack/sub", true},
		{".hidden", true},
		{"-leading", true},
		{"_leading", true},
		{"..", true},
		{"has space", true},
		{"weird?", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := manifest.ValidatePackName(tc.name)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidatePackName(%q) err=%v wantErr=%v", tc.name, err, tc.wantErr)
			}
		})
	}
}
