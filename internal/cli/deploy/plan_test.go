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

// TestRunDeployPlan_PhaseWhenIsRendered pins that the phase-level runtime
// `when:` shown by the plan is the RENDERED condition, not the raw
// `${vars.*}` text from deploy.yml. The phase header used to read
// rs.Phase.When directly while execution evaluated the substituted form —
// the one position where the plan still lied about what would run. Checked in
// both output modes, since the human table and the JSON payload build the
// phase header independently.
func TestRunDeployPlan_PhaseWhenIsRendered(t *testing.T) {
	dir := t.TempDir()
	yml := "schema_version: \"2\"\nproject:\n  name: testproject\n  prefix: dwe\nvars:\n  marker: sentinel\n"
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userDeploy := "phases:\n" +
		"  - name: custom\n" +
		"    when:\n" +
		"      type: shell\n" +
		"      cmd: \"test -d ${vars.marker}\"\n" +
		"    steps:\n" +
		"      - name: step1\n" +
		"        type: shell\n" +
		"        cmd: echo hi\n"
	if err := os.WriteFile(filepath.Join(workspaceDir, "deploy.yml"), []byte(userDeploy), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("table", func(t *testing.T) {
		flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml")}
		cmd := &cobra.Command{}
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		if err := runDeployPlan(context.Background(), cmd, flags, deployPlanOpts{}); err != nil {
			t.Fatalf("runDeployPlan: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "test -d sentinel") {
			t.Errorf("phase when not rendered; output:\n%s", out)
		}
		if strings.Contains(out, "${vars.marker}") {
			t.Errorf("phase when still shows the raw reference; output:\n%s", out)
		}
	})

	t.Run("json", func(t *testing.T) {
		flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml"), Output: "json"}
		cmd := &cobra.Command{}
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		if err := runDeployPlan(context.Background(), cmd, flags, deployPlanOpts{}); err != nil {
			t.Fatalf("runDeployPlan: %v", err)
		}
		var payload planJSON
		if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
			t.Fatalf("stdout is not valid JSON: %v", err)
		}
		var when string
		for _, p := range payload.Phases {
			if p.Name == "custom" {
				when = p.When
			}
		}
		if !strings.Contains(when, "test -d sentinel") {
			t.Errorf("phase.when = %q, want the rendered command", when)
		}
	})
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

// TestRunDeployPlan_TextFormatsUnaffectedByJSONPath verifies the --output json
// branch is a short-circuit rather than a refactor of the text paths: each text
// format still renders its own shape, and the JSON payload never leaks into it.
//
// Note this is not a byte-for-byte golden — it pins the distinguishing markers
// of each format plus the absence of the JSON envelope. A regression that
// changed spacing inside the table would pass; one that swapped a renderer,
// emptied it, or let the JSON path run would not.
func TestRunDeployPlan_TextFormatsUnaffectedByJSONPath(t *testing.T) {
	dir := makeMinimalProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml")}

	tests := []struct {
		format string
		want   string
	}{
		{format: "", want: "Deploy plan"},
		{format: "table", want: "Deploy plan"},
		{format: "shell", want: "set -e"},
	}
	for _, tt := range tests {
		cmd := &cobra.Command{}
		var buf bytes.Buffer
		cmd.SetOut(&buf)

		if err := runDeployPlan(context.Background(), cmd, flags, deployPlanOpts{Format: tt.format}); err != nil {
			t.Fatalf("format %q: runDeployPlan: %v", tt.format, err)
		}
		out := buf.String()
		if !strings.Contains(out, tt.want) {
			t.Errorf("format %q: output missing %q: %q", tt.format, tt.want, out)
		}
		if json.Valid(bytes.TrimSpace(buf.Bytes())) && strings.Contains(out, "\"phases\"") {
			t.Errorf("format %q: text output is the JSON payload: %q", tt.format, out)
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

// TestRunDeployPlan_JSONMode_ParallelGroup covers the buildPlanStepJSON
// recursion: a `parallel:` group emits its own object with the group knobs and
// an ordered nested `steps[]`, and carries no `cmd` of its own (the group is a
// container, not a command). Nothing else in this file builds a plan with a
// parallel group, so the whole recursion could be dropped and stay green.
func TestRunDeployPlan_JSONMode_ParallelGroup(t *testing.T) {
	dir := makeMinimalProject(t)

	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userDeploy := "phases:\n" +
		"  - name: custom\n" +
		"    steps:\n" +
		"      - name: fanout\n" +
		"        parallel:\n" +
		"          max_concurrent: 3\n" +
		"          fail_fast: true\n" +
		"          steps:\n" +
		"            - name: sub-a\n" +
		"              type: shell\n" +
		"              cmd: echo a\n" +
		"            - name: sub-b\n" +
		"              type: shell\n" +
		"              cmd: echo b\n"
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

	var group *planStepJSON
	for i := range payload.Phases {
		for j := range payload.Phases[i].Steps {
			if payload.Phases[i].Steps[j].Name == "fanout" {
				group = &payload.Phases[i].Steps[j]
			}
		}
	}
	if group == nil {
		t.Fatalf("parallel step not found in payload: %+v", payload)
	}
	if group.Parallel == nil {
		t.Fatal("parallel group emitted no `parallel` object")
	}
	if group.Cmd != "" {
		t.Errorf("Cmd = %q, want empty for a parallel group", group.Cmd)
	}
	// The payload carries the EFFECTIVE value the executor will use, not the
	// authored one: resolve clamps max_concurrent to the number of sub-steps.
	if group.Parallel.MaxConcurrent != 2 {
		t.Errorf("MaxConcurrent = %d, want 2 (authored 3, clamped to 2 sub-steps)", group.Parallel.MaxConcurrent)
	}
	if !group.Parallel.FailFast {
		t.Error("FailFast = false, want true")
	}
	if len(group.Parallel.Steps) != 2 {
		t.Fatalf("nested steps = %d, want 2: %+v", len(group.Parallel.Steps), group.Parallel.Steps)
	}
	for i, want := range []struct{ name, cmd string }{{"sub-a", "echo a"}, {"sub-b", "echo b"}} {
		got := group.Parallel.Steps[i]
		if got.Name != want.name || got.Cmd != want.cmd {
			t.Errorf("nested step %d = {%q, %q}, want {%q, %q}", i, got.Name, got.Cmd, want.name, want.cmd)
		}
	}
}
