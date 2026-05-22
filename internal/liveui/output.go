package liveui

import (
	"bytes"
	"io"
	"regexp"
	"sync"
)

// ANSIOnlyRe matches ANSI/VT100 escape sequences only. It deliberately does
// NOT match `\r` — bare carriage returns are data for the frame parser
// (LineTee) and the log sanitiser (LogSanitizer), each of which handles them.
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
//     survive the per-write pass so the LineTee buffer can assemble
//     the complete sequence for its double-strip
//     0x5D (]) — OSC introducer, handled by the OSC branch above
//     This covers ESC 7/ESC 8 (save/restore cursor from tput/ncurses),
//     ESC = / ESC > (keypad modes), ESC M (reverse linefeed), ESC ( B / ESC # 8
//     (charset designation, DEC alignment), and similar two-byte controls.
var ANSIOnlyRe = regexp.MustCompile("" +
	`\x1b\[[0-9:;<=>?]*[ -/]*[@-~]` + // CSI (standard + private-mode)
	`|\x1b\][^\x1b\x07]*(?:\x07|\x1b\\)` + // OSC terminated by BEL or ST (ESC \)
	`|\x1b[\x20-\x2f]*[\x30-\x5a\x5e-\x7e]`, // Fp/Fe/Fs two-byte (+ intermediate) sequences
)

// ANSIOnlyStripper wraps an io.Writer, stripping ANSI escape sequences but
// preserving `\r` and `\n` byte values. Used by the tee path so the frame
// parser sees `\r` as data.
type ANSIOnlyStripper struct{ W io.Writer }

func (s *ANSIOnlyStripper) Write(p []byte) (int, error) {
	stripped := ANSIOnlyRe.ReplaceAll(p, nil)
	if _, err := s.W.Write(stripped); err != nil {
		return 0, err
	}
	return len(p), nil
}

// LogSanitizer wraps an io.Writer for log-file destinations. It strips ANSI
// escape sequences and normalises carriage returns in a single stateless
// pass:
//
//   - `\r\n` (CRLF, common when PTYs apply ONLCR translation) collapses to
//     a single `\n`, so the on-disk log does not double-space every line.
//   - lone `\r` (used by tools like docker compose and curl for in-place
//     progress redraws) becomes `\n`, so the log records each redraw frame
//     on its own line (`50%\r100%\n` → `50%\n100%\n`) instead of
//     concatenating into `50%100%\n`.
//
// Stateless on purpose: no buffered trailing CR, no lifecycle (no Flush/
// Close contract), no mutex. Safe for concurrent writes from multiple
// goroutines.
type LogSanitizer struct{ W io.Writer }

func (s *LogSanitizer) Write(p []byte) (int, error) {
	// ReplaceAll returns the input slice unchanged when there are no matches, so
	// we must not mutate the result in-place — that would corrupt the caller's
	// buffer. Build a fresh slice via bytes.ReplaceAll calls.
	stripped := ANSIOnlyRe.ReplaceAll(p, nil)
	stripped = bytes.ReplaceAll(stripped, []byte("\r\n"), []byte{'\n'})
	stripped = bytes.ReplaceAll(stripped, []byte{'\r'}, []byte{'\n'})
	if _, err := s.W.Write(stripped); err != nil {
		return 0, err
	}
	return len(p), nil
}

// JoinWriters returns a single io.Writer that fan-outs to every non-nil writer
// in ws. If every writer is nil the result is io.Discard (defensive — parallel
// callers always supply at least one non-nil writer). When exactly one writer
// is non-nil it is returned directly to avoid an unnecessary MultiWriter.
//
// io.MultiWriter does not tolerate nil entries; this helper exists so callers
// can pass optional destinations (per-substep log, global pipeline log, line
// tee) without each one guarding the nil case.
func JoinWriters(ws ...io.Writer) io.Writer {
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

// LineTee buffers writes and invokes cb once per frame — a segment ending in
// `\r` (in-progress redraw, `final=false`) or `\n` (committed line,
// `final=true`). CRLF (`\r\n`) within one buffer scan collapses to a single
// `final=true` frame. By default ANSI escape sequences are stripped before
// scanning so callbacks see plain text; use NewLineTeePreserveANSI when the
// caller needs to forward colours (e.g. captured failure dumps). Trailing
// un-terminated bytes are flushed via Flush() as `(tail, false)`.
//
// LineTee is safe for concurrent Write calls from a single source (one
// sub-step) but is NOT designed for concurrent writes from multiple goroutines
// — each parallel sub-step gets its own LineTee instance.
type LineTee struct {
	cb           func(frame string, final bool)
	preserveANSI bool
	mu           sync.Mutex
	buf          bytes.Buffer
}

// NewLineTee constructs a LineTee whose cb is invoked once per frame with
// ANSI escape sequences stripped.
func NewLineTee(cb func(frame string, final bool)) *LineTee {
	return &LineTee{cb: cb}
}

// NewLineTeePreserveANSI constructs a LineTee that keeps ANSI escape
// sequences intact when invoking cb. `\r` and `\n` remain the frame
// terminators — they never appear inside the ANSI sequences this package
// matches, so frame detection is unaffected.
func NewLineTeePreserveANSI(cb func(frame string, final bool)) *LineTee {
	return &LineTee{cb: cb, preserveANSI: true}
}

func (t *LineTee) Write(p []byte) (int, error) {
	// Strip ANSI per-write (handles sequences that arrive in a single Write
	// call) but preserve `\r` so the frame parser sees it as data.
	// Sequences split across multiple Write calls are handled by a second
	// ANSI-strip at frame-emit time below, where the full assembled line is
	// available in the buffer. When preserveANSI is set both passes are
	// skipped — the buffered bytes are forwarded verbatim.
	in := p
	if !t.preserveANSI {
		in = ANSIOnlyRe.ReplaceAll(p, nil)
	}
	t.mu.Lock()
	t.buf.Write(in)
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
		// line in the buffer contains the complete sequence. Skipped when
		// preserveANSI is set so callers receive coloured frames verbatim.
		var frame string
		if t.preserveANSI {
			frame = string(data[:idx])
		} else {
			frame = string(ANSIOnlyRe.ReplaceAll(data[:idx], nil))
		}
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
func (t *LineTee) Flush() {
	t.mu.Lock()
	if t.buf.Len() == 0 {
		t.mu.Unlock()
		return
	}
	// Apply the same double-strip as Write: the tail may contain partial ANSI
	// sequences that were split across PTY reads and are now complete in the buffer.
	// Skipped when preserveANSI is set.
	var tail string
	if t.preserveANSI {
		tail = t.buf.String()
	} else {
		tail = string(ANSIOnlyRe.ReplaceAll(t.buf.Bytes(), nil))
	}
	t.buf.Reset()
	t.mu.Unlock()
	t.cb(tail, false)
}
