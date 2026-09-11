package envtest

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// writeRunStepsCopy builds a disposable copy that looks like one prepared by
// RunScenario: the original's docker.yml names the live stack, and
// WriteDockerIdentity stamps the copy's own name into docker.local.yml. The
// copy carries a `type: script` command that writes its COMPOSE_PROJECT_NAME
// to script.out, so a `type: command` step can target it.
func writeRunStepsCopy(t *testing.T, composeProject string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"workspace.yml": `schema_version: "1"
project:
  name: runnertest
  prefix: dwe
`,
		"workspace/docker.yml": "project_name: live-stack\n",
		"workspace/commands/probe.yml": `commands:
  script:
    type: script
    description: Write the compose project name
    script:
      path: workspace/scripts/probe.sh
`,
		"workspace/scripts/probe.sh": `printf '%s' "$COMPOSE_PROJECT_NAME" > "$DWE_ROOT/script.out"` + "\n",
	}
	for rel, content := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteDockerIdentity(root, composeProject); err != nil {
		t.Fatalf("WriteDockerIdentity: %v", err)
	}
	return root
}

// loadEnvScenario writes a scenario whose shell step writes the step's
// COMPOSE_PROJECT_NAME to shell.out and whose command step runs the script.
func loadEnvScenario(t *testing.T) *Scenario {
	t.Helper()
	path := filepath.Join(t.TempDir(), "env.yml")
	if err := os.WriteFile(path, []byte(`steps:
  - name: shell-name
    type: shell
    cmd: printf '%s' "$COMPOSE_PROJECT_NAME" > shell.out
  - name: script-name
    type: command
    cmd: probe.script
`), 0o644); err != nil {
		t.Fatal(err)
	}
	scn, err := LoadScenario(path)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}
	return scn
}

func readOut(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(data)
}

// TestRunSteps_ShellAndScriptSeeCopyProjectName pins that scenario steps get
// the copy's compose project name from the copy's config, never from the
// process env: the runner scrubs COMPOSE_* and sources no .env, so a live
// COMPOSE_PROJECT_NAME left in the environment must not reach the steps.
func TestRunSteps_ShellAndScriptSeeCopyProjectName(t *testing.T) {
	t.Setenv("COMPOSE_PROJECT_NAME", "live-stack")
	const copyName = "dwe-runnertest-t-env-abc123"
	root := writeRunStepsCopy(t, copyName)

	r := &Runner{}
	failed, err := r.runSteps(context.Background(), root, loadEnvScenario(t), RunRequest{}, noopReporter{}, io.Discard)
	if err != nil {
		t.Fatalf("runSteps: %v (failed step %q)", err, failed)
	}
	if got := readOut(t, root, "shell.out"); got != copyName {
		t.Errorf("shell step COMPOSE_PROJECT_NAME = %q, want %q", got, copyName)
	}
	if got := readOut(t, root, "script.out"); got != copyName {
		t.Errorf("script command COMPOSE_PROJECT_NAME = %q, want %q", got, copyName)
	}
}

// TestRunSteps_ConcurrentCopiesSeeOwnProjectName mirrors --parallel: two
// scenarios run as goroutines in one process, so a process-global variable
// could carry only one name. Each copy's steps must see their own.
func TestRunSteps_ConcurrentCopiesSeeOwnProjectName(t *testing.T) {
	t.Setenv("COMPOSE_PROJECT_NAME", "live-stack")
	names := []string{"dwe-runnertest-t-a-111111", "dwe-runnertest-t-b-222222"}
	roots := make([]string, len(names))
	scenarios := make([]*Scenario, len(names))
	for i, name := range names {
		roots[i] = writeRunStepsCopy(t, name)
		scenarios[i] = loadEnvScenario(t)
	}

	errs := make([]error, len(names))
	var wg sync.WaitGroup
	for i := range names {
		wg.Go(func() {
			var log strings.Builder
			r := &Runner{}
			_, errs[i] = r.runSteps(context.Background(), roots[i], scenarios[i], RunRequest{}, noopReporter{}, &log)
		})
	}
	wg.Wait()

	for i, name := range names {
		if errs[i] != nil {
			t.Fatalf("runSteps(%s): %v", name, errs[i])
		}
		for _, out := range []string{"shell.out", "script.out"} {
			if got := readOut(t, roots[i], out); got != name {
				t.Errorf("copy %s: %s = %q, want %q", name, out, got, name)
			}
		}
	}
}
