package deploy

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"

	"github.com/spf13/cobra"
)

// makeMinimalProject writes a bare v2 devbox.yml so config + (empty)
// registry loads succeed. Returns the project root.
func makeMinimalProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	yml := "schema_version: \"2\"\nproject:\n  name: testproject\n  prefix: devbox\n"
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestRunDeployPlan_DefaultFormatRendersTitle verifies the table-format
// branch writes the "Deploy plan" section title.
func TestRunDeployPlan_DefaultFormatRendersTitle(t *testing.T) {
	dir := makeMinimalProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "devbox.yml")}

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	if err := runDeployPlan(context.Background(), cmd, flags, deployPlanOpts{}); err != nil {
		t.Fatalf("runDeployPlan: %v", err)
	}
	// SectionTitle wraps the plain text in ANSI styling but the
	// payload is always present.
	if !strings.Contains(buf.String(), "Deploy plan") {
		t.Errorf("output missing 'Deploy plan': %q", buf.String())
	}
}

// TestRunDeployPlan_ShellFormat verifies --format shell emits the shell
// header (which is distinct from the table-format title path).
func TestRunDeployPlan_ShellFormat(t *testing.T) {
	dir := makeMinimalProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "devbox.yml")}

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	if err := runDeployPlan(context.Background(), cmd, flags, deployPlanOpts{Format: "shell"}); err != nil {
		t.Fatalf("runDeployPlan(shell): %v", err)
	}
	if !strings.Contains(buf.String(), "# Deploy plan") {
		t.Errorf("shell output missing '# Deploy plan' header: %q", buf.String())
	}
}

// TestRunDeployPlan_UnknownServiceErrors verifies the explicit guard for
// unknown service names. (Without it, an empty plan would be returned with
// no error.)
func TestRunDeployPlan_UnknownServiceErrors(t *testing.T) {
	dir := makeMinimalProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "devbox.yml")}

	err := runDeployPlan(context.Background(), &cobra.Command{}, flags, deployPlanOpts{ServiceName: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown service, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("err = %v, want one mentioning the unknown service name", err)
	}
}

// TestRunDeployPlan_DefaultPipelineWhenNoDeployYML verifies that a bare project
// (no devbox/deploy.yml) succeeds, includes the docker-up step from the built-in
// default, and prints the info line on stderr.
func TestRunDeployPlan_DefaultPipelineWhenNoDeployYML(t *testing.T) {
	dir := makeMinimalProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "devbox.yml")}

	cmd := &cobra.Command{}
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)

	if err := runDeployPlan(context.Background(), cmd, flags, deployPlanOpts{}); err != nil {
		t.Fatalf("runDeployPlan: %v", err)
	}

	if !strings.Contains(outBuf.String(), "docker up --wait") {
		t.Errorf("plan output missing 'docker up --wait'; got:\n%s", outBuf.String())
	}

	const wantNotice = "Using built-in default deploy pipeline (override with devbox/deploy.yml)."
	if !strings.Contains(errBuf.String(), wantNotice) {
		t.Errorf("stderr missing info line %q; got:\n%s", wantNotice, errBuf.String())
	}
}

// TestRunDeployPlan_JSONModeNoInfoLine verifies that --output json suppresses
// the default-pipeline info line on stderr.
func TestRunDeployPlan_JSONModeNoInfoLine(t *testing.T) {
	dir := makeMinimalProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "devbox.yml"), Output: "json"}

	cmd := &cobra.Command{}
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)

	// The plan command still writes text to stdout even in json mode (plan
	// does not have a JSON output path for the plan table), so we don't assert
	// on stdout here — only that stderr is empty.
	_ = runDeployPlan(context.Background(), cmd, flags, deployPlanOpts{})

	if errBuf.Len() != 0 {
		t.Errorf("json mode: expected empty stderr, got %q", errBuf.String())
	}
}

// TestRunDeployPlan_ServiceScopeNoInfoLine verifies that --service plans do
// NOT emit the orchestrator-default notice, since the orchestrator pipeline
// is not what drives a per-service plan (ResolveServicePlan reads the
// per-service file). The notice would be misleading even though the file is
// absent at the project level.
func TestRunDeployPlan_ServiceScopeNoInfoLine(t *testing.T) {
	dir := makeMinimalProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "devbox.yml")}

	cmd := &cobra.Command{}
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)

	// nonexistent service still triggers the "service not found" error path;
	// the notice gate must run before that error and stay silent.
	_ = runDeployPlan(context.Background(), cmd, flags, deployPlanOpts{ServiceName: "nonexistent"})

	if errBuf.Len() != 0 {
		t.Errorf("expected no info line for --service scope; stderr = %q", errBuf.String())
	}
}

// TestRunDeployPlan_UserDeployYMLNoInfoLine verifies that a project with a
// devbox/deploy.yml does not emit the default-pipeline info line.
func TestRunDeployPlan_UserDeployYMLNoInfoLine(t *testing.T) {
	dir := makeMinimalProject(t)

	// Write a minimal user deploy.yml.
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userDeploy := "phases:\n  - name: custom\n    steps:\n      - name: step1\n        type: shell\n        cmd: echo hello\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "deploy.yml"), []byte(userDeploy), 0o644); err != nil {
		t.Fatal(err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "devbox.yml")}

	cmd := &cobra.Command{}
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)

	if err := runDeployPlan(context.Background(), cmd, flags, deployPlanOpts{}); err != nil {
		t.Fatalf("runDeployPlan: %v", err)
	}

	if errBuf.Len() != 0 {
		t.Errorf("expected no info line when user deploy.yml is present; stderr = %q", errBuf.String())
	}

	// User's step must appear, not the default.
	if !strings.Contains(outBuf.String(), "echo hello") {
		t.Errorf("plan output missing user step 'echo hello'; got:\n%s", outBuf.String())
	}
	if strings.Contains(outBuf.String(), "docker up --wait") {
		t.Errorf("default step 'docker up --wait' should not appear when user deploy.yml is present")
	}
}
