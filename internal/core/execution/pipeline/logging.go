package pipeline

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/semsemyonoff/devbox/internal/shared/render"
)

// unsafeFSRe matches characters not allowed in sanitised filesystem names.
var unsafeFSRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// OpenPipelineLog opens (or skips) a pipeline log file at .devbox/logs/<name>.log.
//
// Returns three separate writers so PlainReporter can drive its live-line UI
// without fan-out at the writer level:
//
//   - screen: status writer around os.Stdout (no log fan-out). PlainReporter
//     writes log lines explicitly next to each emit via a dedicated side-write.
//   - logFile: the raw *os.File (when enabled) or nil. PlainReporter wraps it
//     with liveui.LogSanitizer internally — the file on disk receives clean
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
// caller must handle a nil writer (liveui.JoinWriters drops nils).
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
