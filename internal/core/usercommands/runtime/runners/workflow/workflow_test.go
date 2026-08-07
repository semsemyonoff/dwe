package workflow

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

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
	err := r.Run(context.Background(), ctx)
	return outBuf.String(), errBuf.String(), err
}

func readFileBytes(path string) ([]byte, error) {
	return os.ReadFile(path)
}

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
	err := r.Run(context.Background(), ctx)
	if err == nil {
		t.Fatal("expected error when registry is nil")
	}
	if !strings.Contains(err.Error(), "registry") {
		t.Errorf("expected 'registry' in error; got %q", err.Error())
	}
}

func TestWorkflowRunner_StepSequencing(t *testing.T) {
	dir := t.TempDir()
	logFile := dir + "/order.log"

	step1 := &CommandDef{
		Type:      CommandTypeShell,
		ID:        "wf.step1",
		Group:     "wf",
		LocalName: "step1",
		Cmd:       `printf 'step1\n' >> ` + logFile,
	}
	step2 := &CommandDef{
		Type:      CommandTypeShell,
		ID:        "wf.step2",
		Group:     "wf",
		LocalName: "step2",
		Cmd:       `printf 'step2\n' >> ` + logFile,
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

func TestWorkflowRunner_WithParamOverride(t *testing.T) {
	dir := t.TempDir()
	outFile := dir + "/param.txt"

	// A command that prints the value of a param via env var.
	echoCmd := &CommandDef{
		Type:      CommandTypeShell,
		ID:        "wf.echo",
		Group:     "wf",
		LocalName: "echo",
		Params: map[string]ParamDef{
			"msg": {Type: ParamTypeString, Default: "default-msg"},
		},
		Cmd: `printf '%s' "$MSG" > ` + outFile,
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
		Type:      CommandTypeShell,
		ID:        "wf.echo2",
		Group:     "wf",
		LocalName: "echo2",
		Params: map[string]ParamDef{
			"msg": {Type: ParamTypeString, Default: "default-msg"},
		},
		Cmd: `printf '%s' "$MSG" > ` + outFile,
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

func TestWorkflowRunner_PrivateCommand_Callable(t *testing.T) {
	dir := t.TempDir()
	logFile := dir + "/private.log"

	privateCmd := &CommandDef{
		Type:      CommandTypeShell,
		ID:        "wf.internal",
		Group:     "wf",
		LocalName: "internal",
		Private:   true,
		Cmd:       `printf 'private-ran\n' >> ` + logFile,
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
	t.Setenv("DWE_NONINTERACTIVE", "1")

	dir := t.TempDir()
	logFile := dir + "/confirm.log"

	step := &CommandDef{
		Type:      CommandTypeShell,
		ID:        "wf.after-confirm",
		Group:     "wf",
		LocalName: "after-confirm",
		Cmd:       `printf 'ran\n' >> ` + logFile,
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
// Confirm step — TTY path (huh wrapper)
// ---------------------------------------------------------------------------

func TestWorkflowRunner_ConfirmStep_TTY_Confirmed(t *testing.T) {
	origRC := runConfirm
	origIsInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() {
		runConfirm = origRC
		widgets.IsInteractiveFn = origIsInteractive
	})

	widgets.IsInteractiveFn = func(_ io.Reader) bool { return true }
	runConfirm = func(title, affirmative, negative string) (bool, error) {
		return true, nil
	}

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.tty-confirm",
		Group:     "wf",
		LocalName: "tty-confirm",
		Steps: []WorkflowStep{
			{Confirm: "Do you want to continue?"},
		},
	}
	reg := buildWorkflowRegistry(wf)

	var outBuf bytes.Buffer
	ctx := RunContext{
		Cmd:      wf,
		Params:   map[string]any{},
		Context:  map[string]any{},
		Render:   &tpl.RenderContext{},
		Registry: reg,
		Stdout:   &outBuf,
		Stderr:   &outBuf,
		Stdin:    bytes.NewBufferString(""),
	}
	err := (&WorkflowRunner{}).Run(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error on confirmed TTY prompt: %v", err)
	}
}

func TestWorkflowRunner_ConfirmStep_TTY_Denied(t *testing.T) {
	origRC := runConfirm
	origIsInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() {
		runConfirm = origRC
		widgets.IsInteractiveFn = origIsInteractive
	})

	widgets.IsInteractiveFn = func(_ io.Reader) bool { return true }
	runConfirm = func(title, affirmative, negative string) (bool, error) {
		return false, nil
	}

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.tty-deny",
		Group:     "wf",
		LocalName: "tty-deny",
		Steps: []WorkflowStep{
			{Confirm: "Do you want to continue?"},
		},
	}
	reg := buildWorkflowRegistry(wf)

	var outBuf bytes.Buffer
	ctx := RunContext{
		Cmd:      wf,
		Params:   map[string]any{},
		Context:  map[string]any{},
		Render:   &tpl.RenderContext{},
		Registry: reg,
		Stdout:   &outBuf,
		Stderr:   &outBuf,
		Stdin:    bytes.NewBufferString(""),
	}
	err := (&WorkflowRunner{}).Run(context.Background(), ctx)
	if err == nil {
		t.Fatal("expected error when user denies TTY confirm")
	}
	if !strings.Contains(err.Error(), "aborted by user") {
		t.Errorf("expected 'aborted by user' in error; got %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Confirm step — non-TTY path (bufio stdin fallback)
// ---------------------------------------------------------------------------

func TestWorkflowRunner_ConfirmStep_NonTTY_YInput(t *testing.T) {
	origIsInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() { widgets.IsInteractiveFn = origIsInteractive })

	widgets.IsInteractiveFn = func(_ io.Reader) bool { return false }

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.nontty-y",
		Group:     "wf",
		LocalName: "nontty-y",
		Steps: []WorkflowStep{
			{Confirm: "Continue?"},
		},
	}
	reg := buildWorkflowRegistry(wf)

	var outBuf bytes.Buffer
	ctx := RunContext{
		Cmd:      wf,
		Params:   map[string]any{},
		Context:  map[string]any{},
		Render:   &tpl.RenderContext{},
		Registry: reg,
		Stdout:   &outBuf,
		Stderr:   &outBuf,
		Stdin:    bytes.NewBufferString("y\n"),
	}
	err := (&WorkflowRunner{}).Run(context.Background(), ctx)
	if err != nil {
		t.Fatalf("expected no error for 'y' input; got %v", err)
	}
}

// TestWorkflowRunner_ConfirmStep_NonTTY_CIEnv verifies that CI=1 auto-confirms
// the prompt — matching the behavior of builtin/print confirm paths.
func TestWorkflowRunner_ConfirmStep_NonTTY_CIEnv(t *testing.T) {
	origIsInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() { widgets.IsInteractiveFn = origIsInteractive })

	widgets.IsInteractiveFn = func(_ io.Reader) bool { return false }
	t.Setenv("CI", "1")

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.nontty-ci",
		Group:     "wf",
		LocalName: "nontty-ci",
		Steps: []WorkflowStep{
			{Confirm: "Continue?"},
		},
	}
	reg := buildWorkflowRegistry(wf)

	var outBuf bytes.Buffer
	ctx := RunContext{
		Cmd:      wf,
		Params:   map[string]any{},
		Context:  map[string]any{},
		Render:   &tpl.RenderContext{},
		Registry: reg,
		Stdout:   &outBuf,
		Stderr:   &outBuf,
		Stdin:    bytes.NewBufferString(""), // no input — CI must short-circuit
	}
	if err := (&WorkflowRunner{}).Run(context.Background(), ctx); err != nil {
		t.Fatalf("expected no error under CI=1; got %v", err)
	}
}

func TestWorkflowRunner_ConfirmStep_NonTTY_NInput(t *testing.T) {
	// render.Writer.Confirm short-circuits to "yes" when $CI is set (GitHub
	// Actions sets CI=true); clear it so the "n" abort path is exercised.
	t.Setenv("CI", "")
	origIsInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() { widgets.IsInteractiveFn = origIsInteractive })

	widgets.IsInteractiveFn = func(_ io.Reader) bool { return false }

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.nontty-n",
		Group:     "wf",
		LocalName: "nontty-n",
		Steps: []WorkflowStep{
			{Confirm: "Continue?"},
		},
	}
	reg := buildWorkflowRegistry(wf)

	var outBuf bytes.Buffer
	ctx := RunContext{
		Cmd:      wf,
		Params:   map[string]any{},
		Context:  map[string]any{},
		Render:   &tpl.RenderContext{},
		Registry: reg,
		Stdout:   &outBuf,
		Stderr:   &outBuf,
		Stdin:    bytes.NewBufferString("n\n"),
	}
	err := (&WorkflowRunner{}).Run(context.Background(), ctx)
	if err == nil {
		t.Fatal("expected error for 'n' input")
	}
	if !strings.Contains(err.Error(), "aborted by user") {
		t.Errorf("expected 'aborted by user'; got %q", err.Error())
	}
}

