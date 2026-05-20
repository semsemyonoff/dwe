package model

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func mustParse(t *testing.T, yaml string) *CommandFile {
	t.Helper()
	cf, err := ParseCommandFile([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	return cf
}

// ---------------------------------------------------------------------------
// Type constant tests
// ---------------------------------------------------------------------------

func TestCommandTypeConstants(t *testing.T) {
	types := []CommandType{
		CommandTypeShell,
		CommandTypeScript,
		CommandTypeServiceExec,
		CommandTypeServiceRun,
		CommandTypeWorkflow,
	}
	want := []string{"shell", "script", "service_exec", "service_run", "workflow"}
	for i, ct := range types {
		if string(ct) != want[i] {
			t.Errorf("CommandType[%d] = %q, want %q", i, ct, want[i])
		}
	}
}

func TestParamTypeConstants(t *testing.T) {
	types := []ParamType{ParamTypeString, ParamTypeBool, ParamTypeInt, ParamTypePath}
	want := []string{"string", "bool", "int", "path"}
	for i, pt := range types {
		if string(pt) != want[i] {
			t.Errorf("ParamType[%d] = %q, want %q", i, pt, want[i])
		}
	}
}

func TestUserModeConstants(t *testing.T) {
	if string(UserModeCurrent) != "current" {
		t.Errorf("UserModeCurrent = %q, want current", UserModeCurrent)
	}
	if string(UserModeRoot) != "root" {
		t.Errorf("UserModeRoot = %q, want root", UserModeRoot)
	}
	if string(UserModeInternal) != "internal" {
		t.Errorf("UserModeInternal = %q, want internal", UserModeInternal)
	}
}

func TestExecModeConstants(t *testing.T) {
	modes := []ExecMode{ExecModeExec, ExecModeRun, ExecModeExecOrRun}
	want := []string{"exec", "run", "exec-or-run"}
	for i, m := range modes {
		if string(m) != want[i] {
			t.Errorf("ExecMode[%d] = %q, want %q", i, m, want[i])
		}
	}
}

// ---------------------------------------------------------------------------
// YAML unmarshalling — valid cases
// ---------------------------------------------------------------------------

func TestParseCommandFile_GroupMeta(t *testing.T) {
	yaml := `
group:
  title: "Database"
  description: "Database management commands"
commands:
  wait:
    type: shell
    cmd: echo waiting
`
	cf := mustParse(t, yaml)
	if cf.Group.Title != "Database" {
		t.Errorf("Group.Title = %q, want %q", cf.Group.Title, "Database")
	}
	if cf.Group.Description != "Database management commands" {
		t.Errorf("Group.Description = %q, want %q", cf.Group.Description, "Database management commands")
	}
}

func TestParseCommandFile_CommandType_Cmd(t *testing.T) {
	yaml := `
commands:
  install:
    type: shell
    description: Install dependencies
    cmd: composer install --no-interaction
    workdir: /var/www/html
    env:
      COMPOSER_HOME: /tmp/composer
`
	cf := mustParse(t, yaml)
	cmd, ok := cf.Commands["install"]
	if !ok {
		t.Fatal("command 'install' not found")
	}
	if cmd.Type != CommandTypeShell {
		t.Errorf("Type = %q, want shell", cmd.Type)
	}
	if cmd.Cmd != "composer install --no-interaction" {
		t.Errorf("Cmd = %q, unexpected", cmd.Cmd)
	}
	if cmd.Workdir != "/var/www/html" {
		t.Errorf("Workdir = %q, unexpected", cmd.Workdir)
	}
	if cmd.Env["COMPOSER_HOME"] != "/tmp/composer" {
		t.Errorf("Env[COMPOSER_HOME] = %q, unexpected", cmd.Env["COMPOSER_HOME"])
	}
}

func TestParseCommandFile_CommandType_Argv(t *testing.T) {
	yaml := `
commands:
  echo:
    type: shell
    argv:
      - echo
      - hello world
`
	cf := mustParse(t, yaml)
	cmd := cf.Commands["echo"]
	if len(cmd.Argv) != 2 {
		t.Fatalf("Argv len = %d, want 2", len(cmd.Argv))
	}
	if cmd.Argv[1] != "hello world" {
		t.Errorf("Argv[1] = %q, want 'hello world'", cmd.Argv[1])
	}
}

func TestParseCommandFile_ConfirmationFields(t *testing.T) {
	yaml := `
commands:
  reset:
    type: shell
    confirmation: true
    confirmation_text: "Drop local data?"
    cmd: echo reset
`
	cf := mustParse(t, yaml)
	cmd := cf.Commands["reset"]
	if !cmd.Confirmation {
		t.Error("Confirmation should be true")
	}
	if cmd.ConfirmationText != "Drop local data?" {
		t.Errorf("ConfirmationText = %q, want custom text", cmd.ConfirmationText)
	}
	if cmd.EffectiveConfirmationText() != "Drop local data?" {
		t.Errorf("EffectiveConfirmationText() = %q", cmd.EffectiveConfirmationText())
	}
}

func TestParseCommandFile_Messages(t *testing.T) {
	yaml := `
commands:
  create:
    type: shell
    messages:
      success: "Created ${param.name}"
      error: "Failed ${param.name}"
    cmd: echo create
`
	cf := mustParse(t, yaml)
	cmd := cf.Commands["create"]
	if cmd.Messages.Success != "Created ${param.name}" {
		t.Errorf("Messages.Success = %q", cmd.Messages.Success)
	}
	if cmd.Messages.Error != "Failed ${param.name}" {
		t.Errorf("Messages.Error = %q", cmd.Messages.Error)
	}
}

func TestCommandDef_EffectiveConfirmationText_Default(t *testing.T) {
	cmd := &CommandDef{Confirmation: true}
	if got := cmd.EffectiveConfirmationText(); got != DefaultConfirmationText {
		t.Errorf("EffectiveConfirmationText() = %q, want %q", got, DefaultConfirmationText)
	}
}

func TestParseCommandFile_ServiceExec(t *testing.T) {
	yaml := `
commands:
  migrate:
    type: service_exec
    description: Run migrations
    service: app-main
    user: current
    workdir: /var/www/html
    mode: exec-or-run
    cmd: php artisan migrate --force
    params:
      fresh:
        type: bool
        description: Drop all tables first
        default: "false"
        env: MIGRATE_FRESH
    context:
      db_host:
        from: runtime.db.host
        required: true
        env: DB_HOST
`
	cf := mustParse(t, yaml)
	cmd := cf.Commands["migrate"]
	if cmd.Type != CommandTypeServiceExec {
		t.Errorf("Type = %q, want service_exec", cmd.Type)
	}
	if cmd.Service != "app-main" {
		t.Errorf("Service = %q, want app-main", cmd.Service)
	}
	if cmd.User != UserModeCurrent {
		t.Errorf("User = %q, want current", cmd.User)
	}
	if cmd.Mode != ExecModeExecOrRun {
		t.Errorf("Mode = %q, want exec-or-run", cmd.Mode)
	}
	p, ok := cmd.Params["fresh"]
	if !ok {
		t.Fatal("param 'fresh' not found")
	}
	if p.Type != ParamTypeBool {
		t.Errorf("Params[fresh].Type = %q, want bool", p.Type)
	}
	if p.Env != "MIGRATE_FRESH" {
		t.Errorf("Params[fresh].Env = %q, want MIGRATE_FRESH", p.Env)
	}
	ctx, ok := cmd.Context["db_host"]
	if !ok {
		t.Fatal("context 'db_host' not found")
	}
	if ctx.From != "runtime.db.host" {
		t.Errorf("Context[db_host].From = %q, unexpected", ctx.From)
	}
	if !ctx.Required {
		t.Error("Context[db_host].Required should be true")
	}
}

func TestParseCommandFile_ServiceRun(t *testing.T) {
	yaml := `
commands:
  create-db:
    type: service_run
    service: app-main
    cmd: php artisan db:create
`
	cf := mustParse(t, yaml)
	cmd := cf.Commands["create-db"]
	if cmd.Type != CommandTypeServiceRun {
		t.Errorf("Type = %q, want service_run", cmd.Type)
	}
}

func TestParseCommandFile_ScriptSimpleMode(t *testing.T) {
	yaml := `
commands:
  setup:
    type: script
    script:
      path: scripts/setup.sh
      shell: bash
`
	cf := mustParse(t, yaml)
	cmd := cf.Commands["setup"]
	if cmd.Type != CommandTypeScript {
		t.Errorf("Type = %q, want script", cmd.Type)
	}
	if cmd.Script == nil {
		t.Fatal("Script block is nil")
	}
	if cmd.Script.Path != "scripts/setup.sh" {
		t.Errorf("Script.Path = %q, unexpected", cmd.Script.Path)
	}
	if cmd.Script.Shell != "bash" {
		t.Errorf("Script.Shell = %q, want bash", cmd.Script.Shell)
	}
}

func TestParseCommandFile_ScriptPhasedMode(t *testing.T) {
	yaml := `
commands:
  deploy:
    type: script
    script:
      shell: sh
      plan: scripts/deploy-plan.sh
      run: scripts/deploy-run.sh
      cleanup: scripts/deploy-cleanup.sh
`
	cf := mustParse(t, yaml)
	cmd := cf.Commands["deploy"]
	s := cmd.Script
	if s == nil {
		t.Fatal("Script block is nil")
	}
	if s.Plan != "scripts/deploy-plan.sh" {
		t.Errorf("Script.Plan = %q, unexpected", s.Plan)
	}
	if s.Run != "scripts/deploy-run.sh" {
		t.Errorf("Script.Run = %q, unexpected", s.Run)
	}
	if s.Cleanup != "scripts/deploy-cleanup.sh" {
		t.Errorf("Script.Cleanup = %q, unexpected", s.Cleanup)
	}
}

func TestParseCommandFile_Workflow(t *testing.T) {
	yaml := `
commands:
  bootstrap:
    type: workflow
    description: Full bootstrap sequence
    steps:
      - command: services.main.db-create
      - confirm: "Proceed with migration?"
      - command: services.main.migrate
        with:
          fresh: "true"
`
	cf := mustParse(t, yaml)
	cmd := cf.Commands["bootstrap"]
	if cmd.Type != CommandTypeWorkflow {
		t.Errorf("Type = %q, want workflow", cmd.Type)
	}
	if len(cmd.Steps) != 3 {
		t.Fatalf("Steps len = %d, want 3", len(cmd.Steps))
	}
	if cmd.Steps[0].Command != "services.main.db-create" {
		t.Errorf("Steps[0].Command = %q, unexpected", cmd.Steps[0].Command)
	}
	if cmd.Steps[1].Confirm != "Proceed with migration?" {
		t.Errorf("Steps[1].Confirm = %q, unexpected", cmd.Steps[1].Confirm)
	}
	if cmd.Steps[2].With["fresh"] != "true" {
		t.Errorf("Steps[2].With[fresh] = %q, want true", cmd.Steps[2].With["fresh"])
	}
}

func TestParseCommandFile_PrivateCommand(t *testing.T) {
	yaml := `
commands:
  internal-task:
    type: shell
    private: true
    cmd: echo internal
`
	cf := mustParse(t, yaml)
	cmd := cf.Commands["internal-task"]
	if !cmd.Private {
		t.Error("Private should be true")
	}
}

func TestParseCommandFile_RunnerOverride(t *testing.T) {
	yaml := `
commands:
  root-task:
    type: service_exec
    service: app-main
    cmd: whoami
    runner:
      user: root
      workdir: /
      mode: exec
`
	cf := mustParse(t, yaml)
	cmd := cf.Commands["root-task"]
	if cmd.Runner == nil {
		t.Fatal("Runner is nil")
	}
	if cmd.Runner.User != UserModeRoot {
		t.Errorf("Runner.User = %q, want root", cmd.Runner.User)
	}
	if cmd.Runner.Mode != ExecModeExec {
		t.Errorf("Runner.Mode = %q, want exec", cmd.Runner.Mode)
	}
}

func TestParseCommandFile_ParamDefaultFrom(t *testing.T) {
	yaml := `
commands:
  connect:
    type: service_exec
    service: app-main
    cmd: mysql
    params:
      db:
        type: string
        default_from: runtime.db.database
        required: false
`
	cf := mustParse(t, yaml)
	cmd := cf.Commands["connect"]
	p := cmd.Params["db"]
	if p.DefaultFrom != "runtime.db.database" {
		t.Errorf("Params[db].DefaultFrom = %q, unexpected", p.DefaultFrom)
	}
}

func TestParseCommandFile_EmptyFile(t *testing.T) {
	cf := mustParse(t, "{}")
	if len(cf.Commands) != 0 {
		t.Errorf("Commands len = %d, want 0", len(cf.Commands))
	}
}

// ---------------------------------------------------------------------------
// Validation — valid cases
// ---------------------------------------------------------------------------

func TestValidate_CommandShellCmd(t *testing.T) {
	cf := mustParse(t, `
commands:
  test:
    type: shell
    cmd: echo ok
`)
	cf.Commands["test"] = setID(cf.Commands["test"], "grp.test")
	cmd := cf.Commands["test"]
	if err := cmd.Validate(); err != nil {
		t.Errorf("unexpected validation error: %v", err)
	}
}

func TestValidate_CommandShellArgv(t *testing.T) {
	cmd := CommandDef{Type: CommandTypeShell, Argv: []string{"ls", "-la"}, ID: "g.ls"}
	if err := cmd.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_ScriptSimple(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.s",
		Script: &ScriptDef{Path: "scripts/s.sh"},
	}
	if err := cmd.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_ScriptPhased(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.s",
		Script: &ScriptDef{Run: "scripts/run.sh", Cleanup: "scripts/cleanup.sh"},
	}
	if err := cmd.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_ServiceExec(t *testing.T) {
	cmd := CommandDef{
		Type:    CommandTypeServiceExec,
		ID:      "g.e",
		Service: "app-main",
		Cmd:     "php artisan migrate",
	}
	if err := cmd.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_ServiceExecWithRunnerOverride(t *testing.T) {
	// service set only via runner override
	cmd := CommandDef{
		Type: CommandTypeServiceExec,
		ID:   "g.e",
		Cmd:  "whoami",
		Runner: &RunnerDef{
			Service: "app-main",
			User:    UserModeRoot,
		},
	}
	if err := cmd.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_Workflow(t *testing.T) {
	cmd := CommandDef{
		Type: CommandTypeWorkflow,
		ID:   "g.w",
		Steps: []WorkflowStep{
			{Command: "g.step1"},
			{Confirm: "Continue?"},
		},
	}
	if err := cmd.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Validation — invalid/conflicting field combinations
// ---------------------------------------------------------------------------

func TestValidate_MissingType(t *testing.T) {
	cmd := CommandDef{ID: "g.bad", Cmd: "echo"}
	err := cmd.Validate()
	if err == nil {
		t.Error("expected error for missing type")
	}
}

func TestValidate_UnknownType(t *testing.T) {
	cmd := CommandDef{ID: "g.bad", Type: "unknown"}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("expected unknown type error, got %v", err)
	}
}

func TestValidate_Command_CmdAndArgvBothSet(t *testing.T) {
	cmd := CommandDef{
		Type: CommandTypeShell,
		ID:   "g.bad",
		Cmd:  "echo",
		Argv: []string{"echo"},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually exclusive error, got %v", err)
	}
}

func TestValidate_Command_NeitherCmdNorArgv(t *testing.T) {
	cmd := CommandDef{Type: CommandTypeShell, ID: "g.bad"}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "one of cmd or argv") {
		t.Errorf("expected 'one of cmd or argv' error, got %v", err)
	}
}

func TestValidate_Command_ScriptFieldSet(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeShell,
		ID:     "g.bad",
		Cmd:    "echo",
		Script: &ScriptDef{Path: "s.sh"},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "script field") {
		t.Errorf("expected script field error, got %v", err)
	}
}

func TestValidate_Command_StepsFieldSet(t *testing.T) {
	cmd := CommandDef{
		Type:  CommandTypeShell,
		ID:    "g.bad",
		Cmd:   "echo",
		Steps: []WorkflowStep{{Command: "g.x"}},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "steps field") {
		t.Errorf("expected steps field error, got %v", err)
	}
}

func TestValidate_Command_ServiceFieldSet(t *testing.T) {
	cmd := CommandDef{
		Type:    CommandTypeShell,
		ID:      "g.bad",
		Cmd:     "echo",
		Service: "app-main",
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "service field") {
		t.Errorf("expected service field error, got %v", err)
	}
}

func TestValidate_Script_NoScriptBlock(t *testing.T) {
	cmd := CommandDef{Type: CommandTypeScript, ID: "g.bad"}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "script block is required") {
		t.Errorf("expected script block required error, got %v", err)
	}
}

func TestValidate_Script_PathAndRunBothSet(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.bad",
		Script: &ScriptDef{Path: "s.sh", Run: "r.sh"},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually exclusive error, got %v", err)
	}
}

func TestValidate_Script_PathWithPlanOrCleanup(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.bad",
		Script: &ScriptDef{Path: "s.sh", Plan: "p.sh"},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "plan/cleanup") {
		t.Errorf("expected plan/cleanup error, got %v", err)
	}
}

