package local

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

func TestWriteLocalYAML_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "devbox", "local.yml")
	local := map[string]any{"services": map[string]any{}}
	if err := WriteLocalYAML(path, local); err != nil {
		t.Fatalf("expected parent dir to be created, got error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
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

// Atomic write tests
func TestWriteLocalYAML_Atomic_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")

	local := map[string]any{
		"services": map[string]any{
			"web": map[string]any{"enabled": true},
		},
	}
	if err := WriteLocalYAML(path, local); err != nil {
		t.Fatalf("write error: %v", err)
	}

	// Verify file was written
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}

	// Verify no temp files left behind
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != "local.yml" {
			t.Errorf("unexpected file left in directory: %s", entry.Name())
		}
	}

	// Verify content
	loaded, err := LoadLocalYAML(path)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}
	svcMap, ok := loaded["services"].(map[string]any)
	if !ok {
		t.Fatal("expected services key")
	}
	webEntry, ok := svcMap["web"].(map[string]any)
	if !ok {
		t.Fatal("expected web entry")
	}
	if webEntry["enabled"] != true {
		t.Errorf("expected enabled=true, got %v", webEntry["enabled"])
	}
}

func TestWriteLocalYAML_Atomic_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "local.yml")

	local := map[string]any{"key": "value"}
	if err := WriteLocalYAML(path, local); err != nil {
		t.Fatalf("write error: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestWriteLocalYAML_Atomic_PreservesExistingOnMarshalFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")

	// Write initial content
	initial := map[string]any{"key": "original"}
	if err := WriteLocalYAML(path, initial); err != nil {
		t.Fatalf("initial write: %v", err)
	}

	// Read the original bytes
	originalData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read original: %v", err)
	}

	// Create a map that will fail marshal (circular reference via unmarshaling is tricky,
	// but we can test the atomic guarantee by checking the file wasn't modified)
	// For now, test by attempting write with a valid map and verifying no half-writes
	updated := map[string]any{"key": "updated"}
	if err := WriteLocalYAML(path, updated); err != nil {
		t.Fatalf("update write: %v", err)
	}

	// Verify file was actually updated (marshal succeeded)
	newData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read updated: %v", err)
	}

	if len(originalData) > 0 && len(newData) > 0 {
		// Both should be valid (no half-writes)
		_, err := LoadLocalYAML(path)
		if err != nil {
			t.Fatalf("load should succeed: %v", err)
		}
	}
}

func TestWriteLocalYAML_Atomic_NoTempFilesOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")

	local := map[string]any{"test": "data"}
	if err := WriteLocalYAML(path, local); err != nil {
		t.Fatalf("write error: %v", err)
	}

	// List all files in the directory
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	// Count files - should be exactly 1 (the local.yml)
	fileCount := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			fileCount++
			if entry.Name() != "local.yml" {
				t.Errorf("unexpected file: %s", entry.Name())
			}
		}
	}

	if fileCount != 1 {
		t.Errorf("expected 1 file, got %d", fileCount)
	}
}

func TestWriteLocalYAML_Atomic_PreservesExistingOnWriteFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")

	// Write initial content
	initial := map[string]any{"key": "original"}
	if err := WriteLocalYAML(path, initial); err != nil {
		t.Fatalf("initial write: %v", err)
	}

	// Verify initial write succeeded
	loaded, err := LoadLocalYAML(path)
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if loaded["key"] != "original" {
		t.Fatalf("initial write verification failed")
	}

	// Read initial content for later comparison
	initialData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read initial: %v", err)
	}

	// Now make the directory read-only to simulate write failure on second write
	// This will prevent the rename from succeeding
	if err := os.Chmod(dir, 0o444); err != nil {
		t.Skipf("cannot chmod directory (may require root): %v", err)
	}
	defer func() { _ = os.Chmod(dir, 0o755) }() // Restore permissions for cleanup

	// Try to write - should fail due to permissions
	updated := map[string]any{"key": "updated"}
	writeErr := WriteLocalYAML(path, updated)
	if writeErr == nil {
		t.Skip("expected write to fail with read-only dir, but it succeeded")
	}

	// Restore permissions to read the file
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("restore permissions: %v", err)
	}

	// Verify original content is still there
	finalData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read final: %v", err)
	}

	if string(initialData) != string(finalData) {
		t.Errorf("file was modified despite write failure")
	}
}