func TestWorkflowRunner_ConfirmStep_NonInteractiveContext_SkipsConfirm(t *testing.T) {
	dir := t.TempDir()
	logFile := dir + "/step.log"

	step := &CommandDef{
		Type:      CommandTypeShell,
		ID:        "wf.after-confirm",
		Group:     "wf",
		LocalName: "after-confirm",
		Cmd:       `printf 'ran\n' >> ` + logFile,
	}
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.confirm-skip",
		Group:     "wf",
		LocalName: "confirm-skip",
		Steps: []WorkflowStep{
			{Confirm: "Do you want to continue?"},
			{Command: "wf.after-confirm"},
		},
	}

	reg := buildWorkflowRegistry(wf, step)

	var outBuf bytes.Buffer
	ctx := RunContext{
		Cmd:            wf,
		Params:         map[string]any{},
		Context:        map[string]any{},
		Render:         &tpl.RenderContext{},
		Registry:       reg,
		Stdout:         &outBuf,
		Stderr:         &outBuf,
		Stdin:          bytes.NewBufferString(""), // Empty stdin; would block if prompt tried to read
		NonInteractive: true,
	}
	err := (&WorkflowRunner{}).Run(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error with NonInteractive=true: %v", err)
	}

	data, _ := readFileBytes(logFile)
	if !strings.Contains(string(data), "ran") {
		t.Errorf("expected step after confirm to run; log: %q", string(data))
	}
}

