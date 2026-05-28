package runtime

import (
	"io"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

// parallelChildIO conditionally allocates a PTY for a parallel sub-step's
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
func parallelChildIO(rc RunContext, c *exec.Cmd, stdoutSink io.Writer) (used bool, cleanup func()) {
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
