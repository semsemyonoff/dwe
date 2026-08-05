package builtin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"
)

const shellDefaultTimeout = 10 * time.Second

// Shell is the `shell` predicate builtin. It runs a `sh -c` command and
// reports success based on exit status.
type Shell struct{}

// Validate checks that the cmd param is present and timeout is parseable.
func (Shell) Validate(with map[string]any) error {
	cmd := spec.GetStringParam(with, "cmd", "")
	if cmd == "" {
		return errors.New("missing required param 'cmd'")
	}
	if _, err := spec.GetDurationParam(with, "timeout", shellDefaultTimeout); err != nil {
		return err
	}
	return nil
}

// Describe returns a one-line summary for plan output.
func (Shell) Describe(with map[string]any) string {
	cmd := spec.GetStringParam(with, "cmd", "")
	return fmt.Sprintf("builtin: shell(cmd=%s)", cmd)
}

// Run executes the command via `sh -c` in the project root with the configured
// timeout. A timeout of 0 (explicit `timeout: "0"`) is unbounded — matching
// parseStepTimeout's convention and `when:`, which has no timeout at all;
// without this, context.WithTimeout(ctx, 0) would yield an already-expired
// context.
func (Shell) Run(ctx context.Context, with map[string]any, ectx spec.ExecContext) error {
	cmdStr := spec.GetStringParam(with, "cmd", "")
	timeout, err := spec.GetDurationParam(with, "timeout", shellDefaultTimeout)
	if err != nil {
		return err
	}

	runCtx := ctx
	cancel := context.CancelFunc(func() {})
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	var stderr bytes.Buffer
	// Hardcoded sh -c matches deploy/condition `when:` convention (see CLAUDE.md).
	c := exec.CommandContext(runCtx, "sh", "-c", cmdStr)
	// Relative paths must mean the same thing as in condition.EvalCmd, which
	// sets cmd.Dir = projectRoot. Without this the command ran in the process
	// CWD, so a `check:` and the step body it guards disagreed whenever dwe was
	// invoked from a subdirectory. Nil-safe: callers passing a zero ExecContext
	// keep the previous behaviour.
	if ectx.ProjectRoot != "" {
		c.Dir = ectx.ProjectRoot
	}
	c.Stderr = &stderr
	// On timeout, exec.CommandContext SIGKILLs only the direct `sh`. Orphan
	// descendants (e.g. `sh -c "sleep 5"` leaves `sleep` running) keep
	// stderr's read end alive and Wait() blocks until they exit. WaitDelay
	// caps that wait — after the grace period Go closes the pipes itself
	// and Wait returns.
	c.WaitDelay = 100 * time.Millisecond

	err = c.Run()
	if timeout > 0 && runCtx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("timeout after %s", timeout)
	}
	if err != nil {
		last := lastLine(stderr.String())
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			if last != "" {
				return fmt.Errorf("exit status %d: %s", exitErr.ExitCode(), last)
			}
			return fmt.Errorf("exit status %d", exitErr.ExitCode())
		}
		return err
	}
	return nil
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return strings.TrimSpace(s)
}