func TestWorkflowRunner_WhenParam_Truthy_Runs(t *testing.T) {
	dir := t.TempDir()
	logFile := dir + "/when.log"

	step := &CommandDef{
		Type:      CommandTypeShell,
		ID:        "wf.step",
		Group:     "wf",
		LocalName: "step",
		Cmd:       `printf 'ran\n' >> ` + logFile,
	}
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.when-true",
		Group:     "wf",
		LocalName: "when-true",
		Steps: []WorkflowStep{
			{Command: "wf.step", When: "${param.enabled}"},
		},
	}

	reg := buildWorkflowRegistry(wf, step)
	var outBuf bytes.Buffer
	ctx := RunContext{
		Cmd:     wf,
		Params:  map[string]any{},
		Context: map[string]any{},
		Render: &tpl.RenderContext{
			Params: map[string]any{"enabled": "true"},
		},
		Registry: reg,
		Stdout:   &outBuf,
		Stderr:   &outBuf,
	}
	err := (&WorkflowRunner{}).Run(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := readFileBytes(logFile)
	if !strings.Contains(string(data), "ran") {
		t.Errorf("expected step to run with truthy when; log: %q", string(data))
	}
}

func TestWorkflowRunner_WhenParam_Falsy_Skips(t *testing.T) {
	dir := t.TempDir()
	logFile := dir + "/when.log"

	step := &CommandDef{
		Type:      CommandTypeShell,
		ID:        "wf.step",
		Group:     "wf",
		LocalName: "step",
		Cmd:       `printf 'ran\n' >> ` + logFile,
	}
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.when-false",
		Group:     "wf",
		LocalName: "when-false",
		Steps: []WorkflowStep{
			{Command: "wf.step", When: "${param.enabled}"},
		},
	}

	reg := buildWorkflowRegistry(wf, step)
	var outBuf bytes.Buffer
	ctx := RunContext{
		Cmd:     wf,
		Params:  map[string]any{},
		Context: map[string]any{},
		Render: &tpl.RenderContext{
			Params: map[string]any{"enabled": "false"},
		},
		Registry: reg,
		Stdout:   &outBuf,
		Stderr:   &outBuf,
	}
	err := (&WorkflowRunner{}).Run(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Step should be skipped, so logFile won't exist or be empty
	data, _ := readFileBytes(logFile)
	if strings.Contains(string(data), "ran") {
		t.Errorf("expected step to be skipped with falsy when; log: %q", string(data))
	}
}

