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
		{"app.yml", "app"},
		{"services/catalog.yml", "services.catalog"},
		{"services/catalog/db.yml", "services.catalog.db"},
		{"services/main/index.yml", "services.main.index"},
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
		"services/main/cache.yml",
		"services/main/README.md", // should be ignored
		"services/catalog.yml",
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

// ---------------------------------------------------------------------------
// Files directive
// ---------------------------------------------------------------------------

func TestLoadCommandFile_FilesDirective(t *testing.T) {
	dir := t.TempDir()
	absPath := writeYAML(t, dir, "db.yml", `
commands:
  dump-create:
    type: script
    description: Create database dump
    script:
      path: devbox/scripts/db/dump-create.sh
    files:
      dump:
        access: write
        path: runtime/dumps/db_{{ date }}.sql.gz
        mkdir: true
        overwrite: true
        on_error: remove
        env: DUMP_FILE
      backup:
        access: read
        path: runtime/backups/db.sql
        required: false
        env: BACKUP_FILE
`)

	cf, err := LoadCommandFile(absPath, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cmd, ok := cf.Commands["dump-create"]
	if !ok {
		t.Fatal("command 'dump-create' not found")
	}

	// Verify Files field was unmarshalled correctly
	if len(cmd.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(cmd.Files))
	}

	// Check dump file spec
	dumpSpec, ok := cmd.Files["dump"]
	if !ok {
		t.Fatal("files.dump not found")
	}
	if dumpSpec.Access != FileAccessWrite {
		t.Errorf("dump.Access = %q, want %q", dumpSpec.Access, FileAccessWrite)
	}
	if dumpSpec.Path != "runtime/dumps/db_{{ date }}.sql.gz" {
		t.Errorf("dump.Path = %q, want %q", dumpSpec.Path, "runtime/dumps/db_{{ date }}.sql.gz")
	}
	if !dumpSpec.Mkdir {
		t.Error("dump.Mkdir should be true")
	}
	if !dumpSpec.Overwrite {
		t.Error("dump.Overwrite should be true")
	}
	if dumpSpec.OnError != FileOnErrorRemove {
		t.Errorf("dump.OnError = %q, want %q", dumpSpec.OnError, FileOnErrorRemove)
	}
	if dumpSpec.Env != "DUMP_FILE" {
		t.Errorf("dump.Env = %q, want %q", dumpSpec.Env, "DUMP_FILE")
	}

	// Check backup file spec
	backupSpec, ok := cmd.Files["backup"]
	if !ok {
		t.Fatal("files.backup not found")
	}
	if backupSpec.Access != FileAccessRead {
		t.Errorf("backup.Access = %q, want %q", backupSpec.Access, FileAccessRead)
	}
	if backupSpec.Path != "runtime/backups/db.sql" {
		t.Errorf("backup.Path = %q, want %q", backupSpec.Path, "runtime/backups/db.sql")
	}
	if backupSpec.Required {
		t.Error("backup.Required should be false")
	}
	if backupSpec.Env != "BACKUP_FILE" {
		t.Errorf("backup.Env = %q, want %q", backupSpec.Env, "BACKUP_FILE")
	}
}

func TestLoadCommandFile_FilesCandidates(t *testing.T) {
	dir := t.TempDir()
	absPath := writeYAML(t, dir, "db.yml", `
commands:
  dump-deploy:
    type: script
    description: Restore database dump
    script:
      path: devbox/scripts/db/dump-deploy.sh
    files:
      dump:
        access: read
        candidates:
          - glob: "runtime/dumps/db_*.sql.gz"
            match: "db_\\d{4}-\\d{2}-\\d{2}"
            sort: modtime_desc
          - path: "runtime/dumps/db.sql.gz"
        required: true
        env: DUMP_FILE
`)

	cf, err := LoadCommandFile(absPath, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cmd, ok := cf.Commands["dump-deploy"]
	if !ok {
		t.Fatal("command 'dump-deploy' not found")
	}

	dumpSpec, ok := cmd.Files["dump"]
	if !ok {
		t.Fatal("files.dump not found")
	}

	if dumpSpec.Access != FileAccessRead {
		t.Errorf("dump.Access = %q, want %q", dumpSpec.Access, FileAccessRead)
	}
	if !dumpSpec.Required {
		t.Error("dump.Required should be true")
	}
	if len(dumpSpec.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(dumpSpec.Candidates))
	}

	// First candidate: glob+match+sort
	cand0 := dumpSpec.Candidates[0]
	if cand0.Glob != "runtime/dumps/db_*.sql.gz" {
		t.Errorf("candidates[0].Glob = %q, want %q", cand0.Glob, "runtime/dumps/db_*.sql.gz")
	}
	if cand0.Match != `db_\d{4}-\d{2}-\d{2}` {
		t.Errorf("candidates[0].Match = %q", cand0.Match)
	}
	if cand0.Sort != FileSortModtimeDesc {
		t.Errorf("candidates[0].Sort = %q, want %q", cand0.Sort, FileSortModtimeDesc)
	}

	// Second candidate: path
	cand1 := dumpSpec.Candidates[1]
	if cand1.Path != "runtime/dumps/db.sql.gz" {
		t.Errorf("candidates[1].Path = %q, want %q", cand1.Path, "runtime/dumps/db.sql.gz")
	}
}

