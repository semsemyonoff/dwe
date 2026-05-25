package linters

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync/atomic"
	"time"

	"devbox-cli/internal/config"
	"devbox-cli/internal/validate"
)

// DefaultLinterTimeout caps the wall-clock duration of a single linter
// invocation. Tests override this via withTestTimeout to keep timeout-firing
// cases fast and deterministic. It is exported (capitalized) so test helpers
// in the same package can swap it without unsafe gymnastics; users have no
// configuration surface for it.
var DefaultLinterTimeout = 5 * time.Minute

// MaxLinterOutputBytes caps the combined stdout+stderr per linter invocation.
// Each stream is independently bounded to MaxLinterOutputBytes / 2 via
// boundedWriter; bytes past the cap are silently dropped and a Warning is
// emitted. The 50 MB default protects against runaway output (real shellcheck
// runs over a giant repo can emit tens of MB of JSON) without OOMing the CLI.
var MaxLinterOutputBytes int64 = 50 << 20

// linterValidator wraps a configured LinterEntry + Adapter into a
// validate.Validator. One instance per configured linter.
type linterValidator struct {
	entry         config.LinterEntry
	adapter       Adapter
	baseDir       string
	binExplicit   bool // user set bin: in YAML
	pathsExplicit bool // user set paths: in YAML
}

// newLinterValidator constructs a runtime validator from an adapter and the
// per-entry configuration. Defaults are layered in here (not at load time) so
// the canonical wire shape stays minimal — but we record explicit-vs-default
// flags first so the runtime can distinguish "autodetect" from "user said so"
// (e.g. for the missing-bin Warning vs silent-skip decision).
func newLinterValidator(entry config.LinterEntry, adapter Adapter, baseDir string) *linterValidator {
	// Generic entries without an explicit bin: still use an explicit binary
	// (the entry ID), which the user chose by registering the entry — treat
	// that as explicit so a missing binary emits a Warning, not a silent skip.
	binExplicit := entry.Bin != "" || entry.Type == "generic"
	pathsExplicit := len(entry.Paths) > 0
	if entry.Bin == "" {
		entry.Bin = adapter.DefaultBin()
	}
	if len(entry.Paths) == 0 {
		entry.Paths = append([]string(nil), adapter.DefaultPaths()...)
	}
	if len(entry.Extensions) == 0 {
		entry.Extensions = append([]string(nil), adapter.DefaultExtensions()...)
	}
	if len(entry.Filenames) == 0 {
		entry.Filenames = append([]string(nil), adapter.DefaultFilenames()...)
	}
	return &linterValidator{
		entry:         entry,
		adapter:       adapter,
		baseDir:       baseDir,
		binExplicit:   binExplicit,
		pathsExplicit: pathsExplicit,
	}
}

// ID returns the linter's stable identifier (same as the YAML map key).
func (v *linterValidator) ID() string { return v.entry.ID }

// Domain returns the linters diagnostic domain.
func (v *linterValidator) Domain() string { return Domain }