func TestWorkflowRunner_WhenCmd_True_Runs(t *testing.T) {
	dir := t.TempDir()
	logFile := dir + "/when-cmd.log"

	step := &CommandDef{
		Type:      CommandTypeShell,
		ID:        "wf.step",
		Group:     "wf",
		LocalName: "step",
		Cmd:       `printf 'ran\n' >> ` + logFile,
	}
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.when-cmd-true",
		Group:     "wf",
		LocalName: "when-cmd-true",
		Steps: []WorkflowStep{
			{Command: "wf.step", When: "cmd: true"},
		},
	}

	reg := buildWorkflowRegistry(wf, step)
	var outBuf bytes.Buffer
	ctx := RunContext{
		Cmd:         wf,
		Params:      map[string]any{},
		Context:     map[string]any{},
		Render:      &tpl.RenderContext{},
		Registry:    reg,
		ProjectRoot: "/tmp",
		Stdout:      &outBuf,
		Stderr:      &outBuf,
	}
	err := (&WorkflowRunner{}).Run(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := readFileBytes(logFile)
	if !strings.Contains(string(data), "ran") {
		t.Errorf("expected step to run with cmd: true; log: %q", string(data))
	}
}

func TestWorkflowRunner_WhenBuiltin_FileExistsInTemplate(t *testing.T) {
	dir := t.TempDir()
	logFile := dir + "/when-builtin.log"
	testFile := dir + "/test.txt"
	// Create the test file
	if err := os.WriteFile(testFile, []byte("exists"), 0600); err != nil {
		t.Fatal(err)
	}

	step := &CommandDef{
		Type:      CommandTypeShell,
		ID:        "wf.step",
		Group:     "wf",
		LocalName: "step",
		Cmd:       `printf 'ran\n' >> ` + logFile,
	}
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.when-builtin",
		Group:     "wf",
		LocalName: "when-builtin",
		Steps: []WorkflowStep{
			{Command: "wf.step", When: "file-exists ${param.path}"},
		},
	}

	reg := buildWorkflowRegistry(wf, step)
	var outBuf bytes.Buffer
	ctx := RunContext{
		Cmd:     wf,
		Params:  map[string]any{},
		Context: map[string]any{},
		Render: &tpl.RenderContext{
			Params: map[string]any{"path": testFile},
		},
		Registry:    reg,
		ProjectRoot: dir,
		Stdout:      &outBuf,
		Stderr:      &outBuf,
	}
	err := (&WorkflowRunner{}).Run(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := readFileBytes(logFile)
	if !strings.Contains(string(data), "ran") {
		t.Errorf("expected step to run with file-exists predicate; log: %q", string(data))
	}
}

func TestWorkflowRunner_WhenInvalidExpr_Error(t *testing.T) {
	step := &CommandDef{
		Type:      CommandTypeShell,
		ID:        "wf.step",
		Group:     "wf",
		LocalName: "step",
		Cmd:       `echo hi`,
	}
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.when-invalid",
		Group:     "wf",
		LocalName: "when-invalid",
		Steps: []WorkflowStep{
			{Command: "wf.step", When: "{{ .Unclosed"},
		},
	}

	reg := buildWorkflowRegistry(wf, step)
	_, _, err := runWorkflowCtx(t, reg, wf)
	if err == nil {
		t.Fatal("expected error for invalid when expression")
	}
	if !strings.Contains(err.Error(), "eval when") {
		t.Errorf("error should mention 'eval when'; got %q", err.Error())
	}
}

func TestWorkflowRunner_ContinueOnError_True_Continues(t *testing.T) {
	dir := t.TempDir()
	logFile := dir + "/order.log"

	failStep := &CommandDef{
		Type:      CommandTypeShell,
		ID:        "wf.fail",
		Group:     "wf",
		LocalName: "fail",
		Cmd:       `exit 1`,
	}
	successStep := &CommandDef{
		Type:      CommandTypeShell,
		ID:        "wf.success",
		Group:     "wf",
		LocalName: "success",
		Cmd:       `printf 'success-ran\n' >> ` + logFile,
	}
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.continue",
		Group:     "wf",
		LocalName: "continue",
		Steps: []WorkflowStep{
			{Command: "wf.fail", ContinueOnError: true},
			{Command: "wf.success"},
		},
	}

	reg := buildWorkflowRegistry(wf, failStep, successStep)
	_, _, err := runWorkflowCtx(t, reg, wf)
	if err != nil {
		t.Fatalf("unexpected error; workflow should continue after ContinueOnError: %v", err)
	}

	data, _ := readFileBytes(logFile)
	if !strings.Contains(string(data), "success-ran") {
		t.Errorf("expected step after failure to run with continue_on_error; log: %q", string(data))
	}
}

