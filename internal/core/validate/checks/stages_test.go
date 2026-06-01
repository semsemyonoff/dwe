package checks

import (
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

func TestMatchStage(t *testing.T) {
	entry := config.CheckEntry{Stages: []string{"deploy", "run"}}
	cases := []struct {
		stage string
		want  bool
	}{
		{"", true},
		{"deploy", true},
		{"run", true},
		{"stop", false},
		{"other", false},
	}
	for _, tc := range cases {
		if got := MatchStage(entry, tc.stage); got != tc.want {
			t.Errorf("MatchStage(%q) = %v, want %v", tc.stage, got, tc.want)
		}
	}
	// No stages → only the empty filter matches.
	empty := config.CheckEntry{Stages: nil}
	if !MatchStage(empty, "") {
		t.Error("empty filter must match an entry with no stages")
	}
	if MatchStage(empty, "deploy") {
		t.Error("a specific stage must not match an entry with no stages")
	}
}
