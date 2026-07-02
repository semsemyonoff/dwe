package tui

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
)

// ttyOpts returns RunOptions whose capability seams report a usable terminal of
// the given size, so a test exercises the launch path without a real terminal.
func ttyOpts(w, h int) RunOptions {
	return RunOptions{
		isTTY: func() bool { return true },
		size:  func() (int, int, error) { return w, h, nil },
	}
}

// stubRunProgram swaps runProgram for a stub returning model/err and restores it
// on cleanup. It records whether the seam was invoked and captures the model so
// tests can inspect the frame's envelope.
func stubRunProgram(t *testing.T, runErr error) *runProbe {
	t.Helper()
	orig := runProgram
	t.Cleanup(func() { runProgram = orig })
	probe := &runProbe{}
	runProgram = func(m tea.Model, _ *tea.Program) (tea.Model, error) {
		probe.called = true
		probe.model = m
		return m, runErr
	}
	return probe
}

type runProbe struct {
	called bool
	model  tea.Model
}

// TestRun_NonTTY asserts the non-TTY path returns ErrNotTTY before any program
// start and never calls plugin.Init.
func TestRun_NonTTY(t *testing.T) {
	probe := stubRunProgram(t, nil)
	p := newStubPlugin()

	_, err := Run(p, RunOptions{isTTY: func() bool { return false }})
	if !errors.Is(err, ErrNotTTY) {
		t.Errorf("err = %v; want ErrNotTTY", err)
	}
	if probe.called {
		t.Error("runProgram was invoked on the non-TTY path")
	}
	if p.initCalled {
		t.Error("plugin.Init ran on the non-TTY path")
	}
	if p.closeCalled {
		t.Error("plugin.Close ran though the plugin never started")
	}
}

// TestRun_TooNarrow asserts the narrow path returns the ErrTooNarrow fallback
// sentinel and never starts a program.
func TestRun_TooNarrow(t *testing.T) {
	probe := stubRunProgram(t, nil)
	p := newStubPlugin()

	_, err := Run(p, ttyOpts(minWidth-1, minHeight-1))
	if !errors.Is(err, ErrTooNarrow) {
		t.Errorf("err = %v; want ErrTooNarrow", err)
	}
	if probe.called {
		t.Error("runProgram was invoked on the narrow path")
	}
}