func TestValidate_Script_NeitherPathNorRun(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.bad",
		Script: &ScriptDef{Shell: "bash"},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "one of path or run") {
		t.Errorf("expected 'one of path or run' error, got %v", err)
	}
}

func TestValidate_ServiceExec_NoService(t *testing.T) {
	cmd := CommandDef{Type: CommandTypeServiceExec, ID: "g.bad", Cmd: "echo"}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "service is required") {
		t.Errorf("expected service required error, got %v", err)
	}
}

func TestValidate_ServiceExec_CmdAndArgvBothSet(t *testing.T) {
	cmd := CommandDef{
		Type:    CommandTypeServiceExec,
		ID:      "g.bad",
		Service: "app-main",
		Cmd:     "echo",
		Argv:    []string{"echo"},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually exclusive error, got %v", err)
	}
}

func TestValidate_ServiceExec_NoCmdOrArgv(t *testing.T) {
	cmd := CommandDef{
		Type:    CommandTypeServiceExec,
		ID:      "g.bad",
		Service: "app-main",
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "one of cmd or argv") {
		t.Errorf("expected cmd/argv required error, got %v", err)
	}
}

func TestValidate_ServiceRun_ModeMismatch(t *testing.T) {
	cmd := CommandDef{
		Type:    CommandTypeServiceRun,
		ID:      "g.bad",
		Service: "app-main",
		Cmd:     "echo",
		Mode:    ExecModeExec, // invalid for service_run
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "mode is not applicable") {
		t.Errorf("expected mode not applicable error, got %v", err)
	}
}