func TestLoadCommandFile_DumpCreateFixture(t *testing.T) {
	// Load and validate the dump-create fixture to ensure it parses and validates
	// This verifies that YAML fixtures can define complex files directives.
	dir := t.TempDir()
	fixtureContent := `
commands:
  dump-create:
    type: script
    description: Create a database dump file (test fixture)
    params:
      database:
        type: string
        description: Database name to dump
        default: mydb
      dump_dir:
        type: string
        description: Directory to store the dump file
        default: /tmp/dumps
      dump_date:
        type: bool
        description: Include date suffix in filename
        default: true
    env:
      DB_NAME: "${param.database}"
      DB_USER: "${db.user}"
      DB_PASSWORD: "${db.password}"
      DUMP_LOCATION: "${files.dump.path}"
    files:
      dump:
        access: write
        path: "${param.dump_dir}/${param.database}{{ if .Params.dump_date }}_{{ date }}{{ end }}.sql.gz"
        mkdir: true
        overwrite: true
        on_error: remove
        env: DUMP_FILE
    script:
      path: devbox/scripts/db/dump-create.sh
    messages:
      success: "Database dump created at ${files.dump.path}"
      error: "Failed to create database dump"
`
	absPath := writeYAML(t, dir, "db.yml", fixtureContent)

	cf, err := LoadCommandFile(absPath, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cmd, ok := cf.Commands["dump-create"]
	if !ok {
		t.Fatal("command 'dump-create' not found")
	}

	// Verify Files field was unmarshalled correctly
	if len(cmd.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(cmd.Files))
	}

	// Check dump file spec
	dumpSpec, ok := cmd.Files["dump"]
	if !ok {
		t.Fatal("files.dump not found")
	}
	if dumpSpec.Access != FileAccessWrite {
		t.Errorf("dump.Access = %q, want %q", dumpSpec.Access, FileAccessWrite)
	}
	if !dumpSpec.Mkdir {
		t.Error("dump.Mkdir should be true")
	}
	if !dumpSpec.Overwrite {
		t.Error("dump.Overwrite should be true")
	}
	if dumpSpec.OnError != FileOnErrorRemove {
		t.Errorf("dump.OnError = %q, want %q", dumpSpec.OnError, FileOnErrorRemove)
	}
	if dumpSpec.Env != "DUMP_FILE" {
		t.Errorf("dump.Env = %q, want %q", dumpSpec.Env, "DUMP_FILE")
	}

	// Verify params
	if len(cmd.Params) != 3 {
		t.Fatalf("expected 3 params, got %d", len(cmd.Params))
	}
	if cmd.Params["database"].Default != "mydb" {
		t.Errorf("database.Default = %q, want %q", cmd.Params["database"].Default, "mydb")
	}

	// Verify env references files directive
	if cmd.Env["DUMP_LOCATION"] != "${files.dump.path}" {
		t.Errorf("env.DUMP_LOCATION = %q, want %q", cmd.Env["DUMP_LOCATION"], "${files.dump.path}")
	}

	// Verify success message references files directive
	if cmd.Messages.Success != "Database dump created at ${files.dump.path}" {
		t.Errorf("messages.success = %q", cmd.Messages.Success)
	}
}

func TestLoadCommandFile_DumpCreateEnvConflict(t *testing.T) {
	// Adding params.database.env: DUMP_FILE conflicts with files.dump.env: DUMP_FILE.
	dir := t.TempDir()
	absPath := writeYAML(t, dir, "db.yml", `
commands:
  dump-create:
    type: script
    description: Create a database dump file
    params:
      database:
        type: string
        description: Database name to dump
        default: mydb
        env: DUMP_FILE
    files:
      dump:
        access: write
        path: /tmp/dumps/mydb.sql.gz
        env: DUMP_FILE
    script:
      path: devbox/scripts/db/dump-create.sh
`)

	_, err := LoadCommandFile(absPath, dir)
	if err == nil {
		t.Fatal("expected env-conflict error when params.database.env and files.dump.env both declare DUMP_FILE, got nil")
	}
}

