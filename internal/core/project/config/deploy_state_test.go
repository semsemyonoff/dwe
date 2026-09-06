package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestLoadProjectDeployConfigWithState pins the deploy sibling of
// LoadResetConfigWithState across the four shapes `dwe deploy eject` branches
// on: an authored file, an all-comment file, a file whose only key is `log:`
// (authored to the decoder, inert to `dwe validate`, which is why the eject
// refusal keys on phases too), and an absent file.
func TestLoadProjectDeployConfigWithState(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		wantState  PipelineFileState
		wantPhases int
		wantLog    bool
	}{
		{
			name:       "authored",
			content:    "phases:\n  - name: build\n    steps:\n      - name: noop\n        type: shell\n        cmd: \"true\"\n",
			wantState:  PipelineStateAuthored,
			wantPhases: 1,
			wantLog:    true, // deploy's defaultLog fills the absent log: key
		},
		{
			name:       "all-comment",
			content:    "# nothing active here\n#   phases: []\n\n",
			wantState:  PipelineStateDefaultFallback,
			wantPhases: 0,
			// defaultLog is applied on the io.EOF path too, so an inert file is
			// distinguishable only by its state and phase count, never by Log.
			wantLog: true,
		},
		{
			name:       "log-only",
			content:    "log: false\n",
			wantState:  PipelineStateAuthored,
			wantPhases: 0,
			wantLog:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "deploy.yml")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatalf("write deploy.yml: %v", err)
			}
			cfg, state, err := LoadProjectDeployConfigWithState(path)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if state != tc.wantState {
				t.Fatalf("state = %s, want %s", pipelineStateName(state), pipelineStateName(tc.wantState))
			}
			if cfg == nil {
				t.Fatal("cfg = nil, want non-nil")
			}
			if len(cfg.Phases) != tc.wantPhases {
				t.Fatalf("phases = %d, want %d", len(cfg.Phases), tc.wantPhases)
			}
			if got := cfg.LogEnabled(); got != tc.wantLog {
				t.Fatalf("LogEnabled() = %v, want %v", got, tc.wantLog)
			}
		})
	}

	t.Run("absent", func(t *testing.T) {
		_, _, err := LoadProjectDeployConfigWithState(filepath.Join(t.TempDir(), "deploy.yml"))
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("err = %v, want os.ErrNotExist", err)
		}
	})

	// The lenient validator shape is a different loader on purpose: after: is
	// structurally rejected here and accepted there.
	t.Run("after-rejected", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "deploy.yml")
		content := "after: [db]\nphases: []\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write deploy.yml: %v", err)
		}
		if _, _, err := LoadProjectDeployConfigWithState(path); err == nil {
			t.Fatal("err = nil, want a strict-decode failure on after:")
		}
	})
}
