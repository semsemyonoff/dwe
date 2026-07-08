package test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/workflow/envtest"

	"github.com/spf13/cobra"
)

// fakeClean is the test double for cleanFn: it returns a scripted result/
// error, and records every request and the COMPOSE_* env state at call time.
type fakeClean struct {
	result           *envtest.CleanResult
	err              error
	calls            []envtest.CleanRequest
	composeEnvAtCall []string
}

func (f *fakeClean) run(_ context.Context, req envtest.CleanRequest) (*envtest.CleanResult, error) {
	f.calls = append(f.calls, req)
	f.composeEnvAtCall = append(f.composeEnvAtCall, os.Getenv("COMPOSE_PROJECT_NAME"))
	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil {
		return f.result, nil
	}
	return &envtest.CleanResult{}, nil
}

func withFakeClean(t *testing.T, f *fakeClean) {
	t.Helper()
	orig := cleanFn
	cleanFn = f.run
	t.Cleanup(func() { cleanFn = orig })
}

func newCleanTestCmd() (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	var out, errW bytes.Buffer
	cmd := &cobra.Command{Use: "clean"}
	cmd.SetOut(&out)
	cmd.SetErr(&errW)
	cmd.SetContext(context.Background())
	return cmd, &out, &errW
}

func TestRunTestClean_ScrubsComposeEnvBeforeCleanFn(t *testing.T) {
	t.Setenv("COMPOSE_PROJECT_NAME", "leftover")
	f := &fakeClean{}
	withFakeClean(t, f)

	flags := &cmdctx.RootFlags{Root: t.TempDir()}
	cmd, _, _ := newCleanTestCmd()

	if err := runTestClean(cmd, flags, nil, false); err != nil {
		t.Fatalf("runTestClean: %v", err)
	}
	if len(f.composeEnvAtCall) != 1 || f.composeEnvAtCall[0] != "" {
		t.Errorf("expected COMPOSE_PROJECT_NAME to be scrubbed before Clean, got %q", f.composeEnvAtCall)
	}
}

func TestRunTestClean_DryRunAndScenarioArgsThreaded(t *testing.T) {
	f := &fakeClean{}
	withFakeClean(t, f)

	flags := &cmdctx.RootFlags{Root: t.TempDir()}
	cmd, _, _ := newCleanTestCmd()

	if err := runTestClean(cmd, flags, []string{"smoke", "redis-off"}, true); err != nil {
		t.Fatalf("runTestClean: %v", err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("expected exactly one Clean call, got %d", len(f.calls))
	}
	req := f.calls[0]
	if !req.DryRun {
		t.Error("expected DryRun=true threaded through")
	}
	if len(req.Scenarios) != 2 || req.Scenarios[0] != "smoke" || req.Scenarios[1] != "redis-off" {
		t.Errorf("expected scenario args threaded, got %+v", req.Scenarios)
	}
}

func TestRunTestClean_JSONShape(t *testing.T) {
	f := &fakeClean{
		result: &envtest.CleanResult{
			Swept:   []envtest.CleanEntry{{Scenario: "smoke", ComposeProject: "proj-t-smoke-abc", CopyPath: "/tmp/a"}},
			Skipped: []envtest.SkippedEntry{{CleanEntry: envtest.CleanEntry{Scenario: "live-one"}, Reason: "live"}},
			Failed:  []envtest.FailedEntry{{CleanEntry: envtest.CleanEntry{Scenario: "broke"}, Error: "compose down: boom"}},
			Orphans: []envtest.OrphanEntry{{ComposeProject: "proj-t-ghost-xyz", Note: "no manifest — remove manually"}},
		},
	}
	withFakeClean(t, f)

	flags := &cmdctx.RootFlags{Root: t.TempDir(), Output: "json"}
	cmd, out, _ := newCleanTestCmd()

	err := runTestClean(cmd, flags, nil, false)
	var oe *testRunOutcomeError
	if !errors.As(err, &oe) || oe.ExitCode() != 1 {
		t.Fatalf("expected exit-code-1 error (Failed entries present), got %v", err)
	}

	var got testCleanJSON
	if uerr := json.Unmarshal(out.Bytes(), &got); uerr != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", uerr, out.String())
	}
	if len(got.Swept) != 1 || got.Swept[0].Scenario != "smoke" || got.Swept[0].ComposeProject != "proj-t-smoke-abc" {
		t.Errorf("unexpected swept: %+v", got.Swept)
	}
	if len(got.Skipped) != 1 || got.Skipped[0].Reason != "live" {
		t.Errorf("unexpected skipped: %+v", got.Skipped)
	}
	if len(got.Failed) != 1 || got.Failed[0].Error != "compose down: boom" {
		t.Errorf("unexpected failed: %+v", got.Failed)
	}
	if len(got.Orphans) != 1 || got.Orphans[0].ComposeProject != "proj-t-ghost-xyz" {
		t.Errorf("unexpected orphans: %+v", got.Orphans)
	}
}