func TestWorkflowRunner_ContinueOnError_False_Aborts(t *testing.T) {
	failStep := &CommandDef{
		Type:      CommandTypeShell,
		ID:        "wf.fail",
		Group:     "wf",
		LocalName: "fail",
		Cmd:       `exit 1`,
	}
	successStep := &CommandDef{
		Type:      CommandTypeShell,
		ID:        "wf.success",
		Group:     "wf",
		LocalName: "success",
		Cmd:       `echo success`,
	}
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.abort",
		Group:     "wf",
		LocalName: "abort",
		Steps: []WorkflowStep{
			{Command: "wf.fail", ContinueOnError: false},
			{Command: "wf.success"},
		},
	}

	reg := buildWorkflowRegistry(wf, failStep, successStep)
	_, _, err := runWorkflowCtx(t, reg, wf)
	if err == nil {
		t.Fatal("expected error when step fails without continue_on_error")
	}
}

func TestWorkflowRunner_ContinueOnError_SkippedWithFalsyWhen(t *testing.T) {
	dir := t.TempDir()
	logFile := dir + "/order.log"

	step1 := &CommandDef{
		Type:      CommandTypeShell,
		ID:        "wf.step1",
		Group:     "wf",
		LocalName: "step1",
		Cmd:       `exit 1`,
	}
	step2 := &CommandDef{
		Type:      CommandTypeShell,
		ID:        "wf.step2",
		Group:     "wf",
		LocalName: "step2",
		Cmd:       `printf 'step2-ran\n' >> ` + logFile,
	}
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.skip",
		Group:     "wf",
		LocalName: "skip",
		Steps: []WorkflowStep{
			{Command: "wf.step1", When: "false", ContinueOnError: true},
			{Command: "wf.step2"},
		},
	}

	reg := buildWorkflowRegistry(wf, step1, step2)
	_, _, err := runWorkflowCtx(t, reg, wf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// step1 should be skipped, so step2 should run without triggering continue_on_error
	data, _ := readFileBytes(logFile)
	if !strings.Contains(string(data), "step2-ran") {
		t.Errorf("expected step2 to run after skipped step1; log: %q", string(data))
	}
}

func TestWorkflowRunner_ContinueOnError_OnConfirmStep_Rejected(t *testing.T) {
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.invalid-confirm",
		Group:     "wf",
		LocalName: "invalid-confirm",
		Steps: []WorkflowStep{
			{Confirm: "Continue?", ContinueOnError: true},
		},
	}

	if err := wf.Validate(); err == nil {
		t.Fatal("expected validation error for continue_on_error on confirm step")
	}
}

// When a workflow step references a Hidden command (resolved via
// reg.ApplyVisibility), the step is skipped before any other gate fires.
// The skip reason logged to stderr distinguishes hidden from when=false.
func TestWorkflowRunner_HiddenTarget_SkipsStep(t *testing.T) {
	dir := t.TempDir()
	logFile := dir + "/hidden.log"

	hidden := &CommandDef{
		Type:      CommandTypeShell,
		ID:        "wf.target",
		Group:     "wf",
		LocalName: "target",
		Hidden:    true, // pre-set; production sets this via reg.ApplyVisibility
		Cmd:       `printf 'should-not-run\n' >> ` + logFile,
	}
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.caller",
		Group:     "wf",
		LocalName: "caller",
		Steps: []WorkflowStep{
			{Command: "wf.target"},
		},
	}

	reg := buildWorkflowRegistry(wf, hidden)
	_, stderr, err := runWorkflowCtx(t, reg, wf)
	if err != nil {
		t.Fatalf("workflow should not fail when target is hidden: %v", err)
	}

	if !strings.Contains(stderr, "target hidden") {
		t.Errorf("stderr should mention 'target hidden'; got %q", stderr)
	}

	data, _ := readFileBytes(logFile)
	if len(data) != 0 {
		t.Errorf("hidden command body must not run; log: %q", string(data))
	}
}
