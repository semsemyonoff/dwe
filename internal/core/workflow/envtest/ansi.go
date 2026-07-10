package envtest

import (
	"bytes"
	"io"
	"sync"

	"github.com/charmbracelet/x/ansi"
)

// stripANSI wraps w so every write has its ANSI escape sequences removed before
// reaching w. It exists for one job: the `dwe validate` / `dwe deploy run`
// subprocess is spawned with CLICOLOR_FORCE=1 (RunRequest.ForceColor) so its
// output stays colored when streamed to the user's terminal even though its
// stdout is a pipe — but that same colored stream is teed into the copy's run
// log (and, on failure, the report's pipeline.log), which must stay plain. This
// writer sits on the log side of that tee.
//
// It is stateful: an escape sequence split across two Write calls (the pipe can
// deliver a partial sequence) would defeat a naive per-write ansi.Strip, so an
// incomplete trailing sequence is held back and prepended to the next write.
func stripANSI(w io.Writer) io.Writer { return &ansiStripper{w: w} }

// maxPendingESC caps the held-back buffer: a lone ESC that never terminates
// (malformed output) must not grow the buffer without bound. Past the cap the
// pending bytes are flushed as-is (stripped best-effort) and the buffer reset.
const maxPendingESC = 256

type ansiStripper struct {
	w   io.Writer
	mu  sync.Mutex // guards buf: a single stripper may back both a subprocess's stdout and stderr
	buf []byte     // an incomplete trailing escape sequence carried to the next write
}

func (a *ansiStripper) Write(p []byte) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := len(p)
	data := p
	if len(a.buf) > 0 {
		combined := make([]byte, 0, len(a.buf)+len(p))
		combined = append(combined, a.buf...)
		combined = append(combined, p...)
		data = combined
		a.buf = nil
	}
	// Hold back an incomplete trailing escape sequence so ansi.Strip only ever
	// sees complete sequences (unless the held bytes exceed the cap).
	if i := incompleteTrailingESC(data); i >= 0 && len(data)-i <= maxPendingESC {
		a.buf = append(a.buf[:0], data[i:]...)
		data = data[:i]
	}
	if len(data) == 0 {
		return n, nil
	}
	if _, err := io.WriteString(a.w, ansi.Strip(string(data))); err != nil {
		return 0, err
	}
	return n, nil
}

// incompleteTrailingESC returns the index of an ESC (0x1b) that begins an
// unterminated escape sequence at the tail of data, or -1 when data ends on a
// complete sequence or plain text. Only the last ESC can be trailing-incomplete
// — any earlier ESC is followed by its own terminator before the last one.
func incompleteTrailingESC(data []byte) int {
	j := bytes.LastIndexByte(data, 0x1b)
	if j < 0 {
		return -1
	}
	rest := data[j:]
	if len(rest) == 1 { // bare ESC at end
		return j
	}
	switch rest[1] {
	case '[': // CSI: ESC [ params… final(0x40–0x7E)
		for k := 2; k < len(rest); k++ {
			if rest[k] >= 0x40 && rest[k] <= 0x7e {
				return -1 // found the final byte → complete
			}
		}
		return j
	case ']': // OSC: ESC ] … terminated by BEL or ST (ESC \)
		for k := 2; k < len(rest); k++ {
			if rest[k] == 0x07 {
				return -1
			}
			if rest[k] == 0x1b && k+1 < len(rest) && rest[k+1] == '\\' {
				return -1
			}
		}
		return j
	default:
		// A two-byte escape (ESC <byte>): already complete since len(rest) >= 2.
		return -1
	}
}
