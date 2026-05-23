package tpl

import (
	"strings"
	"testing"
)

func TestCompileVarSyntax_snapshot(t *testing.T) {
	got := CompileVarSyntax("${snapshot.name}")
	want := `{{ resolveMap .Snapshot "name" }}`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderCommand_snapshotInCreateScope(t *testing.T) {
	ctx := &RenderContext{
		Snapshot: map[string]any{
			"name": "feature-x",
			"path": "/snap/feature-x",
		},
		SnapshotScope: SnapshotScopeCreate,
	}
	got, err := RenderCommand("dump --to ${snapshot.path}/db.sql for ${snapshot.name}", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "dump --to /snap/feature-x/db.sql for feature-x"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderCommand_snapshotInNoneScopeRejected(t *testing.T) {
	ctx := &RenderContext{}
	_, err := RenderCommand("${snapshot.name}", ctx)
	if err == nil {
		t.Fatal("expected error for snapshot.name in None scope")
	}
	if !strings.Contains(err.Error(), "outside a snapshot workflow") {
		t.Errorf("error %q missing scope hint", err.Error())
	}
}

func TestRenderCommand_snapshotCreatedAtInCreateRejected(t *testing.T) {
	ctx := &RenderContext{
		Snapshot:      map[string]any{"created_at": "2026-01-01T00:00:00Z"},
		SnapshotScope: SnapshotScopeCreate,
	}
	_, err := RenderCommand("at=${snapshot.created_at}", ctx)
	if err == nil {
		t.Fatal("expected error for created_at in Create scope")
	}
	if !strings.Contains(err.Error(), "created_at") {
		t.Errorf("error %q missing created_at mention", err.Error())
	}
}

func TestRenderCommand_snapshotCreatedAtInRestoreOK(t *testing.T) {
	ctx := &RenderContext{
		Snapshot:      map[string]any{"created_at": "2026-01-01T00:00:00Z"},
		SnapshotScope: SnapshotScopeRestoreOrRemove,
	}
	got, err := RenderCommand("at=${snapshot.created_at}", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "at=2026-01-01T00:00:00Z" {
		t.Errorf("got %q", got)
	}
}

func TestRenderCommand_snapshotMissingKeyEmpty(t *testing.T) {
	ctx := &RenderContext{
		Snapshot:      map[string]any{"name": "x"},
		SnapshotScope: SnapshotScopeRestoreOrRemove,
	}
	got, err := RenderCommand("v=${snapshot.variant}", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "v=" {
		t.Errorf("got %q, want %q", got, "v=")
	}
}

func TestSnapshotScope_String(t *testing.T) {
	cases := []struct {
		s    SnapshotScope
		want string
	}{
		{SnapshotScopeNone, "none"},
		{SnapshotScopeCreate, "create"},
		{SnapshotScopeRestoreOrRemove, "restore"},
	}
	for _, c := range cases {
		if c.s.String() != c.want {
			t.Errorf("%d.String() = %q, want %q", c.s, c.s.String(), c.want)
		}
	}
}
