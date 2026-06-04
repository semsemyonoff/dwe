package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// TestValidateDocsFlags checks that invalid flag values are rejected.
func TestValidateDocsFlags(t *testing.T) {
	t.Run("valid flags", func(t *testing.T) {
		cases := []docsFlags{
			{},
			{lang: "en"},
			{lang: "ru"},
			{includePrivate: true},
		}
		for _, df := range cases {
			if err := validateDocsFlags(&df); err != nil {
				t.Errorf("expected no error for %+v, got: %v", df, err)
			}
		}
	})

	t.Run("lang=all rejected", func(t *testing.T) {
		df := docsFlags{lang: "all"}
		if err := validateDocsFlags(&df); err == nil {
			t.Error("expected error for lang=all")
		}
	})
}

// TestGenRegistryMarkdown checks that markdown files are written for usercommands.
func TestGenRegistryMarkdown(t *testing.T) {
	reg := buildTestRegistryForDocs(t)

	// Load the embedded i18n store for testing
	store, err := i18n.Load("")
	if err != nil {
		t.Fatalf("failed to load i18n store: %v", err)
	}

	dir := t.TempDir()
	if err := genRegistryMarkdown(reg, dir, false, store, "en"); err != nil {
		t.Fatalf("genRegistryMarkdown: %v", err)
	}

	// Expect a file for the "migrate" command in the services/main group.
	expectedPath := filepath.Join(dir, "services", "main", "migrate.md")
	data, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("expected file %s: %v", expectedPath, err)
	}

	content := string(data)
	// Check key content is present.
	if !strings.Contains(content, "services.main.migrate") {
		t.Errorf("expected command ID in output, got:\n%s", content)
	}
	if !strings.Contains(content, "service_exec") {
		t.Errorf("expected type in output, got:\n%s", content)
	}
}

