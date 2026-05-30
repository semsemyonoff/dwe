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

	"github.com/creack/pty"

	"devbox-cli/internal/core/usercommands/model"
	"devbox-cli/internal/core/usercommands/resolve"
	"devbox-cli/internal/core/usercommands/runtime/spec"
	"devbox-cli/internal/shared/tpl"
)

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
