package workflow

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// unsafeWorkflowFSRe matches characters not allowed in sanitised filesystem names.
// Matches the pattern used by pipeline.sanitizeForFS so workflow logs sit
// alongside pipeline parallel logs under a shared `.devbox/logs/parallel/` tree.
var unsafeWorkflowFSRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// sanitizeWorkflowFS returns a filesystem-safe version of s. Empty / all-unsafe
// inputs collapse to "_" so file creation never receives an empty basename.
func sanitizeWorkflowFS(s string) string {
	out := unsafeWorkflowFSRe.ReplaceAllString(s, "_")
	out = strings.Trim(out, ".")
	if out == "" {
		return "_"
	}
	return out
}

// openWorkflowSubStepLog opens a per-sub-step log file at
// .devbox/logs/parallel/workflow/<workflow-id>/<sub-name>.log.
//
// Mirrors pipeline.OpenSubStepLog but lives here to avoid an import cycle
// (pipeline imports usercommands → runtime). When enabled is false or workDir
// is empty (no project context) returns (nil, "", nil).
func openWorkflowSubStepLog(workDir, workflowID, subName string, enabled bool) (io.WriteCloser, string, error) {
	if !enabled || workDir == "" {
		return nil, "", nil
	}
	dir := filepath.Join(
		workDir, ".devbox", "logs", "parallel", "workflow",
		sanitizeWorkflowFS(workflowID),
	)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", fmt.Errorf("creating sub-step log directory %s: %w", dir, err)
	}
	base := sanitizeWorkflowFS(subName)
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
		// A log-file infrastructure failure is non-fatal: the sub-step runs
		// without capturing output rather than aborting with a misleading error.
		return nil, "", nil
	}
	return f, path, nil
}
