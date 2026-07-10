package test

import (
	"io"
	"os"

	"github.com/charmbracelet/x/term"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/workflow/envtest"
)

// Status glyphs, shared with the aggregated live view's ✓/✗ vocabulary so the
// final text report reads the same way. Emitted only in color (TTY) mode — see
// the byte-identity note on writerIsTTY.
const (
	glyphPass = "✓"
	glyphFail = "✗"
	glyphInfo = "•"
)

// writerIsTTY reports whether w is a real terminal. Styling (lipgloss color +
// glyphs) is emitted only when it is. A *bytes.Buffer (tests) or a pipe/redirect
// (`dwe test … > file`, CI) is not a *os.File TTY, so every non-interactive run
// — and therefore every exact-match golden — stays byte-identical to the plain,
// unstyled form: the color=false branches below produce exactly the bytes the
// pre-styling code did.
func writerIsTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(f.Fd())
}

// cName styles a scenario name with the accent token (bold), the same identity
// color scenario names carry across the dwe UI. Plain when color is off.
func cName(s string, color bool) string {
	if !color {
		return s
	}
	return styles.AccentStyle().Bold(true).Render(s)
}

// cMuted styles secondary text (elapsed, paths, reasons) with the muted token.
func cMuted(s string, color bool) string {
	if !color {
		return s
	}
	return styles.MutedStyle().Render(s)
}

// statusWord renders a scenario/clean status word colored by severity: success
// for passed/swept, danger for failed/error, warning for skipped. Plain when
// color is off.
func statusWord(word string, sev severity, color bool) string {
	if !color {
		return word
	}
	switch sev {
	case sevSuccess:
		return styles.SuccessStyle().Render(word)
	case sevDanger:
		return styles.DangerStyle().Render(word)
	default:
		return styles.WarningStyle().Render(word)
	}
}

// statusGlyph returns the colored leading glyph for a severity, or "" when color
// is off (so the plain line keeps no glyph and stays byte-identical).
func statusGlyph(sev severity, color bool) string {
	if !color {
		return ""
	}
	switch sev {
	case sevSuccess:
		return styles.SuccessStyle().Render(glyphPass)
	case sevDanger:
		return styles.DangerStyle().Render(glyphFail)
	default:
		return styles.WarningStyle().Render(glyphInfo)
	}
}

// severity classifies an outcome for coloring/glyph selection.
type severity int

const (
	sevSuccess severity = iota
	sevWarning
	sevDanger
)

// scenarioSeverity maps a run status to its severity.
func scenarioSeverity(s envtest.ScenarioStatus) severity {
	switch s {
	case envtest.StatusPassed:
		return sevSuccess
	case envtest.StatusFailed, envtest.StatusError:
		return sevDanger
	default:
		return sevWarning
	}
}

// styledWarning formats one warning line in the project palette: an optional
// accent [<name>] scenario tag, then the warning-token "warning: <msg>" (the
// same yellow the rest of dwe uses for warnings). Plain — the exact
// "[<name>] warning: <msg>" / "warning: <msg>" form — when color is off, so
// piped/CI stderr stays ANSI-free.
func styledWarning(name, msg string, color bool) string {
	body := "warning: " + msg
	if color {
		body = styles.WarningStyle().Render(body)
	}
	if name == "" {
		return body
	}
	tag := "[" + name + "]"
	if color {
		tag = styles.AccentStyle().Bold(true).Render(tag)
	}
	return tag + " " + body
}