// TestGenCommandsIndex checks the commands index file.
func TestGenCommandsIndex(t *testing.T) {
	reg := buildTestRegistryForDocs(t)
	dir := t.TempDir()

	// Load the embedded i18n store for testing
	store, err := i18n.Load("")
	if err != nil {
		t.Fatalf("failed to load i18n store: %v", err)
	}

	if err := genCommandsIndex(reg, dir, false, store, "en"); err != nil {
		t.Fatalf("genCommandsIndex: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "index.md"))
	if err != nil {
		t.Fatalf("index.md not written: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "Commands Reference") {
		t.Errorf("missing header in index: %s", content)
	}
	// Public command should appear.
	if !strings.Contains(content, "services.main.migrate") {
		t.Errorf("public command missing from index: %s", content)
	}
	// Private command should NOT appear with includePrivate=false.
	if strings.Contains(content, "services.main.db.create") {
		t.Errorf("private command should not appear with includePrivate=false: %s", content)
	}
}

// TestGenCommandsIndexIncludePrivate verifies private commands appear when requested.
func TestGenCommandsIndexIncludePrivate(t *testing.T) {
	reg := buildTestRegistryForDocs(t)
	dir := t.TempDir()

	// Load the embedded i18n store for testing
	store, err := i18n.Load("")
	if err != nil {
		t.Fatalf("failed to load i18n store: %v", err)
	}

	if err := genCommandsIndex(reg, dir, true, store, "en"); err != nil {
		t.Fatalf("genCommandsIndex: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "index.md"))
	if err != nil {
		t.Fatalf("index.md not written: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "services.main.db.create") {
		t.Errorf("private command missing from index with includePrivate=true: %s", content)
	}
}

// TestGenTopLevelIndex checks the top-level index file content.
func TestGenTopLevelIndex(t *testing.T) {
	dir := t.TempDir()

	if err := genTopLevelIndex(dir); err != nil {
		t.Fatalf("genTopLevelIndex: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "index.md"))
	if err != nil {
		t.Fatalf("index.md not written: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "Commands Reference") {
		t.Errorf("missing Commands Reference link: %s", content)
	}
	if strings.Contains(content, "CLI Reference") {
		t.Errorf("CLI Reference should not appear (cli scope removed): %s", content)
	}
}

// TestWriteCommandMarkdown_Workflow checks workflow step rendering: command,
// parallel, and confirm steps, plus the registry-driven description annotation.
func TestWriteCommandMarkdown_Workflow(t *testing.T) {
	failFast := false
	def := &usercommands.CommandDef{
		ID:          "db.bootstrap",
		Group:       "db",
		LocalName:   "bootstrap",
		Type:        usercommands.CommandTypeWorkflow,
		Description: "Bootstrap the database",
		Steps: []usercommands.WorkflowStep{
			{Command: "db.create"},
			{Command: "db.migrate", With: map[string]string{"env": "test"}},
			{
				When: "${param.warm}",
				Parallel: &usercommands.WorkflowParallel{
					MaxConcurrent: 2,
					FailFast:      &failFast,
					Steps: []usercommands.WorkflowStep{
						{Command: "db.seed"},
						{Command: "db.vacuum", ContinueOnError: true},
					},
				},
			},
			{Confirm: "All done?"},
		},
	}

	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "db.create", Description: "Create the database"})
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "db.migrate", Description: "Apply migrations"})
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "db.seed", Description: "Seed initial data"})
	reg.AddCommandForTest(&usercommands.CommandDef{ID: "db.vacuum", Description: "Vacuum tables"})

	store, err := i18n.Load("")
	if err != nil {
		t.Fatalf("failed to load i18n store: %v", err)
	}

	dir := t.TempDir()
	if err := writeCommandMarkdown(def, dir, reg, store, "en"); err != nil {
		t.Fatalf("writeCommandMarkdown: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "bootstrap.md"))
	if err != nil {
		t.Fatalf("bootstrap.md not written: %v", err)
	}
	content := string(data)

	wants := []string{
		"1. `db.create` — Create the database",
		"2. `db.migrate` — Apply migrations (with: env=test)",
		"3. **parallel:** 2 sub-steps (max_concurrent=2, fail_fast=false) (when: ${param.warm})",
		"   - `db.seed` — Seed initial data",
		"   - `db.vacuum` — Vacuum tables (continue_on_error)",
		"4. **confirm:** All done?",
	}
	for _, w := range wants {
		if !strings.Contains(content, w) {
			t.Errorf("expected substring %q not found in output:\n%s", w, content)
		}
	}
}

// TestWriteCommandMarkdown_Params checks parameter table rendering.
func TestWriteCommandMarkdown_Params(t *testing.T) {
	def := &usercommands.CommandDef{
		ID:        "app.install",
		Group:     "app",
		LocalName: "install",
		Type:      usercommands.CommandTypeShell,
		Cmd:       "composer install",
		Params: map[string]usercommands.ParamDef{
			"env": {
				Type:        usercommands.ParamTypeString,
				Description: "Target environment",
				Default:     "local",
			},
		},
	}

	// Load the embedded i18n store for testing
	store, err := i18n.Load("")
	if err != nil {
		t.Fatalf("failed to load i18n store: %v", err)
	}

	dir := t.TempDir()
	if err := writeCommandMarkdown(def, dir, nil, store, "en"); err != nil {
		t.Fatalf("writeCommandMarkdown: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "install.md"))
	if err != nil {
		t.Fatalf("install.md not written: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "## Parameters") {
		t.Errorf("parameters section missing: %s", content)
	}
	if !strings.Contains(content, "env") {
		t.Errorf("param name missing: %s", content)
	}
	if !strings.Contains(content, "Target environment") {
		t.Errorf("param description missing: %s", content)
	}
}

// TestDocsGenerateCommand_Integration runs the full docs generate command against a temp project.
func TestDocsGenerateCommand_Integration(t *testing.T) {
	tmpDir := t.TempDir()

	// Minimal workspace.yml.
	cfgYAML := `project:
  name: test
  prefix: dwe
services:
  main:
    type: app
    dir: ./services/main
`
	if err := os.WriteFile(filepath.Join(tmpDir, "workspace.yml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	outputDir := filepath.Join(tmpDir, "docs", "reference")

	store, err := i18n.Load("")
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	flags := &cmdctx.RootFlags{
		ConfigPath: filepath.Join(tmpDir, "workspace.yml"),
		I18n:       store,
		Locale:     "en",
	}
	df := &docsFlags{
		output:         outputDir,
		includePrivate: false,
	}

	root := buildDocsTestRoot(flags)

	// Find the docs generate subcommand.
	docsCmd, _, err2 := root.Find([]string{"docs", "generate"})
	if err2 != nil || docsCmd == nil {
		t.Fatal("docs generate command not found in root")
	}

	if err := runDocsGenerate(docsCmd, flags, df); err != nil {
		t.Fatalf("runDocsGenerate: %v", err)
	}

	// Top-level index should be written.
	indexPath := filepath.Join(outputDir, "index.md")
	if _, err := os.Stat(indexPath); err != nil {
		t.Errorf("top-level index.md not written: %v", err)
	}

	// Commands index should be written under the locale subdir (commands/en/).
	commandsIndex := filepath.Join(outputDir, "commands", "en", "index.md")
	if _, err := os.Stat(commandsIndex); err != nil {
		t.Errorf("commands/en/index.md not written: %v", err)
	}
}

// buildTestRegistryForDocs builds a minimal Registry with a public and private command.
// File layout (relative to baseDir):
//
//	services/main.yml          → group "services.main"  → command "services.main.migrate"
//	services/main/db.yml       → group "services.main.db" → command "services.main.db.create"
func buildTestRegistryForDocs(t *testing.T) *usercommands.Registry {
	t.Helper()

	baseDir := t.TempDir()

	servicesDir := filepath.Join(baseDir, "services")
	mainSubDir := filepath.Join(baseDir, "services", "main")
	if err := os.MkdirAll(servicesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mainSubDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Public command: services/main.yml → group "services.main".
	mainYML := `commands:
  migrate:
    type: service_exec
    description: Run database migrations
    service: app-main
    cmd: php artisan migrate
`
	if err := os.WriteFile(filepath.Join(servicesDir, "main.yml"), []byte(mainYML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Private command: services/main/db.yml → group "services.main.db".
	dbYML := `commands:
  create:
    type: service_exec
    description: Create the database
    service: db
    cmd: mysql -e "CREATE DATABASE IF NOT EXISTS laravel"
    private: true
`
	if err := os.WriteFile(filepath.Join(mainSubDir, "db.yml"), []byte(dbYML), 0o644); err != nil {
		t.Fatal(err)
	}

	reg, err := usercommands.LoadRegistry(baseDir)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	return reg
}
