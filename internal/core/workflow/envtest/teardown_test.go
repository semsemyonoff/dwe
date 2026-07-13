package envtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// recordingTeardownDeps returns a TeardownDeps that appends the step name to
// *order on every call, and returns errs[name] (nil if absent).
func recordingTeardownDeps(order *[]string, errs map[string]error) TeardownDeps {
	record := func(name string) {
		*order = append(*order, name)
	}
	return TeardownDeps{
		ComposeDown: func(ctx context.Context, m *Manifest) (bool, error) {
			record("compose_down")
			return false, errs["compose_down"]
		},
		ReapContainers: func(ctx context.Context, m *Manifest) error {
			record("reap_containers")
			return errs["reap_containers"]
		},
		RemoveVolumes: func(ctx context.Context, m *Manifest) error {
			record("remove_volumes")
			return errs["remove_volumes"]
		},
		StopBridge: func(m *Manifest) error {
			record("stop_bridge")
			return errs["stop_bridge"]
		},
		RemoveCopy: func(m *Manifest) error {
			record("remove_copy")
			return errs["remove_copy"]
		},
		DeleteManifest: func(m *Manifest) error {
			record("delete_manifest")
			return errs["delete_manifest"]
		},
	}
}

func testManifest() *Manifest {
	return &Manifest{
		Scenario:       "smoke",
		RunID:          "abc123",
		ComposeProject: "myapp-t-smoke-abc123",
		CopyPath:       "/tmp/does-not-matter",
		BridgeDir:      "/tmp/does-not-matter/.dwe/bridge",
	}
}

func TestTeardown_FullOrder(t *testing.T) {
	var order []string
	deps := recordingTeardownDeps(&order, nil)

	if err := Teardown(context.Background(), testManifest(), deps, nil); err != nil {
		t.Fatalf("Teardown: %v", err)
	}

	want := []string{"compose_down", "reap_containers", "remove_volumes", "stop_bridge", "remove_copy", "delete_manifest"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i, name := range want {
		if order[i] != name {
			t.Errorf("order[%d] = %q, want %q (full order %v)", i, order[i], name, order)
		}
	}
}

func TestTeardown_FailureInStepStillRunsLaterStepsAndJoinsError(t *testing.T) {
	var order []string
	wantErr := errors.New("reap boom")
	deps := recordingTeardownDeps(&order, map[string]error{"reap_containers": wantErr})

	var warnings []string
	err := Teardown(context.Background(), testManifest(), deps, func(msg string) {
		warnings = append(warnings, msg)
	})

	if err == nil {
		t.Fatal("Teardown() = nil error, want non-nil (reap_containers failed)")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("Teardown() error = %v, want it to wrap %v", err, wantErr)
	}

	want := []string{"compose_down", "reap_containers", "remove_volumes", "stop_bridge", "remove_copy", "delete_manifest"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want every step to still run: %v", order, want)
	}

	found := false
	for _, w := range warnings {
		if strings.Contains(w, "reap boom") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want one mentioning the failure", warnings)
	}
}

func TestTeardown_ComposeDownSkippedDegradation(t *testing.T) {
	var order []string
	deps := recordingTeardownDeps(&order, nil)
	deps.ComposeDown = func(ctx context.Context, m *Manifest) (bool, error) {
		order = append(order, "compose_down")
		return true, fmt.Errorf("copy config unloadable")
	}

	var warnings []string
	err := Teardown(context.Background(), testManifest(), deps, func(msg string) {
		warnings = append(warnings, msg)
	})
	if err != nil {
		t.Fatalf("Teardown() = %v, want nil (a skip is not a failure)", err)
	}

	want := []string{"compose_down", "reap_containers", "remove_volumes", "stop_bridge", "remove_copy", "delete_manifest"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want every step to still run despite the compose-down skip: %v", order, want)
	}

	found := false
	for _, w := range warnings {
		if strings.Contains(w, "skipping compose down") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want one about skipping compose down", warnings)
	}
}

func TestTeardown_NilManifest(t *testing.T) {
	if err := Teardown(context.Background(), nil, TeardownDeps{}, nil); err == nil {
		t.Fatal("Teardown(nil manifest) = nil error, want error")
	}
}

func TestTeardown_NilDepsFieldsAreSkipped(t *testing.T) {
	// A zero-value TeardownDeps must not panic — every step is optional.
	if err := Teardown(context.Background(), testManifest(), TeardownDeps{}, nil); err != nil {
		t.Fatalf("Teardown() with zero-value deps = %v, want nil", err)
	}
}

// --- default (real) implementation tests: exec seams recorded, no real docker ---

func TestComposeDownReal_SkippedWhenConfigUnloadable(t *testing.T) {
	origFn := runComposeDownFn
	t.Cleanup(func() { runComposeDownFn = origFn })
	runComposeDownFn = func(ctx context.Context, bin string, args, env []string, dir string, log io.Writer) error {
		t.Fatal("runComposeDownFn must not be called when the copy's config cannot be loaded")
		return nil
	}

	// Unloadable copy (no workspace.yml at all) -> skipped, no subprocess spawned.
	m := &Manifest{ComposeProject: "myapp-t-smoke-abc123", CopyPath: t.TempDir()}
	skipped, err := composeDownReal(context.Background(), m, nil)
	if !skipped {
		t.Fatalf("composeDownReal() skipped = false, want true for a copy with no workspace.yml (err=%v)", err)
	}
	if err == nil {
		t.Error("composeDownReal() err = nil, want a reason for the skip")
	}
}

func TestComposeDownReal_BuildsArgsWithoutVolumesFlag(t *testing.T) {
	origFn := runComposeDownFn
	t.Cleanup(func() { runComposeDownFn = origFn })

	type call struct {
		bin  string
		args []string
		dir  string
	}
	var got call
	runComposeDownFn = func(ctx context.Context, bin string, args, env []string, dir string, log io.Writer) error {
		got = call{bin: bin, args: args, dir: dir}
		return nil
	}

	copyRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(copyRoot, "workspace.yml"), []byte("project:\n  name: test\n  prefix: dwe\n"), 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}
	// A malicious/misconfigured copy sets args.down: ["-v"]. Teardown must
	// bypass this policy entirely (BuildInternalArgs), so -v can never reach the
	// compose invocation and delete shared named volumes.
	workspaceDir := filepath.Join(copyRoot, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "docker.yml"), []byte("project_name: dwe-t-smoke-abc123\nargs:\n  down: [\"-v\"]\n"), 0o644); err != nil {
		t.Fatalf("write docker.yml: %v", err)
	}

	m := &Manifest{ComposeProject: "dwe-t-smoke-abc123", CopyPath: copyRoot}
	skipped, err := composeDownReal(context.Background(), m, nil)
	if skipped || err != nil {
		t.Fatalf("composeDownReal() = skipped=%v err=%v, want a successful (non-skipped) call", skipped, err)
	}

	if got.dir != copyRoot {
		t.Errorf("dir = %q, want %q", got.dir, copyRoot)
	}
	for _, a := range got.args {
		if a == "-v" || a == "--volumes" {
			t.Fatalf("compose down args = %v, must never include -v/--volumes", got.args)
		}
	}
	if got.args[0] != "compose" {
		t.Errorf("args[0] = %q, want %q", got.args[0], "compose")
	}
	joined := strings.Join(got.args, " ")
	if !strings.Contains(joined, "down") {
		t.Errorf("args = %v, want the down subcommand present", got.args)
	}
	if !strings.Contains(joined, "--remove-orphans") {
		t.Errorf("args = %v, want --remove-orphans always present (even with args.down: [-v])", got.args)
	}
}

