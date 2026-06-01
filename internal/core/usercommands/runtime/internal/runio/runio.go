// Package runio holds I/O helpers shared by every runner subpackage:
// stdout/stderr/stdin defaulting, parallel-sub-step PTY allocation, and the
// rendered env builder. Living under internal/ keeps these helpers usable
// only from within runtime/ and its subpackages.
package runio

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"

	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/resolve"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/spec"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// ChildTermDelay is the grace period exec.CommandContext gives a child after
// SIGTERM before sending SIGKILL when ctx is cancelled. Exported so runner
// subpackages can configure it on their constructed *exec.Cmd via BindCancel.
const ChildTermDelay = 5 * time.Second

// BindCancel configures cmd to send SIGTERM (instead of the default SIGKILL)
// when its context is cancelled, and to force-kill after ChildTermDelay.
// Call this immediately after exec.CommandContext to give children a chance
// to clean up.
func BindCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = ChildTermDelay
}

// ParallelColorForceEnv returns env entries that coerce common CLI tools to
// keep ANSI colours when the child's stdout is a pipe rather than a TTY.
// Inside a workflow parallel sub-step each child writes through a LineTee
// (no PTY is allocated — concurrent sub-steps cannot share one), so without
// these vars tools like lipgloss, npm/yarn, jest, chalk-based tools, BSD
// ls, brew, and others auto-disable colours and the captured failure /
// always_show_output dump on stderr ends up plain text.
//
// Returns nil outside parallel so non-parallel runs keep the existing
// auto-detection behaviour.
func ParallelColorForceEnv(rc spec.RunContext) []string {
	if !rc.UnderParallel {
		return nil
	}
	if os.Getenv("NO_COLOR") != "" {
		return nil
	}
	return []string{
		"CLICOLOR_FORCE=1",    // BSD ls, brew, lipgloss
		"FORCE_COLOR=1",       // Node ecosystem (npm/yarn/jest/eslint/chalk)
		"COLORTERM=truecolor", // anything that key-checks COLORTERM
	}
}

// IsNonInteractive returns true when the DEVBOX_NONINTERACTIVE environment
// variable is set to "1" or "true". Hoisted to runio so every runner
// subpackage can consult it without round-tripping through the workflow
// package, which would create a runner→workflow import cycle for the
// type=builtin runner that also gates on this signal.
func IsNonInteractive() bool {
	v := os.Getenv("DEVBOX_NONINTERACTIVE")
	return v == "1" || v == "true"
}

// StdoutOf returns the writer to use for stdout, defaulting to os.Stdout.
func StdoutOf(ctx spec.RunContext) io.Writer {
	if ctx.Stdout != nil {
		return ctx.Stdout
	}
	return os.Stdout
}

// StderrOf returns the writer to use for stderr, defaulting to os.Stderr.
func StderrOf(ctx spec.RunContext) io.Writer {
	if ctx.Stderr != nil {
		return ctx.Stderr
	}
	return os.Stderr
}

// StdinOrOS returns ctx.Stdin if set, otherwise os.Stdin.
func StdinOrOS(ctx spec.RunContext) io.Reader {
	if ctx.Stdin != nil {
		return ctx.Stdin
	}
	return os.Stdin
}

// ParallelChildIO conditionally allocates a PTY for a parallel sub-step's
// child process so tools that key colour output off `isatty(STDOUT)` — Pest,
// PHPUnit/Symfony Console, ripgrep, fzf, lipgloss, … — keep emitting ANSI
// codes even though the captured output is consumed by a LineTee rather than
// a real terminal. Env-only forcing (CLICOLOR_FORCE/FORCE_COLOR/COLORTERM)
// covers the env-aware fraction; the PTY closes the gap for everything else.
//
// Behaviour:
//   - rc.UnderParallel == false → no-op; caller wires stdout/stderr directly
//     and the returned cleanup is a no-op. (Returns false.)
//   - pty.Open() fails → no-op; caller falls back to direct wiring so the
//     sub-step still runs without colours.
//   - success → c.Stdin / c.Stdout / c.Stderr are pointed at the PTY slave,
//     a goroutine copies the master FD into stdoutSink (typically the
//     workflow's LineTee), and cleanup closes both ends after waiting for
//     the goroutine to drain.
//
// stdinSrc is intentionally ignored when a PTY is in use: parallel sub-steps
// must not read from a shared stdin (the workflow runner already pins it to
// an empty reader), and writing to the master side is reserved for future
// "send keys" features that do not exist yet.
//
// Returns (used, cleanup). When used==false the cleanup is a no-op and the
// caller must wire stdin/stdout/stderr itself; when used==true the runner
// must NOT overwrite c.Stdin / c.Stdout / c.Stderr afterwards.
func ParallelChildIO(rc spec.RunContext, c *exec.Cmd, stdoutSink io.Writer) (used bool, cleanup func()) {
	if !rc.UnderParallel || stdoutSink == nil {
		return false, func() {}
	}
	ptmx, tty, err := pty.Open()
	if err != nil {
		return false, func() {}
	}
	c.Stdin = tty
	c.Stdout = tty
	c.Stderr = tty

	done := make(chan struct{})
	go func() {
		defer close(done)
		// io.Copy returns when ptmx hits EOF (after tty is closed and the
		// kernel has flushed buffered output). Errors are expected on close
		// and are intentionally ignored.
		_, _ = io.Copy(stdoutSink, ptmx)
	}()

	var once sync.Once
	cleanup = func() {
		once.Do(func() {
			// Close the slave first so the master sees EOF and the copy
			// goroutine exits. Closing the master before the slave can race
			// the kernel buffer flush and lose trailing bytes.
			_ = tty.Close()
			<-done
			_ = ptmx.Close()
		})
	}
	return true, cleanup
}

// BuildRenderedEnv renders all env values (which may contain ${...} expressions)
// and returns the final string→string map.
func BuildRenderedEnv(cmd *model.CommandDef, ctx spec.RunContext) (map[string]string, error) {
	files := make(map[string]tpl.ResolvedFile)
	if ctx.Render != nil && ctx.Render.Files != nil {
		files = ctx.Render.Files
	}
	raw, err := resolve.BuildEnv(cmd, ctx.Params, ctx.Context, files)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(raw))
	for k, v := range raw {
		rendered, err := tpl.RenderCommand(v, ctx.Render)
		if err != nil {
			return nil, fmt.Errorf("render env %q: %w", k, err)
		}
		result[k] = rendered
	}
	return result, nil
}
