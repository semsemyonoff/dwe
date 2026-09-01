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

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"

	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/resolve"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/spec"
	"github.com/semsemyonoff/dwe/internal/shared/bridgeclient"
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

// ColorForced reports whether this dwe process was asked to force colored
// output: CLICOLOR_FORCE is truthy and NO_COLOR is absent. The bridge shim
// sets CLICOLOR_FORCE=1 when the container-side terminal is interactive; a
// host user piping dwe can set it by hand. Children of a real terminal
// inherit the tty and need no forcing.
func ColorForced() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	v := os.Getenv("CLICOLOR_FORCE")
	return v != "" && v != "0"
}

// stdoutIsTerminal is the production tty probe; injectable for tests.
var stdoutIsTerminal = func() bool { return term.IsTerminal(os.Stdout.Fd()) }

// isTerminal reports whether an arbitrary stdio stream — an io.Writer or an
// io.Reader — is backed by a terminal. Only an *os.File can be one; anything
// else (a bytes.Buffer, the workflow's LineTee, an io.MultiWriter, nil) is
// not. Injectable for tests.
//
// Deliberately a SECOND seam beside stdoutIsTerminal rather than a
// generalisation of it: stdoutIsTerminal answers for the process's own
// os.Stdout, which is what colorForceActive and bridgedTTYActive mean, while
// this one answers for whichever stream a particular RunContext carries.
var isTerminal = func(stream any) bool {
	f, ok := stream.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(f.Fd())
}

// colorForceActive reports whether child processes need explicit color
// coercion: inside a workflow parallel sub-step (children write through a
// LineTee, never a tty), when this dwe itself runs color-forced on a pipe
// (the host-bridge shape), or when the caller is about to deny the child the
// terminal it would otherwise have inherited. On a real terminal children
// inherit the tty and auto-detect correctly.
//
// forceOnSuppressedTTY is supplied by the caller, not derived here: only the
// service runner can see the effective compose flag vector, and therefore only
// it knows whether it just injected `-T` (and whether the child is detached,
// in which case its output never reaches rc.Stdout at all).
//
// The third disjunct probes the RAW rc.Stdout, deliberately NOT the
// StdoutOf(rc) default that WantContainerTTY resolves through. The asymmetry
// is load-bearing: an internal caller that leaves Stdout nil (or points it at
// io.Discard, as validate/checks/loader.go does) parses the child's output, so
// it must never be handed forced colour just because the process's own
// os.Stdout happens to be a terminal. A later "consistency" cleanup unifying
// the two would inject ANSI escapes into parsed output.
func colorForceActive(rc spec.RunContext, forceOnSuppressedTTY bool) bool {
	return rc.UnderParallel ||
		(ColorForced() && !stdoutIsTerminal()) ||
		(forceOnSuppressedTTY && isTerminal(rc.Stdout))
}

