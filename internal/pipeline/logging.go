package pipeline

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"devbox-cli/internal/render"
)

// ansiRe matches ANSI/VT100 escape sequences and bare carriage returns.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b[a-zA-Z]|\r`)

// ansiStripper wraps an io.Writer, stripping ANSI escape sequences before writing.
type ansiStripper struct{ w io.Writer }

func (s *ansiStripper) Write(p []byte) (int, error) {
	stripped := ansiRe.ReplaceAll(p, nil)
	if _, err := s.w.Write(stripped); err != nil {
		return 0, err
	}
	return len(p), nil
}

// OpenPipelineLog opens (or skips) a pipeline log file at logs/<name>.log.
//
// When enabled is true, it ensures the logs directory exists, creates the log
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
	logsDir := filepath.Join(workDir, "logs")
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
