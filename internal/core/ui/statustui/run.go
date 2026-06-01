package statustui

import (
	"context"
	"errors"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
)

// Run launches the status TUI, returning an error if not a terminal or if
// the program encounters a fatal error. A context is owned by Run and
// canceled on return so in-flight data-fetch goroutines stop cleanly when
// the user quits.
func Run(ctx context.Context, d Deps) error {
	if !isTerminalFn(os.Stdout.Fd()) {
		return errors.New("statustui: not a terminal")
	}

	width, height, err := terminalSizeFn()
	if err != nil {
		return err
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel() // Cancels in-flight buildTabs goroutines on return

	m := newModel(d, runCtx, width, height)
	prog := tea.NewProgram(m, tea.WithContext(runCtx))

	runErr := widgets.RunWithPromptHooks(func() error {
		_, e := prog.Run()
		return e
	})

	return mapRunError(runErr)
}

// mapRunError translates bubbletea's exit errors into cobra-friendly returns.
// CRITICAL: check ErrProgramPanic before ErrProgramKilled because v2 wraps
// recovered panics as `ErrProgramKilled: ErrProgramPanic`; a naive check of
// ErrProgramKilled first would swallow panics as clean exits.
func mapRunError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, tea.ErrProgramPanic) {
		return err
	}
	if errors.Is(err, tea.ErrInterrupted) || errors.Is(err, tea.ErrProgramKilled) {
		return nil // User-initiated exit (q / ctrl+c), not an error
	}
	return err
}
