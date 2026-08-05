package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy"

	"github.com/spf13/cobra"
)

// swapImplicitEnvStep replaces the shared deploy.ImplicitEnvStep var (always
// step 0 of any resolved plan) with a plain shell step for the duration of a
// test. The real step is type "dwe" and resolveDweBin always prefers
// os.Executable() over the configured binary name — inside `go test` that
// resolves to the test binary itself, so executing the real step here would
// recursively re-invoke the whole test suite as a subprocess (the documented
// test-recursion hazard). "touch .env" reproduces the one observable effect
// (an .env file for the post-step SourceDotEnv hook to read) without a
// subprocess.
func swapImplicitEnvStep(t *testing.T) {
	t.Helper()
	orig := deploy.ImplicitEnvStep
	deploy.ImplicitEnvStep = config.DeployStep{Name: orig.Name, Type: "shell", Cmd: "touch .env"}
	t.Cleanup(func() { deploy.ImplicitEnvStep = orig })
}

func runHelperOK(t *testing.T, flags *cmdctx.RootFlags, opts Opts) {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	opts.PreflightFn = noopPreflight
	opts.NonInteractive = true
	opts.Silent = true
	if err := RunHelper(context.Background(), cmd, flags, opts); err != nil {
		t.Fatalf("RunHelper: %v", err)
	}
}

func readMarker(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading marker %s: %v", path, err)
	}
	return string(data)
}

// TestRunHelper_VarsChange_WholeProjectReRuns is the whole-project half of
// the end-to-end guard for hashing vars into ProjectConfigHash (plan task
// 2d): a step whose rendered cmd depends on ${vars.*} must actually
// re-execute — not just report a changed StepHash in isolation — when the
// referenced var changes and no other config changed. Before vars were
// folded into ProjectConfigHash, RunHelper's up-to-date gate short-circuited
// the whole pipeline before any per-step hash was even consulted, so the
// step re-rendered internally but its new output was never observed.
func TestRunHelper_VarsChange_WholeProjectReRuns(t *testing.T) {
	swapImplicitEnvStep(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "workspace.yml")
	marker := filepath.Join(dir, "marker.txt")

	writeWorkspace := func(branch string) {
		yml := fmt.Sprintf("schema_version: \"2\"\nproject:\n  name: test\n  prefix: dwe\nvars:\n  source:\n    branch: %s\n", branch)
		if err := os.WriteFile(cfgPath, []byte(yml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeWorkspace("main")

	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	deployYML := fmt.Sprintf("phases:\n  - name: custom\n    steps:\n      - name: write-marker\n        type: shell\n        cmd: \"printf %%s ${vars.source.branch} > %s\"\n", marker)
	if err := os.WriteFile(filepath.Join(workspaceDir, "deploy.yml"), []byte(deployYML), 0o644); err != nil {
		t.Fatal(err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: cfgPath}

	runHelperOK(t, flags, Opts{})
	if got := readMarker(t, marker); got != "main" {
		t.Fatalf("marker after first run = %q, want %q", got, "main")
	}

	// Change only the var referenced by the step; nothing else in the
	// project config changes.
	writeWorkspace("dev")
	runHelperOK(t, flags, Opts{})
	if got := readMarker(t, marker); got != "dev" {
		t.Fatalf("marker after vars change = %q, want %q — a changed var must invalidate "+
			"the project hash and re-run the step instead of reporting already up-to-date", got, "dev")
	}
}

// TestRunHelper_VarsChange_ServiceScopedReRuns is the --service half: a
// scoped deploy never consults ProjectConfigHash at all (computeScopeState /
// makeSkipDecider compare against ServiceConfigHash for service-scoped
// steps), so vars must be hashed into ServiceConfigHash independently of the
// project-hash fix above.
func TestRunHelper_VarsChange_ServiceScopedReRuns(t *testing.T) {
	swapImplicitEnvStep(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "workspace.yml")
	marker := filepath.Join(dir, "marker.txt")

	writeWorkspace := func(branch string) {
		yml := fmt.Sprintf("schema_version: \"2\"\nproject:\n  name: test\n  prefix: dwe\nvars:\n  source:\n    branch: %s\n", branch)
		if err := os.WriteFile(cfgPath, []byte(yml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeWorkspace("main")

	svcDir := filepath.Join(dir, "workspace", "services", "app")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svcDir, "service.yml"),
		[]byte("type: app\ncontainer: app\nrequired: true\ndir: ./services/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deployYML := fmt.Sprintf("phases:\n  - name: custom\n    steps:\n      - name: write-marker\n        type: shell\n        cmd: \"printf %%s ${vars.source.branch} > %s\"\n", marker)
	if err := os.WriteFile(filepath.Join(svcDir, "deploy.yml"), []byte(deployYML), 0o644); err != nil {
		t.Fatal(err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: cfgPath}

	runHelperOK(t, flags, Opts{Services: []string{"app"}})
	if got := readMarker(t, marker); got != "main" {
		t.Fatalf("marker after first run = %q, want %q", got, "main")
	}

	writeWorkspace("dev")
	runHelperOK(t, flags, Opts{Services: []string{"app"}})
	if got := readMarker(t, marker); got != "dev" {
		t.Fatalf("marker after vars change = %q, want %q — a --service deploy must also re-run "+
			"when a var referenced by that service's deploy.yml changes", got, "dev")
	}
}

// TestRunHelper_VarsChange_UnrelatedVarsAlsoInvalidates pins the accepted
// cost of hashing the whole vars: block rather than only referenced paths
// (owner decision in the plan): changing an unrelated vars entry also causes
// the next deploy to re-run, even though no step references it.
func TestRunHelper_VarsChange_UnrelatedVarsAlsoInvalidates(t *testing.T) {
	swapImplicitEnvStep(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "workspace.yml")
	marker := filepath.Join(dir, "marker.txt")

	writeWorkspace := func(unrelated string) {
		yml := fmt.Sprintf("schema_version: \"2\"\nproject:\n  name: test\n  prefix: dwe\nvars:\n  unrelated: %s\n", unrelated)
		if err := os.WriteFile(cfgPath, []byte(yml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeWorkspace("a")

	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// This step does not reference vars at all — it appends a marker line
	// each time it actually executes, so re-execution is observable.
	deployYML := fmt.Sprintf("phases:\n  - name: custom\n    steps:\n      - name: bump\n        type: shell\n        cmd: \"echo run >> %s\"\n", marker)
	if err := os.WriteFile(filepath.Join(workspaceDir, "deploy.yml"), []byte(deployYML), 0o644); err != nil {
		t.Fatal(err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: cfgPath}

	runHelperOK(t, flags, Opts{})
	first := readMarker(t, marker)
	if first != "run\n" {
		t.Fatalf("marker after first run = %q, want one %q line", first, "run\n")
	}

	// Second run with no config change at all: the project must be reported
	// up-to-date and the step must NOT re-run.
	runHelperOK(t, flags, Opts{})
	if got := readMarker(t, marker); got != first {
		t.Fatalf("marker changed with no config change at all = %q, want unchanged %q", got, first)
	}

	// Change an unrelated vars entry: the step must re-run anyway.
	writeWorkspace("b")
	runHelperOK(t, flags, Opts{})
	if got := readMarker(t, marker); got != "run\nrun\n" {
		t.Fatalf("marker after unrelated vars change = %q, want %q — hashing the whole vars "+
			"block means any change invalidates, even one no step reads", got, "run\nrun\n")
	}
}
