package command

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/deploy"
	"devbox-cli/internal/render"
	"devbox-cli/internal/usercommands"

	"github.com/spf13/cobra"
)

// --- printTreeNodes / printTreeNode ---

func TestPrintTreeNodes_Empty(t *testing.T) {
	var buf bytes.Buffer
	printTreeNodes(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("expected no output for nil nodes, got %q", buf.String())
	}
}

func TestPrintTreeNodes_LeafNode(t *testing.T) {
	nodes := []*render.TreeNode{
		{Label: "db.up", Desc: "Start the database"},
	}
	var buf bytes.Buffer
	printTreeNodes(&buf, nodes)
	out := buf.String()
	if !strings.Contains(out, "db.up") {
		t.Errorf("expected label in output, got: %q", out)
	}
	if !strings.Contains(out, "Start the database") {
		t.Errorf("expected description in output, got: %q", out)
	}
}

func TestPrintTreeNodes_GroupNode(t *testing.T) {
	nodes := []*render.TreeNode{
		{
			Label: "services",
			Children: []*render.TreeNode{
				{Label: "main.migrate"},
			},
		},
	}
	var buf bytes.Buffer
	printTreeNodes(&buf, nodes)
	out := buf.String()
	if !strings.Contains(out, "services") {
		t.Errorf("expected group label, got: %q", out)
	}
	if !strings.Contains(out, "main.migrate") {
		t.Errorf("expected child label, got: %q", out)
	}
}

func TestPrintTreeNodes_NodeWithTags(t *testing.T) {
	nodes := []*render.TreeNode{
		{Label: "app.install", Tags: []string{"service_exec", "private"}},
	}
	var buf bytes.Buffer
	printTreeNodes(&buf, nodes)
	out := buf.String()
	if !strings.Contains(out, "app.install") {
		t.Errorf("expected label, got: %q", out)
	}
}

// --- sourceDotEnv ---