// TestRun_SizeError wraps a terminal-size read failure and never launches.
func TestRun_SizeError(t *testing.T) {
	probe := stubRunProgram(t, nil)
	sentinel := errors.New("size boom")

	_, err := Run(newStubPlugin(), RunOptions{
		isTTY: func() bool { return true },
		size:  func() (int, int, error) { return 0, 0, sentinel },
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v; want wrapped size error", err)
	}
	if probe.called {
		t.Error("runProgram was invoked after a size-read failure")
	}
}

// TestRun_ConstructionError asserts a plugin contract violation surfaces from
// Run before the program starts.
func TestRun_ConstructionError(t *testing.T) {
	probe := stubRunProgram(t, nil)
	p := dupKeyPlugin{newStubPlugin()}

	_, err := Run(p, ttyOpts(100, 40))
	if err == nil {
		t.Error("Run accepted a plugin registering a duplicate key")
	}
	if probe.called {
		t.Error("runProgram was invoked despite a construction error")
	}
}

// TestRun_Result asserts a clean run returns the plugin's typed Result unchanged.
func TestRun_Result(t *testing.T) {
	stubRunProgram(t, nil)
	p := newStubPlugin()
	p.result = stubResult{Selected: "alpha"}

	got, err := Run(p, ttyOpts(100, 40))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	res, ok := got.(stubResult)
	if !ok {
		t.Fatalf("Result type = %T; want stubResult", got)
	}
	if res.Selected != "alpha" {
		t.Errorf("Result = %+v; want Selected=alpha", res)
	}
	if !p.closeCalled {
		t.Error("plugin.Close did not run on the normal-quit path")
	}
}

// TestRun_ProgramError asserts a program error is wrapped and wins over a Close
// error (close-error precedence).
func TestRun_ProgramError(t *testing.T) {
	progErr := errors.New("loop boom")
	stubRunProgram(t, progErr)
	p := newStubPlugin()
	p.closeErr = errors.New("close boom")

	_, err := Run(p, ttyOpts(100, 40))
	if !errors.Is(err, progErr) {
		t.Errorf("err = %v; want wrapped program error", err)
	}
	if errors.Is(err, p.closeErr) {
		t.Error("Close error leaked though the program already errored")
	}
	if !p.closeCalled {
		t.Error("plugin.Close did not run on the error path")
	}
}

// TestRun_InterruptIsCleanCancel asserts a user-initiated interrupt
// (tea.ErrInterrupted) maps to the clean widgets.ErrCancelled sentinel, not a
// fatal wrapped program error.
func TestRun_InterruptIsCleanCancel(t *testing.T) {
	stubRunProgram(t, tea.ErrInterrupted)
	p := newStubPlugin()

	_, err := Run(p, ttyOpts(100, 40))
	if !errors.Is(err, widgets.ErrCancelled) {
		t.Errorf("err = %v; want widgets.ErrCancelled", err)
	}
	if errors.Is(err, tea.ErrInterrupted) {
		t.Error("interrupt leaked as a fatal program error")
	}
	if !p.closeCalled {
		t.Error("plugin.Close did not run on the interrupt path")
	}
}

// TestRun_KilledIsCleanCancel asserts a context kill (tea.ErrProgramKilled) maps
// to the clean widgets.ErrCancelled sentinel.
func TestRun_KilledIsCleanCancel(t *testing.T) {
	stubRunProgram(t, tea.ErrProgramKilled)

	_, err := Run(newStubPlugin(), ttyOpts(100, 40))
	if !errors.Is(err, widgets.ErrCancelled) {
		t.Errorf("err = %v; want widgets.ErrCancelled", err)
	}
}

// TestRun_PanicSurfaces asserts a recovered panic (tea.ErrProgramPanic) is
// surfaced as a fatal error, NOT swallowed as a clean cancel — even though v2
// wraps it as `ErrProgramKilled: ErrProgramPanic`.
func TestRun_PanicSurfaces(t *testing.T) {
	// Mirror v2's wrapping so the ErrProgramKilled branch must not win.
	panicErr := fmt.Errorf("%w: %w", tea.ErrProgramKilled, tea.ErrProgramPanic)
	stubRunProgram(t, panicErr)

	_, err := Run(newStubPlugin(), ttyOpts(100, 40))
	if !errors.Is(err, tea.ErrProgramPanic) {
		t.Errorf("err = %v; want wrapped ErrProgramPanic", err)
	}
	if errors.Is(err, widgets.ErrCancelled) {
		t.Error("panic was swallowed as a clean cancel")
	}
}

// TestRun_CloseErrorPrecedence asserts a Close error surfaces ONLY when the
// program returned no error.
func TestRun_CloseErrorPrecedence(t *testing.T) {
	stubRunProgram(t, nil)
	p := newStubPlugin()
	p.closeErr = errors.New("close boom")

	_, err := Run(p, ttyOpts(100, 40))
	if !errors.Is(err, p.closeErr) {
		t.Errorf("err = %v; want the Close error to surface on a clean program exit", err)
	}
}

// TestRun_MouseFlagReachesFrame asserts RunOptions.Mouse threads into the frame
// (via frameOptions) and View emits CellMotion when mouse=true on a capable
// terminal. We pin TERM=xterm-256color so the capability gate passes on CI
// regardless of the host environment; the TERM=dumb path is covered by the
// frame-level TestFrame_MouseCapabilityGate.
func TestRun_MouseFlagReachesFrame(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	for _, mouse := range []bool{false, true} {
		probe := stubRunProgram(t, nil)
		_, err := Run(newStubPlugin(), RunOptions{
			Mouse: mouse,
			isTTY: func() bool { return true },
			size:  func() (int, int, error) { return 100, 40, nil },
		})
		if err != nil {
			t.Fatalf("mouse=%v: Run: %v", mouse, err)
		}
		f, ok := probe.model.(*Frame)
		if !ok {
			t.Fatalf("mouse=%v: model type = %T; want *Frame", mouse, probe.model)
		}
		if f.opts.mouse != mouse {
			t.Errorf("mouse=%v: frame.opts.mouse = %v; want %v", mouse, f.opts.mouse, mouse)
		}
		var wantMode tea.MouseMode
		if mouse {
			wantMode = tea.MouseModeCellMotion
		}
		if got := f.View().MouseMode; got != wantMode {
			t.Errorf("mouse=%v: MouseMode = %v; want %v", mouse, got, wantMode)
		}
	}
}

// TestBuildProgramOptions asserts the always-present wheel-coalescer filter is
// the only base option (so input is never disabled via WithInput(nil)), and each
// non-nil stdio seam adds exactly one more option.
func TestBuildProgramOptions(t *testing.T) {
	// Base: just the WithFilter wheel coalescer.
	if got := buildProgramOptions(RunOptions{}); len(got) != 1 {
		t.Errorf("zero-value options len = %d; want 1 (WithFilter only, no WithInput(nil))", len(got))
	}
	withIn := buildProgramOptions(RunOptions{input: &bytes.Buffer{}})
	if len(withIn) != 2 {
		t.Errorf("input-only options len = %d; want 2 (filter + input)", len(withIn))
	}
	both := buildProgramOptions(RunOptions{input: &bytes.Buffer{}, output: &bytes.Buffer{}})
	if len(both) != 3 {
		t.Errorf("input+output options len = %d; want 3 (filter + input + output)", len(both))
	}
}