func TestValidate_Workflow_NoSteps(t *testing.T) {
	cmd := CommandDef{Type: CommandTypeWorkflow, ID: "g.bad"}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "steps is required") {
		t.Errorf("expected steps required error, got %v", err)
	}
}

func TestValidate_Workflow_CmdFieldSet(t *testing.T) {
	cmd := CommandDef{
		Type:  CommandTypeWorkflow,
		ID:    "g.bad",
		Cmd:   "echo",
		Steps: []WorkflowStep{{Command: "g.x"}},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "cmd/argv") {
		t.Errorf("expected cmd/argv error, got %v", err)
	}
}

func TestValidate_Workflow_ServiceFieldSet(t *testing.T) {
	cmd := CommandDef{
		Type:    CommandTypeWorkflow,
		ID:      "g.bad",
		Service: "app-main",
		Steps:   []WorkflowStep{{Command: "g.x"}},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "service field") {
		t.Errorf("expected service field error, got %v", err)
	}
}

func TestValidate_WorkflowStep_BothCommandAndConfirm(t *testing.T) {
	step := WorkflowStep{Command: "g.x", Confirm: "Sure?"}
	err := step.Validate()
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually exclusive error, got %v", err)
	}
}

func TestValidate_WorkflowStep_NeitherCommandNorConfirm(t *testing.T) {
	step := WorkflowStep{}
	err := step.Validate()
	if err == nil {
		t.Error("expected error for empty workflow step")
	}
}

