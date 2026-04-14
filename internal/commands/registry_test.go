package commands

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// buildTestRegistry creates a temp dir with the given YAML files and loads a
// Registry from it.  fileMap maps relative path → YAML content.
func buildTestRegistry(t *testing.T, fileMap map[string]string) (*Registry, error) {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range fileMap {
		writeYAML(t, dir, rel, content)
	}
	return LoadRegistry(dir)
}

// mustRegistry is like buildTestRegistry but fatals on error.
func mustRegistry(t *testing.T, fileMap map[string]string) *Registry {
	t.Helper()
	reg, err := buildTestRegistry(t, fileMap)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	return reg
}

// ---------------------------------------------------------------------------
// LoadRegistry — happy path
// ---------------------------------------------------------------------------

const dbYAML = `
group:
  title: Database
  description: Database utilities

commands:
  up:
    type: command
    description: Start db container
    run: "docker compose up -d db"
    private: true
  wait:
    type: command
    description: Wait for db to be ready
    run: "echo waiting"
    private: true
`

const mainYAML = `
commands:
  migrate:
    type: service_exec
    description: Run migrations
    service: app-main
    run: "php artisan migrate"
  composer-install:
    type: service_exec
    description: Install composer deps
    service: app-main
    run: "composer install"
`

const mainDBYAML = `
commands:
  create:
    type: service_exec
    description: Create the database
    service: db
    run: "echo create"
    private: true
`

const workflowYAML = `
commands:
  bootstrap:
    type: workflow
    description: Full service bootstrap
    steps:
      - command: services.main.composer-install
      - command: services.main.migrate
`

func TestLoadRegistry_Basic(t *testing.T) {
	reg := mustRegistry(t, map[string]string{
		"db.yml":               dbYAML,
		"services/main.yml":    mainYAML,
		"services/main/db.yml": mainDBYAML,
	})

	// Spot-check IDs.
	for _, id := range []string{
		"db.up", "db.wait",
		"services.main.migrate", "services.main.composer-install",
		"services.main.db.create",
	} {
		if _, err := reg.Get(id); err != nil {
			t.Errorf("expected command %q to exist: %v", id, err)
		}
	}
}

func TestLoadRegistry_EmptyDir(t *testing.T) {
	reg := mustRegistry(t, map[string]string{})
	if len(reg.byID) != 0 {
		t.Errorf("expected empty registry, got %d commands", len(reg.byID))
	}
}

