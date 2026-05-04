package usercommands

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"devbox-cli/internal/tpl"
	"devbox-cli/internal/ui"
)

func TestRunCommand_Confirmation_NonTTY_YInputRunsCommand(t *testing.T) {
	origIsInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = origIsInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return false }

	dir := t.TempDir()
	logFile := dir + "/run.log"
	cmd := &CommandDef{
		ID:           "test.confirmed",
		Type:         CommandTypeCommand,
		Confirmation: true,
		Run:          `printf 'ran\n' >> ` + logFile,
	}

	var out bytes.Buffer
	err := RunCommand(RunContext{
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
		Type:         CommandTypeCommand,
		Confirmation: true,
		Run:          `printf 'ran\n' >> ` + logFile,
	}

	var out bytes.Buffer
	err := RunCommand(RunContext{
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
		Type:         CommandTypeCommand,
		Confirmation: true,
		Run:          "true",
	}

	var out bytes.Buffer
	_ = RunCommand(RunContext{
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
		Type:             CommandTypeCommand,
		Confirmation:     true,
		ConfirmationText: "Run ${param.task}?",
		Run:              "true",
	}

	err := RunCommand(RunContext{
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
		Type:         CommandTypeCommand,
		Confirmation: true,
		Run:          `printf 'ran\n' >> ` + logFile,
	}

	err := RunCommand(RunContext{
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