func TestValidate_WorkflowStep_ConfirmWithWith(t *testing.T) {
	step := WorkflowStep{Confirm: "Sure?", With: map[string]string{"k": "v"}}
	err := step.Validate()
	if err == nil || !strings.Contains(err.Error(), "with may not be combined") {
		t.Errorf("expected 'with may not be combined' error, got %v", err)
	}
}

func TestValidate_WorkflowStep_ContinueOnErrorWithConfirm(t *testing.T) {
	step := WorkflowStep{Confirm: "Sure?", ContinueOnError: true}
	err := step.Validate()
	if err == nil || !strings.Contains(err.Error(), "continue_on_error") {
		t.Errorf("expected 'continue_on_error' error, got %v", err)
	}
}

func TestParseYAML_WorkflowStep_WithWhen(t *testing.T) {
	yaml := `
commands:
  bootstrap:
    type: workflow
    steps:
      - command: db.create
        when: "${param.migrate}"
      - command: db.seed
`
	cf := mustParse(t, yaml)
	cmd := cf.Commands["bootstrap"]
	if len(cmd.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(cmd.Steps))
	}
	if cmd.Steps[0].When != "${param.migrate}" {
		t.Errorf("Step[0].When = %q, want %q", cmd.Steps[0].When, "${param.migrate}")
	}
	if cmd.Steps[1].When != "" {
		t.Errorf("Step[1].When = %q, want empty", cmd.Steps[1].When)
	}
}

func TestParseYAML_WorkflowStep_WithContinueOnError(t *testing.T) {
	yaml := `
commands:
  cleanup:
    type: workflow
    steps:
      - command: logs.clean
        continue_on_error: true
      - command: db.reset
`
	cf := mustParse(t, yaml)
	cmd := cf.Commands["cleanup"]
	if len(cmd.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(cmd.Steps))
	}
	if !cmd.Steps[0].ContinueOnError {
		t.Errorf("Step[0].ContinueOnError = %v, want true", cmd.Steps[0].ContinueOnError)
	}
	if cmd.Steps[1].ContinueOnError {
		t.Errorf("Step[1].ContinueOnError = %v, want false", cmd.Steps[1].ContinueOnError)
	}
}

func TestParseYAML_WorkflowStep_WhenAndContinueOnError(t *testing.T) {
	yaml := `
commands:
  deploy:
    type: workflow
    steps:
      - command: app.build
        when: "${param.rebuild}"
        continue_on_error: true
`
	cf := mustParse(t, yaml)
	cmd := cf.Commands["deploy"]
	if len(cmd.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(cmd.Steps))
	}
	step := cmd.Steps[0]
	if step.When != "${param.rebuild}" {
		t.Errorf("When = %q, want %q", step.When, "${param.rebuild}")
	}
	if !step.ContinueOnError {
		t.Errorf("ContinueOnError = %v, want true", step.ContinueOnError)
	}
}

func TestValidate_CommandFile_MultipleErrors(t *testing.T) {
	cf := mustParse(t, `
commands:
  bad1:
    type: command
  bad2:
    type: workflow
`)
	cf.FilePath = "test.yml"
	for name, cmd := range cf.Commands {
		cmd.ID = "g." + name
		cf.Commands[name] = cmd
	}
	err := cf.Validate()
	if err == nil {
		t.Error("expected validation errors")
	}
}

// ---------------------------------------------------------------------------
// WorkflowParallel tests
// ---------------------------------------------------------------------------