func TestSourceDotEnv_Basic(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	content := "TEST_VAR_SRCENV=hello\n# comment\nANOTHER=world\n"
	if err := os.WriteFile(envFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := deploy.SourceDotEnv(envFile); err != nil {
		t.Fatalf("SourceDotEnv: %v", err)
	}
	if os.Getenv("TEST_VAR_SRCENV") != "hello" {
		t.Errorf("expected TEST_VAR_SRCENV=hello, got %q", os.Getenv("TEST_VAR_SRCENV"))
	}
	t.Cleanup(func() {
		_ = os.Unsetenv("TEST_VAR_SRCENV")
		_ = os.Unsetenv("ANOTHER")
	})
}

func TestSourceDotEnv_QuotedValues(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	content := `QUOTED_VAR="quoted value"` + "\nSINGLE='single'\n"
	if err := os.WriteFile(envFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := deploy.SourceDotEnv(envFile); err != nil {
		t.Fatalf("SourceDotEnv: %v", err)
	}
	if os.Getenv("QUOTED_VAR") != "quoted value" {
		t.Errorf("expected unquoted value, got %q", os.Getenv("QUOTED_VAR"))
	}
	t.Cleanup(func() {
		_ = os.Unsetenv("QUOTED_VAR")
		_ = os.Unsetenv("SINGLE")
	})
}

func TestSourceDotEnv_MissingFile(t *testing.T) {
	err := deploy.SourceDotEnv("/nonexistent/.env")
	if err == nil {
		t.Fatal("expected error for missing .env file")
	}
}

func TestSourceDotEnv_EmptyLines(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	content := "\n\n# only comments\n\n"
	if err := os.WriteFile(envFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := deploy.SourceDotEnv(envFile); err != nil {
		t.Fatalf("SourceDotEnv with only comments/blanks: %v", err)
	}
}

// --- loadCommandRegistry ---

func TestLoadCommandRegistry_NoDirReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "devbox.yml")
	// No devbox/commands/ directory.
	reg, err := loadCommandRegistry(configPath)
	if err != nil {
		t.Fatalf("expected no error for missing commands dir: %v", err)
	}
	if reg == nil {
		t.Fatal("expected non-nil registry")
	}
}

func TestLoadCommandRegistry_WithCommands(t *testing.T) {
	dir := t.TempDir()
	cmdDir := filepath.Join(dir, "devbox", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yml := `commands:
  up:
    type: shell
    description: Start services
    cmd: docker compose up
`
	if err := os.WriteFile(filepath.Join(cmdDir, "db.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "devbox.yml")
	reg, err := loadCommandRegistry(configPath)
	if err != nil {
		t.Fatalf("loadCommandRegistry: %v", err)
	}
	if _, err := reg.Get("db.up"); err != nil {
		t.Errorf("expected db.up command in registry: %v", err)
	}
}

// --- printInspect ---

func TestPrintCommandInspect_BasicCommand(t *testing.T) {
	def := &usercommands.CommandDef{
		ID:          "db.up",
		Type:        usercommands.CommandTypeShell,
		Description: "Start the database",
		Cmd:         "docker compose up db",
	}
	var buf bytes.Buffer
	printInspect(&buf, def, nil)
	out := buf.String()
	for _, want := range []string{"db.up", "shell", "Start the database", "docker compose up db"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output:\n%s", want, out)
		}
	}
}

func TestPrintCommandInspect_PrivateCommand(t *testing.T) {
	def := &usercommands.CommandDef{
		ID:      "db.create",
		Type:    usercommands.CommandTypeShell,
		Private: true,
		Cmd:     "mysql -e 'CREATE DATABASE'",
	}
	var buf bytes.Buffer
	printInspect(&buf, def, nil)
	out := buf.String()
	if !strings.Contains(out, "true") {
		t.Errorf("expected private=true in output:\n%s", out)
	}
}

func TestPrintCommandInspect_ServiceExec(t *testing.T) {
	def := &usercommands.CommandDef{
		ID:      "services.main.migrate",
		Type:    usercommands.CommandTypeServiceExec,
		Service: "app-main",
		Cmd:     "php artisan migrate",
	}
	var buf bytes.Buffer
	printInspect(&buf, def, nil)
	out := buf.String()
	for _, want := range []string{"service_exec", "app-main", "php artisan migrate"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output:\n%s", want, out)
		}
	}
}

func TestPrintCommandInspect_WorkflowWithSteps(t *testing.T) {
	def := &usercommands.CommandDef{
		ID:   "db.bootstrap",
		Type: usercommands.CommandTypeWorkflow,
		Steps: []usercommands.WorkflowStep{
			{Command: "db.create"},
			{Command: "db.migrate", With: map[string]string{"env": "test"}},
			{Confirm: "Are you sure?"},
		},
	}
	var buf bytes.Buffer
	printInspect(&buf, def, nil)
	out := buf.String()
	if !strings.Contains(out, "workflow") {
		t.Errorf("expected workflow type in output:\n%s", out)
	}
	if !strings.Contains(out, "db.create") {
		t.Errorf("expected step db.create:\n%s", out)
	}
	if !strings.Contains(out, "Are you sure?") {
		t.Errorf("expected confirm step:\n%s", out)
	}
}

func TestPrintCommandInspect_WithParams(t *testing.T) {
	def := &usercommands.CommandDef{
		ID:   "app.install",
		Type: usercommands.CommandTypeShell,
		Cmd:  "composer install",
		Params: map[string]usercommands.ParamDef{
			"env": {
				Type:        usercommands.ParamTypeString,
				Description: "Target env",
				Default:     "local",
				Required:    false,
			},
		},
	}
	var buf bytes.Buffer
	printInspect(&buf, def, nil)
	out := buf.String()
	if !strings.Contains(out, "Params") {
		t.Errorf("expected Params section:\n%s", out)
	}
	if !strings.Contains(out, "env") {
		t.Errorf("expected param name:\n%s", out)
	}
	if !strings.Contains(out, "local") {
		t.Errorf("expected default value:\n%s", out)
	}
}

func TestPrintCommandInspect_WithContext(t *testing.T) {
	def := &usercommands.CommandDef{
		ID:   "app.install",
		Type: usercommands.CommandTypeShell,
		Cmd:  "make install",
		Context: map[string]usercommands.ContextDef{
			"app_url": {From: "project.url", Required: true, Env: "APP_URL"},
		},
	}
	var buf bytes.Buffer
	printInspect(&buf, def, nil)
	out := buf.String()
	if !strings.Contains(out, "Context") {
		t.Errorf("expected Context section:\n%s", out)
	}
	if !strings.Contains(out, "app_url") {
		t.Errorf("expected context key:\n%s", out)
	}
}

func TestPrintCommandInspect_WithEnv(t *testing.T) {
	def := &usercommands.CommandDef{
		ID:   "app.run",
		Type: usercommands.CommandTypeShell,
		Cmd:  "php artisan serve",
		Env:  map[string]string{"APP_ENV": "local", "DEBUG": "true"},
	}
	var buf bytes.Buffer
	printInspect(&buf, def, nil)
	out := buf.String()
	if !strings.Contains(out, "Env") {
		t.Errorf("expected Env section:\n%s", out)
	}
	if !strings.Contains(out, "APP_ENV") {
		t.Errorf("expected env key:\n%s", out)
	}
}

func TestPrintCommandInspect_Script(t *testing.T) {
	def := &usercommands.CommandDef{
		ID:   "app.build",
		Type: usercommands.CommandTypeScript,
		Script: &usercommands.ScriptDef{
			Shell:   "bash",
			Run:     "npm run build",
			Cleanup: "rm -rf tmp/",
			Plan:    "echo building",
		},
	}
	var buf bytes.Buffer
	printInspect(&buf, def, nil)
	out := buf.String()
	if !strings.Contains(out, "bash") {
		t.Errorf("expected script shell:\n%s", out)
	}
	if !strings.Contains(out, "npm run build") {
		t.Errorf("expected script run:\n%s", out)
	}
}

func TestPrintCommandInspect_ScriptNilShellDefaultsSh(t *testing.T) {
	def := &usercommands.CommandDef{
		ID:     "app.build",
		Type:   usercommands.CommandTypeScript,
		Script: &usercommands.ScriptDef{Run: "make build"},
	}
	var buf bytes.Buffer
	printInspect(&buf, def, nil)
	out := buf.String()
	if !strings.Contains(out, "sh") {
		t.Errorf("expected default shell 'sh':\n%s", out)
	}
}

// --- genCLIDocs ---

func TestGenCLIDocs_Markdown(t *testing.T) {
	dir := t.TempDir()
	root := NewRootCmd()
	if err := genCLIDocs(root, dir, "markdown"); err != nil {
		t.Fatalf("genCLIDocs markdown: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatal("expected markdown files to be written")
	}
}

func TestGenCLIDocs_Yaml(t *testing.T) {
	dir := t.TempDir()
	root := NewRootCmd()
	if err := genCLIDocs(root, dir, "yaml"); err != nil {
		t.Fatalf("genCLIDocs yaml: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatal("expected yaml files to be written")
	}
}

func TestGenCLIDocs_UnknownFormat(t *testing.T) {
	dir := t.TempDir()
	root := NewRootCmd()
	if err := genCLIDocs(root, dir, "xml"); err == nil {
		t.Fatal("expected error for unknown format")
	}
}

// --- genHiddenCLI* and walkAllCommands ---

func TestWalkAllCommands_VisitsAll(t *testing.T) {
	root := NewRootCmd()
	visited := 0
	err := walkAllCommands(root, func(cmd *cobra.Command) error {
		visited++
		return nil
	})
	if err != nil {
		t.Fatalf("walkAllCommands: %v", err)
	}
	if visited < 5 {
		t.Errorf("expected at least 5 commands visited, got %d", visited)
	}
}

func TestGenHiddenCLIMarkdown_NoHidden(t *testing.T) {
	dir := t.TempDir()
	// Build a simple command with no hidden subusercommands.
	root := NewRootCmd()
	if err := genHiddenCLIMarkdown(root, dir); err != nil {
		t.Fatalf("genHiddenCLIMarkdown: %v", err)
	}
}

func TestGenHiddenCLIYaml_NoHidden(t *testing.T) {
	dir := t.TempDir()
	root := NewRootCmd()
	if err := genHiddenCLIYaml(root, dir); err != nil {
		t.Fatalf("genHiddenCLIYaml: %v", err)
	}
}

func TestGenHiddenCLIMan_NoHidden(t *testing.T) {
	dir := t.TempDir()
	root := NewRootCmd()
	if err := genHiddenCLIMan(root, dir); err != nil {
		t.Fatalf("genHiddenCLIMan: %v", err)
	}
}

// --- walkAllCommands error propagation ---

func TestWalkAllCommands_PropagatesError(t *testing.T) {
	root := NewRootCmd()
	sentinel := os.ErrNotExist
	err := walkAllCommands(root, func(cmd *cobra.Command) error {
		return sentinel
	})
	if err != sentinel {
		t.Errorf("expected sentinel error, got: %v", err)
	}
}

// --- genCLIDocs man format ---

func TestGenCLIDocs_Man(t *testing.T) {
	dir := t.TempDir()
	root := NewRootCmd()
	if err := genCLIDocs(root, dir, "man"); err != nil {
		t.Fatalf("genCLIDocs man: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatal("expected man page files to be written")
	}
}

// --- genHiddenCLI* with actual hidden commands ---

func TestGenHiddenCLIMarkdown_WithHiddenCommands(t *testing.T) {
	dir := t.TempDir()
	root := NewRootCmd()
	// The 'print' subgroup contains hidden commands (print success/warning/etc.).
	// genHiddenCLIMarkdown should write .md for each hidden command.
	if err := genHiddenCLIMarkdown(root, dir); err != nil {
		t.Fatalf("genHiddenCLIMarkdown: %v", err)
	}
	// At least some files should be written if there are hidden usercommands.
	entries, _ := os.ReadDir(dir)
	t.Logf("written %d files for hidden markdown", len(entries))
}

func TestGenHiddenCLIYaml_WithHiddenCommands(t *testing.T) {
	dir := t.TempDir()
	root := NewRootCmd()
	if err := genHiddenCLIYaml(root, dir); err != nil {
		t.Fatalf("genHiddenCLIYaml: %v", err)
	}
}

func TestGenHiddenCLIMan_WithHiddenCommands(t *testing.T) {
	dir := t.TempDir()
	root := NewRootCmd()
	if err := genHiddenCLIMan(root, dir); err != nil {
		t.Fatalf("genHiddenCLIMan: %v", err)
	}
}

// --- newCommandListCmd RunE with temp project ---

// makeMinimalProject creates a minimal devbox.yml, docker.yml, and optional commands dir.
func makeMinimalProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	devboxYML := `project:
  name: test
  prefix: devbox
`
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"), []byte(devboxYML), 0o644); err != nil {
		t.Fatal(err)
	}
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	svcDir := filepath.Join(devboxDir, "services", "main")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte("type: app\ndir: ./services/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dockerYML := "project_name: devbox-test\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "docker.yml"), []byte(dockerYML), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCommandListCmd_RunE_NoCommands(t *testing.T) {
	dir := makeMinimalProject(t)
	flags := &rootFlags{configPath: filepath.Join(dir, "devbox.yml")}
	cmd := newCommandListCmd(flags)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No commands found") {
		t.Errorf("expected 'No commands found' for empty project, got: %q", buf.String())
	}
}

func TestCommandListCmd_RunE_WithCommands(t *testing.T) {
	dir := makeMinimalProject(t)
	cmdDir := filepath.Join(dir, "devbox", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yml := `commands:
  up:
    type: shell
    description: Start services
    cmd: docker compose up
`
	if err := os.WriteFile(filepath.Join(cmdDir, "db.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	flags := &rootFlags{configPath: filepath.Join(dir, "devbox.yml")}
	cmd := newCommandListCmd(flags)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCommandListCmd_RunE_WithGroupFilter(t *testing.T) {
	dir := makeMinimalProject(t)
	cmdDir := filepath.Join(dir, "devbox", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yml := `commands:
  up:
    type: shell
    cmd: echo up
`
	if err := os.WriteFile(filepath.Join(cmdDir, "db.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	flags := &rootFlags{configPath: filepath.Join(dir, "devbox.yml")}
	cmd := newCommandListCmd(flags)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	// filter by nonexistent group → "No commands found"
	if err := cmd.RunE(cmd, []string{"nonexistent"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No commands found") {
		t.Errorf("expected 'No commands found' for empty filter, got: %q", buf.String())
	}
}

func TestCommandInspectCmd_RunE_DirectID(t *testing.T) {
	dir := makeMinimalProject(t)
	cmdDir := filepath.Join(dir, "devbox", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yml := `commands:
  up:
    type: shell
    description: Start services
    cmd: docker compose up
`
	if err := os.WriteFile(filepath.Join(cmdDir, "db.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	flags := &rootFlags{configPath: filepath.Join(dir, "devbox.yml")}
	cmd := newCommandCmd(flags)
	if err := cmd.Flags().Set("inspect", "true"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, []string{"db.up"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "db.up") {
		t.Errorf("expected command ID in output, got: %q", buf.String())
	}
}

// --- newDeployPlanCmd RunE with temp project ---

func TestDeployPlanCmd_RunE_EmptyPlan(t *testing.T) {
	dir := makeMinimalProject(t)
	flags := &rootFlags{configPath: filepath.Join(dir, "devbox.yml")}
	cmd := newDeployPlanCmd(flags)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Title rendering exists (RenderSectionTitle returns ANSI-styled text but
	// always contains the plain text payload).
	if !strings.Contains(buf.String(), "Deploy plan") {
		t.Errorf("expected plan output to contain 'Deploy plan', got: %q", buf.String())
	}
}

func TestDeployPlanCmd_RunE_ShellFormat(t *testing.T) {
	dir := makeMinimalProject(t)
	flags := &rootFlags{configPath: filepath.Join(dir, "devbox.yml")}
	cmd := newDeployPlanCmd(flags)
	if err := cmd.Flags().Set("format", "shell"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeployPlanCmd_RunE_UnknownService(t *testing.T) {
	dir := makeMinimalProject(t)
	flags := &rootFlags{configPath: filepath.Join(dir, "devbox.yml")}
	cmd := newDeployPlanCmd(flags)
	if err := cmd.Flags().Set("service", "nonexistent"); err != nil {
		t.Fatal(err)
	}
	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for unknown service")
	}
}

// --- compose commands RunE ---

func TestComposeFilesCmd_RunE(t *testing.T) {
	dir := makeMinimalProject(t)
	// Create a minimal compose.yaml so ComposeFiles() returns at least one.
	composeYAML := "services:\n  web:\n    image: nginx\n"
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(composeYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	flags := &rootFlags{configPath: filepath.Join(dir, "devbox.yml")}
	cmd := newComposeFilesCmd(flags)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestComposeFilesCmd_RunE_InvalidConfig(t *testing.T) {
	flags := &rootFlags{configPath: "/nonexistent/devbox.yml"}
	cmd := newComposeFilesCmd(flags)
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatal("expected error for invalid config path")
	}
}

func TestComposeArgvCmd_RunE(t *testing.T) {
	dir := makeMinimalProject(t)
	flags := &rootFlags{configPath: filepath.Join(dir, "devbox.yml")}
	cmd := newComposeArgvCmd(flags)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, []string{"up"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "docker compose") {
		t.Errorf("expected 'docker compose' in output, got: %q", buf.String())
	}
}

func TestComposeArgvCmd_RunE_InvalidConfig(t *testing.T) {
	flags := &rootFlags{configPath: "/nonexistent/devbox.yml"}
	cmd := newComposeArgvCmd(flags)
	if err := cmd.RunE(cmd, []string{"up"}); err == nil {
		t.Fatal("expected error for invalid config path")
	}
}

// --- runRenderEnv ---

func TestRunRenderEnv_ToFile(t *testing.T) {
	dir := makeMinimalProject(t)
	outputPath := filepath.Join(dir, ".env")
	flags := &rootFlags{configPath: filepath.Join(dir, "devbox.yml")}
	if err := runRenderEnv(flags, outputPath); err != nil {
		t.Fatalf("runRenderEnv: %v", err)
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Errorf("expected .env file to be written: %v", err)
	}
}

func TestRunRenderEnv_InvalidConfig(t *testing.T) {
	flags := &rootFlags{configPath: "/nonexistent/devbox.yml"}
	err := runRenderEnv(flags, "")
	if err == nil {
		t.Fatal("expected error for invalid config")
	}
}

// --- newCommandInspectCmd RunE error paths ---

func TestCommandInspectCmd_RunE_NotFoundID(t *testing.T) {
	dir := makeMinimalProject(t)
	flags := &rootFlags{configPath: filepath.Join(dir, "devbox.yml")}
	cmd := newCommandCmd(flags)
	if err := cmd.Flags().Set("inspect", "true"); err != nil {
		t.Fatal(err)
	}
	err := cmd.RunE(cmd, []string{"nonexistent.cmd"})
	if err == nil {
		t.Fatal("expected error for non-existent command ID")
	}
}

// --- newPrint* RunE (success/warning/info) ---

func TestNewPrintSuccessCmd_RunE(t *testing.T) {
	cmd := newPrintSuccessCmd()
	// RunE writes to render.Stdout() which is os.Stdout; just verify no error.
	err := cmd.RunE(cmd, []string{"all good"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewPrintWarningCmd_RunE(t *testing.T) {
	cmd := newPrintWarningCmd()
	err := cmd.RunE(cmd, []string{"watch out"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewPrintInfoCmd_RunE(t *testing.T) {
	cmd := newPrintInfoCmd()
	err := cmd.RunE(cmd, []string{"just info"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
