package runtime

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"devbox-cli/internal/tpl"
)

func TestRunCommand_Messages_NoMessagesNoExtraOutput(t *testing.T) {
	cmd := &CommandDef{
		ID:   "test.no-messages",
		Type: CommandTypeShell,
		Cmd:  "true",
	}

	var out, errOut bytes.Buffer
	err := RunCommand(context.Background(), RunContext{
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
		Type: CommandTypeShell,
		Messages: CommandMessages{
			Success: "Created ${param.name}.",
			Error:   "Failed ${param.name}.",
		},
		Cmd: "true",
	}

	var out, errOut bytes.Buffer
	err := RunCommand(context.Background(), RunContext{
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
		Type: CommandTypeShell,
		Messages: CommandMessages{
			Success: "Created ${param.name}.",
			Error:   "Failed ${param.name}.",
		},
		Cmd: "false",
	}

	var out, errOut bytes.Buffer
	err := RunCommand(context.Background(), RunContext{
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

type mockTranslator struct {
	successMessage string
	errorMessage   string
}

func (m *mockTranslator) CommandDescription(_, _, fallback string) string {
	return fallback
}

func (m *mockTranslator) CommandConfirmationText(_, _, fallback string) string {
	return fallback
}

func (m *mockTranslator) ParamDescription(_, _, _, fallback string) string {
	return fallback
}

func (m *mockTranslator) GroupTitle(_, _, fallback string) string {
	return fallback
}

func (m *mockTranslator) GroupDescription(_, _, fallback string) string {
	return fallback
}

func (m *mockTranslator) T(_, _, fallback string) string {
	return fallback
}

func (m *mockTranslator) CommandSuccessMessage(_, _, _ string) string {
	return m.successMessage
}

func (m *mockTranslator) CommandErrorMessage(_, _, _ string) string {
	return m.errorMessage
}

func TestRunCommand_Messages_TranslatorConsultedForSuccess(t *testing.T) {
	cmd := &CommandDef{
		ID:   "test.translated-success",
		Type: CommandTypeShell,
		Messages: CommandMessages{
			Success: "Default success message",
		},
		Cmd: "true",
	}

	translator := &mockTranslator{
		successMessage: "Translated success message",
	}

	var out, errOut bytes.Buffer
	err := RunCommand(context.Background(), RunContext{
		Cmd:        cmd,
		Render:     &tpl.RenderContext{},
		Stdout:     &out,
		Stderr:     &errOut,
		Translator: translator,
		Locale:     "en",
	})
	if err != nil {
		t.Fatalf("expected command to succeed: %v", err)
	}
	if !strings.Contains(out.String(), "Translated success message") {
		t.Errorf("expected translated success message in stdout, got %q", out.String())
	}
}

func TestRunCommand_Messages_TranslatorConsultedForError(t *testing.T) {
	cmd := &CommandDef{
		ID:   "test.translated-error",
		Type: CommandTypeShell,
		Messages: CommandMessages{
			Error: "Default error message",
		},
		Cmd: "false",
	}

	translator := &mockTranslator{
		errorMessage: "Translated error message",
	}

	var out, errOut bytes.Buffer
	err := RunCommand(context.Background(), RunContext{
		Cmd:        cmd,
		Render:     &tpl.RenderContext{},
		Stdout:     &out,
		Stderr:     &errOut,
		Translator: translator,
		Locale:     "en",
	})
	if err == nil {
		t.Fatal("expected command to fail")
	}
	if !strings.Contains(errOut.String(), "Translated error message") {
		t.Errorf("expected translated error message in stderr, got %q", errOut.String())
	}
}