func TestReapContainersReal_ListsWithAllFlagAndExactLabelFilter(t *testing.T) {
	origList := listContainersFn
	origRemove := removeContainerFn
	t.Cleanup(func() {
		listContainersFn = origList
		removeContainerFn = origRemove
	})

	var gotArgs []string
	listContainersFn = func(ctx context.Context, dockerBin string, args []string) ([]string, error) {
		gotArgs = args
		return []string{"cid1", "cid2"}, nil
	}
	var removed []string
	removeContainerFn = func(ctx context.Context, dockerBin, name string) error {
		removed = append(removed, name)
		return nil
	}

	m := &Manifest{ComposeProject: "myapp-t-smoke-abc123", CopyPath: t.TempDir()}
	if err := reapContainersReal(context.Background(), m); err != nil {
		t.Fatalf("reapContainersReal: %v", err)
	}

	hasAll := false
	wantFilter := "label=com.docker.compose.project=myapp-t-smoke-abc123"
	hasFilter := false
	for i, a := range gotArgs {
		if a == "-a" || a == "-aq" || a == "-qa" || a == "--all" {
			hasAll = true
		}
		if a == "--filter" && i+1 < len(gotArgs) && gotArgs[i+1] == wantFilter {
			hasFilter = true
		}
	}
	if !hasAll {
		t.Errorf("ps args = %v, want the -a (all containers) flag present", gotArgs)
	}
	if !hasFilter {
		t.Errorf("ps args = %v, want --filter %q", gotArgs, wantFilter)
	}
	if len(removed) != 2 || removed[0] != "cid1" || removed[1] != "cid2" {
		t.Errorf("removed containers = %v, want [cid1 cid2]", removed)
	}
}

func TestReapContainersReal_RemoveFailureDoesNotAbortBatch(t *testing.T) {
	origList := listContainersFn
	origRemove := removeContainerFn
	t.Cleanup(func() {
		listContainersFn = origList
		removeContainerFn = origRemove
	})

	listContainersFn = func(ctx context.Context, dockerBin string, args []string) ([]string, error) {
		return []string{"cid1", "cid2"}, nil
	}
	var removed []string
	removeContainerFn = func(ctx context.Context, dockerBin, name string) error {
		removed = append(removed, name)
		if name == "cid1" {
			return errors.New("rm failed")
		}
		return nil
	}

	m := &Manifest{ComposeProject: "myapp-t-smoke-abc123", CopyPath: t.TempDir()}
	err := reapContainersReal(context.Background(), m)
	if err == nil {
		t.Fatal("reapContainersReal() = nil error, want the cid1 failure surfaced")
	}
	if len(removed) != 2 {
		t.Errorf("removed = %v, want both containers attempted despite the cid1 failure", removed)
	}
}

func TestRunComposeDownProcess_KilledWhenContextExpires(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep binary not available on PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := runComposeDownProcess(ctx, "sleep", []string{"5"}, nil, "", nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("runComposeDownProcess() = nil error, want the process to be killed by the expired context")
	}
	if elapsed > 3*time.Second {
		t.Errorf("runComposeDownProcess() took %s, want it killed well before the 5s sleep completed", elapsed)
	}
}