func TestLoadRegistry_DuplicateID(t *testing.T) {
	// Two files that would produce the same command ID.
	_, err := buildTestRegistry(t, map[string]string{
		"db.yml": `
commands:
  up:
    type: command
    run: "echo a"
`,
		// index.yml at root level also produces group "" — but that still gives
		// a different group.  Instead use two files with the same group path
		// by creating db/index.yml which also yields group "db".
		"db/index.yml": `
commands:
  up:
    type: command
    run: "echo b"
`,
	})
	if err == nil {
		t.Error("expected duplicate ID error, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate command ID") {
		t.Errorf("error should mention duplicate ID, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Get
// ---------------------------------------------------------------------------

func TestRegistry_Get_Found(t *testing.T) {
	reg := mustRegistry(t, map[string]string{"db.yml": dbYAML})
	cmd, err := reg.Get("db.up")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if cmd.ID != "db.up" {
		t.Errorf("ID = %q, want %q", cmd.ID, "db.up")
	}
}

func TestRegistry_Get_NotFound(t *testing.T) {
	reg := mustRegistry(t, map[string]string{"db.yml": dbYAML})
	_, err := reg.Get("db.nonexistent")
	if err == nil {
		t.Error("expected error for missing command, got nil")
	}
}

// ---------------------------------------------------------------------------
// List / ListAll
// ---------------------------------------------------------------------------

func TestRegistry_List_ExcludesPrivate(t *testing.T) {
	reg := mustRegistry(t, map[string]string{"db.yml": dbYAML})
	// db.up and db.wait are both private — List should return nothing.
	got := reg.List("")
	if len(got) != 0 {
		t.Errorf("expected 0 public commands, got %d: %v", len(got), got)
	}
}

func TestRegistry_ListAll_IncludesPrivate(t *testing.T) {
	reg := mustRegistry(t, map[string]string{"db.yml": dbYAML})
	got := reg.ListAll("")
	if len(got) != 2 {
		t.Errorf("expected 2 commands (including private), got %d", len(got))
	}
}

func TestRegistry_List_GroupPrefix(t *testing.T) {
	reg := mustRegistry(t, map[string]string{
		"db.yml":            dbYAML,
		"services/main.yml": mainYAML,
	})

	// Public commands only under "services.main".
	got := reg.List("services.main")
	if len(got) != 2 {
		t.Errorf("expected 2 public services.main commands, got %d: %v", len(got), got)
	}
	for _, cmd := range got {
		if !strings.HasPrefix(cmd.ID, "services.main") {
			t.Errorf("unexpected command outside prefix: %q", cmd.ID)
		}
		if cmd.Private {
			t.Errorf("List returned private command %q", cmd.ID)
		}
	}
}

func TestRegistry_List_ExactIDMatch(t *testing.T) {
	// A prefix "services.main" should NOT match "services.main.db.create".
	// But "services" should match "services.main.migrate".
	reg := mustRegistry(t, map[string]string{
		"services/main.yml":    mainYAML,
		"services/main/db.yml": mainDBYAML,
	})

	// "services.main" should return migrate and composer-install (public) only.
	got := reg.List("services.main")
	if len(got) != 2 {
		t.Errorf("expected 2 commands for prefix services.main, got %d: %v", len(got), got)
	}

	// "services" should return those same 2 (main.db.create is private).
	got = reg.List("services")
	if len(got) != 2 {
		t.Errorf("expected 2 public commands for prefix services, got %d: %v", len(got), got)
	}
}

func TestRegistry_List_SortedByID(t *testing.T) {
	reg := mustRegistry(t, map[string]string{"services/main.yml": mainYAML})
	got := reg.List("")
	for i := 1; i < len(got); i++ {
		if got[i].ID < got[i-1].ID {
			t.Errorf("list not sorted: %q before %q", got[i-1].ID, got[i].ID)
		}
	}
}

// ---------------------------------------------------------------------------
// Groups / GroupNode tree
// ---------------------------------------------------------------------------

func TestRegistry_Groups_Root(t *testing.T) {
	reg := mustRegistry(t, map[string]string{
		"db.yml":            dbYAML,
		"services/main.yml": mainYAML,
	})
	root := reg.Groups()
	if root == nil {
		t.Fatal("Groups() returned nil")
	}
	if root.ID != "" {
		t.Errorf("root ID = %q, want empty string", root.ID)
	}
}

func TestRegistry_Groups_Children(t *testing.T) {
	reg := mustRegistry(t, map[string]string{
		"db.yml":            dbYAML,
		"services/main.yml": mainYAML,
	})
	root := reg.Groups()

	// Root should have children "db" and "services".
	names := make(map[string]bool)
	for _, ch := range root.Children {
		names[ch.Name] = true
	}
	for _, want := range []string{"db", "services"} {
		if !names[want] {
			t.Errorf("expected root child %q, got children: %v", want, root.Children)
		}
	}
}

func TestRegistry_Groups_Meta(t *testing.T) {
	reg := mustRegistry(t, map[string]string{"db.yml": dbYAML})
	root := reg.Groups()

	var dbNode *GroupNode
	for _, ch := range root.Children {
		if ch.Name == "db" {
			dbNode = ch
		}
	}
	if dbNode == nil {
		t.Fatal("db group node not found")
	}
	if dbNode.Meta.Title != "Database" {
		t.Errorf("Meta.Title = %q, want %q", dbNode.Meta.Title, "Database")
	}
}

func TestRegistry_Groups_Commands(t *testing.T) {
	reg := mustRegistry(t, map[string]string{"db.yml": dbYAML})
	root := reg.Groups()

	var dbNode *GroupNode
	for _, ch := range root.Children {
		if ch.Name == "db" {
			dbNode = ch
		}
	}
	if dbNode == nil {
		t.Fatal("db group node not found")
	}
	if len(dbNode.Commands) != 2 {
		t.Errorf("db node Commands len = %d, want 2", len(dbNode.Commands))
	}
}

// ---------------------------------------------------------------------------
// Validate — cross-registry checks
// ---------------------------------------------------------------------------

func TestRegistry_Validate_Valid(t *testing.T) {
	reg := mustRegistry(t, map[string]string{
		"services/main.yml":       mainYAML,
		"services/main/index.yml": workflowYAML,
	})
	if err := reg.Validate(); err != nil {
		t.Errorf("expected no validation error, got: %v", err)
	}
}

func TestRegistry_Validate_MissingWorkflowRef(t *testing.T) {
	// Workflow references a command ID that doesn't exist.
	_, err := buildTestRegistry(t, map[string]string{
		"services/main/index.yml": `
commands:
  bootstrap:
    type: workflow
    description: Bootstrap
    steps:
      - command: services.main.nonexistent
`,
	})
	// LoadRegistry itself does not call Validate; we need to call it explicitly.
	// If it did load successfully, validate should catch the missing ref.
	// But since LoadRegistry doesn't call Validate, we test Validate separately.
	// The above will load fine; validate will catch the bad ref.
	if err != nil {
		// LoadRegistry itself errored (unexpected).
		t.Fatalf("LoadRegistry: %v", err)
	}
}

func TestRegistry_Validate_MissingRef_Direct(t *testing.T) {
	reg := mustRegistry(t, map[string]string{
		"services/main/index.yml": `
commands:
  bootstrap:
    type: workflow
    description: Bootstrap
    steps:
      - command: services.main.nonexistent
`,
	})
	err := reg.Validate()
	if err == nil {
		t.Error("expected validation error for missing workflow ref, got nil")
	}
	if !strings.Contains(err.Error(), "services.main.nonexistent") {
		t.Errorf("error should mention missing ID, got: %v", err)
	}
}

func TestRegistry_Validate_PrivateRef_Allowed(t *testing.T) {
	// Workflows may reference private commands.
	reg := mustRegistry(t, map[string]string{
		"db.yml": dbYAML, // db.up and db.wait are private
		"services/main/index.yml": `
commands:
  start:
    type: workflow
    description: Start services
    steps:
      - command: db.up
      - command: db.wait
`,
	})
	if err := reg.Validate(); err != nil {
		t.Errorf("private command reference in workflow should be valid, got: %v", err)
	}
}

func TestRegistry_Validate_NoWorkflows(t *testing.T) {
	// No workflows — validate should always pass.
	reg := mustRegistry(t, map[string]string{"db.yml": dbYAML})
	if err := reg.Validate(); err != nil {
		t.Errorf("expected no error with no workflows, got: %v", err)
	}
}
