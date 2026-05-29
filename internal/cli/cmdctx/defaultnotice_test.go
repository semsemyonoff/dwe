package cmdctx

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestEmitDefaultNotice(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		pipeline   string
		file       string
		wantInErr  string
		wantSilent bool
	}{
		{
			name:      "deploy text mode",
			output:    "text",
			pipeline:  "deploy",
			file:      "deploy",
			wantInErr: "Using built-in default deploy pipeline (override with devbox/deploy.yml).",
		},
		{
			name:      "reset text mode",
			output:    "text",
			pipeline:  "reset",
			file:      "reset",
			wantInErr: "Using built-in default reset pipeline (override with devbox/reset.yml).",
		},
		{
			name:      "run lifecycle text mode",
			output:    "text",
			pipeline:  "run",
			file:      "lifecycle",
			wantInErr: "Using built-in default run pipeline (override with devbox/lifecycle.yml).",
		},
		{
			name:      "stop lifecycle text mode",
			output:    "text",
			pipeline:  "stop",
			file:      "lifecycle",
			wantInErr: "Using built-in default stop pipeline (override with devbox/lifecycle.yml).",
		},
		{
			name:       "json mode suppressed",
			output:     "json",
			pipeline:   "deploy",
			file:       "deploy",
			wantSilent: true,
		},
		{
			name:      "empty output treated as text",
			output:    "",
			pipeline:  "deploy",
			file:      "deploy",
			wantInErr: "Using built-in default deploy pipeline (override with devbox/deploy.yml).",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			var errBuf bytes.Buffer
			cmd.SetErr(&errBuf)
			flags := &RootFlags{Output: tc.output}

			EmitDefaultNotice(cmd, flags, tc.pipeline, tc.file)

			got := errBuf.String()
			if tc.wantSilent {
				if got != "" {
					t.Errorf("json mode: expected empty stderr, got %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.wantInErr) {
				t.Errorf("stderr = %q, want to contain %q", got, tc.wantInErr)
			}
		})
	}
}
