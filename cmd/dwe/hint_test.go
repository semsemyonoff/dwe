package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"charm.land/fang/v2"
	lipglossv2 "charm.land/lipgloss/v2"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
)

// hintStyles is a minimal stand-in for what fang hands the error handler. The
// real one carries a width and margins; the test only needs a style whose
// Render is defined, and pins the wrapping separately below.
func hintStyles() fang.Styles {
	return fang.Styles{ErrorText: lipglossv2.NewStyle()}
}

// TestWriteHint pins the one call that makes a CodedError's hint reach a human:
// CodedError.Error() is the message alone, so without it every WithHint in the
// tree is a --output json-only field.
func TestWriteHint(t *testing.T) {
	coded := cmdctx.Err("secrets_already_initialized", "already initialized").
		WithHint("run 'dwe secrets rekey' to replace it")

	tests := []struct {
		name string
		err  error
		want string // "" = nothing written at all
	}{
		{
			name: "coded error with a hint",
			err:  coded,
			want: "hint: run 'dwe secrets rekey' to replace it",
		},
		{
			// The hint travels through a wrap: cobra and the workflow runner
			// both return errors that carry a CodedError further down.
			name: "wrapped coded error",
			err:  fmt.Errorf("running the command: %w", coded),
			want: "hint: run 'dwe secrets rekey' to replace it",
		},
		{
			name: "coded error without a hint",
			err:  cmdctx.Err("secrets_scan_failed", "the scan failed"),
		},
		{
			name: "plain error",
			err:  errors.New("something went wrong"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			writeHint(&buf, hintStyles(), tt.err)

			got := buf.String()
			if tt.want == "" {
				if got != "" {
					t.Fatalf("writeHint wrote %q, want nothing", got)
				}
				return
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("writeHint wrote %q, want it to carry %q", got, tt.want)
			}
			// The message above it is fang's; the hint must not repeat it.
			if strings.Contains(got, "already initialized") {
				t.Errorf("writeHint repeated the message: %q", got)
			}
		})
	}
}

// TestWriteHintWrapsAtTheErrorBlockWidth pins that the hint inherits the error
// block's geometry rather than running past it: a hint is routinely a full
// paragraph, and fang's own message is wrapped by exactly this style.
func TestWriteHintWrapsAtTheErrorBlockWidth(t *testing.T) {
	const width = 40
	styles := fang.Styles{ErrorText: lipglossv2.NewStyle().MarginLeft(2).Width(width - 4)}
	long := strings.Repeat("recovery instruction ", 12)

	var buf bytes.Buffer
	writeHint(&buf, styles, cmdctx.Err("code", "message").WithHint(long))

	// Printable width, not byte length: the style emits colour escapes.
	for line := range strings.SplitSeq(strings.TrimRight(buf.String(), "\n"), "\n") {
		if w := lipglossv2.Width(line); w > width {
			t.Errorf("line %d cells wide, want at most %d: %q", w, width, line)
		}
	}
}
