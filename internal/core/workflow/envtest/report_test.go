package envtest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func reportTestManifest(t *testing.T) *Manifest {
	t.Helper()
	copyPath := t.TempDir()
	return &Manifest{
		Scenario:       "smoke",
		RunID:          "abc123",
		ComposeProject: "myapp-t-smoke-abc123",
		CopyPath:       copyPath,
		ReportDir:      filepath.Join(t.TempDir(), "reports", "smoke"),
	}
}

func stubReportDeps(ps, logs string, psErr, logsErr error) ReportDeps {
	return ReportDeps{
		PS:   func(ctx context.Context, m *Manifest) (string, error) { return ps, psErr },
		Logs: func(ctx context.Context, m *Manifest) (string, error) { return logs, logsErr },
	}
}

func TestCollectReport_WritesAllThreeArtifactsAndOverwritesExisting(t *testing.T) {
	m := reportTestManifest(t)
	if err := os.MkdirAll(filepath.Join(m.CopyPath, ".dwe", "logs"), 0o755); err != nil {
		t.Fatalf("mkdir logs dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(m.CopyPath, ".dwe", "logs", "test.log"), []byte("pipeline output\n"), 0o644); err != nil {
		t.Fatalf("write test.log: %v", err)
	}

	// Pre-existing report dir with a stale file that must not survive.
	if err := os.MkdirAll(m.ReportDir, 0o755); err != nil {
		t.Fatalf("mkdir report dir: %v", err)
	}
	stalePath := filepath.Join(m.ReportDir, "stale.txt")
	if err := os.WriteFile(stalePath, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}

	deps := stubReportDeps("ps output", "logs output", nil, nil)
	dir, err := CollectReport(context.Background(), m, deps, nil)
	if err != nil {
		t.Fatalf("CollectReport: %v", err)
	}
	if dir != m.ReportDir {
		t.Errorf("dir = %q, want %q", dir, m.ReportDir)
	}

	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Errorf("stale.txt still present, want the report dir cleared before writing")
	}

	pipelineLog, err := os.ReadFile(filepath.Join(m.ReportDir, "pipeline.log"))
	if err != nil {
		t.Fatalf("reading pipeline.log: %v", err)
	}
	if string(pipelineLog) != "pipeline output\n" {
		t.Errorf("pipeline.log = %q, want %q", pipelineLog, "pipeline output\n")
	}

	ps, err := os.ReadFile(filepath.Join(m.ReportDir, "compose-ps.txt"))
	if err != nil {
		t.Fatalf("reading compose-ps.txt: %v", err)
	}
	if string(ps) != "ps output" {
		t.Errorf("compose-ps.txt = %q, want %q", ps, "ps output")
	}

	logs, err := os.ReadFile(filepath.Join(m.ReportDir, "container-logs.txt"))
	if err != nil {
		t.Fatalf("reading container-logs.txt: %v", err)
	}
	if string(logs) != "logs output" {
		t.Errorf("container-logs.txt = %q, want %q", logs, "logs output")
	}
}

