package commands

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"devbox-cli/internal/tpl"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// buildWorkflowRegistry creates a Registry with the given commands pre-loaded
// without going through YAML files.
func buildWorkflowRegistry(cmds ...*CommandDef) *Registry {
	reg := &Registry{
		byID:   make(map[string]*CommandDef),
		groups: make(map[string]*GroupNode),
	}
	reg.root = reg.ensureGroup("")
	for _, cmd := range cmds {
		reg.byID[cmd.ID] = cmd
		gn := reg.ensureGroup(cmd.Group)
		gn.Commands = append(gn.Commands, cmd)
	}
	return reg
}

// runWorkflowCtx executes a workflow command and returns stdout, stderr, and error.
func runWorkflowCtx(t *testing.T, reg *Registry, workflowCmd *CommandDef) (string, string, error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	ctx := RunContext{
		Cmd:      workflowCmd,
		Params:   map[string]any{},
		Context:  map[string]any{},
		Render:   &tpl.RenderContext{},
		Registry: reg,
		Stdout:   &outBuf,
		Stderr:   &errBuf,
	}
	r := &WorkflowRunner{}
	err := r.Run(ctx)
	return outBuf.String(), errBuf.String(), err
}

// readFileBytes reads a file into bytes, returning nil on error.
func readFileBytes(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// ---------------------------------------------------------------------------
// NoRegistry error
// ---------------------------------------------------------------------------

func TestWorkflowRunner_NoRegistry_Error(t *testing.T) {
	cmd := &CommandDef{
		Type: CommandTypeWorkflow,
		ID:   "wf.test",
		Steps: []WorkflowStep{
			{Command: "some.cmd"},
		},
	}
	ctx := RunContext{
		Cmd:    cmd,
		Render: &tpl.RenderContext{},
		// Registry intentionally nil
	}
	r := &WorkflowRunner{}
	err := r.Run(ctx)
	if err == nil {
		t.Fatal("expected error when registry is nil")
	}
	if !strings.Contains(err.Error(), "registry") {
		t.Errorf("expected 'registry' in error; got %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Step sequencing
// ---------------------------------------------------------------------------

func TestWorkflowRunner_StepSequencing(t *testing.T) {
	dir := t.TempDir()
	logFile := dir + "/order.log"

	step1 := &CommandDef{
		Type:      CommandTypeCommand,
		ID:        "wf.step1",
		Group:     "wf",
		LocalName: "step1",
		Run:       `printf 'step1\n' >> ` + logFile,
	}
	step2 := &CommandDef{
		Type:      CommandTypeCommand,
		ID:        "wf.step2",
		Group:     "wf",
		LocalName: "step2",
		Run:       `printf 'step2\n' >> ` + logFile,
	}
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.bootstrap",
		Group:     "wf",
		LocalName: "bootstrap",
		Steps: []WorkflowStep{
			{Command: "wf.step1"},
			{Command: "wf.step2"},
		},
	}

	reg := buildWorkflowRegistry(wf, step1, step2)
	_, _, err := runWorkflowCtx(t, reg, wf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, readErr := readFileBytes(logFile)
	if readErr != nil {
		t.Fatalf("read log: %v", readErr)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 || lines[0] != "step1" || lines[1] != "step2" {
		t.Errorf("expected [step1 step2]; got %v", lines)
	}
}

// ---------------------------------------------------------------------------
// Param passing via `with`
// ---------------------------------------------------------------------------

func TestWorkflowRunner_WithParamOverride(t *testing.T) {
	dir := t.TempDir()
	outFile := dir + "/param.txt"

	// A command that prints the value of a param via env var.
	echoCmd := &CommandDef{
		Type:      CommandTypeCommand,
		ID:        "wf.echo",
		Group:     "wf",
		LocalName: "echo",
		Params: map[string]ParamDef{
			"msg": {Type: ParamTypeString, Default: "default-msg"},
		},
		Run: `printf '%s' "$MSG" > ` + outFile,
		Env: map[string]string{"MSG": "${param.msg}"},
	}
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.run",
		Group:     "wf",
		LocalName: "run",
		Steps: []WorkflowStep{
			{Command: "wf.echo", With: map[string]string{"msg": "hello-with"}},
		},
	}

	reg := buildWorkflowRegistry(wf, echoCmd)
	_, _, err := runWorkflowCtx(t, reg, wf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := readFileBytes(outFile)
	if string(data) != "hello-with" {
		t.Errorf("expected 'hello-with'; got %q", string(data))
	}
}

func TestWorkflowRunner_WithParam_DefaultWhenNotProvided(t *testing.T) {
	dir := t.TempDir()
	outFile := dir + "/param.txt"

	echoCmd := &CommandDef{
		Type:      CommandTypeCommand,
		ID:        "wf.echo2",
		Group:     "wf",
		LocalName: "echo2",
		Params: map[string]ParamDef{
			"msg": {Type: ParamTypeString, Default: "default-msg"},
		},
		Run: `printf '%s' "$MSG" > ` + outFile,
		Env: map[string]string{"MSG": "${param.msg}"},
	}
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.run2",
		Group:     "wf",
		LocalName: "run2",
		Steps: []WorkflowStep{
			{Command: "wf.echo2"}, // no `with`, use default
		},
	}

	reg := buildWorkflowRegistry(wf, echoCmd)
	_, _, err := runWorkflowCtx(t, reg, wf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := readFileBytes(outFile)
	if string(data) != "default-msg" {
		t.Errorf("expected 'default-msg'; got %q", string(data))
	}
}

// ---------------------------------------------------------------------------
// Private command callable from workflow
// ---------------------------------------------------------------------------

func TestWorkflowRunner_PrivateCommand_Callable(t *testing.T) {
	dir := t.TempDir()
	logFile := dir + "/private.log"

	privateCmd := &CommandDef{
		Type:      CommandTypeCommand,
		ID:        "wf.internal",
		Group:     "wf",
		LocalName: "internal",
		Private:   true,
		Run:       `printf 'private-ran\n' >> ` + logFile,
	}
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.caller",
		Group:     "wf",
		LocalName: "caller",
		Steps: []WorkflowStep{
			{Command: "wf.internal"},
		},
	}

	reg := buildWorkflowRegistry(wf, privateCmd)
	_, _, err := runWorkflowCtx(t, reg, wf)
	if err != nil {
		t.Fatalf("private commands should be callable from workflows: %v", err)
	}

	data, _ := readFileBytes(logFile)
	if !strings.Contains(string(data), "private-ran") {
		t.Errorf("expected private command to run; log: %q", string(data))
	}
}

// ---------------------------------------------------------------------------
// Confirm step — non-interactive mode (auto-skip)
// ---------------------------------------------------------------------------

func TestWorkflowRunner_ConfirmStep_NonInteractive_AutoSkip(t *testing.T) {
	t.Setenv("DEVBOX_NONINTERACTIVE", "1")

	dir := t.TempDir()
	logFile := dir + "/confirm.log"

	step := &CommandDef{
		Type:      CommandTypeCommand,
		ID:        "wf.after-confirm",
		Group:     "wf",
		LocalName: "after-confirm",
		Run:       `printf 'ran\n' >> ` + logFile,
	}
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.confirm-wf",
		Group:     "wf",
		LocalName: "confirm-wf",
		Steps: []WorkflowStep{
			{Confirm: "Do you want to continue?"},
			{Command: "wf.after-confirm"},
		},
	}

	reg := buildWorkflowRegistry(wf, step)
	_, _, err := runWorkflowCtx(t, reg, wf)
	if err != nil {
		t.Fatalf("unexpected error in non-interactive mode: %v", err)
	}

	data, _ := readFileBytes(logFile)
	if !strings.Contains(string(data), "ran") {
		t.Errorf("expected step after confirm to run; log: %q", string(data))
	}
}

// ---------------------------------------------------------------------------
// Missing command reference error
// ---------------------------------------------------------------------------

func TestWorkflowRunner_MissingCommandRef_Error(t *testing.T) {
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.broken",
		Group:     "wf",
		LocalName: "broken",
		Steps: []WorkflowStep{
			{Command: "does.not.exist"},
		},
	}

	reg := buildWorkflowRegistry(wf)
	_, _, err := runWorkflowCtx(t, reg, wf)
	if err == nil {
		t.Fatal("expected error for missing command reference")
	}
	if !strings.Contains(err.Error(), "does.not.exist") {
		t.Errorf("expected missing ID in error; got %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// NewRunner dispatching
// ---------------------------------------------------------------------------

func TestNewRunner_Returns_WorkflowRunner(t *testing.T) {
	cmd := &CommandDef{Type: CommandTypeWorkflow}
	runner, err := NewRunner(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := runner.(*WorkflowRunner); !ok {
		t.Errorf("expected *WorkflowRunner, got %T", runner)
	}
}
