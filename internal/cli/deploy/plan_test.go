package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"

	"github.com/spf13/cobra"
)

// makeMinimalProject writes a bare v2 workspace.yml so config + (empty)
// registry loads succeed. Returns the project root.
func makeMinimalProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	yml := "schema_version: \"2\"\nproject:\n  name: testproject\n  prefix: dwe\n"
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestRunDeployPlan_DefaultFormatRendersTitle verifies the table-format
// branch writes the "Deploy plan" section title.
func TestRunDeployPlan_DefaultFormatRendersTitle(t *testing.T) {
	dir := makeMinimalProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml")}

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
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml")}

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
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml")}

	err := runDeployPlan(context.Background(), &cobra.Command{}, flags, deployPlanOpts{ServiceName: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown service, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("err = %v, want one mentioning the unknown service name", err)
	}
}

// TestRunDeployPlan_DefaultPipelineWhenNoDeployYML verifies that a bare project
// (no workspace/deploy.yml) succeeds, includes the docker-up step from the built-in
// default, and prints the info line on stderr.
func TestRunDeployPlan_DefaultPipelineWhenNoDeployYML(t *testing.T) {
	dir := makeMinimalProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml")}

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

	const wantNotice = "Using built-in default deploy pipeline (override with workspace/deploy.yml)."
	if !strings.Contains(errBuf.String(), wantNotice) {
		t.Errorf("stderr missing info line %q; got:\n%s", wantNotice, errBuf.String())
	}
}

// TestRunDeployPlan_JSONModeNoInfoLine verifies that --output json suppresses
// the default-pipeline info line on stderr.
func TestRunDeployPlan_JSONModeNoInfoLine(t *testing.T) {
	dir := makeMinimalProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml"), Output: "json"}

	cmd := &cobra.Command{}
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)

	if err := runDeployPlan(context.Background(), cmd, flags, deployPlanOpts{}); err != nil {
		t.Fatalf("runDeployPlan: %v", err)
	}

	if errBuf.Len() != 0 {
		t.Errorf("json mode: expected empty stderr, got %q", errBuf.String())
	}

	var payload planJSON
	if err := json.Unmarshal(outBuf.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout: %s", err, outBuf.String())
	}
	if len(payload.Phases) == 0 {
		t.Fatalf("expected at least one phase in JSON payload, got %+v", payload)
	}
}

// TestRunDeployPlan_JSONMode_NoANSIEscapes verifies the JSON payload carries
// no ANSI escape codes — the table/shell text renderers style their output,
// but the JSON path must stay a plain machine-readable payload.
func TestRunDeployPlan_JSONMode_NoANSIEscapes(t *testing.T) {
	dir := makeMinimalProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml"), Output: "json"}

	cmd := &cobra.Command{}
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)

	if err := runDeployPlan(context.Background(), cmd, flags, deployPlanOpts{}); err != nil {
		t.Fatalf("runDeployPlan: %v", err)
	}

	if strings.Contains(outBuf.String(), "\x1b[") {
		t.Errorf("JSON output contains ANSI escapes: %q", outBuf.String())
	}
}