func TestCollectReport_MissingPipelineLogSkippedOthersStillWritten(t *testing.T) {
	m := reportTestManifest(t)
	// No .dwe/logs/test.log written at all.

	var warnings []string
	deps := stubReportDeps("ps output", "logs output", nil, nil)
	dir, err := CollectReport(context.Background(), m, deps, func(msg string) { warnings = append(warnings, msg) })
	if err != nil {
		t.Fatalf("CollectReport: %v", err)
	}
	if dir != m.ReportDir {
		t.Errorf("dir = %q, want %q", dir, m.ReportDir)
	}

	if _, err := os.Stat(filepath.Join(m.ReportDir, "pipeline.log")); !os.IsNotExist(err) {
		t.Errorf("pipeline.log present, want it skipped (no source)")
	}
	if _, err := os.Stat(filepath.Join(m.ReportDir, "compose-ps.txt")); err != nil {
		t.Errorf("compose-ps.txt missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.ReportDir, "container-logs.txt")); err != nil {
		t.Errorf("container-logs.txt missing: %v", err)
	}

	found := false
	for _, w := range warnings {
		if strings.Contains(w, "pipeline log") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want one about the missing pipeline log", warnings)
	}
}

func TestCollectReport_PartialPSAndLogsErrorsStillWriteFilesAndWarn(t *testing.T) {
	m := reportTestManifest(t)

	wantPSErr := errors.New("ps boom")
	wantLogsErr := errors.New("logs boom")
	var warnings []string
	deps := stubReportDeps("partial ps", "partial logs", wantPSErr, wantLogsErr)
	if _, err := CollectReport(context.Background(), m, deps, func(msg string) { warnings = append(warnings, msg) }); err != nil {
		t.Fatalf("CollectReport: %v", err)
	}

	// On a capture error the partial output is retained AND a visible
	// "capture failed" marker is prepended, so a report read from CI artifacts
	// (detached from the run's warn output) still shows the failure.
	ps, err := os.ReadFile(filepath.Join(m.ReportDir, "compose-ps.txt"))
	if err != nil {
		t.Fatalf("reading compose-ps.txt: %v", err)
	}
	if !strings.Contains(string(ps), "partial ps") || !strings.Contains(string(ps), "capture failed: ps boom") {
		t.Errorf("compose-ps.txt = %q, want the partial output plus a capture-failed marker", ps)
	}

	logs, err := os.ReadFile(filepath.Join(m.ReportDir, "container-logs.txt"))
	if err != nil {
		t.Fatalf("reading container-logs.txt: %v", err)
	}
	if !strings.Contains(string(logs), "partial logs") || !strings.Contains(string(logs), "capture failed: logs boom") {
		t.Errorf("container-logs.txt = %q, want the partial output plus a capture-failed marker", logs)
	}

	joined := strings.Join(warnings, " | ")
	if !strings.Contains(joined, "ps boom") {
		t.Errorf("warnings = %v, want one mentioning the PS error", warnings)
	}
	if !strings.Contains(joined, "logs boom") {
		t.Errorf("warnings = %v, want one mentioning the Logs error", warnings)
	}
}

func TestCollectReport_OwnerOnlyPermissions(t *testing.T) {
	m := reportTestManifest(t)
	deps := stubReportDeps("ps out", "logs out", nil, nil)
	if _, err := CollectReport(context.Background(), m, deps, nil); err != nil {
		t.Fatalf("CollectReport: %v", err)
	}

	// The report can carry secrets a service printed to its logs, so the
	// directory is owner-only (0o700) and the captured artifacts are 0o600.
	dirInfo, err := os.Stat(m.ReportDir)
	if err != nil {
		t.Fatalf("stat report dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("report dir perm = %o, want 700", perm)
	}
	for _, name := range []string{"compose-ps.txt", "container-logs.txt"} {
		info, err := os.Stat(filepath.Join(m.ReportDir, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s perm = %o, want 600", name, perm)
		}
	}
}

func TestCollectReport_NilManifest(t *testing.T) {
	if _, err := CollectReport(context.Background(), nil, ReportDeps{}, nil); err == nil {
		t.Fatal("CollectReport(nil manifest) = nil error, want error")
	}
}

func TestCollectReport_NilDepsFieldsAreSkipped(t *testing.T) {
	m := reportTestManifest(t)
	if _, err := CollectReport(context.Background(), m, ReportDeps{}, nil); err != nil {
		t.Fatalf("CollectReport() with zero-value deps = %v, want nil", err)
	}
	if _, err := os.Stat(filepath.Join(m.ReportDir, "compose-ps.txt")); !os.IsNotExist(err) {
		t.Errorf("compose-ps.txt present, want it skipped (nil PS dep)")
	}
	if _, err := os.Stat(filepath.Join(m.ReportDir, "container-logs.txt")); !os.IsNotExist(err) {
		t.Errorf("container-logs.txt present, want it skipped (nil Logs dep)")
	}
}

// --- default (real) implementation tests: capture/list seams recorded, no real docker ---

func writeReportCopyConfig(t *testing.T, copyRoot, logsArgsYAML string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(copyRoot, "workspace.yml"), []byte("project:\n  name: test\n  prefix: dwe\n"), 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}
	workspaceDir := filepath.Join(copyRoot, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace/: %v", err)
	}
	dockerYAML := "project_name: \"${project.prefix}-${project.name}\"\n"
	if logsArgsYAML != "" {
		dockerYAML += "args:\n  logs: " + logsArgsYAML + "\n"
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "docker.yml"), []byte(dockerYAML), 0o644); err != nil {
		t.Fatalf("write docker.yml: %v", err)
	}
}

func TestReportPSReal_ValidConfigUsesBuildInternalArgsWithAllFlag(t *testing.T) {
	origCapture := captureCmdFn
	t.Cleanup(func() { captureCmdFn = origCapture })

	var gotArgs []string
	captureCmdFn = func(ctx context.Context, bin string, args, env []string, dir string) ([]byte, error) {
		gotArgs = args
		return []byte("ps output"), nil
	}

	copyRoot := t.TempDir()
	writeReportCopyConfig(t, copyRoot, `["-f"]`)

	m := &Manifest{ComposeProject: "dwe-t-smoke-abc123", CopyPath: copyRoot}
	out, err := reportPSReal(context.Background(), m)
	if err != nil {
		t.Fatalf("reportPSReal: %v", err)
	}
	if out != "ps output" {
		t.Errorf("out = %q, want %q", out, "ps output")
	}

	if gotArgs[0] != "compose" {
		t.Fatalf("args[0] = %q, want %q (args=%v)", gotArgs[0], "compose", gotArgs)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "ps") {
		t.Errorf("args = %v, want the ps subcommand present", gotArgs)
	}
	hasAll := false
	for _, a := range gotArgs {
		if a == "--all" {
			hasAll = true
		}
		if a == "--ansi" || a == "--progress" {
			t.Errorf("args = %v, must not include global args (BuildInternalArgs, not BuildArgs)", gotArgs)
		}
	}
	if !hasAll {
		t.Errorf("args = %v, want --all present (compose ps defaults to running-only)", gotArgs)
	}
}

func TestReportLogsReal_ValidConfigNeverFollowsUserLogsPolicy(t *testing.T) {
	origCapture := captureCmdFn
	t.Cleanup(func() { captureCmdFn = origCapture })

	var gotArgs []string
	captureCmdFn = func(ctx context.Context, bin string, args, env []string, dir string) ([]byte, error) {
		gotArgs = args
		return []byte("logs output"), nil
	}

	copyRoot := t.TempDir()
	// docker.yml sets args.logs: ["-f"] — the follow-hang regression pin.
	writeReportCopyConfig(t, copyRoot, `["-f"]`)

	m := &Manifest{ComposeProject: "dwe-t-smoke-abc123", CopyPath: copyRoot}
	out, err := reportLogsReal(context.Background(), m)
	if err != nil {
		t.Fatalf("reportLogsReal: %v", err)
	}
	if out != "logs output" {
		t.Errorf("out = %q, want %q", out, "logs output")
	}

	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "logs") {
		t.Errorf("args = %v, want the logs subcommand present", gotArgs)
	}
	if !strings.Contains(joined, "--no-color") {
		t.Errorf("args = %v, want --no-color present", gotArgs)
	}
	if !strings.Contains(joined, "--tail 200") {
		t.Errorf("args = %v, want --tail 200 present", gotArgs)
	}
	for _, a := range gotArgs {
		if a == "-f" {
			t.Errorf("args = %v, must NOT include the project's args.logs -f override (would hang report collection)", gotArgs)
		}
	}
}

