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

func TestMatchServices(t *testing.T) {
	services := map[string]config.ServiceConfig{
		"api":           {Enabled: true},
		"worker":        {Enabled: false},
		"also-disabled": {Enabled: false},
		"db":            {Enabled: true},
	}
	cases := []struct {
		name  string
		entry config.CheckEntry
		want  bool
	}{
		{"no services clause runs unconditionally", config.CheckEntry{Services: nil}, true},
		{"empty list runs unconditionally", config.CheckEntry{Services: []string{}}, true},
		{"single enabled service matches", config.CheckEntry{Services: []string{"api"}}, true},
		{"single disabled service skips", config.CheckEntry{Services: []string{"worker"}}, false},
		{"OR: at least one enabled matches", config.CheckEntry{Services: []string{"worker", "api"}}, true},
		{"all disabled (multi) skips", config.CheckEntry{Services: []string{"worker", "also-disabled"}}, false},
		{"unknown name contributes nothing", config.CheckEntry{Services: []string{"typo"}}, false},
		{"unknown + enabled still matches", config.CheckEntry{Services: []string{"typo", "db"}}, true},
		{"unknown + disabled still skips", config.CheckEntry{Services: []string{"typo", "worker"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchServices(tc.entry, services); got != tc.want {
				t.Errorf("MatchServices = %v, want %v", got, tc.want)
			}
		})
	}

	// Nil services map disables the gate (treat as "no view of services"; same as no clause).
	if !MatchServices(config.CheckEntry{Services: nil}, nil) {
		t.Error("nil services + no clause should pass")
	}
	if MatchServices(config.CheckEntry{Services: []string{"api"}}, nil) {
		t.Error("nil services map + non-empty clause should NOT match (no service is Enabled)")
	}
}