func TestParseYAML_WorkflowParallel_Valid(t *testing.T) {
	yaml := `
commands:
  fanout:
    type: workflow
    steps:
      - parallel:
          max_concurrent: 4
          fail_fast: false
          steps:
            - command: g.a
            - command: g.b
              with:
                k: v
`
	cf := mustParse(t, yaml)
	cmd := cf.Commands["fanout"]
	if len(cmd.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(cmd.Steps))
	}
	p := cmd.Steps[0].Parallel
	if p == nil {
		t.Fatal("expected Parallel to be non-nil")
	}
	if p.MaxConcurrent != 4 {
		t.Errorf("MaxConcurrent = %d, want 4", p.MaxConcurrent)
	}
	if p.FailFast == nil || *p.FailFast {
		t.Errorf("FailFast = %v, want explicit false", p.FailFast)
	}
	if len(p.Steps) != 2 {
		t.Fatalf("expected 2 sub-steps, got %d", len(p.Steps))
	}
	cmd.ID = "g.fanout"
	if err := cmd.Validate(); err != nil {
		t.Errorf("unexpected validate err: %v", err)
	}
}

func TestParseYAML_WorkflowParallel_FailFastDefaultsNil(t *testing.T) {
	yaml := `
commands:
  fan:
    type: workflow
    steps:
      - parallel:
          steps:
            - command: g.a
            - command: g.b
`
	cf := mustParse(t, yaml)
	p := cf.Commands["fan"].Steps[0].Parallel
	if p == nil {
		t.Fatal("expected Parallel non-nil")
	}
	if p.FailFast != nil {
		t.Errorf("FailFast = %v, want nil (default)", *p.FailFast)
	}
}

func TestParseYAML_WorkflowParallel_FailFastTrueExplicit(t *testing.T) {
	yaml := `
commands:
  fan:
    type: workflow
    steps:
      - parallel:
          fail_fast: true
          steps:
            - command: g.a
            - command: g.b
`
	cf := mustParse(t, yaml)
	p := cf.Commands["fan"].Steps[0].Parallel
	if p.FailFast == nil || !*p.FailFast {
		t.Errorf("FailFast = %v, want explicit true", p.FailFast)
	}
}

func TestValidate_WorkflowStep_ParallelAndCommand(t *testing.T) {
	step := WorkflowStep{
		Command: "g.x",
		Parallel: &WorkflowParallel{Steps: []WorkflowStep{
			{Command: "g.a"}, {Command: "g.b"},
		}},
	}
	err := step.Validate()
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually exclusive, got %v", err)
	}
}

func TestValidate_WorkflowStep_ParallelAndConfirm(t *testing.T) {
	step := WorkflowStep{
		Confirm: "go?",
		Parallel: &WorkflowParallel{Steps: []WorkflowStep{
			{Command: "g.a"}, {Command: "g.b"},
		}},
	}
	err := step.Validate()
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually exclusive, got %v", err)
	}
}

func TestValidate_WorkflowStep_ParallelWithWith(t *testing.T) {
	step := WorkflowStep{
		With: map[string]string{"k": "v"},
		Parallel: &WorkflowParallel{Steps: []WorkflowStep{
			{Command: "g.a"}, {Command: "g.b"},
		}},
	}
	err := step.Validate()
	if err == nil || !strings.Contains(err.Error(), "with may not be combined with parallel") {
		t.Errorf("expected with+parallel error, got %v", err)
	}
}

func TestValidate_WorkflowStep_ParallelStepsTooFew(t *testing.T) {
	tests := []struct {
		name  string
		steps []WorkflowStep
	}{
		{"empty", []WorkflowStep{}},
		{"one", []WorkflowStep{{Command: "g.a"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			step := WorkflowStep{Parallel: &WorkflowParallel{Steps: tc.steps}}
			err := step.Validate()
			if err == nil || !strings.Contains(err.Error(), "at least 2 sub-steps") {
				t.Errorf("expected at-least-2 error, got %v", err)
			}
		})
	}
}

func TestValidate_WorkflowStep_NestedParallelRejected(t *testing.T) {
	step := WorkflowStep{
		Parallel: &WorkflowParallel{Steps: []WorkflowStep{
			{Command: "g.a"},
			{Parallel: &WorkflowParallel{Steps: []WorkflowStep{
				{Command: "g.x"}, {Command: "g.y"},
			}}},
		}},
	}
	err := step.Validate()
	if err == nil || !strings.Contains(err.Error(), "nested parallel") {
		t.Errorf("expected nested-parallel error, got %v", err)
	}
}

func TestValidate_WorkflowStep_ConfirmInsideParallelRejected(t *testing.T) {
	step := WorkflowStep{
		Parallel: &WorkflowParallel{Steps: []WorkflowStep{
			{Command: "g.a"},
			{Confirm: "Sure?"},
		}},
	}
	err := step.Validate()
	if err == nil || !strings.Contains(err.Error(), "confirm is not allowed inside a parallel group") {
		t.Errorf("expected confirm-in-parallel error, got %v", err)
	}
}

func TestValidate_WorkflowStep_ParallelSubStepCommandRequired(t *testing.T) {
	step := WorkflowStep{
		Parallel: &WorkflowParallel{Steps: []WorkflowStep{
			{Command: "g.a"},
			{}, // empty sub-step
		}},
	}
	err := step.Validate()
	if err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Errorf("expected command-required error, got %v", err)
	}
}

