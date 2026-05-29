package builtin

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/shared/render"
)

// --- serviceConfigsCopyBuiltin.Validate ---

func TestServiceConfigsCopy_Validate_MissingService(t *testing.T) {
	b := serviceConfigsCopyBuiltin{}
	err := b.Validate(nil)
	if err == nil {
		t.Fatal("expected error for missing service")
	}
	if !strings.Contains(err.Error(), "service") {
		t.Errorf("error should mention 'service', got: %v", err)
	}
}

func TestServiceConfigsCopy_Validate_InvalidMode(t *testing.T) {
	b := serviceConfigsCopyBuiltin{}
	err := b.Validate(map[string]any{"service": "main", "mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
}

func TestServiceConfigsCopy_Validate_ValidModes(t *testing.T) {
	b := serviceConfigsCopyBuiltin{}
	for _, mode := range []string{"default", "replace", "update"} {
		err := b.Validate(map[string]any{"service": "main", "mode": mode})
		if err != nil {
			t.Errorf("unexpected error for mode %q: %v", mode, err)
		}
	}
}

func TestServiceConfigsCopy_Validate_DefaultMode(t *testing.T) {
	b := serviceConfigsCopyBuiltin{}
	err := b.Validate(map[string]any{"service": "main"})
	if err != nil {
		t.Fatalf("expected no error when mode is omitted (defaults to replace): %v", err)
	}
}

// --- serviceConfigsCopyBuiltin.Describe ---

func TestServiceConfigsCopy_Describe(t *testing.T) {
	b := serviceConfigsCopyBuiltin{}
	desc := b.Describe(map[string]any{"service": "main", "mode": "update"})
	if !strings.Contains(desc, "main") {
		t.Errorf("expected service name in describe, got %q", desc)
	}
	if !strings.Contains(desc, "update") {
		t.Errorf("expected mode in describe, got %q", desc)
	}
}

// --- CopyConfigFile ---

func TestCopyConfigFile_Replace(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.env")
	dest := filepath.Join(dir, "dest.env")
	if err := os.WriteFile(src, []byte("A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CopyConfigFile(src, dest, "replace"); err != nil {
		t.Fatalf("CopyConfigFile replace: %v", err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "A=1\n" {
		t.Errorf("unexpected dest content: %q", data)
	}
}

func TestCopyConfigFile_Default_SkipsExisting(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.env")
	dest := filepath.Join(dir, "dest.env")
	if err := os.WriteFile(src, []byte("NEW=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("OLD=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CopyConfigFile(src, dest, "default"); err != nil {
		t.Fatalf("CopyConfigFile default: %v", err)
	}
	data, _ := os.ReadFile(dest)
	if string(data) != "OLD=1\n" {
		t.Errorf("default mode should skip existing dest, got: %q", data)
	}
}

func TestCopyConfigFile_Update_MergesNewKeys(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.env")
	dest := filepath.Join(dir, "dest.env")
	if err := os.WriteFile(src, []byte("A=1\nB=2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("A=old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CopyConfigFile(src, dest, "update"); err != nil {
		t.Fatalf("CopyConfigFile update: %v", err)
	}
	data, _ := os.ReadFile(dest)
	content := string(data)
	if !strings.Contains(content, "A=old") {
		t.Errorf("update mode should preserve existing A=old, got: %q", content)
	}
	if !strings.Contains(content, "B=2") {
		t.Errorf("update mode should add new key B=2, got: %q", content)
	}
}

func TestCopyConfigFile_MissingSrc(t *testing.T) {
	dir := t.TempDir()
	err := CopyConfigFile(filepath.Join(dir, "nosrc.env"), filepath.Join(dir, "dest.env"), "replace")
	if err == nil {
		t.Fatal("expected error for missing src")
	}
}

func TestCopyConfigFile_Default_DestMissing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.env")
	dest := filepath.Join(dir, "sub", "dest.env")
	if err := os.WriteFile(src, []byte("A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CopyConfigFile(src, dest, "default"); err != nil {
		t.Fatalf("CopyConfigFile default with missing dest: %v", err)
	}
	data, _ := os.ReadFile(dest)
	if string(data) != "A=1\n" {
		t.Errorf("expected content written to new dest, got: %q", data)
	}
}

func TestCopyConfigFile_UnknownMode_DestMissing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.env")
	dest := filepath.Join(dir, "dest.env")
	if err := os.WriteFile(src, []byte("A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CopyConfigFile(src, dest, "unknown_mode"); err != nil {
		t.Fatalf("unknown mode fallback should not error: %v", err)
	}
}

func TestCopyConfigFile_UnknownMode_DestExists(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.env")
	dest := filepath.Join(dir, "dest.env")
	if err := os.WriteFile(src, []byte("NEW=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("OLD=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CopyConfigFile(src, dest, "unknown_mode"); err != nil {
		t.Fatalf("unknown mode with existing dest: %v", err)
	}
	data, _ := os.ReadFile(dest)
	if string(data) != "OLD=1\n" {
		t.Errorf("unknown mode should preserve existing dest, got: %q", data)
	}
}

func TestCopyConfigFile_Update_NewFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.env")
	dest := filepath.Join(dir, "dest.env")
	if err := os.WriteFile(src, []byte("A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// dest does not exist: update mode should write src content
	if err := CopyConfigFile(src, dest, "update"); err != nil {
		t.Fatalf("update mode with missing dest: %v", err)
	}
	data, _ := os.ReadFile(dest)
	if !strings.Contains(string(data), "A=1") {
		t.Errorf("expected src content written to new dest, got: %q", data)
	}
}

func TestCopyConfigFile_Update_NoAdditions(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.env")
	dest := filepath.Join(dir, "dest.env")
	if err := os.WriteFile(src, []byte("A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("A=existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// All keys in src already exist in dest — no additions.
	if err := CopyConfigFile(src, dest, "update"); err != nil {
		t.Fatalf("update mode with no new keys: %v", err)
	}
	data, _ := os.ReadFile(dest)
	if string(data) != "A=existing\n" {
		t.Errorf("update with no new keys should leave dest unchanged, got: %q", data)
	}
}

// --- removePathsBuiltin.Validate edge case ---

func TestRemovePaths_Validate_InvalidPathsType(t *testing.T) {
	b := removePathsBuiltin{}
	err := b.Validate(map[string]any{"paths": 123})
	if err == nil {
		t.Fatal("expected error for invalid paths type")
	}
}

// --- serviceConfigsCopyBuiltin.Run early error paths ---

func TestServiceConfigsCopy_Run_ServiceNotFound(t *testing.T) {
	b := serviceConfigsCopyBuiltin{}
	ctx := ExecContext{
		Config:      &config.DevboxConfig{Services: map[string]config.ServiceConfig{}},
		ProjectRoot: t.TempDir(),
		Output:      render.NewWriter(&bytes.Buffer{}),
	}
	err := b.Run(context.Background(), map[string]any{"service": "missing"}, ctx)
	if err == nil {
		t.Fatal("expected error when service not found")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error should mention service name, got: %v", err)
	}
}

func TestServiceConfigsCopy_Run_EmptyServiceDir(t *testing.T) {
	b := serviceConfigsCopyBuiltin{}
	ctx := ExecContext{
		Config: &config.DevboxConfig{
			Services: map[string]config.ServiceConfig{
				"main": {Dir: ""},
			},
		},
		ProjectRoot: t.TempDir(),
		Output:      render.NewWriter(&bytes.Buffer{}),
	}
	err := b.Run(context.Background(), map[string]any{"service": "main"}, ctx)
	if err == nil {
		t.Fatal("expected error when service dir is empty")
	}
}

func TestServiceConfigsCopy_Run_NoConfigs_Succeeds(t *testing.T) {
	b := serviceConfigsCopyBuiltin{}
	ctx := ExecContext{
		Config: &config.DevboxConfig{
			Services: map[string]config.ServiceConfig{
				"main": {Dir: "services/main", Configs: nil},
			},
		},
		ProjectRoot: t.TempDir(),
		Output:      render.NewWriter(&bytes.Buffer{}),
	}
	err := b.Run(context.Background(), map[string]any{"service": "main"}, ctx)
	if err != nil {
		t.Fatalf("Run with no configs should succeed: %v", err)
	}
}