// Run executes the linter and returns diagnostics. The result is the
// concatenation of two buckets — operational diagnostics (about the linter
// invocation itself: missing bin, missing path, timeout, output truncation,
// parse failure, panic) and adapter findings (about the user's code). The
// severity clamp from entry.Severity is applied to findings ONLY, so users
// cannot accidentally silence runtime failure signals via `severity: info`.
func (v *linterValidator) Run(vctx validate.Context) []validate.Diagnostic {
	var operationalDiags []validate.Diagnostic
	var findings []validate.Diagnostic

	// 1. enabled gating. Nil = autodetect (proceed). Explicit false = silent
	// skip. Explicit true = proceed.
	if v.entry.Enabled != nil && !*v.entry.Enabled {
		return nil
	}

	// 2. resolve the binary. Autodetect path silently skips when the default
	// bin is missing; explicit `bin:` configuration produces a Warning so the
	// user knows their config didn't take effect.
	bin := v.entry.Bin
	if _, err := exec.LookPath(bin); err != nil {
		if !v.binExplicit {
			// Autodetected default — silent skip.
			return nil
		}
		return append(operationalDiags, warn(
			v.ID(),
			fmt.Sprintf("%s: bin %q not found on PATH", v.ID(), bin),
			"install the linter or set a different bin: in devbox/validate.yml",
		))
	}

	// 3. collect files. pathsAreDefaults is true when the user did not declare
	// paths: explicitly; missing default paths are silently dropped (the
	// project just doesn't ship that directory). Missing user-declared paths
	// produce a Warning.
	pathsAreDefaults := !v.pathsExplicit
	files, missing, err := collectFiles(
		v.baseDir,
		v.entry.Paths,
		v.entry.Extensions,
		v.entry.Filenames,
		pathsAreDefaults,
	)
	if err != nil {
		return append(operationalDiags, fail(
			v.ID(),
			fmt.Sprintf("%s: walk failed: %v", v.ID(), err),
			"",
		))
	}
	for _, m := range missing {
		operationalDiags = append(operationalDiags, warn(
			v.ID(),
			fmt.Sprintf("%s: configured path %q does not exist", v.ID(), m),
			"remove the entry from paths: or create the directory",
		))
	}
	if len(files) == 0 {
		// Nothing to lint — surface only the operational diagnostics (which
		// will be empty in the autodetect-with-no-files-found case → silent).
		return operationalDiags
	}

	// 4. exec with timeout. context.WithTimeout panics on nil parent, so fall
	// back to context.Background() — mirrors validate/env/docker.go.
	parent := vctx.Ctx
	if parent == nil {
		parent = context.Background()
	}
	runCtx, cancel := context.WithTimeout(parent, DefaultLinterTimeout)
	defer cancel()

	argv := v.adapter.BuildArgs(files, v.entry.Flags)
	cmd := exec.CommandContext(runCtx, bin, argv...)

	// Bound each stream independently so a runaway linter can't OOM us. The
	// split (half-and-half) keeps stderr useful when a parser fails — we don't
	// want a megabyte of stdout to crowd out a one-line stderr error message.
	streamCap := MaxLinterOutputBytes / 2
	stdoutBuf := newBoundedWriter(streamCap)
	stderrBuf := newBoundedWriter(streamCap)
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	runErr := cmd.Run()
	exitCode := 0
	if runErr != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	// 5. context errors take precedence — partial output is not worth parsing.
	// Guard on runErr: if the process exits cleanly at the exact moment a
	// deadline fires, runCtx.Err() can be non-nil even though the run succeeded.
	if runErr != nil && runCtx.Err() != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return append(operationalDiags, fail(
				v.ID(),
				fmt.Sprintf("%s timed out after %s", v.ID(), DefaultLinterTimeout),
				"narrow paths: in devbox/validate.yml to reduce the number of files scanned",
			))
		}
		// Parent context cancelled (e.g. Ctrl-C) — not a linter failure.
		return operationalDiags
	}

	// 6. truncation Warning — emitted before parse so the user knows the
	// findings might be incomplete.
	if stdoutBuf.Truncated() || stderrBuf.Truncated() {
		operationalDiags = append(operationalDiags, warn(
			v.ID(),
			fmt.Sprintf("%s: output truncated at %d bytes per stream", v.ID(), streamCap),
			"the parsed findings may be incomplete",
		))
	}

	// 7. delegate to adapter parse.
	parsed, parseErr := v.adapter.ParseOutput(stdoutBuf.Bytes(), stderrBuf.Bytes(), exitCode)
	findings = append(findings, parsed...)
	if parseErr != nil {
		operationalDiags = append(operationalDiags, fail(
			v.ID(),
			fmt.Sprintf("%s: failed to parse output: %v", v.ID(), parseErr),
			"",
		))
	}

	// 8. severity clamp applies to findings only — operational diagnostics
	// (timeouts, parse failures, missing-bin warnings) are never muted.
	if v.entry.Severity != nil {
		max := *v.entry.Severity
		for i := range findings {
			if findings[i].Severity > max {
				findings[i].Severity = max
			}
		}
	}

	// 9. defensive stamping in case adapter omitted Target/Domain.
	for i := range findings {
		if findings[i].Domain == "" {
			findings[i].Domain = Domain
		}
		if findings[i].Target == "" {
			findings[i].Target = v.ID()
		}
	}
	for i := range operationalDiags {
		if operationalDiags[i].Domain == "" {
			operationalDiags[i].Domain = Domain
		}
		if operationalDiags[i].Target == "" {
			operationalDiags[i].Target = v.ID()
		}
	}

	return append(operationalDiags, findings...)
}

// boundedWriter is an io.Writer with a hard byte cap. Writes past the cap are
// silently dropped and a flag flips so the caller can surface a Warning.
type boundedWriter struct {
	buf       []byte
	cap       int64
	written   int64
	truncated atomic.Bool
}

func newBoundedWriter(cap int64) *boundedWriter {
	if cap < 0 {
		cap = 0
	}
	return &boundedWriter{cap: cap}
}

// Write appends up to cap-written bytes from p; any remainder is dropped and
// truncated is set. Always returns len(p) and nil so the subprocess pipe pump
// keeps draining instead of stalling on a short write.
func (w *boundedWriter) Write(p []byte) (int, error) {
	remaining := w.cap - w.written
	if remaining <= 0 {
		w.truncated.Store(true)
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		w.buf = append(w.buf, p[:remaining]...)
		w.written += remaining
		w.truncated.Store(true)
		return len(p), nil
	}
	w.buf = append(w.buf, p...)
	w.written += int64(len(p))
	return len(p), nil
}

// Bytes returns the captured (possibly-truncated) byte slice.
func (w *boundedWriter) Bytes() []byte { return w.buf }

// Truncated reports whether any bytes were dropped.
func (w *boundedWriter) Truncated() bool { return w.truncated.Load() }