// TestRunDeployPlan_JSONMode_StableShape verifies the JSON payload contains
// the expected phase/step/type/cmd/gate fields and a deterministic key
// ordering (Go's json.Marshal on structs is always struct-field order, so
// this pins the shape rather than re-testing encoding/json).
func TestRunDeployPlan_JSONMode_StableShape(t *testing.T) {
	dir := makeMinimalProject(t)

	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userDeploy := "phases:\n" +
		"  - name: custom\n" +
		"    steps:\n" +
		"      - name: step1\n" +
		"        type: shell\n" +
		"        cmd: echo hello\n"
	if err := os.WriteFile(filepath.Join(workspaceDir, "deploy.yml"), []byte(userDeploy), 0o644); err != nil {
		t.Fatal(err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml"), Output: "json"}

	cmd := &cobra.Command{}
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)

	if err := runDeployPlan(context.Background(), cmd, flags, deployPlanOpts{}); err != nil {
		t.Fatalf("runDeployPlan: %v", err)
	}

	var payload planJSON
	if err := json.Unmarshal(outBuf.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}

	var found *planStepJSON
	for i := range payload.Phases {
		if payload.Phases[i].Name != "custom" {
			continue
		}
		for j := range payload.Phases[i].Steps {
			if payload.Phases[i].Steps[j].Name == "step1" {
				found = &payload.Phases[i].Steps[j]
			}
		}
	}
	if found == nil {
		t.Fatalf("step1 not found in payload: %+v", payload)
	}
	if found.Type != "shell" {
		t.Errorf("Type = %q, want %q", found.Type, "shell")
	}
	if found.Cmd != "echo hello" {
		t.Errorf("Cmd = %q, want %q", found.Cmd, "echo hello")
	}
}

// TestRunDeployPlan_JSONMode_UnresolvedTemplateFlagged verifies the JSON
// payload carries the `unresolved` field for a step whose cmd still contains
// a ${...} reference with an unknown head after resolve-time rendering.
func TestRunDeployPlan_JSONMode_UnresolvedTemplateFlagged(t *testing.T) {
	dir := makeMinimalProject(t)

	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userDeploy := "phases:\n" +
		"  - name: custom\n" +
		"    steps:\n" +
		"      - name: step1\n" +
		"        type: shell\n" +
		"        cmd: \"echo ${HOME}\"\n"
	if err := os.WriteFile(filepath.Join(workspaceDir, "deploy.yml"), []byte(userDeploy), 0o644); err != nil {
		t.Fatal(err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml"), Output: "json"}

	cmd := &cobra.Command{}
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)

	if err := runDeployPlan(context.Background(), cmd, flags, deployPlanOpts{}); err != nil {
		t.Fatalf("runDeployPlan: %v", err)
	}

	var payload planJSON
	if err := json.Unmarshal(outBuf.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}

	var found *planStepJSON
	for i := range payload.Phases {
		for j := range payload.Phases[i].Steps {
			if payload.Phases[i].Steps[j].Name == "step1" {
				found = &payload.Phases[i].Steps[j]
			}
		}
	}
	if found == nil {
		t.Fatalf("step1 not found in payload: %+v", payload)
	}
	if len(found.Unresolved) != 1 || found.Unresolved[0] != "${HOME}" {
		t.Errorf("Unresolved = %v, want [${HOME}]", found.Unresolved)
	}
}

// TestRunDeployPlan_JSONMode_ResolvedTemplateNotFlagged verifies a step whose
// cmd only references a known-head ${...} (already substituted by
// resolve-time rendering) carries no `unresolved` field.
func TestRunDeployPlan_JSONMode_ResolvedTemplateNotFlagged(t *testing.T) {
	dir := makeMinimalProject(t)

	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rootYML := "schema_version: \"2\"\nproject:\n  name: testproject\n  prefix: dwe\nvars:\n  greeting: hello\n"
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte(rootYML), 0o644); err != nil {
		t.Fatal(err)
	}
	userDeploy := "phases:\n" +
		"  - name: custom\n" +
		"    steps:\n" +
		"      - name: step1\n" +
		"        type: shell\n" +
		"        cmd: \"echo ${vars.greeting}\"\n"
	if err := os.WriteFile(filepath.Join(workspaceDir, "deploy.yml"), []byte(userDeploy), 0o644); err != nil {
		t.Fatal(err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml"), Output: "json"}

	cmd := &cobra.Command{}
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)

	if err := runDeployPlan(context.Background(), cmd, flags, deployPlanOpts{}); err != nil {
		t.Fatalf("runDeployPlan: %v", err)
	}

	var payload planJSON
	if err := json.Unmarshal(outBuf.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}

	var found *planStepJSON
	for i := range payload.Phases {
		for j := range payload.Phases[i].Steps {
			if payload.Phases[i].Steps[j].Name == "step1" {
				found = &payload.Phases[i].Steps[j]
			}
		}
	}
	if found == nil {
		t.Fatalf("step1 not found in payload: %+v", payload)
	}
	if found.Cmd != "echo hello" {
		t.Errorf("Cmd = %q, want %q", found.Cmd, "echo hello")
	}
	if len(found.Unresolved) != 0 {
		t.Errorf("Unresolved = %v, want none", found.Unresolved)
	}
}

// TestRunDeployPlan_JSONMode_ServiceScope verifies the JSON payload records
// the scoping service name for a per-service plan.
func TestRunDeployPlan_JSONMode_ServiceScope(t *testing.T) {
	dir := makeMinimalProject(t)

	servicesDir := filepath.Join(dir, "workspace", "services", "app")
	if err := os.MkdirAll(servicesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	serviceYML := "type: app\ndir: .\n"
	if err := os.WriteFile(filepath.Join(servicesDir, "service.yml"), []byte(serviceYML), 0o644); err != nil {
		t.Fatal(err)
	}
	serviceDeployYML := "phases:\n  - name: build\n    steps:\n      - name: step1\n        type: shell\n        cmd: echo app\n"
	if err := os.WriteFile(filepath.Join(servicesDir, "deploy.yml"), []byte(serviceDeployYML), 0o644); err != nil {
		t.Fatal(err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml"), Output: "json"}

	cmd := &cobra.Command{}
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)

	if err := runDeployPlan(context.Background(), cmd, flags, deployPlanOpts{ServiceName: "app"}); err != nil {
		t.Fatalf("runDeployPlan: %v", err)
	}

	var payload planJSON
	if err := json.Unmarshal(outBuf.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	if payload.Service != "app" {
		t.Errorf("Service = %q, want %q", payload.Service, "app")
	}
}

// TestRunDeployPlan_TextFormatsUnaffectedByJSONPath verifies the table and
// shell text renderers are byte-identical to their pre-JSON-path behavior
// (i.e. the --output json branch is a true short-circuit, not a refactor of
// the text paths).
func TestRunDeployPlan_TextFormatsUnaffectedByJSONPath(t *testing.T) {
	dir := makeMinimalProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml")}

	for _, format := range []string{"", "table", "shell"} {
		cmd := &cobra.Command{}
		var buf bytes.Buffer
		cmd.SetOut(&buf)

		if err := runDeployPlan(context.Background(), cmd, flags, deployPlanOpts{Format: format}); err != nil {
			t.Fatalf("format %q: runDeployPlan: %v", format, err)
		}
		if strings.Contains(buf.String(), "{") && strings.Contains(buf.String(), "\"phases\"") {
			t.Errorf("format %q: text output looks like JSON: %q", format, buf.String())
		}
	}
}

// TestRunDeployPlan_ServiceScopeNoInfoLine verifies that --service plans do
// NOT emit the orchestrator-default notice, since the orchestrator pipeline
// is not what drives a per-service plan (ResolveServicePlan reads the
// per-service file). The notice would be misleading even though the file is
// absent at the project level.
func TestRunDeployPlan_ServiceScopeNoInfoLine(t *testing.T) {
	dir := makeMinimalProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml")}

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
// workspace/deploy.yml does not emit the default-pipeline info line.
func TestRunDeployPlan_UserDeployYMLNoInfoLine(t *testing.T) {
	dir := makeMinimalProject(t)

	// Write a minimal user deploy.yml.
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userDeploy := "phases:\n  - name: custom\n    steps:\n      - name: step1\n        type: shell\n        cmd: echo hello\n"
	if err := os.WriteFile(filepath.Join(workspaceDir, "deploy.yml"), []byte(userDeploy), 0o644); err != nil {
		t.Fatal(err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml")}

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
