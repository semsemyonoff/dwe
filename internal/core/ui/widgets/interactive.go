package widgets

import (
	"io"
	"os"

	"github.com/charmbracelet/x/term"
)

// IsInteractiveFn returns true when both stdout and stdin are real TTYs.
// huh forms require both a TTY stdin (for keypresses) and a TTY stdout (for rendering).
// Tests inject bytes.Buffer as stdin, which fails the *os.File check and routes
// to the non-interactive fallback without any global swapping.
var IsInteractiveFn = func(stdin io.Reader) bool {
	if !term.IsTerminal(os.Stdout.Fd()) {
		return false
	}
	f, ok := stdin.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(f.Fd())
}
