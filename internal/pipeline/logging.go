package pipeline

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"devbox-cli/internal/render"
)

// ansiOnlyRe matches ANSI/VT100 escape sequences only. It deliberately does
// NOT match `\r` — bare carriage returns are data for the frame parser
// (lineTee) and the log sanitiser (logSanitizer), each of which handles them.
//
// The three alternatives cover:
//   - Standard and private-mode CSI: ESC [ <params> <intermediates> <final>
//     where params are 0x30–0x3F (digits, :;<=>?), intermediates are 0x20–0x2F
//     (space through /), and final bytes are 0x40–0x7E (@ through ~). This
//     covers SGR colours, cursor moves, erase sequences, and DEC private modes
//     like \x1b[?25l (hide cursor) and \x1b[?2026h (synchronized output).
//   - OSC sequences terminated by BEL (0x07) OR ST (ESC \): a single combined
//     branch `\x1b\][^\x1b\x07]*(?:\x07|\x1b\\)` stops at whichever terminator
//     comes first. The content charset `[^\x1b\x07]*` excludes both ESC and BEL
//     so the branch cannot over-match across a mixed-terminator sequence like
//     `ESC]8;;url ESC\VISIBLE ESC]0;t BEL` — without this constraint the old
//     BEL branch `[^\x07]*\x07` would greedily absorb ESC and consume VISIBLE.
//     Covers window title changes and OSC 8 hyperlinks from curl, git, ls, etc.
//   - Two-byte ESC sequences with optional intermediate bytes (0x20–0x2F) and
//     a final byte in the Fp (0x30–0x3A), Fe (0x40–0x5A), or Fs (0x5E–0x7E)
//     ranges. Three final bytes are intentionally excluded:
//     0x5B ([) — CSI introducer, handled by the CSI branch above
//     0x5C (\) — ST terminator; excluding it preserves split-write OSC
//     correctness: the ST in chunk 2 of a split "ESC]…ESC\" must
//     survive the per-write pass so the lineTee buffer can assemble
//     the complete sequence for its double-strip
//     0x5D (]) — OSC introducer, handled by the OSC branch above
//     This covers ESC 7/ESC 8 (save/restore cursor from tput/ncurses),
//     ESC = / ESC > (keypad modes), ESC M (reverse linefeed), ESC ( B / ESC # 8
//     (charset designation, DEC alignment), and similar two-byte controls.
var ansiOnlyRe = regexp.MustCompile("" +
	`\x1b\[[0-9:;<=>?]*[ -/]*[@-~]` + // CSI (standard + private-mode)
	`|\x1b\][^\x1b\x07]*(?:\x07|\x1b\\)` + // OSC terminated by BEL or ST (ESC \)
	`|\x1b[\x20-\x2f]*[\x30-\x5a\x5e-\x7e]`, // Fp/Fe/Fs two-byte (+ intermediate) sequences
)

// unsafeFSRe matches characters not allowed in sanitised filesystem names.
var unsafeFSRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// ansiOnlyStripper wraps an io.Writer, stripping ANSI escape sequences but
// preserving `\r` and `\n` byte values. Used by the tee path so the frame
// parser sees `\r` as data.
type ansiOnlyStripper struct{ w io.Writer }

func (s *ansiOnlyStripper) Write(p []byte) (int, error) {
	stripped := ansiOnlyRe.ReplaceAll(p, nil)
	if _, err := s.w.Write(stripped); err != nil {
		return 0, err
	}
	return len(p), nil
}

// logSanitizer wraps an io.Writer for log-file destinations. It strips ANSI
// escape sequences and converts EVERY `\r` byte to `\n`, in a single stateless
// pass. This means `50%\r100%\n` becomes `50%\n100%\n` (one frame per line on
// disk) and `\r\n` within one Write becomes `\n\n` (one extra blank line — an
// accepted trade-off, see plan).
//
// Stateless on purpose: no buffered trailing CR, no lifecycle (no Flush/Close
// contract), no mutex. Safe for concurrent writes from multiple goroutines.
type logSanitizer struct{ w io.Writer }