func TestRunTestClean_TextSummary_Swept(t *testing.T) {
	f := &fakeClean{
		result: &envtest.CleanResult{
			Swept: []envtest.CleanEntry{{Scenario: "smoke", ComposeProject: "proj-t-smoke-abc", CopyPath: "/tmp/a"}},
		},
	}
	withFakeClean(t, f)

	flags := &cmdctx.RootFlags{Root: t.TempDir()}
	cmd, out, _ := newCleanTestCmd()

	if err := runTestClean(cmd, flags, nil, false); err != nil {
		t.Fatalf("runTestClean: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "smoke: swept (proj-t-smoke-abc)") {
		t.Errorf("expected swept line, got %q", text)
	}
	if !strings.Contains(text, "1 swept, 0 skipped (live), 0 failed, 0 orphan(s)") {
		t.Errorf("expected summary line, got %q", text)
	}
}

func TestRunTestClean_TextSummary_DryRunWording(t *testing.T) {
	f := &fakeClean{
		result: &envtest.CleanResult{
			DryRun: true,
			Swept:  []envtest.CleanEntry{{Scenario: "smoke", ComposeProject: "proj-t-smoke-abc"}},
		},
	}
	withFakeClean(t, f)

	flags := &cmdctx.RootFlags{Root: t.TempDir()}
	cmd, out, _ := newCleanTestCmd()

	if err := runTestClean(cmd, flags, nil, true); err != nil {
		t.Fatalf("runTestClean: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "would sweep") {
		t.Errorf("expected dry-run wording, got %q", text)
	}
}

func TestRunTestClean_FailedEntry_ExitOne_PayloadStillEmitted(t *testing.T) {
	f := &fakeClean{
		result: &envtest.CleanResult{
			Failed: []envtest.FailedEntry{{CleanEntry: envtest.CleanEntry{Scenario: "broke"}, Error: "boom"}},
		},
	}
	withFakeClean(t, f)

	flags := &cmdctx.RootFlags{Root: t.TempDir()}
	cmd, out, _ := newCleanTestCmd()

	err := runTestClean(cmd, flags, nil, false)
	var oe *testRunOutcomeError
	if !errors.As(err, &oe) || oe.ExitCode() != 1 {
		t.Fatalf("expected exit-code-1 error, got %v", err)
	}
	if oe.Error() != "" {
		t.Errorf("outcome error must render no text, got %q", oe.Error())
	}
	if !strings.Contains(out.String(), "broke: failed (boom)") {
		t.Errorf("expected the failure to still be rendered, got %q", out.String())
	}
}

func TestRunTestClean_NoFailures_ExitZero(t *testing.T) {
	f := &fakeClean{
		result: &envtest.CleanResult{
			Skipped: []envtest.SkippedEntry{{CleanEntry: envtest.CleanEntry{Scenario: "live-one"}, Reason: "live"}},
			Orphans: []envtest.OrphanEntry{{ComposeProject: "proj-t-ghost", Note: "no manifest — remove manually"}},
		},
	}
	withFakeClean(t, f)

	flags := &cmdctx.RootFlags{Root: t.TempDir()}
	cmd, _, _ := newCleanTestCmd()

	if err := runTestClean(cmd, flags, nil, false); err != nil {
		t.Fatalf("expected nil (exit 0) with only skipped/orphans, got %v", err)
	}
}

func TestRunTestClean_HardError_NonZeroExit(t *testing.T) {
	f := &fakeClean{err: errors.New("unreadable manifests dir")}
	withFakeClean(t, f)

	flags := &cmdctx.RootFlags{Root: t.TempDir()}
	cmd, _, _ := newCleanTestCmd()

	err := runTestClean(cmd, flags, nil, false)
	if err == nil {
		t.Fatal("expected a hard error")
	}
	ce, ok := errors.AsType[*cmdctx.CodedError](err)
	if !ok || ce.Code != "test_clean_failed" {
		t.Fatalf("expected test_clean_failed CodedError, got %T: %v", err, err)
	}
	if cmdctx.ExitCodeFor(err) == 0 {
		t.Errorf("expected non-zero exit code, got 0")
	}
}
