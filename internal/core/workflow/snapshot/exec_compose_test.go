package snapshot

import (
	"bytes"
	"context"
	"os"
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

// writeDockerYML writes workspace/docker.yml under dir.
func writeDockerYML(t *testing.T, dir, content string) {
	t.Helper()
	ws := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "docker.yml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRunWorkflow_LeavesSeeDockerProjectName pins that snapshot workflow
// leaves get the docker config: without it rc.Compose() falls back to
// project.prefix/name and the shell/script contract ignores project_name.
func TestRunWorkflow_LeavesSeeDockerProjectName(t *testing.T) {
	t.Setenv("COMPOSE_PROJECT_NAME", "ambient")
	tmp := t.TempDir()
	writeDockerYML(t, tmp, "project_name: custom\n")
	if err := os.WriteFile(filepath.Join(tmp, "s.sh"), []byte(`printf 'SCRIPT=%s\n' "$COMPOSE_PROJECT_NAME"`), 0o755); err != nil {
		t.Fatal(err)
	}

	reg := registry.NewEmptyRegistry()
	registerShellEcho(t, reg, "fake.shell", `printf 'SHELL=%s\n' "$COMPOSE_PROJECT_NAME"`)
	reg.AddCommandForTest(&model.CommandDef{
		ID:     "fake.script",
		Type:   model.CommandTypeScript,
		Files:  map[string]model.FileSpec{},
		Script: &model.ScriptDef{Path: "s.sh"},
	})

	var out, errBuf bytes.Buffer
	err := RunWorkflow(context.Background(), ExecParams{
		Cfg:      testCfg(),
		Registry: reg,
		BaseDir:  tmp,
		Workflow: &config.SnapshotWorkflow{Steps: []model.WorkflowStep{
			{Command: "fake.shell"},
			{Command: "fake.script"},
		}},
		Vars:   meta.BuildSnapshotVars("snapname", filepath.Join(tmp, "snap"), "", "", time.Time{}),
		Scope:  tpl.SnapshotScopeCreate,
		Stdout: &out,
		Stderr: &errBuf,
	})
	if err != nil {
		t.Fatalf("RunWorkflow: %v (stderr=%s)", err, errBuf.String())
	}
	for _, want := range []string{"SHELL=custom", "SCRIPT=custom"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout missing %q\nstdout: %s\nstderr: %s", want, out.String(), errBuf.String())
		}
	}
}

func TestRunWorkflow_DockerConfigLoadErrorReturned(t *testing.T) {
	tmp := t.TempDir()
	writeDockerYML(t, tmp, "project_name: [\n")

	reg := registry.NewEmptyRegistry()
	registerShellEcho(t, reg, "fake.echo", `echo should-not-run`)

	var out, errBuf bytes.Buffer
	err := RunWorkflow(context.Background(), ExecParams{
		Cfg:      testCfg(),
		Registry: reg,
		BaseDir:  tmp,
		Workflow: &config.SnapshotWorkflow{Steps: []model.WorkflowStep{{Command: "fake.echo"}}},
		Vars:     meta.BuildSnapshotVars("snapname", filepath.Join(tmp, "snap"), "", "", time.Time{}),
		Scope:    tpl.SnapshotScopeCreate,
		Stdout:   &out,
		Stderr:   &errBuf,
	})
	if err == nil || !strings.Contains(err.Error(), "docker config") {
		t.Fatalf("RunWorkflow error = %v, want a docker config load error", err)
	}
	if strings.Contains(out.String(), "should-not-run") {
		t.Error("the workflow ran despite the docker config load error")
	}
}