func (s *logSanitizer) Write(p []byte) (int, error) {
	// ReplaceAll returns the input slice unchanged when there are no matches, so
	// we must not mutate the result in-place — that would corrupt the caller's
	// buffer. Use bytes.ReplaceAll for the \r→\n pass instead.
	stripped := bytes.ReplaceAll(ansiOnlyRe.ReplaceAll(p, nil), []byte{'\r'}, []byte{'\n'})
	if _, err := s.w.Write(stripped); err != nil {
		return 0, err
	}
	return len(p), nil
}

// OpenPipelineLog opens (or skips) a pipeline log file at .devbox/logs/<name>.log.
//
// Returns three separate writers so PlainReporter can drive its live-line UI
// without fan-out at the writer level:
//
//   - screen: status writer around os.Stdout (no log fan-out). PlainReporter
//     writes log lines explicitly next to each emit via a dedicated side-write.
//   - logFile: the raw *os.File (when enabled) or nil. PlainReporter wraps it
//     with logSanitizer internally — the file on disk receives clean
//     line-terminated text.
//   - termOut: raw os.Stdout for cursor ANSI when the terminal is a TTY, or
//     io.Discard otherwise. LiveLine writes its cursor/spinner sequences here.
//
// logPath is the destination path used in trailing "Log saved to:" messages.
// cleanup is always non-nil and safe to call.
func OpenPipelineLog(workDir, name string, enabled bool) (
	screen *render.Writer,
	logFile io.Writer,
	termOut io.Writer,
	logPath string,
	cleanup func(),
	err error,
) {
	termOut = io.Discard
	if stdoutIsTTY() {
		termOut = os.Stdout
	}
	if !enabled {
		return render.Stdout(), nil, termOut, "", func() {}, nil
	}
	logsDir := filepath.Join(workDir, ".devbox", "logs")
	if mkErr := os.MkdirAll(logsDir, 0o755); mkErr != nil {
		return nil, nil, nil, "", func() {}, fmt.Errorf("creating logs directory %s: %w", logsDir, mkErr)
	}
	logPath = filepath.Join(logsDir, name+".log")
	f, createErr := os.Create(logPath)
	if createErr != nil {
		return nil, nil, nil, "", func() {}, fmt.Errorf("creating %s log %s: %w", name, logPath, createErr)
	}
	return render.NewWriter(os.Stdout), f, termOut, logPath, func() { _ = f.Close() }, nil
}

// OpenSubStepLog opens (or skips) a per-sub-step log file at
// .devbox/logs/parallel/<pipeline>/<group>/<sub>.log.
//
// When enabled is true it creates the directory tree and returns the open file
// plus its absolute path. When enabled is false it returns (nil, "", nil) — the
// caller must handle a nil writer (joinWriters drops nils).
//
// Pipeline / group / sub-step names are sanitised via sanitizeForFS so that
// arbitrary user-provided names do not escape the parallel logs root.
func OpenSubStepLog(workDir, pipelineName, groupName, subStepName string, enabled bool) (io.WriteCloser, string, error) {
	if !enabled {
		return nil, "", nil
	}
	dir := filepath.Join(
		workDir, ".devbox", "logs", "parallel",
		sanitizeForFS(pipelineName),
		sanitizeForFS(groupName),
	)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", fmt.Errorf("creating sub-step log directory %s: %w", dir, err)
	}
	// Use O_EXCL so that two sub-steps whose names sanitize identically (e.g.
	// "step 1" and "step:1" both → "step_1") get distinct log files instead of
	// the second truncating the first.
	base := sanitizeForFS(subStepName)
	path := filepath.Join(dir, base+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, os.ErrExist) {
		for n := 2; n <= 1000; n++ {
			path = filepath.Join(dir, fmt.Sprintf("%s_%d.log", base, n))
			f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if err == nil || !errors.Is(err, os.ErrExist) {
				break
			}
		}
	}
	if err != nil {
		return nil, "", fmt.Errorf("creating sub-step log %s: %w", path, err)
	}
	return f, path, nil
}

// sanitizeForFS returns a filesystem-safe version of s. Empty / all-unsafe
// inputs collapse to "_" so file creation never receives an empty basename.
func sanitizeForFS(s string) string {
	out := unsafeFSRe.ReplaceAllString(s, "_")
	out = strings.Trim(out, ".")
	if out == "" {
		return "_"
	}
	return out
}

