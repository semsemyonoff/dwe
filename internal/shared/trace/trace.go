// Package trace is the leaf diagnostic sink for dwe's verbose/debug output.
//
// It is configured once at startup (mirroring slog's global-default pattern)
// and exposes cheap, level-gated emit functions used across the docker/git
// chokepoints and the execution engine. All diagnostic output is destined for
// stderr only; stdout stays untouched so --output json remains a clean
// machine-readable contract.
//
// Output destination is resolved at each emit by a three-level precedence:
//
//  1. a context-carried printer (WithLinePrinter) — set by the executor's
//     parallel path so concurrent sub-steps attribute correctly; overrides all;
//  2. the global active printer (SetPrinter) — set by the pipeline reporter to
//     a printer wrapping the live-view's safe screen path; the safe baseline.
//     Entries are removed by identity, so concurrent pipelines (parallel test
//     scenarios) that register and restore out of order never drop each
//     other's still-active printer;
//  3. the configured fallback writer (Configure) — used outside any pipeline.
//
// When the level is LevelOff every emit returns immediately after one atomic
// load, so a normal run carries zero overhead.
//
// Every emitted line passes through a process-global redactor
// (RegisterRedaction) so decrypted secrets never appear in dwe's own echoes.
// Child-process output is deliberately out of scope.
package trace

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// Level controls which diagnostic emits are active. Levels are ordered:
// higher levels include everything below them.
type Level int32

const (
	// LevelOff disables all diagnostic output (the default).
	LevelOff Level = iota
	// LevelVerbose echoes executed commands and key pipeline decisions.
	LevelVerbose
	// LevelDebug is the structured firehose: everything Verbose shows plus
	// read-only probes, timings, env, and slog Debug records.
	LevelDebug
)

// LinePrinter receives a single fully-formatted diagnostic line (no trailing
// newline). Implementations must be safe for concurrent use.
type LinePrinter interface {
	PrintLine(s string)
}

// printerEntry pairs a registered global printer with a unique id so its
// restore can remove exactly its own entry, even when concurrent callers
// restore out of order.
type printerEntry struct {
	id int64
	p  LinePrinter
}

var (
	level atomic.Int32

	mu         sync.Mutex
	fallback   io.Writer
	printers   []printerEntry // active global printers; last entry is the active one
	printerSeq int64          // monotonic id source for printerEntry
	redactor   *secrets.Redactor
)

type ctxKey struct{}

// Configure sets the fallback writer and active level. It is called once from
// the CLI root. w may be nil (emits to the fallback then become no-ops).
func Configure(w io.Writer, lvl Level) {
	mu.Lock()
	fallback = w
	mu.Unlock()
	level.Store(int32(lvl))
}

func currentLevel() Level { return Level(level.Load()) }

// Enabled reports whether emits at level l are currently active. Use it to
// guard expensive formatting before calling an emit function.
func Enabled(l Level) bool {
	return currentLevel() >= l
}

