package workflow

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// ffTrue / ffFalse are reusable *bool literals for the FailFast tristate.
var (
	ffTrue  = func() *bool { v := true; return &v }()
	ffFalse = func() *bool { v := false; return &v }()
)

// makeShellLeaf builds a minimal shell command suitable as a parallel sub-step.
func makeShellLeaf(id, body string) *CommandDef {
	parts := strings.Split(id, ".")
	return &CommandDef{
		Type:      CommandTypeShell,
		ID:        id,
		Group:     strings.Join(parts[:len(parts)-1], "."),
		LocalName: parts[len(parts)-1],
		Cmd:       body,
	}
}

func runParallelWorkflowCtx(t *testing.T, projectRoot string, reg *Registry, wf *CommandDef) (string, string, error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	ctx := RunContext{
		Cmd:         wf,
		Params:      map[string]any{},
		Context:     map[string]any{},
		Render:      &tpl.RenderContext{Params: map[string]any{}},
		Registry:    reg,
		ProjectRoot: projectRoot,
		Stdout:      &outBuf,
		Stderr:      &errBuf,
	}
	err := (&WorkflowRunner{}).Run(context.Background(), ctx)
	return outBuf.String(), errBuf.String(), err
}

// ---------------------------------------------------------------------------
// Validation: workflow parallel YAML
// ---------------------------------------------------------------------------