// joinWriters returns a single io.Writer that fan-outs to every non-nil writer
// in ws. If every writer is nil the result is io.Discard (defensive — parallel
// callers always supply at least one non-nil writer). When exactly one writer
// is non-nil it is returned directly to avoid an unnecessary MultiWriter.
//
// io.MultiWriter does not tolerate nil entries; this helper exists so callers
// can pass optional destinations (per-substep log, global pipeline log, line
// tee) without each one guarding the nil case.
func joinWriters(ws ...io.Writer) io.Writer {
	nonNil := make([]io.Writer, 0, len(ws))
	for _, w := range ws {
		if w != nil {
			nonNil = append(nonNil, w)
		}
	}
	switch len(nonNil) {
	case 0:
		return io.Discard
	case 1:
		return nonNil[0]
	default:
		return io.MultiWriter(nonNil...)
	}
}

// lineTee buffers writes and invokes cb once per frame — a segment ending in
// `\r` (in-progress redraw, `final=false`) or `\n` (committed line,
// `final=true`). CRLF (`\r\n`) within one buffer scan collapses to a single
// `final=true` frame. ANSI escape sequences are stripped before scanning so
// callbacks see plain text. Trailing un-terminated bytes are flushed via
// Flush() as `(tail, false)`.
//
// lineTee is safe for concurrent Write calls from a single source (one
// sub-step) but is NOT designed for concurrent writes from multiple goroutines
// — each parallel sub-step gets its own lineTee instance.
type lineTee struct {
	cb  func(frame string, final bool)
	mu  sync.Mutex
	buf bytes.Buffer
}

func newLineTee(cb func(frame string, final bool)) *lineTee {
	return &lineTee{cb: cb}
}

func (t *lineTee) Write(p []byte) (int, error) {
	// Strip ANSI per-write (handles sequences that arrive in a single Write
	// call) but preserve `\r` so the frame parser sees it as data.
	// Sequences split across multiple Write calls are handled by a second
	// ANSI-strip at frame-emit time below, where the full assembled line is
	// available in the buffer.
	stripped := ansiOnlyRe.ReplaceAll(p, nil)
	t.mu.Lock()
	t.buf.Write(stripped)
	for {
		data := t.buf.Bytes()
		// Find the earliest of `\r` or `\n` as the frame terminator.
		idxN := bytes.IndexByte(data, '\n')
		idxR := bytes.IndexByte(data, '\r')
		if idxN < 0 && idxR < 0 {
			break
		}
		var idx int
		isR := false
		switch {
		case idxN < 0:
			idx, isR = idxR, true
		case idxR < 0:
			idx = idxN
		case idxR < idxN:
			idx, isR = idxR, true
		default:
			idx = idxN
		}

		// Strip ANSI a second time on the assembled frame bytes. This handles
		// OSC/CSI sequences that were split across PTY read boundaries: neither
		// half matched the regex during the per-write pass, but the reassembled
		// line in the buffer contains the complete sequence.
		frame := string(ansiOnlyRe.ReplaceAll(data[:idx], nil))
		consume := idx + 1
		final := !isR
		// CRLF collapses to a single final frame.
		if isR && idx+1 < len(data) && data[idx+1] == '\n' {
			consume = idx + 2
			final = true
		}
		_ = t.buf.Next(consume)
		t.mu.Unlock()
		t.cb(frame, final)
		t.mu.Lock()
	}
	t.mu.Unlock()
	return len(p), nil
}

// Flush emits any buffered un-terminated trailing bytes as a non-final frame.
// The reporter's commitTrailingTail (Task 9) is responsible for committing the
// tail to scrollback/log at step-finish time.
func (t *lineTee) Flush() {
	t.mu.Lock()
	if t.buf.Len() == 0 {
		t.mu.Unlock()
		return
	}
	// Apply the same double-strip as Write: the tail may contain partial ANSI
	// sequences that were split across PTY reads and are now complete in the buffer.
	tail := string(ansiOnlyRe.ReplaceAll(t.buf.Bytes(), nil))
	t.buf.Reset()
	t.mu.Unlock()
	t.cb(tail, false)
}
