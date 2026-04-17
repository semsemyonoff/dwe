package pipeline

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
)

// newBufReporter returns a PlainReporter backed by a buffer for assertions.
func newBufReporter() (*PlainReporter, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	w := render.NewWriter(buf)
	return NewPlainReporter(w), buf
}

// stripANSI removes ANSI escape sequences from s.
func stripANSI(s string) string {
	out := s
	for {
		start := strings.Index(out, "\033[")
		if start == -1 {
			break
		}
		end := strings.Index(out[start:], "m")
		if end == -1 {
			break
		}
		out = out[:start] + out[start+end+1:]
	}
	return out
}

// lines splits s into non-empty trimmed lines for readable assertions.
func lines(s string) []string {
	var result []string
	for l := range strings.SplitSeq(s, "\n") {
		if l != "" {
			result = append(result, l)
		}
	}
	return result
}

// --- StartPipeline ---

func TestPlainReporter_StartPipeline_NoOutput(t *testing.T) {
	r, buf := newBufReporter()
	r.StartPipeline("deploy", 5)
	if buf.Len() != 0 {
		t.Errorf("StartPipeline should produce no output, got: %q", buf.String())
	}
}

// --- EnterPhase ---

func TestPlainReporter_EnterPhase_WithDescription(t *testing.T) {
	r, buf := newBufReporter()
	phase := config.DeployPhase{Name: "init", Description: "Initialization"}
	r.EnterPhase("init", phase)
	got := stripANSI(buf.String())
	want := "Phase: init: Initialization\n"
	if got != want {
		t.Errorf("EnterPhase with description: got %q, want %q", got, want)
	}
}

func TestPlainReporter_EnterPhase_NoDescription(t *testing.T) {
	r, buf := newBufReporter()
	phase := config.DeployPhase{Name: "deploy"}
	r.EnterPhase("deploy", phase)
	got := stripANSI(buf.String())
	want := "Phase: deploy\n"
	if got != want {
		t.Errorf("EnterPhase no description: got %q, want %q", got, want)
	}
}

func TestPlainReporter_EnterPhase_ServicePrefix(t *testing.T) {
	r, buf := newBufReporter()
	phase := config.DeployPhase{Name: "setup", Description: "Service setup"}
	r.EnterPhase("main/setup", phase)
	got := stripANSI(buf.String())
	want := "Phase: main/setup: Service setup\n"
	if got != want {
		t.Errorf("EnterPhase with service prefix: got %q, want %q", got, want)
	}
}

// --- SkipPhase ---

func TestPlainReporter_SkipPhase(t *testing.T) {
	r, buf := newBufReporter()
	phase := config.DeployPhase{Name: "deploy"}
	r.SkipPhase("deploy", phase, "when: dir-empty services/main")
	got := stripANSI(buf.String())
	want := "  Skipping phase deploy (when: dir-empty services/main)\n"
	if got != want {
		t.Errorf("SkipPhase: got %q, want %q", got, want)
	}
}

// --- StartStep ---

func TestPlainReporter_StartStep_WithDescription(t *testing.T) {
	r, buf := newBufReporter()
	step := config.DeployStep{Name: "render-env", Description: "Generate .env from config"}
	r.StartStep("init/render-env", step, 1, 5)
	got := stripANSI(buf.String())
	want := "  [1/5] init/render-env: Generate .env from config\n"
	if got != want {
		t.Errorf("StartStep with description: got %q, want %q", got, want)
	}
}

func TestPlainReporter_StartStep_NoDescription(t *testing.T) {
	r, buf := newBufReporter()
	step := config.DeployStep{Name: "migrate"}
	r.StartStep("main/setup/migrate", step, 3, 7)
	got := stripANSI(buf.String())
	want := "  [3/7] main/setup/migrate\n"
	if got != want {
		t.Errorf("StartStep no description: got %q, want %q", got, want)
	}
}

// --- SkipStep ---

func TestPlainReporter_SkipStep_WhenCondition(t *testing.T) {
	r, buf := newBufReporter()
	step := config.DeployStep{Name: "migrate"}
	r.SkipStep("init/migrate", step, 2, 4, "when: dir-empty services/main/src")
	got := stripANSI(buf.String())
	want := "  [2/4] Skipped: init/migrate (when: dir-empty services/main/src)\n"
	if got != want {
		t.Errorf("SkipStep: got %q, want %q", got, want)
	}
}

func TestPlainReporter_SkipStep_PhaseWhenCondition(t *testing.T) {
	r, buf := newBufReporter()
	step := config.DeployStep{Name: "key-gen"}
	r.SkipStep("main/setup/key-gen", step, 3, 5, "phase when: cmd: check")
	got := stripANSI(buf.String())
	want := "  [3/5] Skipped: main/setup/key-gen (phase when: cmd: check)\n"
	if got != want {
		t.Errorf("SkipStep with phase when: got %q, want %q", got, want)
	}
}

// --- FinishStep ---

func TestPlainReporter_FinishStep(t *testing.T) {
	r, buf := newBufReporter()
	step := config.DeployStep{Name: "render-env"}
	r.FinishStep("init/render-env", step, 1, 5)
	got := stripANSI(buf.String())
	want := "  [1/5] Done: init/render-env\n"
	if got != want {
		t.Errorf("FinishStep: got %q, want %q", got, want)
	}
}

// --- FailStep ---

func TestPlainReporter_FailStep(t *testing.T) {
	r, buf := newBufReporter()
	r.StartPipeline("deploy", 7)
	step := config.DeployStep{Name: "migrate"}
	r.FailStep("main/setup/migrate", step, 4, 7, errors.New("exit status 1"))
	got := stripANSI(buf.String())
	wantLines := []string{
		`Deploy failed at step "main/setup/migrate"`,
		"  exit status 1",
	}
	gotLines := lines(got)
	if len(gotLines) != len(wantLines) {
		t.Fatalf("FailStep: got %d lines, want %d; output: %q", len(gotLines), len(wantLines), got)
	}
	for i, want := range wantLines {
		if gotLines[i] != want {
			t.Errorf("FailStep line %d: got %q, want %q", i, gotLines[i], want)
		}
	}
}

