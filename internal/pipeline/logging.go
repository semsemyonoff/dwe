package pipeline

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"devbox-cli/internal/render"
)

// ansiRe matches ANSI/VT100 escape sequences and bare carriage returns.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b[a-zA-Z]|\r`)

// unsafeFSRe matches characters not allowed in sanitised filesystem names.
var unsafeFSRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// ansiStripper wraps an io.Writer, stripping ANSI escape sequences before writing.
type ansiStripper struct{ w io.Writer }

func (s *ansiStripper) Write(p []byte) (int, error) {
	stripped := ansiRe.ReplaceAll(p, nil)
	if _, err := s.w.Write(stripped); err != nil {
		return 0, err
	}
	return len(p), nil
}

// OpenPipelineLog opens (or skips) a pipeline log file at .devbox/logs/<name>.log.
//
// When enabled is true, it ensures the .devbox/logs directory exists, creates the log
// file, and returns a Writer that tees devbox status messages to both stdout
// and the log file (with ANSI codes stripped from the file copy). The returned
// io.Writer is the raw log file (for child-process tee) and logPath is the
// destination path used in trailing "Log saved to:" messages.
//
// When enabled is false, it returns the plain stdout writer with nil log file
// and an empty path. cleanup is always non-nil and safe to call.
func OpenPipelineLog(workDir, name string, enabled bool) (*render.Writer, io.Writer, string, func(), error) {
	if !enabled {
		return render.Stdout(), nil, "", func() {}, nil
	}
	logsDir := filepath.Join(workDir, ".devbox", "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return nil, nil, "", func() {}, fmt.Errorf("creating logs directory %s: %w", logsDir, err)
	}
	logPath := filepath.Join(logsDir, name+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, nil, "", func() {}, fmt.Errorf("creating %s log %s: %w", name, logPath, err)
	}
	tee := io.MultiWriter(os.Stdout, &ansiStripper{logFile})
	return render.NewWriter(tee), logFile, logPath, func() { _ = logFile.Close() }, nil
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
	path := filepath.Join(dir, sanitizeForFS(subStepName)+".log")
	f, err := os.Create(path)
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

// lineTee buffers writes and invokes cb once per complete \n-terminated line.
// ANSI escape sequences are stripped before line-splitting so callbacks see
// plain text. Trailing un-terminated bytes are flushed via Flush().
//
// lineTee is safe for concurrent Write calls from a single source (one
// sub-step) but is NOT designed for concurrent writes from multiple goroutines
// — each parallel sub-step gets its own lineTee instance.
type lineTee struct {
	cb  func(string)
	mu  sync.Mutex
	buf bytes.Buffer
}

func newLineTee(cb func(string)) *lineTee {
	return &lineTee{cb: cb}
}

func (t *lineTee) Write(p []byte) (int, error) {
	stripped := ansiRe.ReplaceAll(p, nil)
	t.mu.Lock()
	t.buf.Write(stripped)
	for {
		data := t.buf.Bytes()
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		line := string(data[:i])
		// Consume the line plus its terminator from the buffer.
		_ = t.buf.Next(i + 1)
		t.mu.Unlock()
		t.cb(line)
		t.mu.Lock()
	}
	t.mu.Unlock()
	return len(p), nil
}

// Flush emits any buffered un-terminated trailing bytes as a final line.
func (t *lineTee) Flush() {
	t.mu.Lock()
	if t.buf.Len() == 0 {
		t.mu.Unlock()
		return
	}
	line := t.buf.String()
	t.buf.Reset()
	t.mu.Unlock()
	t.cb(line)
}
