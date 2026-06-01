package daemon

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestResolveContainerName(t *testing.T) {
	tests := []struct {
		name        string
		projectFull string
		rendered    string
		want        string
		wantErr     bool
	}{
		{
			name:        "with project prefix",
			projectFull: "my-proj",
			rendered:    "php_queue_default",
			want:        "my-proj-php_queue_default",
		},
		{
			name:        "without project prefix",
			projectFull: "",
			rendered:    "php_queue_default",
			want:        "php_queue_default",
		},
		{
			name:        "alnum dots dashes underscores ok",
			projectFull: "proj",
			rendered:    "svc.worker_1-a",
			want:        "proj-svc.worker_1-a",
		},
		{
			name:        "empty rendered",
			projectFull: "proj",
			rendered:    "",
			wantErr:     true,
		},
		{
			name:        "shell metachar semicolon",
			projectFull: "proj",
			rendered:    "svc;rm",
			wantErr:     true,
		},
		{
			name:        "dollar sign rejected",
			projectFull: "proj",
			rendered:    "svc$x",
			wantErr:     true,
		},
		{
			name:        "backtick rejected",
			projectFull: "proj",
			rendered:    "svc`x`",
			wantErr:     true,
		},
		{
			name:        "newline rejected",
			projectFull: "proj",
			rendered:    "svc\nx",
			wantErr:     true,
		},
		{
			name:        "leading dash rejected (when no prefix)",
			projectFull: "",
			rendered:    "-svc",
			wantErr:     true,
		},
		{
			name:        "leading dot rejected (no prefix)",
			projectFull: "",
			rendered:    ".svc",
			wantErr:     true,
		},
		{
			name:        "space rejected",
			projectFull: "proj",
			rendered:    "svc x",
			wantErr:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveContainerName(tt.projectFull, tt.rendered)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ResolveContainerName(%q,%q) = %q, want error", tt.projectFull, tt.rendered, got)
				}
				if !errors.Is(err, ErrContainerNameInvalid) {
					t.Fatalf("err = %v, want errors.Is ErrContainerNameInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStandardLabels_KeyStability(t *testing.T) {
	got := StandardLabels("my-proj", "services.main.queue", map[string]any{
		"name":  "emails",
		"queue": "high",
	})
	// argv shape: [--label k=v --label k=v --label k=v]
	if len(got) != 6 {
		t.Fatalf("len = %d, want 6", len(got))
	}
	if got[0] != "--label" || got[2] != "--label" || got[4] != "--label" {
		t.Fatalf("flag positions wrong: %v", got)
	}
	if got[1] != LabelProject+"=my-proj" {
		t.Errorf("first label wrong: got %q, want %q", got[1], LabelProject+"=my-proj")
	}
	if got[3] != LabelDaemonID+"=services.main.queue" {
		t.Errorf("second label wrong: got %q, want %q", got[3], LabelDaemonID+"=services.main.queue")
	}
	if !strings.HasPrefix(got[5], LabelDaemonParams+"=") {
		t.Errorf("third label not daemon.params: %q", got[5])
	}
}

func TestStandardLabels_JSONRoundtrip(t *testing.T) {
	tests := map[string]map[string]any{
		"plain":          {"name": "default"},
		"with quotes":    {"name": `he said "hi"`},
		"with backslash": {"path": `C:\Users\x`},
		"control chars":  {"x": "a\nb\tc"},
		"empty":          {},
		"nil":            nil,
	}
	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			got := StandardLabels("p", "d", params)
			// extract the params label value
			paramsKV := got[5]
			_, jsonVal, ok := strings.Cut(paramsKV, "=")
			if !ok {
				t.Fatalf("malformed label: %q", paramsKV)
			}
			var decoded map[string]any
			if err := json.Unmarshal([]byte(jsonVal), &decoded); err != nil {
				t.Fatalf("json roundtrip failed: %v (raw=%q)", err, jsonVal)
			}
			expectLen := len(params)
			if len(decoded) != expectLen {
				t.Fatalf("decoded len %d, want %d (decoded=%v)", len(decoded), expectLen, decoded)
			}
			for k, v := range params {
				if decoded[k] != v {
					t.Errorf("key %q: got %v, want %v", k, decoded[k], v)
				}
			}
		})
	}
}

func TestStandardLabels_DeterministicKeyOrder(t *testing.T) {
	params := map[string]any{"b": "2", "a": "1", "c": "3"}
	// Run multiple times; the JSON value should be identical each call.
	first := StandardLabels("p", "d", params)[5]
	for range 20 {
		got := StandardLabels("p", "d", params)[5]
		if got != first {
			t.Fatalf("non-deterministic label output: %q vs %q", first, got)
		}
	}
}

func TestFilterArgsByLabels(t *testing.T) {
	got := FilterArgsByLabels("my-proj", "services.main.queue")
	want := []string{
		"--filter", "label=dwe.project=my-proj",
		"--filter", "label=dwe.daemon.id=services.main.queue",
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFilterArgsByLabels_EmptyDaemonID(t *testing.T) {
	// When daemon ID is empty (e.g. reap enumeration), filter on label
	// existence only.
	got := FilterArgsByLabels("my-proj", "")
	want := []string{
		"--filter", "label=dwe.project=my-proj",
		"--filter", "label=dwe.daemon.id",
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLabelKeyConstants(t *testing.T) {
	// These constants are part of the public contract — any rename is a
	// schema break visible via `docker inspect`. Pin them here.
	if LabelProject != "dwe.project" {
		t.Errorf("LabelProject = %q", LabelProject)
	}
	if LabelDaemonID != "dwe.daemon.id" {
		t.Errorf("LabelDaemonID = %q", LabelDaemonID)
	}
	if LabelDaemonParams != "dwe.daemon.params" {
		t.Errorf("LabelDaemonParams = %q", LabelDaemonParams)
	}
}