func TestReportPSReal_MissingConfigFallsBackToIdentityLabel(t *testing.T) {
	origCapture := captureCmdFn
	t.Cleanup(func() { captureCmdFn = origCapture })

	var gotBin string
	var gotArgs []string
	captureCmdFn = func(ctx context.Context, bin string, args, env []string, dir string) ([]byte, error) {
		gotBin = bin
		gotArgs = args
		return []byte("fallback ps"), nil
	}

	m := &Manifest{ComposeProject: "myapp-t-smoke-abc123", CopyPath: t.TempDir() /* no workspace.yml */}
	out, err := reportPSReal(context.Background(), m)
	if err != nil {
		t.Fatalf("reportPSReal: %v", err)
	}
	if out != "fallback ps" {
		t.Errorf("out = %q, want %q", out, "fallback ps")
	}
	if gotBin != "docker" {
		t.Errorf("bin = %q, want %q", gotBin, "docker")
	}

	wantFilter := "label=com.docker.compose.project=myapp-t-smoke-abc123"
	want := []string{"ps", "-a", "--filter", wantFilter}
	if len(gotArgs) != len(want) {
		t.Fatalf("args = %v, want %v", gotArgs, want)
	}
	for i, a := range want {
		if gotArgs[i] != a {
			t.Errorf("args[%d] = %q, want %q (args=%v)", i, gotArgs[i], a, gotArgs)
		}
	}
}