func TestValidate_Workflow_WithParallelStep(t *testing.T) {
	cmd := CommandDef{
		Type: CommandTypeWorkflow,
		ID:   "g.fanout",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{Steps: []WorkflowStep{
				{Command: "g.a"}, {Command: "g.b"},
			}}},
		},
	}
	if err := cmd.Validate(); err != nil {
		t.Errorf("expected workflow with parallel step to validate, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// File spec validation tests
// ---------------------------------------------------------------------------

func TestFileAccessConstants(t *testing.T) {
	modes := []FileAccess{FileAccessRead, FileAccessWrite, FileAccessReadWrite}
	want := []string{"read", "write", "read_write"}
	for i, m := range modes {
		if string(m) != want[i] {
			t.Errorf("FileAccess[%d] = %q, want %q", i, m, want[i])
		}
	}
}

func TestFileSortConstants(t *testing.T) {
	sorts := []FileSort{
		FileSortNameAsc, FileSortNameDesc,
		FileSortModtimeAsc, FileSortModtimeDesc,
	}
	want := []string{"name_asc", "name_desc", "modtime_asc", "modtime_desc"}
	for i, s := range sorts {
		if string(s) != want[i] {
			t.Errorf("FileSort[%d] = %q, want %q", i, s, want[i])
		}
	}
}

func TestFileOnErrorConstants(t *testing.T) {
	errs := []FileOnError{FileOnErrorKeep, FileOnErrorRemove}
	want := []string{"keep", "remove"}
	for i, e := range errs {
		if string(e) != want[i] {
			t.Errorf("FileOnError[%d] = %q, want %q", i, e, want[i])
		}
	}
}

// Test valid file specs with different access modes.
func TestValidate_Files_WriteWithPath(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessWrite,
				Path:   "/tmp/dump.sql",
			},
		},
	}
	if err := cmd.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_Files_ReadWithPath(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"config": {
				Access:   FileAccessRead,
				Path:     "/etc/config.ini",
				Required: true,
			},
		},
	}
	if err := cmd.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_Files_ReadWriteWithPath(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"db": {
				Access: FileAccessReadWrite,
				Path:   "/var/lib/db.sqlite",
			},
		},
	}
	if err := cmd.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_Files_ReadWithCandidates(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessRead,
				Candidates: []FileCandidate{
					{Path: "/tmp/dump.sql"},
					{Glob: "/backups/db_*.sql.gz", Match: ".*", Sort: FileSortNameDesc},
				},
				Required: true,
			},
		},
	}
	if err := cmd.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_Files_WriteWithMkdir(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"out": {
				Access: FileAccessWrite,
				Path:   "/data/output/file.txt",
				Mkdir:  true,
			},
		},
	}
	if err := cmd.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_Files_WriteWithEnv(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessWrite,
				Path:   "/tmp/dump.sql",
				Env:    "DUMP_FILE",
			},
		},
	}
	if err := cmd.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_Files_ReadWriteIgnoresRequired(t *testing.T) {
	// read_write with required: false should still validate (runtime enforces presence)
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"db": {
				Access:   FileAccessReadWrite,
				Path:     "/var/lib/db.sqlite",
				Required: false, // ignored at validation, enforced at runtime
			},
		},
	}
	if err := cmd.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// Test invalid file specs.
func TestValidate_Files_BadIDWithHyphen(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"dump-file": { // invalid: hyphen not allowed
				Access: FileAccessRead,
				Path:   "/tmp/dump.sql",
			},
		},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "id must match") {
		t.Errorf("expected id validation error, got %v", err)
	}
}

func TestValidate_Files_BadIDStartsWithNumber(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"1dump": { // invalid: starts with number
				Access: FileAccessRead,
				Path:   "/tmp/dump.sql",
			},
		},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "id must match") {
		t.Errorf("expected id validation error, got %v", err)
	}
}

func TestValidate_Files_MissingAccess(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"dump": {
				Path: "/tmp/dump.sql",
				// access not set
			},
		},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "access is required") {
		t.Errorf("expected access required error, got %v", err)
	}
}

func TestValidate_Files_InvalidAccess(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccess("invalid"),
				Path:   "/tmp/dump.sql",
			},
		},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "access must be one of") {
		t.Errorf("expected invalid access error, got %v", err)
	}
}

func TestValidate_Files_WriteMissingPath(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessWrite,
				// path not set
			},
		},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Errorf("expected path required for write error, got %v", err)
	}
}

func TestValidate_Files_WriteWithCandidates(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessWrite,
				Path:   "/tmp/dump.sql",
				Candidates: []FileCandidate{
					{Path: "/backup/dump.sql"},
				},
			},
		},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "candidates are not allowed") {
		t.Errorf("expected candidates not allowed error, got %v", err)
	}
}

func TestValidate_Files_ReadMissingPathAndCandidates(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessRead,
				// neither path nor candidates
			},
		},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "one of path or candidates") {
		t.Errorf("expected one of path or candidates error, got %v", err)
	}
}

func TestValidate_Files_PathAndCandidatesBothSet(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessRead,
				Path:   "/tmp/dump.sql",
				Candidates: []FileCandidate{
					{Path: "/backup/dump.sql"},
				},
			},
		},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually exclusive error, got %v", err)
	}
}

func TestValidate_Files_CandidatePathAndGlobBothSet(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessRead,
				Candidates: []FileCandidate{
					{Path: "/tmp/a.sql", Glob: "/tmp/*.sql"},
				},
			},
		},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually exclusive error, got %v", err)
	}
}

func TestValidate_Files_CandidateNeitherPathNorGlob(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessRead,
				Candidates: []FileCandidate{
					{}, // neither path nor glob
				},
			},
		},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "one of path or glob") {
		t.Errorf("expected one of path or glob error, got %v", err)
	}
}

func TestValidate_Files_CandidatePathWithMatch(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessRead,
				Candidates: []FileCandidate{
					{Path: "/tmp/dump.sql", Match: ".*"}, // match only valid with glob
				},
			},
		},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "only valid with glob") {
		t.Errorf("expected match only with glob error, got %v", err)
	}
}

func TestValidate_Files_CandidatePathWithSort(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessRead,
				Candidates: []FileCandidate{
					{Path: "/tmp/dump.sql", Sort: FileSortNameDesc}, // sort only valid with glob
				},
			},
		},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "only valid with glob") {
		t.Errorf("expected sort only with glob error, got %v", err)
	}
}

