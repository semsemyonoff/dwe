package snapshot

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/core/workflow/snapshot/meta"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// testCfg returns a minimal DevboxConfig usable for snapshot exec tests.
func testCfg() *config.DevboxConfig {
	return &config.DevboxConfig{
		Raw: map[string]any{
			"project": map[string]any{"name": "fixture"},
		},
		Project: config.ProjectConfig{Name: "fixture"},
	}
}

// registerShellEcho registers a `type: shell` command that echoes a literal
// run: string under the given ID.
func registerShellEcho(t *testing.T, reg *registry.Registry, id, run string) {
	t.Helper()
	cmd := &model.CommandDef{
		ID:    id,
		Type:  model.CommandTypeShell,
		Files: map[string]model.FileSpec{},
		Cmd:   run,
	}
	reg.AddCommandForTest(cmd)
}

func TestRunWorkflow_PropagatesSnapshotVarsToShellLeaf(t *testing.T) {
	reg := registry.NewEmptyRegistry()
	// Marker is written via ${snapshot.path} resolved through the workflow's
	// `with:` -> param indirection; the shell command receives the rendered
	// value as $SNAPSHOT_PATH via a literal echo to stdout.
	registerShellEcho(t, reg, "fake.echo", `echo "PATH=${snapshot.path} NAME=${snapshot.name}"`)

	wf := &config.SnapshotWorkflow{
		Steps: []model.WorkflowStep{
			{Command: "fake.echo"},
		},
	}

	tmp := t.TempDir()
	snapPath := filepath.Join(tmp, "snap")
	vars := meta.BuildSnapshotVars("snapname", snapPath, "desc", "", time.Time{})

	var out, errBuf bytes.Buffer
	err := RunWorkflow(context.Background(), ExecParams{
		Cfg:      testCfg(),
		Registry: reg,
		BaseDir:  tmp,
		Workflow: wf,
		Vars:     vars,
		Scope:    tpl.SnapshotScopeCreate,
		Stdout:   &out,
		Stderr:   &errBuf,
	})
	if err != nil {
		t.Fatalf("RunWorkflow: %v (stderr=%s)", err, errBuf.String())
	}

	got := out.String()
	wantPath := "PATH=" + snapPath
	if !strings.Contains(got, wantPath) {
		t.Errorf("stdout missing %q\nstdout: %s\nstderr: %s", wantPath, got, errBuf.String())
	}
	if !strings.Contains(got, "NAME=snapname") {
		t.Errorf("stdout missing NAME=snapname\nstdout: %s\nstderr: %s", got, errBuf.String())
	}
}

func TestRunWorkflow_WhenFileExistsShortCircuits(t *testing.T) {
	reg := registry.NewEmptyRegistry()
	registerShellEcho(t, reg, "should.not.run", `echo "RAN"`)

	tmp := t.TempDir()
	missing := filepath.Join(tmp, "missing")

	wf := &config.SnapshotWorkflow{
		Steps: []model.WorkflowStep{
			{
				Command: "should.not.run",
				When:    "file-exists " + missing,
			},
		},
	}

	var out, errBuf bytes.Buffer
	err := RunWorkflow(context.Background(), ExecParams{
		Cfg:      testCfg(),
		Registry: reg,
		BaseDir:  tmp,
		Workflow: wf,
		Vars:     meta.BuildSnapshotVars("n", tmp, "", "", time.Time{}),
		Scope:    tpl.SnapshotScopeRestoreOrRemove,
		Stdout:   &out,
		Stderr:   &errBuf,
	})
	if err != nil {
		t.Fatalf("RunWorkflow: %v (stderr=%s)", err, errBuf.String())
	}
	if strings.Contains(out.String(), "RAN") {
		t.Errorf("step should have been skipped; stdout=%q", out.String())
	}
}

func TestRunWorkflow_NilWorkflow(t *testing.T) {
	err := RunWorkflow(context.Background(), ExecParams{})
	if err == nil || !strings.Contains(err.Error(), "nil workflow") {
		t.Fatalf("want nil-workflow error, got %v", err)
	}
}

func TestRunWorkflow_NoSteps(t *testing.T) {
	err := RunWorkflow(context.Background(), ExecParams{
		Workflow: &config.SnapshotWorkflow{},
	})
	if err == nil || !strings.Contains(err.Error(), "no steps") {
		t.Fatalf("want no-steps error, got %v", err)
	}
}