func TestWorkflowRunner_Parallel_RunsConcurrently(t *testing.T) {
	dir := t.TempDir()
	// Each leaf writes to its own file AND increments a shared counter through a
	// barrier file so the test fails if any sub-step starts AFTER another
	// finishes (i.e. they did NOT actually overlap).
	barrier := filepath.Join(dir, "barrier")
	leafBody := func(name string) string {
		return fmt.Sprintf(
			`set -e; printf 'x' >> %s; for _ in 1 2 3 4 5 6 7 8 9 10; do n=$(wc -c < %s 2>/dev/null | tr -d ' '); if [ "${n:-0}" -ge 3 ]; then printf '%s\n' > %s/%s.done; exit 0; fi; sleep 0.05; done; echo "timeout waiting for siblings" >&2; exit 1`,
			barrier, barrier, "ran", dir, name,
		)
	}

	a := makeShellLeaf("wf.a", leafBody("a"))
	b := makeShellLeaf("wf.b", leafBody("b"))
	c := makeShellLeaf("wf.c", leafBody("c"))
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.par",
		Group:     "wf",
		LocalName: "par",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				Steps: []WorkflowStep{
					{Command: "wf.a"},
					{Command: "wf.b"},
					{Command: "wf.c"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(wf, a, b, c)
	start := time.Now()
	_, errOut, err := runParallelWorkflowCtx(t, dir, reg, wf)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("expected nil err; got %v\nstderr:\n%s", err, errOut)
	}
	// With true concurrency the barrier resolves quickly; 5s is a generous cap.
	if elapsed > 5*time.Second {
		t.Fatalf("parallel group took too long (%v); sub-steps may not be concurrent", elapsed)
	}
	if !strings.Contains(errOut, "✓ [1/3]") || !strings.Contains(errOut, "✓ [3/3]") {
		t.Errorf("expected success rows in stderr; got:\n%s", errOut)
	}
}

func TestWorkflowRunner_Parallel_FailFastTrue_CancelsSiblings(t *testing.T) {
	dir := t.TempDir()
	fail := makeShellLeaf("wf.fail", `exit 7`)
	// Sibling exec-replaces sh with sleep so SIGTERM directly kills the
	// process (sh signal-delivery during `wait` is implementation-dependent).
	slow := makeShellLeaf("wf.slow", `exec sleep 30`)

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.ff",
		Group:     "wf",
		LocalName: "ff",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				FailFast: ffTrue,
				Steps: []WorkflowStep{
					{Command: "wf.fail"},
					{Command: "wf.slow"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(wf, fail, slow)
	start := time.Now()
	_, _, err := runParallelWorkflowCtx(t, dir, reg, wf)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected fail_fast error")
	}
	if !strings.Contains(err.Error(), "wf.fail") {
		t.Errorf("expected error to mention wf.fail; got %q", err.Error())
	}
	if elapsed > 4*time.Second {
		t.Fatalf("fail_fast did not cancel sibling promptly (%v)", elapsed)
	}
}

func TestWorkflowRunner_Parallel_FailFastFalse_AggregatesErrors(t *testing.T) {
	dir := t.TempDir()
	failA := makeShellLeaf("wf.fa", `exit 1`)
	failB := makeShellLeaf("wf.fb", `exit 2`)
	ok := makeShellLeaf("wf.ok", `exit 0`)

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.agg",
		Group:     "wf",
		LocalName: "agg",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				FailFast: ffFalse,
				Steps: []WorkflowStep{
					{Command: "wf.fa"},
					{Command: "wf.fb"},
					{Command: "wf.ok"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(wf, failA, failB, ok)
	_, errOut, err := runParallelWorkflowCtx(t, dir, reg, wf)
	if err == nil {
		t.Fatal("expected aggregated error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "wf.fa") || !strings.Contains(msg, "wf.fb") {
		t.Errorf("expected both failures in joined error; got %q", msg)
	}
	if !strings.Contains(errOut, "✓ [3/3] Done: wf.ok") {
		t.Errorf("expected ok row to render; got:\n%s", errOut)
	}
}

func TestWorkflowRunner_Parallel_SubStepWhenFalse_Skips(t *testing.T) {
	dir := t.TempDir()
	run := makeShellLeaf("wf.run", `echo hi`)

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.whenfalse",
		Group:     "wf",
		LocalName: "whenfalse",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				Steps: []WorkflowStep{
					{Command: "wf.run", When: "false"},
					{Command: "wf.run"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(wf, run)
	_, errOut, err := runParallelWorkflowCtx(t, dir, reg, wf)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr:\n%s", err, errOut)
	}
	if !strings.Contains(errOut, "◎ [1/2] Skipped: wf.run") {
		t.Errorf("expected skipped row; got:\n%s", errOut)
	}
	if !strings.Contains(errOut, "✓ [2/2] Done: wf.run") {
		t.Errorf("expected ok row; got:\n%s", errOut)
	}
}

func TestWorkflowRunner_Parallel_OutputIsolation(t *testing.T) {
	dir := t.TempDir()
	// Each leaf writes to its own stdout; we then read the per-sub-step log
	// files to confirm no cross-talk.
	one := makeShellLeaf("wf.one", `for i in 1 2 3; do echo one-$i; sleep 0.02; done`)
	two := makeShellLeaf("wf.two", `for i in 1 2 3; do echo two-$i; sleep 0.02; done`)

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.iso",
		Group:     "wf",
		LocalName: "iso",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				Steps: []WorkflowStep{
					{Command: "wf.one"},
					{Command: "wf.two"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(wf, one, two)
	_, _, err := runParallelWorkflowCtx(t, dir, reg, wf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logDir := filepath.Join(dir, ".devbox", "logs", "parallel", "workflow", "wf.iso")
	for sub, want := range map[string]string{"wf.one": "one-", "wf.two": "two-"} {
		path := filepath.Join(logDir, sub+".log")
		data, readErr := readFileBytes(path)
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		s := string(data)
		other := "two-"
		if sub == "wf.two" {
			other = "one-"
		}
		if !strings.Contains(s, want) {
			t.Errorf("%s missing %q; got %q", sub, want, s)
		}
		if strings.Contains(s, other) {
			t.Errorf("%s leaked sibling output (%q): %q", sub, other, s)
		}
	}
}

func TestWorkflowRunner_Parallel_ContinueOnError_SubStep(t *testing.T) {
	dir := t.TempDir()
	fail := makeShellLeaf("wf.coe-fail", `exit 5`)
	ok := makeShellLeaf("wf.coe-ok", `exit 0`)

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.coe",
		Group:     "wf",
		LocalName: "coe",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				FailFast: ffTrue,
				Steps: []WorkflowStep{
					{Command: "wf.coe-fail", ContinueOnError: true},
					{Command: "wf.coe-ok"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(wf, fail, ok)
	_, _, err := runParallelWorkflowCtx(t, dir, reg, wf)
	if err != nil {
		t.Fatalf("expected nil err with sub-step continue_on_error; got %v", err)
	}
}

func TestWorkflowRunner_Parallel_ContinueOnError_Container(t *testing.T) {
	dir := t.TempDir()
	a := makeShellLeaf("wf.a", `exit 1`)
	b := makeShellLeaf("wf.b", `exit 1`)
	after := makeShellLeaf("wf.after", `echo after`)

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.coe-c",
		Group:     "wf",
		LocalName: "coe-c",
		Steps: []WorkflowStep{
			{ContinueOnError: true, Parallel: &WorkflowParallel{
				FailFast: ffTrue,
				Steps: []WorkflowStep{
					{Command: "wf.a"},
					{Command: "wf.b"},
				},
			}},
			{Command: "wf.after"},
		},
	}
	reg := buildWorkflowRegistry(wf, a, b, after)
	_, errOut, err := runParallelWorkflowCtx(t, dir, reg, wf)
	if err != nil {
		t.Fatalf("expected nil err with container continue_on_error; got %v", err)
	}
	if !strings.Contains(errOut, "continue_on_error") {
		t.Errorf("expected continue_on_error warning in stderr; got:\n%s", errOut)
	}
}

func TestWorkflowRunner_Parallel_ConfirmationPreflight_Direct(t *testing.T) {
	dir := t.TempDir()
	confirming := makeShellLeaf("wf.conf", `echo hi`)
	confirming.Confirmation = true
	other := makeShellLeaf("wf.other", `echo other`)

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.cf",
		Group:     "wf",
		LocalName: "cf",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				Steps: []WorkflowStep{
					{Command: "wf.conf"},
					{Command: "wf.other"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(wf, confirming, other)

	// Without --yes / NonInteractive, preflight must reject before launching.
	_, _, err := runParallelWorkflowCtx(t, dir, reg, wf)
	if err == nil {
		t.Fatal("expected preflight error for confirmation-required sub-step")
	}
	if !strings.Contains(err.Error(), "requires confirmation") {
		t.Errorf("expected 'requires confirmation' in error; got %q", err.Error())
	}

	// With SkipConfirm=true the preflight passes and the inner ConfirmCommand
	// short-circuits as well.
	var outBuf, errBuf bytes.Buffer
	rc := RunContext{
		Cmd:         wf,
		Params:      map[string]any{},
		Context:     map[string]any{},
		Render:      &tpl.RenderContext{Params: map[string]any{}},
		Registry:    reg,
		ProjectRoot: dir,
		Stdout:      &outBuf,
		Stderr:      &errBuf,
		SkipConfirm: true,
	}
	if err := (&WorkflowRunner{}).Run(context.Background(), rc); err != nil {
		t.Fatalf("expected SkipConfirm run to succeed; got %v\nstderr:\n%s", err, errBuf.String())
	}
}

func TestWorkflowRunner_Parallel_ConfirmationPreflight_LenientWhenFalse(t *testing.T) {
	dir := t.TempDir()
	confirming := makeShellLeaf("wf.conf", `echo hi`)
	confirming.Confirmation = true
	other := makeShellLeaf("wf.other", `echo other`)

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.cf2",
		Group:     "wf",
		LocalName: "cf2",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				Steps: []WorkflowStep{
					{Command: "wf.conf", When: "false"},
					{Command: "wf.other"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(wf, confirming, other)
	_, errOut, err := runParallelWorkflowCtx(t, dir, reg, wf)
	if err != nil {
		t.Fatalf("preflight should be lenient for when=false; got %v\nstderr:\n%s", err, errOut)
	}
	if !strings.Contains(errOut, "◎ [1/2] Skipped") {
		t.Errorf("expected skipped row; got:\n%s", errOut)
	}
}

// TestWorkflowRunner_Parallel_MaxConcurrent smoke-tests that MaxConcurrent caps
// parallelism (4 sub-steps × ~100ms with cap=2 → total ≥ ~200ms).
func TestWorkflowRunner_Parallel_MaxConcurrent(t *testing.T) {
	dir := t.TempDir()
	body := `sleep 0.1`
	a := makeShellLeaf("wf.a", body)
	b := makeShellLeaf("wf.b", body)
	c := makeShellLeaf("wf.c", body)
	d := makeShellLeaf("wf.d", body)

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.cap",
		Group:     "wf",
		LocalName: "cap",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				MaxConcurrent: 2,
				Steps: []WorkflowStep{
					{Command: "wf.a"},
					{Command: "wf.b"},
					{Command: "wf.c"},
					{Command: "wf.d"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(wf, a, b, c, d)

	// Replace shell with a Go-side wrapper isn't worth it; instead trust that
	// MaxConcurrent is wired through SetLimit and just smoke-test elapsed.
	start := time.Now()
	_, _, err := runParallelWorkflowCtx(t, dir, reg, wf)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	elapsed := time.Since(start)
	// 4 sub-steps × 100ms with cap=2 → expect ≥ ~200ms, well under 1s.
	if elapsed < 150*time.Millisecond {
		t.Errorf("cap=2 too fast (%v); expected >= ~200ms", elapsed)
	}
}

// TestWorkflowRunner_Parallel_FailureDumpLabelled verifies that the failure
// dump separator is labelled with the failing sub-step's command name so
// multi-failure dumps stay attributable.
func TestWorkflowRunner_Parallel_FailureDumpLabelled(t *testing.T) {
	dir := t.TempDir()
	a := makeShellLeaf("wf.fa", `echo aaaa; exit 1`)
	b := makeShellLeaf("wf.fb", `echo bbbb; exit 1`)

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.dump",
		Group:     "wf",
		LocalName: "dump",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				FailFast: ffFalse,
				Steps: []WorkflowStep{
					{Command: "wf.fa"},
					{Command: "wf.fb"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(wf, a, b)
	_, errOut, err := runParallelWorkflowCtx(t, dir, reg, wf)
	if err == nil {
		t.Fatal("expected aggregated error")
	}
	for _, want := range []string{
		"───── output: wf.fa ─────",
		"───── output: wf.fb ─────",
		"aaaa",
		"bbbb",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("expected %q in stderr; got:\n%s", want, errOut)
		}
	}
}

// TestWorkflowRunner_Parallel_ForceColorEnv_Exported verifies that workflow
// parallel sub-steps inherit the colour-forcing env vars used to coax CLI
// tools into emitting ANSI even when their stdout is a pipe (`docker compose`,
// `npm`, lipgloss-based CLIs, …). Without these vars most tools auto-disable
// colours and the captured failure / always_show_output dump on stderr ends
// up plain text. The shell echoes each var verbatim; the assertion checks
// every expected key surfaces in the captured output.
func TestWorkflowRunner_Parallel_ForceColorEnv_Exported(t *testing.T) {
	dir := t.TempDir()
	leaf := makeShellLeaf("wf.envprobe",
		`printf 'CLICOLOR_FORCE=%s\nFORCE_COLOR=%s\nCOLORTERM=%s\n' "$CLICOLOR_FORCE" "$FORCE_COLOR" "$COLORTERM"`)
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.env",
		Group:     "wf",
		LocalName: "env",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				AlwaysShowOutput: true,
				Steps: []WorkflowStep{
					{Command: "wf.envprobe"},
					{Command: "wf.envprobe"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(wf, leaf)
	_, errOut, err := runParallelWorkflowCtx(t, dir, reg, wf)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr:\n%s", err, errOut)
	}
	for _, want := range []string{
		"CLICOLOR_FORCE=1",
		"FORCE_COLOR=1",
		"COLORTERM=truecolor",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("expected %q in parallel sub-step output (always_show_output dump); got:\n%s", want, errOut)
		}
	}
}

// TestWorkflowRunner_Parallel_FailureDumpPreservesANSI verifies that ANSI
// escape sequences emitted by a failing sub-step survive in the captured
// failure dump (the live row strips ANSI, but the dump on stderr keeps it
// so the user sees the child's colours).
func TestWorkflowRunner_Parallel_FailureDumpPreservesANSI(t *testing.T) {
	dir := t.TempDir()
	// printf the SGR red sequence + text + reset, terminated with \n so it
	// commits as a final frame. Then exit non-zero to trigger the dump.
	colored := makeShellLeaf("wf.col", `printf '\033[31mRED-TEXT\033[0m\n'; exit 1`)
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.ansi",
		Group:     "wf",
		LocalName: "ansi",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				FailFast: ffFalse,
				Steps: []WorkflowStep{
					{Command: "wf.col"},
					{Command: "wf.col"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(wf, colored)
	_, errOut, err := runParallelWorkflowCtx(t, dir, reg, wf)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(errOut, "\x1b[31mRED-TEXT\x1b[0m") {
		t.Errorf("expected raw ANSI sequence preserved in dump; got:\n%q", errOut)
	}
}

// TestWorkflowRunner_Parallel_AlwaysShowOutput verifies that the new
// always_show_output flag dumps every sub-step's output between separator
// bars even when the sub-step succeeded.
func TestWorkflowRunner_Parallel_AlwaysShowOutput(t *testing.T) {
	dir := t.TempDir()
	a := makeShellLeaf("wf.ok-a", `echo greeting-a`)
	b := makeShellLeaf("wf.ok-b", `echo greeting-b`)

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.always",
		Group:     "wf",
		LocalName: "always",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				AlwaysShowOutput: true,
				Steps: []WorkflowStep{
					{Command: "wf.ok-a"},
					{Command: "wf.ok-b"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(wf, a, b)
	_, errOut, err := runParallelWorkflowCtx(t, dir, reg, wf)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr:\n%s", err, errOut)
	}
	for _, want := range []string{
		"───── output: wf.ok-a ─────",
		"───── output: wf.ok-b ─────",
		"greeting-a",
		"greeting-b",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("expected %q in stderr; got:\n%s", want, errOut)
		}
	}
}

// TestWorkflowRunner_Parallel_AlwaysShowOutput_DefaultsToFailureOnly verifies
// that with the default (always_show_output: false) successful sub-step
// output is NOT dumped — only failures continue to be shown.
func TestWorkflowRunner_Parallel_AlwaysShowOutput_DefaultsToFailureOnly(t *testing.T) {
	dir := t.TempDir()
	a := makeShellLeaf("wf.qa", `echo quiet-a`)
	b := makeShellLeaf("wf.qb", `echo quiet-b`)

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.quiet",
		Group:     "wf",
		LocalName: "quiet",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				Steps: []WorkflowStep{
					{Command: "wf.qa"},
					{Command: "wf.qb"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(wf, a, b)
	_, errOut, err := runParallelWorkflowCtx(t, dir, reg, wf)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr:\n%s", err, errOut)
	}
	for _, unwanted := range []string{
		"───── output:",
		"quiet-a",
		"quiet-b",
	} {
		if strings.Contains(errOut, unwanted) {
			t.Errorf("did not expect %q in stderr (default should hide success output); got:\n%s", unwanted, errOut)
		}
	}
}
