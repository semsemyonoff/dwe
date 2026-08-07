package tui

import (
	"errors"
	"fmt"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/term"

	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// run.go holds the framework's launch path. Run owns the terminal-capability
// gate (non-TTY → error, too-small → fallback sentinel), tea.NewProgram
// construction, plugin teardown, and result extraction. Alt-screen and the mouse
// mode are NOT program options in bubbletea/v2 — they are owned by Frame.View,
// fed via frameOptions mapped from RunOptions here. newFrame stays unexported:
// consumers launch through Run (or, in tests, through testsupport.go's
// RenderFrame / BuildHelp).

// ErrNotTTY is returned by [Run] when stdout is not a terminal. It is reported
// BEFORE any program start, so the plugin's Init never runs on this path — a
// caller can detect it and fall back to a non-interactive surface.
var ErrNotTTY = errors.New("tui: stdout is not a terminal")

// ErrTooNarrow is the fallback sentinel returned by [Run] when the terminal is
// below the minimum usable size (see [tooNarrow]). Callers drop to a plain
// non-full-screen surface instead of rendering a torn frame.
var ErrTooNarrow = errors.New("tui: terminal too small for the framework")

// RunOptions configures a [Run] launch. Brand/Project feed the status line;
// Mouse enables CellMotion mouse reporting (click + wheel) when true and the
// terminal is capable (not TERM=dumb). The trailing fields are unexported test
// seams so the non-TTY, narrow, zero-value-input, mouse-flag, and close-error
// paths are deterministic without a real terminal:
//
//   - input / output override the program's stdio. They are appended as
//     tea.WithInput / tea.WithOutput ONLY when non-nil — a zero-value RunOptions
//     must fall through to the default stdin/stdout, because in bubbletea/v2
//     WithInput(nil) DISABLES input rather than defaulting it.
//   - isTTY / size override the terminal-capability probes (default: the real
//     term.IsTerminal / term.GetSize on os.Stdout).
type RunOptions struct {
	Brand   string
	Project string
	Mouse   bool

	// Translator / Locale resolve the help-modal display strings. A nil
	// Translator falls back to i18n.NopTranslator (English code-level
	// fallbacks); an empty Locale falls back to "en". Production callers thread
	// the resolved rflags.I18n / locale here.
	Translator i18n.Translator
	Locale     string

	// test seams (unexported — production callers leave them zero).
	input  io.Reader
	output io.Writer
	isTTY  func() bool
	size   func() (int, int, error)
}

// runProgram is the package-private seam through which [Run] drives the event
// loop. Production uses (*tea.Program).Run; tests reassign it to return a chosen
// model/error without spinning a real event loop. The model argument is the
// frame passed to NewProgram, exposed so tests can inspect its envelope.
var runProgram = func(_ tea.Model, p *tea.Program) (tea.Model, error) { return p.Run() }

// defaultIsTTY / defaultSize are the real terminal probes used when the matching
// RunOptions seam is nil.
func defaultIsTTY() bool { return term.IsTerminal(os.Stdout.Fd()) }

func defaultSize() (int, int, error) { return term.GetSize(os.Stdout.Fd()) }

// buildProgramOptions maps the stdio seams onto tea.ProgramOptions. It appends a
// WithInput / WithOutput ONLY for a non-nil seam, so a zero-value RunOptions
// yields no options and the program keeps its default stdin/stdout (passing
// WithInput(nil) would instead disable input in bubbletea/v2).
func buildProgramOptions(opts RunOptions) []tea.ProgramOption {
	// wheelFilter coalesces buffered mouse-wheel floods before Update/View so a
	// momentum/trackpad flood cannot stall the event loop ahead of later keys or
	// clicks (the "freeze after stopping" + "can't interrupt"). See frame.go.
	po := []tea.ProgramOption{tea.WithFilter(wheelFilter)}
	if opts.input != nil {
		po = append(po, tea.WithInput(opts.input))
	}
	if opts.output != nil {
		po = append(po, tea.WithOutput(opts.output))
	}
	return po
}

// Run launches a [Plugin] inside the framework [Frame] and returns the plugin's
// typed Result UNCHANGED (no wrapper type).
//
// Capability gate, in order, BEFORE any program start (so Init never runs on a
// rejected path):
//
//   - non-TTY → [ErrNotTTY]
//   - terminal-size read failure → wrapped error
//   - terminal below the minimum size → [ErrTooNarrow]
//   - plugin contract violation (duplicate action/key, empty/zero-weight panels)
//     → newFrame's error
//
// Teardown: once the frame is built, plugin.Close() is deferred so it runs on
// the normal-quit AND error/interrupt paths. Close-error precedence: a Close
// error surfaces ONLY when the program itself returned no error — a program
// error always wins (it is the more useful diagnostic). Both are errcheck-safe.
//
// Exit-error mapping mirrors the other full-screen TUIs (statustui/cmdbrowser):
// a user-initiated exit (q / ctrl+c / context kill, surfaced as
// [tea.ErrInterrupted] or [tea.ErrProgramKilled]) is reported as the clean
// [widgets.ErrCancelled] sentinel, NOT a fatal wrapped error. A recovered panic
// ([tea.ErrProgramPanic]) is always surfaced — and checked FIRST, because v2
// wraps it as `ErrProgramKilled: ErrProgramPanic`, so a naive ErrProgramKilled
// check would otherwise swallow panics as clean exits.
//
// The program runs inside [widgets.RunWithPromptHooks] so a surrounding
// [LiveLine] footer pauses before the alt-screen takes over and resumes on every
// exit path (the documented contract for full-screen UI; see packages.md).
func Run(p Plugin, opts RunOptions) (result any, err error) {
	isTTY := opts.isTTY
	if isTTY == nil {
		isTTY = defaultIsTTY
	}
	if !isTTY() {
		return nil, ErrNotTTY
	}

	size := opts.size
	if size == nil {
		size = defaultSize
	}
	w, h, sizeErr := size()
	if sizeErr != nil {
		return nil, fmt.Errorf("tui: reading terminal size: %w", sizeErr)
	}
	if tooNarrow(w, h) {
		return nil, ErrTooNarrow
	}

	frame, err := newFrame(p,
		withBrand(opts.Brand),
		withProject(opts.Project),
		withMouse(opts.Mouse),
		withTranslator(opts.Translator),
		withLocale(opts.Locale),
	)
	if err != nil {
		// Construction error (duplicate action/key, invalid panels) surfaces here,
		// before the program starts and before the plugin acquires resources — so
		// no Close is owed.
		return nil, err
	}

	// From here the plugin owns resources; Close must run on every exit path. The
	// named-return policy lets a Close error surface only when the program
	// returned no error.
	defer func() {
		cerr := p.Close()
		if err == nil && cerr != nil {
			err = fmt.Errorf("tui: closing plugin: %w", cerr)
		}
	}()

	prog := tea.NewProgram(frame, buildProgramOptions(opts)...)
	runErr := widgets.RunWithPromptHooks(func() error {
		_, e := runProgram(frame, prog)
		return e
	})
	if runErr != nil {
		// ErrProgramPanic FIRST: v2 wraps a recovered panic as
		// `ErrProgramKilled: ErrProgramPanic`, so the kill check below must not
		// run first or it would swallow panics as clean exits.
		if errors.Is(runErr, tea.ErrProgramPanic) {
			return nil, fmt.Errorf("tui: running program: %w", runErr)
		}
		// User-initiated exit (q / ctrl+c / context kill) — a clean cancel, not
		// a fatal error.
		if errors.Is(runErr, tea.ErrInterrupted) || errors.Is(runErr, tea.ErrProgramKilled) {
			return nil, widgets.ErrCancelled
		}
		return nil, fmt.Errorf("tui: running program: %w", runErr)
	}
	return p.Result(), nil
}
