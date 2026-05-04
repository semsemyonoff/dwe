package runtime

import (
	"bytes"
	"strings"
	"testing"

	"devbox-cli/internal/tpl"
)

func TestRunCommand_Messages_NoMessagesNoExtraOutput(t *testing.T) {
	cmd := &CommandDef{
		ID:   "test.no-messages",
		Type: CommandTypeCommand,
		Run:  "true",
	}

	var out, errOut bytes.Buffer
	err := RunCommand(RunContext{
		Cmd:    cmd,
		Render: &tpl.RenderContext{},
		Stdout: &out,
		Stderr: &errOut,
	})
	if err != nil {
		t.Fatalf("expected command to succeed: %v", err)
	}
	if out.String() != "" {
		t.Errorf("expected no stdout, got %q", out.String())
	}
	if errOut.String() != "" {
		t.Errorf("expected no stderr, got %q", errOut.String())
	}
}

func TestRunCommand_Messages_SuccessPrintedAfterSuccess(t *testing.T) {
	cmd := &CommandDef{
		ID:   "test.success-message",
		Type: CommandTypeCommand,
		Messages: CommandMessages{
			Success: "Created ${param.name}.",
			Error:   "Failed ${param.name}.",
		},
		Run: "true",
	}

	var out, errOut bytes.Buffer
	err := RunCommand(RunContext{
		Cmd:    cmd,
		Params: map[string]any{"name": "demo"},
		Render: &tpl.RenderContext{Params: map[string]any{"name": "demo"}},
		Stdout: &out,
		Stderr: &errOut,
	})
	if err != nil {
		t.Fatalf("expected command to succeed: %v", err)
	}
	if !strings.Contains(out.String(), "Created demo.") {
		t.Errorf("expected success message in stdout, got %q", out.String())
	}
	if strings.Contains(out.String(), "Failed demo.") {
		t.Errorf("error message should not be printed on success, got %q", out.String())
	}
	if errOut.String() != "" {
		t.Errorf("expected no stderr on success, got %q", errOut.String())
	}
}

func TestRunCommand_Messages_ErrorPrintedAfterFailure(t *testing.T) {
	cmd := &CommandDef{
		ID:   "test.error-message",
		Type: CommandTypeCommand,
		Messages: CommandMessages{
			Success: "Created ${param.name}.",
			Error:   "Failed ${param.name}.",
		},
		Run: "false",
	}

	var out, errOut bytes.Buffer
	err := RunCommand(RunContext{
		Cmd:    cmd,
		Params: map[string]any{"name": "demo"},
		Render: &tpl.RenderContext{Params: map[string]any{"name": "demo"}},
		Stdout: &out,
		Stderr: &errOut,
	})
	if err == nil {
		t.Fatal("expected command failure")
	}
	if out.String() != "" {
		t.Errorf("success message should not be printed on failure, got %q", out.String())
	}
	if !strings.Contains(errOut.String(), "Failed demo.") {
		t.Errorf("expected error message in stderr, got %q", errOut.String())
	}
}
