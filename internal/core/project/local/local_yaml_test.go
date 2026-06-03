package local

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
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
	path := filepath.Join(dir, "workspace", "local.yml")
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

// TestRoundTrip_ServiceTogglePreservesComposeExtra pins behavior: a local.yml
// containing project-wide compose.extra and per-service compose.extra entries
// must survive a ApplyServiceTogglesToYAML + WriteLocalYAML + LoadLocalYAML
// round-trip. The LoadLocalYAML loader stores everything as map[string]any so
// preservation is automatic; this test guards against a future refactor to
// typed structs that would silently drop unknown keys.
func TestRoundTrip_ServiceTogglePreservesComposeExtra(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")

	initial := map[string]any{
		"compose": map[string]any{
			"extra": []any{"compose.local.yml", "extra/two.yml"},
		},
		"services": map[string]any{
			"web": map[string]any{
				"enabled": true,
				"ports":   map[string]any{"http": 3001},
				"compose": map[string]any{
					"extra": []any{"compose/web.local.yml"},
				},
			},
			"api": map[string]any{
				"enabled": true,
				"hosts":   map[string]any{"api": "api.local"},
				"compose": map[string]any{
					"extra": []any{"compose/api.local.yml", "compose/api.extra.yml"},
				},
			},
		},
	}
	if err := WriteLocalYAML(path, initial); err != nil {
		t.Fatalf("initial write: %v", err)
	}

	loaded, err := LoadLocalYAML(path)
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}

	cfg := &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"web": {Required: false},
			"api": {Required: false},
		},
	}
	// Flip api's enabled bit (the toggle-write path used by `dwe services disable`).
	if err := ApplyServiceTogglesToYAML(cfg, loaded, nil, []string{"api"}); err != nil {
		t.Fatalf("apply toggles: %v", err)
	}

	if err := WriteLocalYAML(path, loaded); err != nil {
		t.Fatalf("write after toggle: %v", err)
	}

	reloaded, err := LoadLocalYAML(path)
	if err != nil {
		t.Fatalf("reload after toggle: %v", err)
	}

	// Project-wide compose.extra survives.
	projectCompose, ok := reloaded["compose"].(map[string]any)
	if !ok {
		t.Fatalf("project-wide compose block lost; got %T (%v)", reloaded["compose"], reloaded["compose"])
	}
	wantProjectExtra := []any{"compose.local.yml", "extra/two.yml"}
	if !reflect.DeepEqual(projectCompose["extra"], wantProjectExtra) {
		t.Errorf("project-wide compose.extra: want %v, got %v", wantProjectExtra, projectCompose["extra"])
	}

	svcMap, ok := reloaded["services"].(map[string]any)
	if !ok {
		t.Fatalf("services block lost; got %T", reloaded["services"])
	}

	// web: untouched — compose.extra + ports survive, enabled still true.
	webEntry, ok := svcMap["web"].(map[string]any)
	if !ok {
		t.Fatalf("web entry lost; got %T", svcMap["web"])
	}
	if webEntry["enabled"] != true {
		t.Errorf("web.enabled: want true, got %v", webEntry["enabled"])
	}
	webCompose, ok := webEntry["compose"].(map[string]any)
	if !ok {
		t.Fatalf("web.compose lost; got %T", webEntry["compose"])
	}
	wantWebExtra := []any{"compose/web.local.yml"}
	if !reflect.DeepEqual(webCompose["extra"], wantWebExtra) {
		t.Errorf("web.compose.extra: want %v, got %v", wantWebExtra, webCompose["extra"])
	}
	if _, ok := webEntry["ports"].(map[string]any); !ok {
		t.Errorf("web.ports lost; got %T", webEntry["ports"])
	}

	// api: enabled flipped to false, but compose.extra + hosts must survive.
	apiEntry, ok := svcMap["api"].(map[string]any)
	if !ok {
		t.Fatalf("api entry lost; got %T", svcMap["api"])
	}
	if apiEntry["enabled"] != false {
		t.Errorf("api.enabled: want false (toggled), got %v", apiEntry["enabled"])
	}
	apiCompose, ok := apiEntry["compose"].(map[string]any)
	if !ok {
		t.Fatalf("api.compose lost; got %T", apiEntry["compose"])
	}
	wantAPIExtra := []any{"compose/api.local.yml", "compose/api.extra.yml"}
	if !reflect.DeepEqual(apiCompose["extra"], wantAPIExtra) {
		t.Errorf("api.compose.extra: want %v, got %v", wantAPIExtra, apiCompose["extra"])
	}
	if _, ok := apiEntry["hosts"].(map[string]any); !ok {
		t.Errorf("api.hosts lost; got %T", apiEntry["hosts"])
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