func TestLoadCommandFile_DumpDeployFixture(t *testing.T) {
	// Load and validate the dump-deploy fixture with read-mode candidates and glob+match+sort.
	dir := t.TempDir()
	fixtureContent := `
commands:
  dump-deploy:
    type: script
    description: Restore a database from a dump file (test fixture)
    params:
      database:
        type: string
        description: Source database
        default: mydb
      target_database:
        type: string
        description: Target database to restore to
        default: mydb_restored
      dump_dir:
        type: string
        description: Directory containing dump files
        default: /tmp/dumps
      check_exists:
        type: bool
        description: Check if target database exists before restore
        default: false
    env:
      DB_NAME: "${param.database}"
      TARGET_DB_NAME: "${param.target_database}"
      CHECK_EXISTS: "{{ if .Params.check_exists }}1{{ else }}0{{ end }}"
      DB_USER: "${db.user}"
      DB_PASSWORD: "${db.password}"
      DUMP_LOCATION: "${files.dump.path}"
    files:
      dump:
        access: read
        candidates:
          - glob: "${param.dump_dir}/${param.database}_*.sql.gz"
            match: '\d{4}-\d{2}-\d{2}'
            sort: name_desc
          - path: "${param.dump_dir}/${param.database}.sql.gz"
        required: true
        env: DUMP_FILE
    script:
      path: devbox/scripts/db/dump-deploy.sh
    messages:
      success: "Database restored from ${files.dump.path}"
      error: "Failed to restore database"
`
	absPath := writeYAML(t, dir, "db.yml", fixtureContent)

	cf, err := LoadCommandFile(absPath, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cmd, ok := cf.Commands["dump-deploy"]
	if !ok {
		t.Fatal("command 'dump-deploy' not found")
	}

	// Verify Files field was unmarshalled correctly
	if len(cmd.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(cmd.Files))
	}

	// Check dump file spec
	dumpSpec, ok := cmd.Files["dump"]
	if !ok {
		t.Fatal("files.dump not found")
	}
	if dumpSpec.Access != FileAccessRead {
		t.Errorf("dump.Access = %q, want %q", dumpSpec.Access, FileAccessRead)
	}
	if !dumpSpec.Required {
		t.Error("dump.Required should be true")
	}
	if len(dumpSpec.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(dumpSpec.Candidates))
	}

	// First candidate: glob+match+sort
	cand0 := dumpSpec.Candidates[0]
	if cand0.Glob != "${param.dump_dir}/${param.database}_*.sql.gz" {
		t.Errorf("candidates[0].Glob = %q", cand0.Glob)
	}
	if cand0.Match != `\d{4}-\d{2}-\d{2}` {
		t.Errorf("candidates[0].Match = %q", cand0.Match)
	}
	if cand0.Sort != FileSortNameDesc {
		t.Errorf("candidates[0].Sort = %q, want %q", cand0.Sort, FileSortNameDesc)
	}

	// Second candidate: path
	cand1 := dumpSpec.Candidates[1]
	if cand1.Path != "${param.dump_dir}/${param.database}.sql.gz" {
		t.Errorf("candidates[1].Path = %q", cand1.Path)
	}
	if dumpSpec.Env != "DUMP_FILE" {
		t.Errorf("dump.Env = %q, want %q", dumpSpec.Env, "DUMP_FILE")
	}

	// Verify params
	if len(cmd.Params) != 4 {
		t.Fatalf("expected 4 params, got %d", len(cmd.Params))
	}
	if cmd.Params["database"].Default != "mydb" {
		t.Errorf("database.Default = %q, want %q", cmd.Params["database"].Default, "mydb")
	}
	if cmd.Params["target_database"].Default != "mydb_restored" {
		t.Errorf("target_database.Default = %q", cmd.Params["target_database"].Default)
	}

	// Verify env references both files and params directives
	if cmd.Env["DUMP_LOCATION"] != "${files.dump.path}" {
		t.Errorf("env.DUMP_LOCATION = %q, want %q", cmd.Env["DUMP_LOCATION"], "${files.dump.path}")
	}
	if cmd.Env["TARGET_DB_NAME"] != "${param.target_database}" {
		t.Errorf("env.TARGET_DB_NAME = %q", cmd.Env["TARGET_DB_NAME"])
	}

	// Verify success message references files directive
	if cmd.Messages.Success != "Database restored from ${files.dump.path}" {
		t.Errorf("messages.success = %q", cmd.Messages.Success)
	}
}
