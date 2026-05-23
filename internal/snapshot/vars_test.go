package snapshot

import (
	"testing"
	"time"
)

func TestBuildSnapshotVars(t *testing.T) {
	ts := time.Date(2026, 5, 24, 11, 2, 0, 0, time.UTC)
	got := BuildSnapshotVars("feature-x", "/abs/snapshots/feature-x", "WIP feature x", "db-only", ts)

	want := map[string]string{
		"name":        "feature-x",
		"path":        "/abs/snapshots/feature-x",
		"description": "WIP feature x",
		"variant":     "db-only",
		"created_at":  "2026-05-24T11:02:00Z",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("key %q: got %v, want %q", k, got[k], v)
		}
	}
}

func TestBuildSnapshotVars_normalizesToUTC(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("tz data unavailable: %v", err)
	}
	ts := time.Date(2026, 5, 24, 7, 2, 0, 0, loc) // 11:02 UTC
	got := BuildSnapshotVars("n", "p", "", "", ts)
	if got["created_at"] != "2026-05-24T11:02:00Z" {
		t.Errorf("created_at = %v, want UTC RFC3339", got["created_at"])
	}
}
