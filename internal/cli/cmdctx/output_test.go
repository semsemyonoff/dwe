package cmdctx_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"devbox-cli/internal/cli/cmdctx"

	"github.com/spf13/cobra"
)

// newTestCmd returns a cobra.Command with its output wired to the supplied
// buffers so tests can capture stdout/stderr without touching os.Stdout.
func newTestCmd(stdout, stderr *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	return cmd
}

// ---- WriteData ----

func TestWriteData_TextMode(t *testing.T) {
	var out bytes.Buffer
	cmd := newTestCmd(&out, &bytes.Buffer{})
	flags := &cmdctx.RootFlags{Output: "text"}

	err := cmdctx.WriteData(flags, cmd, "hello", func(s string) string { return s })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimRight(out.String(), "\n"); got != "hello" {
		t.Errorf("text output: got %q, want %q", got, "hello")
	}
}

func TestWriteData_EmptyOutputDefaultsToText(t *testing.T) {
	var out bytes.Buffer
	cmd := newTestCmd(&out, &bytes.Buffer{})
	flags := &cmdctx.RootFlags{} // Output == ""

	err := cmdctx.WriteData(flags, cmd, 42, func(n int) string { return "forty-two" })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimRight(out.String(), "\n"); got != "forty-two" {
		t.Errorf("text output: got %q, want %q", got, "forty-two")
	}
}

func TestWriteData_JSONMode_Compact(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	var out bytes.Buffer
	cmd := newTestCmd(&out, &bytes.Buffer{})
	flags := &cmdctx.RootFlags{Output: "json"}

	err := cmdctx.WriteData(flags, cmd, payload{Name: "alice", Age: 30}, func(p payload) string { return "" })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// json.Encoder always appends a newline; strip it for comparison
	got := strings.TrimRight(out.String(), "\n")
	want := `{"name":"alice","age":30}`
	if got != want {
		t.Errorf("compact JSON: got %q, want %q", got, want)
	}
}

func TestWriteData_JSONMode_Pretty(t *testing.T) {
	type payload struct {
		X int `json:"x"`
	}
	var out bytes.Buffer
	cmd := newTestCmd(&out, &bytes.Buffer{})
	flags := &cmdctx.RootFlags{Output: "json", Pretty: true}

	if err := cmdctx.WriteData(flags, cmd, payload{X: 1}, func(p payload) string { return "" }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "\n  ") {
		t.Errorf("pretty JSON should contain indentation, got: %q", got)
	}
}

// ---- WriteError ----

func TestWriteError_TextMode_Noop(t *testing.T) {
	var errBuf bytes.Buffer
	cmd := newTestCmd(&bytes.Buffer{}, &errBuf)
	flags := &cmdctx.RootFlags{Output: "text"}

	cmdctx.WriteError(flags, cmd, errors.New("boom"))
	if errBuf.Len() != 0 {
		t.Errorf("text mode WriteError should be a no-op, got: %q", errBuf.String())
	}
}

func TestWriteError_JSONMode_PlainError(t *testing.T) {
	var errBuf bytes.Buffer
	cmd := newTestCmd(&bytes.Buffer{}, &errBuf)
	flags := &cmdctx.RootFlags{Output: "json"}

	cmdctx.WriteError(flags, cmd, errors.New("something went wrong"))
	got := errBuf.String()
	if !strings.Contains(got, `"code":"internal_error"`) {
		t.Errorf("expected internal_error code, got: %q", got)
	}
	if !strings.Contains(got, `"message":"something went wrong"`) {
		t.Errorf("expected message field, got: %q", got)
	}
}

func TestWriteError_JSONMode_CodedError(t *testing.T) {
	var errBuf bytes.Buffer
	cmd := newTestCmd(&bytes.Buffer{}, &errBuf)
	flags := &cmdctx.RootFlags{Output: "json"}

	ce := cmdctx.Err("project_not_found", "no devbox.yml found").
		WithHint("run from a Devbox project directory").
		WithDetail("searched_path", "/home/user/proj")

	cmdctx.WriteError(flags, cmd, ce)
	got := errBuf.String()
	if !strings.Contains(got, `"code":"project_not_found"`) {
		t.Errorf("expected code field, got: %q", got)
	}
	if !strings.Contains(got, `"hint"`) {
		t.Errorf("expected hint field, got: %q", got)
	}
	if !strings.Contains(got, `"details"`) {
		t.Errorf("expected details field, got: %q", got)
	}
}

func TestWriteError_NilError_Noop(t *testing.T) {
	var errBuf bytes.Buffer
	cmd := newTestCmd(&bytes.Buffer{}, &errBuf)
	flags := &cmdctx.RootFlags{Output: "json"}

	cmdctx.WriteError(flags, cmd, nil)
	if errBuf.Len() != 0 {
		t.Errorf("nil error should be a no-op, got: %q", errBuf.String())
	}
}

// ---- CodedError ----

func TestCodedError_ErrorsAs(t *testing.T) {
	inner := errors.New("inner")
	ce := cmdctx.ErrWrap("test_code", inner)
	wrapped := fmt.Errorf("outer: %w", ce)

	var target *cmdctx.CodedError
	if !errors.As(wrapped, &target) {
		t.Fatal("errors.As should find *CodedError through wrapping")
	}
	if target.Code != "test_code" {
		t.Errorf("code: got %q, want %q", target.Code, "test_code")
	}
}

func TestCodedError_Unwrap(t *testing.T) {
	inner := errors.New("root cause")
	ce := cmdctx.ErrWrap("some_code", inner)
	if !errors.Is(ce, inner) {
		t.Error("errors.Is should traverse Unwrap chain to find inner error")
	}
}

func TestCodedError_Chaining(t *testing.T) {
	ce := cmdctx.Err("e", "msg").WithHint("hint text").WithDetail("k", "v")
	if ce.Hint != "hint text" {
		t.Errorf("hint: got %q, want %q", ce.Hint, "hint text")
	}
	if ce.Details["k"] != "v" {
		t.Errorf("detail: got %v, want %q", ce.Details["k"], "v")
	}
}

func TestCodedError_NoHintOmitted(t *testing.T) {
	var errBuf bytes.Buffer
	cmd := newTestCmd(&bytes.Buffer{}, &errBuf)
	flags := &cmdctx.RootFlags{Output: "json"}

	cmdctx.WriteError(flags, cmd, cmdctx.Err("test", "msg"))
	got := errBuf.String()
	if strings.Contains(got, `"hint"`) {
		t.Errorf("hint field should be omitted when empty, got: %q", got)
	}
	if strings.Contains(got, `"details"`) {
		t.Errorf("details field should be omitted when nil, got: %q", got)
	}
}
