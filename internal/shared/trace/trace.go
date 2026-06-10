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
//     a printer wrapping the live-view's safe screen path; the safe baseline;
//  3. the configured fallback writer (Configure) — used outside any pipeline.
//
// When the level is LevelOff every emit returns immediately after one atomic
// load, so a normal run carries zero overhead.
package trace

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
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

var (
	level atomic.Int32

	mu       sync.Mutex
	fallback io.Writer
	printers []LinePrinter // save/restore stack; top is the active global printer
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

// Command echoes an executed command at Verbose+ as a copy-pasteable line
// prefixed with "$ ".
func Command(ctx context.Context, name string, args ...string) {
	if currentLevel() < LevelVerbose {
		return
	}
	emit(ctx, "$ "+FormatCommand(append([]string{name}, args...)))
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
	if p := printerFrom(ctx); p != nil {
		p.PrintLine(line)
		return
	}
	mu.Lock()
	if n := len(printers); n > 0 {
		p := printers[n-1]
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

// SetPrinter pushes p as the global active printer and returns a restore
// function that pops it (and anything pushed above it). Restore is idempotent.
// Only sequential callers (nested pipelines) mutate the stack; parallel
// sub-steps attribute via WithLinePrinter and never touch it.
func SetPrinter(p LinePrinter) (restore func()) {
	mu.Lock()
	printers = append(printers, p)
	idx := len(printers) - 1
	mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			mu.Lock()
			defer mu.Unlock()
			if idx < len(printers) {
				printers = printers[:idx]
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
