package preflight

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

// TestRun_SkipEmitsDecision asserts the --skip-preflight bypass emits a Decision
// at Verbose (and stays silent at LevelOff).
func TestRun_SkipEmitsDecision(t *testing.T) {
	cases := []struct {
		name  string
		level trace.Level
		want  bool
	}{
		{"verbose", trace.LevelVerbose, true},
		{"off", trace.LevelOff, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var diag bytes.Buffer
			trace.Configure(&diag, tc.level)
			defer trace.Configure(nil, trace.LevelOff)

			err := Run(context.Background(), &config.DweConfig{}, nil, t.TempDir(), "run", true, io.Discard)
			if err != nil {
				t.Fatalf("Run(skip=true): %v", err)
			}
			got := strings.Contains(diag.String(), "preflight skipped")
			if got != tc.want {
				t.Fatalf("level=%v: skip decision present=%v want %v (diag=%q)", tc.level, got, tc.want, diag.String())
			}
		})
	}
}

// TestRun_EmitsRunningAndResultDecisions asserts a real preflight pass emits the
// "running" and "result" decisions at Verbose and nothing at LevelOff.
func TestRun_EmitsRunningAndResultDecisions(t *testing.T) {
	var diag bytes.Buffer
	trace.Configure(&diag, trace.LevelVerbose)
	defer trace.Configure(nil, trace.LevelOff)

	// An empty config in an empty dir may surface diagnostics; we only assert
	// the decision lines, so ignore the returned *Error.
	_ = Run(context.Background(), &config.DweConfig{}, nil, t.TempDir(), "run", false, io.Discard)

	out := diag.String()
	if !strings.Contains(out, "preflight running for stage") {
		t.Errorf("missing running decision: %q", out)
	}
	if !strings.Contains(out, "preflight result for stage") {
		t.Errorf("missing result decision: %q", out)
	}

	diag.Reset()
	trace.Configure(&diag, trace.LevelOff)
	_ = Run(context.Background(), &config.DweConfig{}, nil, t.TempDir(), "run", false, io.Discard)
	if diag.Len() != 0 {
		t.Fatalf("off: decisions leaked: %q", diag.String())
	}
}