func TestPlainReporter_FailStep_Reset(t *testing.T) {
	r, buf := newBufReporter()
	r.StartPipeline("reset", 3)
	step := config.DeployStep{Name: "stop"}
	r.FailStep("cleanup/stop", step, 2, 3, errors.New("exit status 2"))
	got := stripANSI(buf.String())
	wantLines := []string{
		`Reset failed at step "cleanup/stop"`,
		"  exit status 2",
	}
	gotLines := lines(got)
	if len(gotLines) != len(wantLines) {
		t.Fatalf("FailStep reset: got %d lines, want %d; output: %q", len(gotLines), len(wantLines), got)
	}
	for i, want := range wantLines {
		if gotLines[i] != want {
			t.Errorf("FailStep reset line %d: got %q, want %q", i, gotLines[i], want)
		}
	}
}

func TestPlainReporter_FailStep_NoStartPipeline(t *testing.T) {
	r, buf := newBufReporter()
	step := config.DeployStep{Name: "migrate"}
	r.FailStep("init/migrate", step, 1, 1, errors.New("timeout"))
	got := stripANSI(buf.String())
	wantLines := []string{
		`Pipeline failed at step "init/migrate"`,
		"  timeout",
	}
	gotLines := lines(got)
	if len(gotLines) != len(wantLines) {
		t.Fatalf("FailStep no name: got %d lines, want %d; output: %q", len(gotLines), len(wantLines), got)
	}
	for i, want := range wantLines {
		if gotLines[i] != want {
			t.Errorf("FailStep no name line %d: got %q, want %q", i, gotLines[i], want)
		}
	}
}

// --- FinishPipeline ---

func TestPlainReporter_FinishPipeline_NoOutput(t *testing.T) {
	r, buf := newBufReporter()
	r.FinishPipeline(true)
	if buf.Len() != 0 {
		t.Errorf("FinishPipeline(true) should produce no output, got: %q", buf.String())
	}
}

func TestPlainReporter_FinishPipelineFailure_NoOutput(t *testing.T) {
	r, buf := newBufReporter()
	r.FinishPipeline(false)
	if buf.Len() != 0 {
		t.Errorf("FinishPipeline(false) should produce no output, got: %q", buf.String())
	}
}

// --- SuspendForExec / ResumeAfterExec ---

func TestPlainReporter_SuspendResumeNoOps(t *testing.T) {
	r, buf := newBufReporter()
	r.SuspendForExec()
	r.ResumeAfterExec()
	if buf.Len() != 0 {
		t.Errorf("SuspendForExec/ResumeAfterExec should produce no output, got: %q", buf.String())
	}
}

// --- Full event sequence ---

func TestPlainReporter_FullEventSequence(t *testing.T) {
	r, buf := newBufReporter()

	phase1 := config.DeployPhase{Name: "env", Description: "Environment"}
	phase2 := config.DeployPhase{Name: "setup"}
	phase3 := config.DeployPhase{Name: "post-deploy"}

	step1 := config.DeployStep{Name: "render-env", Description: "Generate .env from config"}
	step2 := config.DeployStep{Name: "dirs-ensure"}
	step3 := config.DeployStep{Name: "migrate"}
	step4 := config.DeployStep{Name: "success"}

	r.StartPipeline("deploy", 4)

	r.EnterPhase("env", phase1)
	r.StartStep("env/render-env", step1, 1, 4)
	r.SuspendForExec()
	r.ResumeAfterExec()
	r.FinishStep("env/render-env", step1, 1, 4)

	r.EnterPhase("main/setup", phase2)
	r.StartStep("main/setup/dirs-ensure", step2, 2, 4)
	r.FinishStep("main/setup/dirs-ensure", step2, 2, 4)
	r.StartStep("main/setup/migrate", step3, 3, 4)
	r.SkipStep("main/setup/migrate", step3, 3, 4, "when: dir-not-empty services/main/src")

	r.EnterPhase("post-deploy", phase3)
	r.StartStep("post-deploy/success", step4, 4, 4)
	r.FinishStep("post-deploy/success", step4, 4, 4)

	r.FinishPipeline(true)

	got := stripANSI(buf.String())
	wantLines := []string{
		"Phase: env: Environment",
		"  [1/4] env/render-env: Generate .env from config",
		"  [1/4] Done: env/render-env",
		"Phase: main/setup",
		"  [2/4] main/setup/dirs-ensure",
		"  [2/4] Done: main/setup/dirs-ensure",
		"  [3/4] main/setup/migrate",
		"  [3/4] Skipped: main/setup/migrate (when: dir-not-empty services/main/src)",
		"Phase: post-deploy",
		"  [4/4] post-deploy/success",
		"  [4/4] Done: post-deploy/success",
	}
	gotLines := lines(got)
	if len(gotLines) != len(wantLines) {
		t.Fatalf("FullEventSequence: got %d lines, want %d\ngot:\n%s\nwant:\n%s",
			len(gotLines), len(wantLines),
			strings.Join(gotLines, "\n"),
			strings.Join(wantLines, "\n"),
		)
	}
	for i, want := range wantLines {
		if gotLines[i] != want {
			t.Errorf("line %d: got %q, want %q", i, gotLines[i], want)
		}
	}
}

// --- Interface compliance ---

// Verify PlainReporter satisfies the Reporter interface at compile time.
var _ Reporter = (*PlainReporter)(nil)