func TestReportLogsReal_MissingConfigFallsBackToPerContainerCapture(t *testing.T) {
	origCapture := captureCmdFn
	origList := listContainersFn
	t.Cleanup(func() {
		captureCmdFn = origCapture
		listContainersFn = origList
	})

	var gotFilterArgs []string
	listContainersFn = func(ctx context.Context, dockerBin string, args []string) ([]string, error) {
		gotFilterArgs = args
		return []string{"cid1", "cid2"}, nil
	}
	var gotCalls [][]string
	captureCmdFn = func(ctx context.Context, bin string, args, env []string, dir string) ([]byte, error) {
		gotCalls = append(gotCalls, args)
		return []byte("tail for " + args[len(args)-1]), nil
	}

	m := &Manifest{ComposeProject: "myapp-t-smoke-abc123", CopyPath: t.TempDir()}
	out, err := reportLogsReal(context.Background(), m)
	if err != nil {
		t.Fatalf("reportLogsReal: %v", err)
	}

	wantFilter := "label=com.docker.compose.project=myapp-t-smoke-abc123"
	hasFilter := false
	for i, a := range gotFilterArgs {
		if a == "--filter" && i+1 < len(gotFilterArgs) && gotFilterArgs[i+1] == wantFilter {
			hasFilter = true
		}
	}
	if !hasFilter {
		t.Errorf("list args = %v, want --filter %q", gotFilterArgs, wantFilter)
	}

	if len(gotCalls) != 2 {
		t.Fatalf("capture calls = %d, want 2 (one per container)", len(gotCalls))
	}
	for i, id := range []string{"cid1", "cid2"} {
		want := []string{"logs", "--tail", "200", id}
		if strings.Join(gotCalls[i], " ") != strings.Join(want, " ") {
			t.Errorf("call[%d] args = %v, want %v", i, gotCalls[i], want)
		}
	}

	if !strings.Contains(out, "==== cid1 ====") || !strings.Contains(out, "==== cid2 ====") {
		t.Errorf("out = %q, want ==== <id> ==== headers for both containers", out)
	}
}

func TestReportLogsIdentityFallback_ListErrorSurfaces(t *testing.T) {
	origList := listContainersFn
	t.Cleanup(func() { listContainersFn = origList })

	wantErr := errors.New("list boom")
	listContainersFn = func(ctx context.Context, dockerBin string, args []string) ([]string, error) {
		return nil, wantErr
	}

	m := &Manifest{ComposeProject: "myapp-t-smoke-abc123", CopyPath: t.TempDir()}
	if _, err := reportLogsReal(context.Background(), m); !errors.Is(err, wantErr) {
		t.Errorf("reportLogsReal() error = %v, want it to wrap %v", err, wantErr)
	}
}
