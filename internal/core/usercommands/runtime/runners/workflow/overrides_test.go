package workflow

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/core/execution/filesgate"
	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/shared/tpl"
)

// buildLeafWithRequiredFile builds a shell command whose Files block declares a
// single read+required file; tests toggle the file's presence on disk to drive
// the files_gate probe.
func buildLeafWithRequiredFile(id, path string) *CommandDef {
	parts := strings.Split(id, ".")
	return &CommandDef{
		Type:      CommandTypeShell,
		ID:        id,
		Group:     strings.Join(parts[:len(parts)-1], "."),
		LocalName: parts[len(parts)-1],
		Cmd:       "true",
		Files: map[string]FileSpec{
			"dump": {Access: FileAccessRead, Path: path, Required: true},
		},
	}
}

// runWorkflowWithOverrides runs the given workflow with sub_step_overrides
// applied. Returns stdout, stderr, and the runner error.
func runWorkflowWithOverrides(t *testing.T, projectRoot string, reg *Registry, wf *CommandDef, overrides map[string]config.SubStepOverride) (string, string, error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	ctx := RunContext{
		Cmd:                      wf,
		Params:                   map[string]any{},
		Context:                  map[string]any{},
		Render:                   &tpl.RenderContext{Params: map[string]any{}, Raw: map[string]any{}},
		Config:                   &config.DevboxConfig{SchemaVersion: "2"},
		Registry:                 reg,
		ProjectRoot:              projectRoot,
		Stdout:                   &outBuf,
		Stderr:                   &errBuf,
		WorkflowSubStepOverrides: overrides,
	}
	err := (&WorkflowRunner{}).Run(context.Background(), ctx)
	return outBuf.String(), errBuf.String(), err
}

func TestWorkflowOverrides_SequentialSkipsWhenGateMissing(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.sql")
	leaf := buildLeafWithRequiredFile("db.dump-deploy", missing)
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "db.dumps-deploy",
		Group:     "db",
		LocalName: "dumps-deploy",
		Steps: []WorkflowStep{
			{Name: "deploy-main", Command: "db.dump-deploy"},
		},
	}
	reg := buildWorkflowRegistry(wf, leaf)
	overrides := map[string]config.SubStepOverride{
		"deploy-main": {FilesGate: &filesgate.FilesGate{
			State:   filesgate.StateReadable,
			Require: filesgate.RequireRequired{},
		}},
	}
	_, errOut, err := runWorkflowWithOverrides(t, dir, reg, wf, overrides)
	if err != nil {
		t.Fatalf("expected nil err; got %v\nstderr:\n%s", err, errOut)
	}
	if !strings.Contains(errOut, "skipped") || !strings.Contains(errOut, "files_gate") {
		t.Errorf("expected skip-line referencing files_gate; got stderr:\n%s", errOut)
	}
}

func TestWorkflowOverrides_SequentialRunsWhenGateSatisfied(t *testing.T) {
	dir := t.TempDir()
	dump := filepath.Join(dir, "present.sql")
	if err := os.WriteFile(dump, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	leaf := buildLeafWithRequiredFile("db.dump-deploy", dump)
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "db.dumps-deploy",
		Group:     "db",
		LocalName: "dumps-deploy",
		Steps: []WorkflowStep{
			{Name: "deploy-main", Command: "db.dump-deploy"},
		},
	}
	reg := buildWorkflowRegistry(wf, leaf)
	overrides := map[string]config.SubStepOverride{
		"deploy-main": {FilesGate: &filesgate.FilesGate{
			State:   filesgate.StateReadable,
			Require: filesgate.RequireRequired{},
		}},
	}
	_, errOut, err := runWorkflowWithOverrides(t, dir, reg, wf, overrides)
	if err != nil {
		t.Fatalf("expected nil err; got %v\nstderr:\n%s", err, errOut)
	}
	if strings.Contains(errOut, "skipped") {
		t.Errorf("expected no skip; got stderr:\n%s", errOut)
	}
}

func TestWorkflowOverrides_ParallelMixedGate(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "present.sql")
	missing := filepath.Join(dir, "missing.sql")
	if err := os.WriteFile(present, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	mainLeaf := buildLeafWithRequiredFile("db.dump-deploy-main", present)
	stockLeaf := buildLeafWithRequiredFile("db.dump-deploy-stock", missing)
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "db.dumps-deploy",
		Group:     "db",
		LocalName: "dumps-deploy",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{Steps: []WorkflowStep{
				{Name: "deploy-main", Command: "db.dump-deploy-main"},
				{Name: "deploy-stock", Command: "db.dump-deploy-stock"},
			}}},
		},
	}
	reg := buildWorkflowRegistry(wf, mainLeaf, stockLeaf)
	overrides := map[string]config.SubStepOverride{
		"deploy-main": {FilesGate: &filesgate.FilesGate{
			State:   filesgate.StateReadable,
			Require: filesgate.RequireRequired{},
		}},
		"deploy-stock": {FilesGate: &filesgate.FilesGate{
			State:   filesgate.StateReadable,
			Require: filesgate.RequireRequired{},
		}},
	}
	_, errOut, err := runWorkflowWithOverrides(t, dir, reg, wf, overrides)
	if err != nil {
		t.Fatalf("expected nil err; got %v\nstderr:\n%s", err, errOut)
	}
	// Stock should be skipped (missing file), main should run.
	if !strings.Contains(errOut, "deploy-stock") || !strings.Contains(errOut, "Skipped") {
		t.Errorf("expected deploy-stock to be Skipped; got stderr:\n%s", errOut)
	}
	if !strings.Contains(errOut, "deploy-main") || !strings.Contains(errOut, "Done") {
		t.Errorf("expected deploy-main to complete; got stderr:\n%s", errOut)
	}
}

func TestWorkflowOverrides_DoesNotPropagateToInnerWorkflow(t *testing.T) {
	// Outer workflow has a sub-step that itself is a workflow; the inner
	// workflow's sub-step happens to share the override key. The override
	// must NOT activate inside the inner workflow.
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.sql")
	leaf := buildLeafWithRequiredFile("db.dump", missing)
	inner := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "db.inner",
		Group:     "db",
		LocalName: "inner",
		Steps: []WorkflowStep{
			// Same Name as the outer override key — must NOT be gated.
			{Name: "leaf", Command: "db.dump"},
		},
	}
	outer := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "db.outer",
		Group:     "db",
		LocalName: "outer",
		Steps: []WorkflowStep{
			{Name: "leaf", Command: "db.dump"}, // outer's "leaf" → gated, will be skipped
			{Name: "go-inner", Command: "db.inner"},
		},
	}
	reg := buildWorkflowRegistry(outer, inner, leaf)
	overrides := map[string]config.SubStepOverride{
		"leaf": {FilesGate: &filesgate.FilesGate{
			State:   filesgate.StateReadable,
			Require: filesgate.RequireRequired{},
		}},
	}
	_, errOut, err := runWorkflowWithOverrides(t, dir, reg, outer, overrides)
	// Outer "leaf" is gated and skipped. Inner workflow's "leaf" runs — but its
	// command targets a file that does not exist, so the underlying shell
	// command's files: requirement fails at runtime with a hard error.
	if err == nil {
		t.Fatal("expected inner workflow to fail because override did not propagate; got nil")
	}
	if !strings.Contains(errOut, "files_gate") {
		t.Errorf("expected outer leaf skip with files_gate reason; got stderr:\n%s", errOut)
	}
}
