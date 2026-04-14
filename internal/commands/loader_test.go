package commands

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// ComputeGroup
// ---------------------------------------------------------------------------

func TestComputeGroup(t *testing.T) {
	tests := []struct {
		relPath string
		want    string
	}{
		{"db.yml", "db"},
		{"services/main.yml", "services.main"},
		{"services/main/db.yml", "services.main.db"},
		{"services/main/index.yml", "services.main"},
		{"index.yml", ""},
		{"app.yml", "app"},
		{"services/second.yml", "services.second"},
		{"services/second/db.yml", "services.second.db"},
	}

	for _, tc := range tests {
		got := ComputeGroup(tc.relPath)
		if got != tc.want {
			t.Errorf("ComputeGroup(%q) = %q, want %q", tc.relPath, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// ComputeCommandID
// ---------------------------------------------------------------------------

func TestComputeCommandID(t *testing.T) {
	tests := []struct {
		group     string
		localName string
		want      string
	}{
		{"db", "up", "db.up"},
		{"services.main", "migrate", "services.main.migrate"},
		{"services.main.db", "create", "services.main.db.create"},
		{"", "bootstrap", "bootstrap"},
		{"services.main", "composer-install", "services.main.composer-install"},
	}

	for _, tc := range tests {
		got := ComputeCommandID(tc.group, tc.localName)
		if got != tc.want {
			t.Errorf("ComputeCommandID(%q, %q) = %q, want %q", tc.group, tc.localName, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// DiscoverCommandFiles
// ---------------------------------------------------------------------------

func TestDiscoverCommandFiles(t *testing.T) {
	dir := t.TempDir()

	// Create a nested structure of YAML and non-YAML files.
	files := []string{
		"db.yml",
		"app.yml",
		"services/main.yml",
		"services/main/db.yml",
		"services/main/index.yml",
		"services/main/README.md", // should be ignored
		"services/second.yml",
	}
	for _, f := range files {
		full := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte{}, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := DiscoverCommandFiles(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Build expected set (absolute paths, .yml only).
	wantSet := map[string]bool{}
	for _, f := range files {
		if filepath.Ext(f) == ".yml" {
			wantSet[filepath.Join(dir, f)] = true
		}
	}

	if len(got) != len(wantSet) {
		t.Fatalf("got %d files, want %d", len(got), len(wantSet))
	}
	for _, p := range got {
		if !wantSet[p] {
			t.Errorf("unexpected file in result: %s", p)
		}
	}
}

func TestDiscoverCommandFiles_NonExistentDir(t *testing.T) {
	_, err := DiscoverCommandFiles("/nonexistent/path")
	if err == nil {
		t.Error("expected error for non-existent directory, got nil")
	}
}

func TestDiscoverCommandFiles_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	got, err := DiscoverCommandFiles(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty result for empty dir, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// LoadCommandFile
// ---------------------------------------------------------------------------

func writeYAML(t *testing.T, dir, relPath, content string) string {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

const validCommandYAML = `
group:
  title: Database
  description: Database commands

commands:
  up:
    type: command
    description: Start database
    run: "echo start"
  wait:
    type: command
    description: Wait for database
    run: "echo wait"
    private: true
`

func TestLoadCommandFile_Basic(t *testing.T) {
	dir := t.TempDir()
	absPath := writeYAML(t, dir, "db.yml", validCommandYAML)

	cf, err := LoadCommandFile(absPath, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cf.GroupID != "db" {
		t.Errorf("GroupID = %q, want %q", cf.GroupID, "db")
	}
	if cf.FilePath != absPath {
		t.Errorf("FilePath = %q, want %q", cf.FilePath, absPath)
	}

	up, ok := cf.Commands["up"]
	if !ok {
		t.Fatal("command 'up' not found")
	}
	if up.ID != "db.up" {
		t.Errorf("up.ID = %q, want %q", up.ID, "db.up")
	}
	if up.Group != "db" {
		t.Errorf("up.Group = %q, want %q", up.Group, "db")
	}
	if up.LocalName != "up" {
		t.Errorf("up.LocalName = %q, want %q", up.LocalName, "up")
	}

	wait := cf.Commands["wait"]
	if !wait.Private {
		t.Error("wait command should be private")
	}
}

func TestLoadCommandFile_IndexYML(t *testing.T) {
	dir := t.TempDir()
	absPath := writeYAML(t, dir, "services/main/index.yml", `
commands:
  bootstrap:
    type: workflow
    description: Bootstrap main service
    steps:
      - command: services.main.composer-install
`)

	cf, err := LoadCommandFile(absPath, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cf.GroupID != "services.main" {
		t.Errorf("GroupID = %q, want %q", cf.GroupID, "services.main")
	}

	cmd := cf.Commands["bootstrap"]
	if cmd.ID != "services.main.bootstrap" {
		t.Errorf("bootstrap.ID = %q, want %q", cmd.ID, "services.main.bootstrap")
	}
}

func TestLoadCommandFile_NestedGroup(t *testing.T) {
	dir := t.TempDir()
	absPath := writeYAML(t, dir, "services/main/db.yml", `
commands:
  create:
    type: service_exec
    description: Create database
    service: db
    run: "echo create"
    private: true
`)

	cf, err := LoadCommandFile(absPath, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cf.GroupID != "services.main.db" {
		t.Errorf("GroupID = %q, want %q", cf.GroupID, "services.main.db")
	}

	cmd := cf.Commands["create"]
	if cmd.ID != "services.main.db.create" {
		t.Errorf("create.ID = %q, want %q", cmd.ID, "services.main.db.create")
	}
}

func TestLoadCommandFile_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	absPath := writeYAML(t, dir, "bad.yml", `{not: valid: yaml:`)

	_, err := LoadCommandFile(absPath, dir)
	if err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}

func TestLoadCommandFile_ValidationError(t *testing.T) {
	dir := t.TempDir()
	// command type with both run and argv set — should fail validation.
	absPath := writeYAML(t, dir, "bad.yml", `
commands:
  broken:
    type: command
    run: "echo hi"
    argv: ["echo", "hi"]
`)

	_, err := LoadCommandFile(absPath, dir)
	if err == nil {
		t.Error("expected validation error, got nil")
	}
}

func TestLoadCommandFile_FileNotFound(t *testing.T) {
	_, err := LoadCommandFile("/nonexistent/commands/db.yml", "/nonexistent/commands")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}