func TestValidate_Files_InvalidSort(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessRead,
				Candidates: []FileCandidate{
					{Glob: "/tmp/*.sql", Sort: FileSort("invalid-sort")},
				},
			},
		},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "sort must be one of") {
		t.Errorf("expected invalid sort error, got %v", err)
	}
}

func TestValidate_Files_MkdirForRead(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessRead,
				Path:   "/tmp/dump.sql",
				Mkdir:  true, // invalid for read
			},
		},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "mkdir is only valid") {
		t.Errorf("expected mkdir only for write error, got %v", err)
	}
}

func TestValidate_Files_OverwriteForRead(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"dump": {
				Access:    FileAccessRead,
				Path:      "/tmp/dump.sql",
				Overwrite: true, // invalid for read
			},
		},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "overwrite is only valid") {
		t.Errorf("expected overwrite only for write error, got %v", err)
	}
}

func TestValidate_Files_OnErrorForRead(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"dump": {
				Access:  FileAccessRead,
				Path:    "/tmp/dump.sql",
				OnError: FileOnErrorRemove, // invalid for read
			},
		},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "on_error is not valid") {
		t.Errorf("expected on_error not valid for read error, got %v", err)
	}
}

func TestValidate_Files_InvalidOnError(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"dump": {
				Access:  FileAccessWrite,
				Path:    "/tmp/dump.sql",
				OnError: FileOnError("invalid"),
			},
		},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "on_error must be one of") {
		t.Errorf("expected invalid on_error error, got %v", err)
	}
}

func TestValidate_Files_InvalidEnvName(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessWrite,
				Path:   "/tmp/dump.sql",
				Env:    "dump-file", // invalid: has hyphen, lowercase
			},
		},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "valid POSIX env name") {
		t.Errorf("expected env name validation error, got %v", err)
	}
}

func TestValidate_Files_EnvConflictWithEnvBlock(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Env: map[string]string{
			"DUMP_FILE": "value",
		},
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessWrite,
				Path:   "/tmp/dump.sql",
				Env:    "DUMP_FILE", // conflict with Env block
			},
		},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "env conflict") {
		t.Errorf("expected env conflict error, got %v", err)
	}
}

func TestValidate_Files_EnvConflictWithParamEnv(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Params: map[string]ParamDef{
			"file": {Env: "DUMP_FILE"},
		},
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessWrite,
				Path:   "/tmp/dump.sql",
				Env:    "DUMP_FILE", // conflict with param env
			},
		},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "env conflict") {
		t.Errorf("expected env conflict error, got %v", err)
	}
}

func TestValidate_Files_EnvConflictWithContextEnv(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Context: map[string]ContextDef{
			"file": {Env: "DUMP_FILE"},
		},
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessWrite,
				Path:   "/tmp/dump.sql",
				Env:    "DUMP_FILE", // conflict with context env
			},
		},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "env conflict") {
		t.Errorf("expected env conflict error, got %v", err)
	}
}

func TestValidate_Files_MultipleFilesWithUniqueEnvs(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"input": {
				Access: FileAccessRead,
				Path:   "/tmp/input.sql",
				Env:    "INPUT_FILE",
			},
			"output": {
				Access: FileAccessWrite,
				Path:   "/tmp/output.sql",
				Env:    "OUTPUT_FILE",
			},
		},
	}
	if err := cmd.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_Files_MultipleFilesEnvConflict(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Files: map[string]FileSpec{
			"input": {
				Access: FileAccessRead,
				Path:   "/tmp/input.sql",
				Env:    "FILE_PATH",
			},
			"output": {
				Access: FileAccessWrite,
				Path:   "/tmp/output.sql",
				Env:    "FILE_PATH", // conflict with other file
			},
		},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "env conflict") {
		t.Errorf("expected env conflict error, got %v", err)
	}
}

func TestValidate_NoFiles_EnvConflictParamVsEnvBlock(t *testing.T) {
	cmd := CommandDef{
		Type:   CommandTypeScript,
		ID:     "g.f",
		Script: &ScriptDef{Path: "s.sh"},
		Env:    map[string]string{"MY_VAR": "value"},
		Params: map[string]ParamDef{
			"p": {Env: "MY_VAR"},
		},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "env conflict") {
		t.Errorf("expected env conflict error without files block, got %v", err)
	}
}

func TestValidate_NoFiles_EnvConflictContextVsEnvBlock(t *testing.T) {
	cmd := CommandDef{
		Type:    CommandTypeScript,
		ID:      "g.f",
		Script:  &ScriptDef{Path: "s.sh"},
		Env:     map[string]string{"MY_VAR": "value"},
		Context: map[string]ContextDef{"c": {Env: "MY_VAR"}},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "env conflict") {
		t.Errorf("expected env conflict error without files block, got %v", err)
	}
}

