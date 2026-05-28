package runtime

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"devbox-cli/internal/core/ui"
	"devbox-cli/internal/shared/tpl"
)

func TestRunCommand_Confirmation_NonTTY_YInputRunsCommand(t *testing.T) {
	origIsInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = origIsInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return false }

	dir := t.TempDir()
	logFile := dir + "/run.log"
	cmd := &CommandDef{
		ID:           "test.confirmed",
		Type:         CommandTypeShell,
		Confirmation: true,
		Cmd:          `printf 'ran\n' >> ` + logFile,
	}

	var out bytes.Buffer
	err := RunCommand(context.Background(), RunContext{
		Cmd:    cmd,
		Render: &tpl.RenderContext{},
		Stdout: &out,
		Stdin:  bytes.NewBufferString("y\n"),
	})
	if err != nil {
		t.Fatalf("expected confirmed command to run: %v", err)
	}

	data, _ := readFileBytes(logFile)
	if !strings.Contains(string(data), "ran") {
		t.Errorf("expected command output in log, got %q", string(data))
	}
}

func TestRunCommand_Confirmation_NonTTY_NInputAbortsCommand(t *testing.T) {
	origIsInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = origIsInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return false }

	dir := t.TempDir()
	logFile := dir + "/run.log"
	cmd := &CommandDef{
		ID:           "test.denied",
		Type:         CommandTypeShell,
		Confirmation: true,
		Cmd:          `printf 'ran\n' >> ` + logFile,
	}

	var out bytes.Buffer
	err := RunCommand(context.Background(), RunContext{
		Cmd:    cmd,
		Render: &tpl.RenderContext{},
		Stdout: &out,
		Stdin:  bytes.NewBufferString("n\n"),
	})
	if err == nil {
		t.Fatal("expected denial to abort command")
	}
	if !strings.Contains(err.Error(), "aborted by user") {
		t.Errorf("expected aborted by user, got %q", err.Error())
	}
	if data, _ := readFileBytes(logFile); len(data) > 0 {
		t.Errorf("command should not have run, log: %q", string(data))
	}
}

func TestRunCommand_Confirmation_DefaultText(t *testing.T) {
	origIsInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = origIsInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return false }

	cmd := &CommandDef{
		ID:           "test.default-text",
		Type:         CommandTypeShell,
		Confirmation: true,
		Cmd:          "true",
	}

	var out bytes.Buffer
	_ = RunCommand(context.Background(), RunContext{
		Cmd:    cmd,
		Render: &tpl.RenderContext{},
		Stdout: &out,
		Stdin:  bytes.NewBufferString("n\n"),
	})
	if !strings.Contains(out.String(), DefaultConfirmationText) {
		t.Errorf("expected default confirmation text in prompt, got %q", out.String())
	}
}

func TestRunCommand_Confirmation_TTYUsesCustomText(t *testing.T) {
	origRC := runConfirm
	origIsInteractive := ui.IsInteractiveFn
	t.Cleanup(func() {
		runConfirm = origRC
		ui.IsInteractiveFn = origIsInteractive
	})

	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }
	var gotTitle, gotAffirmative, gotNegative string
	runConfirm = func(title, affirmative, negative string) (bool, error) {
		gotTitle = title
		gotAffirmative = affirmative
		gotNegative = negative
		return true, nil
	}

	cmd := &CommandDef{
		ID:               "test.custom-text",
		Type:             CommandTypeShell,
		Confirmation:     true,
		ConfirmationText: "Run ${param.task}?",
		Cmd:              "true",
	}

	err := RunCommand(context.Background(), RunContext{
		Cmd:    cmd,
		Params: map[string]any{"task": "cleanup"},
		Render: &tpl.RenderContext{Params: map[string]any{"task": "cleanup"}},
		Stdin:  bytes.NewBufferString(""),
	})
	if err != nil {
		t.Fatalf("expected confirmed TTY command to run: %v", err)
	}
	if gotTitle != "Run cleanup?" {
		t.Errorf("confirmation title = %q", gotTitle)
	}
	if gotAffirmative != "Yes" || gotNegative != "No" {
		t.Errorf("confirmation labels = %q/%q", gotAffirmative, gotNegative)
	}
}

// trackingReader records whether Read was ever called.
type trackingReader struct{ reads int }

func (r *trackingReader) Read(p []byte) (int, error) {
	r.reads++
	return 0, io.EOF
}

// TestConfirmCommand_FilledParams_SkipConfirm_DoesNotReadStdin guards against
// a regression where the runtime prompts (via stdin) for parameter values or
// confirmation after the orchestrator has already resolved params and asked
// the user. With Params already filled and SkipConfirm=true, the confirmation
// path — the only place runtime could prompt — must not touch stdin nor invoke
// the runConfirm seam.
func TestConfirmCommand_FilledParams_SkipConfirm_DoesNotReadStdin(t *testing.T) {
	origIsInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = origIsInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	origRC := runConfirm
	t.Cleanup(func() { runConfirm = origRC })
	runConfirm = func(title, affirmative, negative string) (bool, error) {
		t.Fatalf("runConfirm should not be called when SkipConfirm is true")
		return false, nil
	}

	cmd := &CommandDef{
		ID:           "test.filled-params",
		Type:         CommandTypeShell,
		Confirmation: true,
		Cmd:          `true`,
	}

	stdin := &trackingReader{}
	err := ConfirmCommand(RunContext{
		Cmd:         cmd,
		Render:      &tpl.RenderContext{},
		Params:      map[string]any{"name": "value"},
		Stdin:       stdin,
		SkipConfirm: true,
	})
	if err != nil {
		t.Fatalf("ConfirmCommand returned error: %v", err)
	}
	if stdin.reads != 0 {
		t.Errorf("expected zero stdin reads, got %d", stdin.reads)
	}
}

func TestRunCommand_Confirmation_SkipConfirmSkipsPrompt(t *testing.T) {
	origRC := runConfirm
	t.Cleanup(func() { runConfirm = origRC })

	prompted := false
	runConfirm = func(title, affirmative, negative string) (bool, error) {
		prompted = true
		return true, nil
	}

	dir := t.TempDir()
	logFile := dir + "/run.log"
	cmd := &CommandDef{
		ID:           "test.skip-confirm",
		Type:         CommandTypeShell,
		Confirmation: true,
		Cmd:          `printf 'ran\n' >> ` + logFile,
	}

	err := RunCommand(context.Background(), RunContext{
		Cmd:         cmd,
		Render:      &tpl.RenderContext{},
		Stdin:       bytes.NewBufferString(""), // Empty stdin; would block if prompt tried to read
		SkipConfirm: true,
	})
	if err != nil {
		t.Fatalf("expected confirmed command to run with SkipConfirm: %v", err)
	}
	if prompted {
		t.Error("expected no prompt when SkipConfirm is true")
	}
	data, _ := readFileBytes(logFile)
	if !strings.Contains(string(data), "ran") {
		t.Errorf("expected command to run, log: %q", string(data))
	}
}