// ColorForceEnv returns env entries that coerce common CLI tools to keep
// ANSI colours when the child's stdout is a pipe rather than a TTY. Without
// these vars tools like lipgloss, npm/yarn, jest, chalk-based tools, BSD
// ls, brew, and others auto-disable colours — inside parallel sub-steps
// (LineTee capture) and over the host bridge alike.
//
// Returns nil when no coercion is needed (real terminal, or NO_COLOR) so
// those runs keep the existing auto-detection behaviour.
//
// forceOnSuppressedTTY lets a caller that is itself taking the child's
// terminal away ask for the coercion; see colorForceActive. Host-side runners
// have no container TTY to suppress and pass false.
func ColorForceEnv(rc spec.RunContext, forceOnSuppressedTTY bool) []string {
	if !colorForceActive(rc, forceOnSuppressedTTY) {
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

// IsNonInteractive returns true when the DWE_NONINTERACTIVE environment
// variable is set to "1" or "true". Hoisted to runio so every runner
// subpackage can consult it without round-tripping through the workflow
// package, which would create a runner→workflow import cycle for the
// type=builtin runner that also gates on this signal.
func IsNonInteractive() bool {
	v := os.Getenv("DWE_NONINTERACTIVE")
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

// bridgedTTYActive reports whether a sequential child should get a full PTY:
// this dwe runs color-forced on pipes (the host-bridge shape) while the
// far-side client sits at a fully interactive terminal — the bridge shim
// sets DWE_BRIDGE_STDIN_TTY=1 only when the container-side stdin is a real
// terminal. The stdin condition is load-bearing: `docker compose exec`
// refuses a TTY-enabled session over a non-terminal stdin, and a PTY wired
// onto piped stdin data (`cat dump.sql | dwe cmd db.import`) would never
// deliver EOF to the child.
func bridgedTTYActive(rc spec.RunContext) bool {
	return !rc.UnderParallel &&
		ColorForced() &&
		!stdoutIsTerminal() &&
		os.Getenv(bridgeclient.EnvBridgeStdinTTY) == "1"
}

// WantContainerTTY reports whether a service command runner should let the
// container process keep a terminal (compose's default) instead of forcing it
// off with `-T`. True only for a run the user launched themselves whose own
// streams are terminals on BOTH ends, or for the bridged-interactive shape.
//
// Why the bridge arm short-circuits the stream probe: over the host bridge
// this dwe's own streams are pipes, and the PTY the child ends up with is
// fabricated by bridgedTTYChildIO inside WireChildIO — that is, AFTER
// BuildCommand has already fixed the argv. Probing the streams at argv-build
// time would answer "pipe" and emit `-T` for a session that is about to be
// handed a real terminal.
//
// Why this is not a plain terminal probe: pipeline.childIO fabricates a PTY
// for a sequential step whenever dwe's own stdout is a terminal and hands it
// to the runner as rc.Stdout/rc.Stderr, so a bare probe answers "yes" in
// exactly the non-interactive case this predicate exists to catch — a
// `type: command` deploy step run from a terminal. Confirmed empirically:
// cells 2 vs 2a of the "TTY matrix — before" table in
// docs/plans/20260901-service-exec-runtime-defaults.md differ only by
// childIO's stdout-tty gate. UserInvoked is what separates the two; the
// pipeline executor and the workflow runner leave it false on the inner
// RunContext they build.
//
// Streams resolve through StdoutOf/StdinOrOS because RunContext.Stdout is a
// nil-able io.Writer and .Stdin a nil-able io.Reader: reading the raw fields
// would answer "not a terminal" for a top-level `dwe cmd` whose caller left
// them unset, which is the one case the predicate exists for.
func WantContainerTTY(rc spec.RunContext) bool {
	if !rc.UserInvoked {
		return false
	}
	return bridgedTTYActive(rc) || (isTerminal(StdoutOf(rc)) && isTerminal(StdinOrOS(rc)))
}

// bridgedTTYChildIO allocates a full PTY (stdin+stdout+stderr) for a
// sequential bridged-interactive child, so isatty-keyed tools — phpcs,
// PHPUnit/Symfony Console, ripgrep, … — and `docker compose exec` (which
// only allocates a container TTY when ITS streams are terminals) behave
// exactly like a host-interactive run. Master output pumps into the
// context stdout; the context stdin pumps into the master so typed input
// still reaches the child. stderr rides the PTY merged into stdout — the
// same thing a real terminal session does.
//
// Returns (false, nil) when inactive or when pty.Open fails (caller falls
// back to direct wiring — uncolored but functional).
func bridgedTTYChildIO(rc spec.RunContext, c *exec.Cmd) (used bool, cleanup func()) {
	if !bridgedTTYActive(rc) {
		return false, nil
	}
	ptmx, tty, err := pty.Open()
	if err != nil {
		return false, nil
	}
	c.Stdin = tty
	c.Stdout = tty
	c.Stderr = tty

	outDone := make(chan struct{})
	go func() {
		defer close(outDone)
		_, _ = io.Copy(StdoutOf(rc), ptmx)
	}()

	stdin := StdinOrOS(rc)
	stdinFile, _ := stdin.(*os.File)
	stdinDone := make(chan struct{})
	go func() {
		defer close(stdinDone)
		// stdin EOF must NOT close the master: the child may still be
		// producing output, and closing its session would SIGHUP it.
		_, _ = io.Copy(ptmx, stdin)
	}()

	var once sync.Once
	cleanup = func() {
		once.Do(func() {
			// Close the slave first so the master drains to EOF (the child
			// has exited and closed its copy by the time cleanup runs).
			_ = tty.Close()
			<-outDone
			if stdinFile != nil && stdinFile.SetReadDeadline(time.Now()) == nil {
				// Deadline-capable stdin: interrupt the parked Read, reap the
				// pump (a pending Read would outlive this step and steal bytes
				// meant for the next one), then clear the deadline for whoever
				// reads stdin next.
				_ = ptmx.Close()
				<-stdinDone
				_ = stdinFile.SetReadDeadline(time.Time{})
				return
			}
			// Deadline-incapable stdin — and that IS the normal bridge shape:
			// the daemon forks this dwe with fd 0 as a plain blocking pipe
			// (exec.Cmd StdinPipe), which Go cannot poll, so SetReadDeadline
			// fails and the parked Read cannot be interrupted. Waiting for the
			// pump here would deadlock the whole session after every child
			// exit (the v1 bridged-command hang). Close the master and abandon
			// the goroutine instead: a late read fails its ptmx write and
			// exits, and process exit reaps a still-parked one. Cost: a
			// keystroke arriving in that window feeds the dying pump instead
			// of the next step — accepted over hanging every bridged command.
			// Non-file stdin (in-memory readers in tests) takes the same path.
			_ = ptmx.Close()
		})
	}
	return true, cleanup
}

// WireChildIO wires c's stdout/stderr/stdin for execution and returns the
// cleanup func the caller must defer. When running as a parallel sub-step it
// allocates a PTY via ParallelChildIO; a sequential bridged-interactive run
// gets a full PTY via bridgedTTYChildIO; otherwise it points c at the
// context's stdout/stderr/stdin defaults and the cleanup is a no-op. Call as
// `defer runio.WireChildIO(rc, c)()` so the wiring happens immediately and
// the teardown runs at function return.
func WireChildIO(rc spec.RunContext, c *exec.Cmd) func() {
	used, cleanup := ParallelChildIO(rc, c, StdoutOf(rc))
	if used {
		return cleanup
	}
	if used, bridgedCleanup := bridgedTTYChildIO(rc, c); used {
		return bridgedCleanup
	}
	c.Stdout = StdoutOf(rc)
	c.Stderr = StderrOf(rc)
	c.Stdin = StdinOrOS(rc)
	return cleanup
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