func TestValidate_NoFiles_EnvConflictParamVsContext(t *testing.T) {
	cmd := CommandDef{
		Type:    CommandTypeScript,
		ID:      "g.f",
		Script:  &ScriptDef{Path: "s.sh"},
		Params:  map[string]ParamDef{"p": {Env: "MY_VAR"}},
		Context: map[string]ContextDef{"c": {Env: "MY_VAR"}},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "env conflict") {
		t.Errorf("expected env conflict error without files block, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// ComposeArgs Tests
// ---------------------------------------------------------------------------

func TestParseYAML_ServiceExec_WithComposeArgs(t *testing.T) {
	yaml := `
commands:
  exec_custom:
    type: service_exec
    service: app-main
    cmd: php -v
    compose_args:
      - "-T"
      - "--name"
      - "custom-container"
`
	cf := mustParse(t, yaml)
	cmd := cf.Commands["exec_custom"]
	if len(cmd.ComposeArgs) != 3 {
		t.Fatalf("expected 3 compose_args, got %d", len(cmd.ComposeArgs))
	}
	expected := []string{"-T", "--name", "custom-container"}
	for i, arg := range expected {
		if cmd.ComposeArgs[i] != arg {
			t.Errorf("ComposeArgs[%d] = %q, want %q", i, cmd.ComposeArgs[i], arg)
		}
	}
}

func TestParseYAML_ServiceRun_WithComposeArgs(t *testing.T) {
	yaml := `
commands:
  run_bg:
    type: service_run
    service: app-main
    cmd: sleep 300
    compose_args:
      - "-d"
      - "--rm"
`
	cf := mustParse(t, yaml)
	cmd := cf.Commands["run_bg"]
	if len(cmd.ComposeArgs) != 2 {
		t.Fatalf("expected 2 compose_args, got %d", len(cmd.ComposeArgs))
	}
	if cmd.ComposeArgs[0] != "-d" || cmd.ComposeArgs[1] != "--rm" {
		t.Errorf("unexpected compose_args: %v", cmd.ComposeArgs)
	}
}

func TestValidate_ComposeArgsRejectedOnShell(t *testing.T) {
	cmd := CommandDef{
		Type:        CommandTypeShell,
		ID:          "g.cmd",
		Cmd:         "echo ok",
		ComposeArgs: []string{"-T"},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "compose_args") {
		t.Errorf("expected compose_args rejection for type=shell, got %v", err)
	}
}

func TestValidate_ComposeArgsRejectedOnScript(t *testing.T) {
	cmd := CommandDef{
		Type:        CommandTypeScript,
		ID:          "g.s",
		Script:      &ScriptDef{Path: "s.sh"},
		ComposeArgs: []string{"-T"},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "compose_args") {
		t.Errorf("expected compose_args rejection for type=script, got %v", err)
	}
}

func TestValidate_ComposeArgsRejectedOnWorkflow(t *testing.T) {
	cmd := CommandDef{
		Type: CommandTypeWorkflow,
		ID:   "g.w",
		Steps: []WorkflowStep{
			{Command: "g.step1"},
		},
		ComposeArgs: []string{"-T"},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "compose_args") {
		t.Errorf("expected compose_args rejection for type=workflow, got %v", err)
	}
}

func TestValidate_ComposeArgsRejectedOnDevbox(t *testing.T) {
	cmd := CommandDef{
		Type:        CommandTypeDevbox,
		ID:          "g.devbox",
		Cmd:         "info",
		ComposeArgs: []string{"-T"},
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "compose_args") {
		t.Errorf("expected compose_args rejection for type=devbox, got %v", err)
	}
}

func TestValidate_WorkdirFromRejectedOnShell(t *testing.T) {
	cmd := CommandDef{
		Type:        CommandTypeShell,
		ID:          "g.cmd",
		Cmd:         "echo hello",
		WorkdirFrom: "some.path",
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "workdir_from") {
		t.Errorf("expected workdir_from rejection for type=shell, got %v", err)
	}
}

func TestValidate_WorkdirFromRejectedOnScript(t *testing.T) {
	cmd := CommandDef{
		Type:        CommandTypeScript,
		ID:          "g.s",
		Script:      &ScriptDef{Path: "s.sh"},
		WorkdirFrom: "some.path",
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "workdir_from") {
		t.Errorf("expected workdir_from rejection for type=script, got %v", err)
	}
}

func TestValidate_WorkdirFromRejectedOnWorkflow(t *testing.T) {
	cmd := CommandDef{
		Type: CommandTypeWorkflow,
		ID:   "g.w",
		Steps: []WorkflowStep{
			{Command: "g.step1"},
		},
		WorkdirFrom: "some.path",
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "workdir_from") {
		t.Errorf("expected workdir_from rejection for type=workflow, got %v", err)
	}
}

func TestValidate_WorkdirFromRejectedOnDevbox(t *testing.T) {
	cmd := CommandDef{
		Type:        CommandTypeDevbox,
		ID:          "g.devbox",
		Cmd:         "info",
		WorkdirFrom: "some.path",
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "workdir_from") {
		t.Errorf("expected workdir_from rejection for type=devbox, got %v", err)
	}
}

func TestValidate_WorkdirRejectedOnWorkflow(t *testing.T) {
	cmd := CommandDef{
		Type: CommandTypeWorkflow,
		ID:   "g.w",
		Steps: []WorkflowStep{
			{Command: "g.step1"},
		},
		Workdir: "/some/path",
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "workdir") {
		t.Errorf("expected workdir rejection for type=workflow, got %v", err)
	}
}

func TestValidate_WorkdirRejectedOnDevbox(t *testing.T) {
	cmd := CommandDef{
		Type:    CommandTypeDevbox,
		ID:      "g.devbox",
		Cmd:     "info",
		Workdir: "/some/path",
	}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "workdir") {
		t.Errorf("expected workdir rejection for type=devbox, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Strict YAML decode — legacy field rejection
// ---------------------------------------------------------------------------

func TestParseCommandFile_StrictDecode_LegacyRunField(t *testing.T) {
	yaml := `
commands:
  test:
    type: shell
    run: echo hello
`
	_, err := ParseCommandFile([]byte(yaml))
	if err == nil {
		t.Error("expected parse error for legacy 'run:' field, got nil")
	}
	if !strings.Contains(err.Error(), "run") || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'field run not found' error, got %v", err)
	}
}

func TestParseCommandFile_StrictDecode_UnknownField(t *testing.T) {
	yaml := `
commands:
  test:
    type: shell
    cmd: echo hello
    unknown_field: value
`
	_, err := ParseCommandFile([]byte(yaml))
	if err == nil {
		t.Error("expected parse error for unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "unknown_field") {
		t.Errorf("expected 'unknown_field' in error, got %v", err)
	}
}

func TestValidate_LegacyCommandType(t *testing.T) {
	cmd := CommandDef{
		Type: "command",
		ID:   "g.cmd",
		Cmd:  "echo",
	}
	err := cmd.Validate()
	if err == nil {
		t.Error("expected validation error for legacy type=command")
	}
	if !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("expected 'unknown type' error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// setID returns a copy of cmd with ID set (used in test helpers).
func setID(cmd CommandDef, id string) CommandDef {
	cmd.ID = id
	return cmd
}