// RegisterRedaction adds values to the process-global redactor applied to every
// diagnostic line. It is a UNION, never a replacement: config.LoadConfig is the
// single installer and several configs may load in one process (nested loads,
// `dwe test --parallel` scenario copies), so nothing is ever removed and no
// restore-ordering problem exists. Values shorter than secrets.MinRedactRunes
// are dropped — see the Redactor docs.
func RegisterRedaction(values []string) {
	if len(values) == 0 {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	merged := append(redactor.Values(), values...)
	redactor = secrets.NewRedactor(merged)
}

// ResetRedaction clears the redactor. For tests only — a process that has seen
// a plaintext keeps redacting it for its whole life.
func ResetRedaction() {
	mu.Lock()
	redactor = nil
	mu.Unlock()
}

// redact applies the registered redactor to s.
func redact(s string) string {
	mu.Lock()
	r := redactor
	mu.Unlock()
	return r.Redact(s)
}

// Redact replaces every registered secret in s with secrets.RedactPlaceholder.
// It is the same process-global redactor Command applies per argument and emit
// applies per line, installed solely by config.LoadConfig via RegisterRedaction;
// with nothing registered it is the identity function.
//
// It is exported for display code outside the diagnostic path — the pipeline's
// plan/dry-run strings (pipeline.StepCommand, FormatAction, FormatCondition) —
// so a decrypted value cannot reach stdout through a surface trace never sees.
// Values shorter than secrets.MinRedactRunes are never redacted, here or
// anywhere else.
func Redact(s string) string { return redact(s) }

// Command echoes an executed command at Verbose+ as a copy-pasteable line
// prefixed with "$ ".
//
// Each argument is redacted BEFORE FormatCommand quotes it: quoteArg escapes
// an embedded apostrophe by breaking out of the surrounding single quotes, so
// a secret containing one would no longer be a substring of the formatted line
// and the emit-level pass would miss it.
// FormatCommand itself stays untouched, so its quoting goldens do not move.
func Command(ctx context.Context, name string, args ...string) {
	if currentLevel() < LevelVerbose {
		return
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, redact(name))
	for _, a := range args {
		parts = append(parts, redact(a))
	}
	emit(ctx, "$ "+FormatCommand(parts))
}

// Decision emits a pipeline decision (step run/skip + reason, when:/condition
// results, preflight results) at Verbose+.
func Decision(ctx context.Context, format string, a ...any) {
	if currentLevel() < LevelVerbose {
		return
	}
	emit(ctx, fmt.Sprintf(format, a...))
}

// Debugf emits a Debug-only diagnostic line (timings, env, config internals,
// cache hits/misses).
func Debugf(ctx context.Context, format string, a ...any) {
	if currentLevel() < LevelDebug {
		return
	}
	emit(ctx, fmt.Sprintf(format, a...))
}

// emit routes a fully-formatted line through the printer precedence:
// ctx override → global active printer → fallback writer.
func emit(ctx context.Context, line string) {
	// Redact before ANY printer sees the line: this single point covers the
	// -v / --debug echoes and their .dwe/logs mirrors for parallel steps.
	line = redact(line)
	if p := printerFrom(ctx); p != nil {
		p.PrintLine(line)
		return
	}
	mu.Lock()
	if n := len(printers); n > 0 {
		p := printers[n-1].p
		mu.Unlock()
		p.PrintLine(line)
		return
	}
	w := fallback
	mu.Unlock()
	if w != nil {
		_, _ = io.WriteString(w, line+"\n")
	}
}

// SetPrinter registers p as the active global printer and returns a restore
// function that removes exactly p's own entry. Restore is idempotent. Restores
// may run out of order relative to registration: this happens both for nested
// sequential pipelines (LIFO) and — since parallel test scenarios each run
// their own in-process pipeline — for concurrent siblings whose pipelines
// finish in an arbitrary order. Removing only the caller's entry (rather than
// truncating everything above it) keeps a still-active sibling's printer
// installed, so its diagnostics never fall through to the fallback writer.
// Parallel sub-steps within one pipeline attribute via WithLinePrinter and
// never touch this stack.
func SetPrinter(p LinePrinter) (restore func()) {
	mu.Lock()
	printerSeq++
	id := printerSeq
	printers = append(printers, printerEntry{id: id, p: p})
	mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			mu.Lock()
			defer mu.Unlock()
			for i := range printers {
				if printers[i].id == id {
					printers = append(printers[:i], printers[i+1:]...)
					return
				}
			}
		})
	}
}

// WithLinePrinter returns a context that carries p as the override printer for
// emits made with that context (used for per-sub-step attribution).
func WithLinePrinter(ctx context.Context, p LinePrinter) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

func printerFrom(ctx context.Context) LinePrinter {
	if ctx == nil {
		return nil
	}
	p, _ := ctx.Value(ctxKey{}).(LinePrinter)
	return p
}

// FormatCommand quotes and joins args into a single copy-pasteable shell line.
func FormatCommand(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = quoteArg(arg)
	}
	return strings.Join(quoted, " ")
}

func quoteArg(arg string) string {
	if arg == "" {
		return "''"
	}
	if strings.ContainsAny(arg, " \t\n\"'\\$`|&;()<>*?[#~=%") {
		return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
	}
	return arg
}