func TestSelectWorkflow_Defaults(t *testing.T) {
	cfg := &config.SnapshotConfig{
		Create:  &config.SnapshotWorkflow{Steps: []model.WorkflowStep{{Command: "a"}}},
		Restore: &config.SnapshotWorkflow{Steps: []model.WorkflowStep{{Command: "b"}}},
		Remove:  &config.SnapshotWorkflow{Steps: []model.WorkflowStep{{Command: "c"}}},
	}
	cases := []struct {
		kind string
		want string
	}{
		{"create", "a"},
		{"restore", "b"},
		{"remove", "c"},
	}
	for _, tc := range cases {
		w, err := SelectWorkflow(cfg, tc.kind, "")
		if err != nil {
			t.Fatalf("kind %s: %v", tc.kind, err)
		}
		if w.Steps[0].Command != tc.want {
			t.Errorf("kind %s: got %q want %q", tc.kind, w.Steps[0].Command, tc.want)
		}
	}
}

func TestSelectWorkflow_CreateMissingVariantErrors(t *testing.T) {
	cfg := &config.SnapshotConfig{
		Create: &config.SnapshotWorkflow{
			Steps: []model.WorkflowStep{{Command: "default"}},
			Variants: map[string]config.SnapshotVariant{
				"db-only": {Steps: []model.WorkflowStep{{Command: "v1"}}},
			},
		},
	}
	if _, err := SelectWorkflow(cfg, "create", "nope"); err == nil {
		t.Fatal("expected error for missing create variant")
	}
	w, err := SelectWorkflow(cfg, "create", "db-only")
	if err != nil {
		t.Fatalf("variant lookup: %v", err)
	}
	if w.Steps[0].Command != "v1" {
		t.Errorf("got %q want v1", w.Steps[0].Command)
	}
}

func TestSelectWorkflow_RestoreMissingVariantFallsBack(t *testing.T) {
	cfg := &config.SnapshotConfig{
		Restore: &config.SnapshotWorkflow{
			Steps: []model.WorkflowStep{{Command: "default-restore"}},
			Variants: map[string]config.SnapshotVariant{
				"db-only": {Steps: []model.WorkflowStep{{Command: "restore-v1"}}},
			},
		},
	}
	w, err := SelectWorkflow(cfg, "restore", "nope")
	if err != nil {
		t.Fatalf("restore fallback: %v", err)
	}
	if w.Steps[0].Command != "default-restore" {
		t.Errorf("want fallback to default, got %q", w.Steps[0].Command)
	}
	w, err = SelectWorkflow(cfg, "restore", "db-only")
	if err != nil {
		t.Fatalf("restore variant: %v", err)
	}
	if w.Steps[0].Command != "restore-v1" {
		t.Errorf("want restore-v1, got %q", w.Steps[0].Command)
	}
}

func TestSelectWorkflow_NilCfgAndUnknownKind(t *testing.T) {
	if _, err := SelectWorkflow(nil, "create", ""); err == nil {
		t.Error("want error on nil cfg")
	}
	if _, err := SelectWorkflow(&config.SnapshotConfig{}, "bogus", ""); err == nil {
		t.Error("want error on unknown kind")
	}
	if _, err := SelectWorkflow(&config.SnapshotConfig{}, "create", ""); err == nil {
		t.Error("want error when create block missing")
	}
}

func TestRunWorkflow_RejectsSnapshotVarOutsideScope(t *testing.T) {
	reg := registry.NewEmptyRegistry()
	registerShellEcho(t, reg, "fake.echo", `echo "${snapshot.path}"`)

	wf := &config.SnapshotWorkflow{
		Steps: []model.WorkflowStep{{Command: "fake.echo"}},
	}

	var out, errBuf bytes.Buffer
	err := RunWorkflow(context.Background(), ExecParams{
		Cfg:      testCfg(),
		Registry: reg,
		BaseDir:  t.TempDir(),
		Workflow: wf,
		Vars:     map[string]any{},
		Scope:    tpl.SnapshotScopeNone, // disallowed
		Stdout:   &out,
		Stderr:   &errBuf,
	})
	// Shell `cmd:` itself is not pre-rendered by the workflow leaf (HostRunner
	// renders at exec time using rc.Render which carries SnapshotScopeNone),
	// so the failure surfaces from the shell runner; assert non-nil error
	// referencing snapshot scope.
	if err == nil {
		t.Fatalf("want error for ${snapshot.*} outside scope (stdout=%q)", out.String())
	}
	if !strings.Contains(err.Error(), "snapshot") {
		t.Errorf("want snapshot scope error, got: %v", err)
	}
}
