package trace

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRedactionOnEveryPrinterPath(t *testing.T) {
	reset(t)
	RegisterRedaction([]string{"super-secret-token"})
	t.Cleanup(ResetRedaction)

	// 1. fallback writer
	var buf bytes.Buffer
	Configure(&buf, LevelVerbose)
	Decision(context.Background(), "token=%s", "super-secret-token")
	if got := buf.String(); strings.Contains(got, "super-secret-token") {
		t.Errorf("fallback line %q still carries the plaintext", got)
	} else if !strings.Contains(got, "token=***") {
		t.Errorf("fallback line = %q, want token=***", got)
	}

	// 2. global printer stack
	global := &capturePrinter{}
	restore := SetPrinter(global)
	Decision(context.Background(), "token=%s", "super-secret-token")
	restore()
	if got := global.snapshot(); len(got) != 1 || got[0] != "token=***" {
		t.Errorf("global printer lines = %v, want [token=***]", got)
	}

	// 3. context printer (the parallel-step path that also mirrors to .dwe/logs)
	ctxPrinter := &capturePrinter{}
	Decision(WithLinePrinter(context.Background(), ctxPrinter), "token=%s", "super-secret-token")
	if got := ctxPrinter.snapshot(); len(got) != 1 || got[0] != "token=***" {
		t.Errorf("ctx printer lines = %v, want [token=***]", got)
	}
}

// TestRedactionOfQuotedArgument pins the per-argument pass in Command:
// quoteArg escapes an embedded apostrophe by breaking out of the surrounding
// single quotes, so a secret containing one is no longer a substring of the
// formatted line and an emit-only pass would miss it.
func TestRedactionOfQuotedArgument(t *testing.T) {
	reset(t)
	const secret = "pa'ss word"
	RegisterRedaction([]string{secret})
	t.Cleanup(ResetRedaction)

	var buf bytes.Buffer
	Configure(&buf, LevelVerbose)
	Command(context.Background(), "psql", "--password", secret)

	got := buf.String()
	if strings.Contains(got, "pa") && strings.Contains(got, "ss word") {
		t.Errorf("line %q still carries the secret", got)
	}
	// The placeholder is quoted like any other argument containing '*' —
	// FormatCommand is untouched and still sees a plain argument.
	if want := "$ psql --password '***'\n"; got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

func TestRegisterRedactionUnions(t *testing.T) {
	reset(t)
	t.Cleanup(ResetRedaction)
	RegisterRedaction([]string{"first-value"})
	RegisterRedaction([]string{"second-value"})

	var buf bytes.Buffer
	Configure(&buf, LevelVerbose)
	Decision(context.Background(), "a=%s b=%s", "first-value", "second-value")

	if got, want := buf.String(), "a=*** b=***\n"; got != want {
		t.Errorf("line = %q, want %q — the second registration replaced the first", got, want)
	}
}

func TestRegisterRedactionSkipsShortValues(t *testing.T) {
	reset(t)
	t.Cleanup(ResetRedaction)
	RegisterRedaction([]string{"ab", "abc"})

	var buf bytes.Buffer
	Configure(&buf, LevelVerbose)
	Decision(context.Background(), "abc and abandon")

	if got, want := buf.String(), "abc and abandon\n"; got != want {
		t.Errorf("line = %q, want %q — short values must not be redacted", got, want)
	}
}

func TestResetRedactionRestoresPlaintext(t *testing.T) {
	reset(t)
	RegisterRedaction([]string{"reversible-value"})
	var buf bytes.Buffer
	Configure(&buf, LevelVerbose)
	Decision(context.Background(), "v=%s", "reversible-value")
	if got := buf.String(); !strings.Contains(got, "***") {
		t.Fatalf("line = %q, want it redacted before the reset", got)
	}

	ResetRedaction()
	buf.Reset()
	Decision(context.Background(), "v=%s", "reversible-value")
	if got, want := buf.String(), "v=reversible-value\n"; got != want {
		t.Errorf("line = %q, want %q after ResetRedaction", got, want)
	}
}

// TestFormatCommandUnaffectedByRedaction pins that quoting is untouched: the
// redaction happens on the arguments Command passes in, never inside
// FormatCommand, so its goldens do not move.
func TestFormatCommandUnaffectedByRedaction(t *testing.T) {
	reset(t)
	RegisterRedaction([]string{"quoted value"})
	t.Cleanup(ResetRedaction)

	if got, want := FormatCommand([]string{"sh", "-c", "quoted value"}), "sh -c 'quoted value'"; got != want {
		t.Errorf("FormatCommand = %q, want %q", got, want)
	}
}

// TestRedactExported covers the exported entry point used by display code
// outside the diagnostic path (pipeline plan strings).
func TestRedactExported(t *testing.T) {
	reset(t)
	t.Cleanup(ResetRedaction)

	if got, want := Redact("v=plain"), "v=plain"; got != want {
		t.Errorf("Redact with nothing registered = %q, want %q", got, want)
	}

	RegisterRedaction([]string{"display-secret"})
	if got, want := Redact("v=display-secret and display-secret"), "v=*** and ***"; got != want {
		t.Errorf("Redact = %q, want %q", got, want)
	}
	if got, want := Redact(""), ""; got != want {
		t.Errorf("Redact(\"\") = %q, want %q", got, want)
	}
}

// TestRedactSkipsShortValues pins the documented limit: a value under
// secrets.MinRedactRunes is never redacted, on any surface.
func TestRedactSkipsShortValues(t *testing.T) {
	reset(t)
	t.Cleanup(ResetRedaction)
	RegisterRedaction([]string{"abc"})

	if got, want := Redact("v=abc"), "v=abc"; got != want {
		t.Errorf("Redact = %q, want %q — a 3-rune value must stay verbatim", got, want)
	}
}

func TestRegisterRedactionEmptyIsNoop(t *testing.T) {
	reset(t)
	t.Cleanup(ResetRedaction)
	RegisterRedaction(nil)
	RegisterRedaction([]string{})

	var buf bytes.Buffer
	Configure(&buf, LevelVerbose)
	Decision(context.Background(), "plain line")
	if got, want := buf.String(), "plain line\n"; got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}
