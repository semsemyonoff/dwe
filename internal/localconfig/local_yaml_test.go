package localconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadLocalYAML_MissingFile(t *testing.T) {
	local, err := LoadLocalYAML("/nonexistent/path/local.yml")
	if err != nil {
		t.Fatalf("missing file should not be an error, got: %v", err)
	}
	if len(local) != 0 {
		t.Errorf("expected empty map for missing file, got %v", local)
	}
}

func TestLoadLocalYAML_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	local, err := LoadLocalYAML(path)
	if err != nil {
		t.Fatalf("empty file should not be an error, got: %v", err)
	}
	if len(local) != 0 {
		t.Errorf("expected empty map for empty file, got %v", local)
	}
}

func TestLoadLocalYAML_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")
	content := "services:\n  second:\n    enabled: true\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	local, err := LoadLocalYAML(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	svcMap, ok := local["services"].(map[string]any)
	if !ok {
		t.Fatal("expected services key in local")
	}
	secondEntry, ok := svcMap["second"].(map[string]any)
	if !ok {
		t.Fatal("expected second key under services")
	}
	if secondEntry["enabled"] != true {
		t.Errorf("expected enabled=true, got %v", secondEntry["enabled"])
	}
}

func TestWriteLocalYAML_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")

	local := map[string]any{
		"services": map[string]any{
			"second": map[string]any{"enabled": true},
		},
	}
	if err := WriteLocalYAML(path, local); err != nil {
		t.Fatalf("write error: %v", err)
	}

	loaded, err := LoadLocalYAML(path)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}
	svcMap, _ := loaded["services"].(map[string]any)
	entry, _ := svcMap["second"].(map[string]any)
	if entry["enabled"] != true {
		t.Errorf("round-trip: expected enabled=true, got %v", entry["enabled"])
	}
}

func TestSetLocalEntryEnabled_CreatesEntry(t *testing.T) {
	subtree := map[string]any{}
	SetLocalEntryEnabled(subtree, "second", true)
	entry, ok := subtree["second"].(map[string]any)
	if !ok {
		t.Fatal("expected map entry for second")
	}
	if entry["enabled"] != true {
		t.Errorf("expected enabled=true, got %v", entry["enabled"])
	}
}

func TestSetLocalEntryEnabled_UpdatesExisting(t *testing.T) {
	subtree := map[string]any{
		"second": map[string]any{"enabled": true},
	}
	SetLocalEntryEnabled(subtree, "second", false)
	entry, _ := subtree["second"].(map[string]any)
	if entry["enabled"] != false {
		t.Errorf("expected enabled=false, got %v", entry["enabled"])
	}
}
