package builtin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const shellDefaultTimeout = 10 * time.Second

type shellBuiltin struct{}

func (shellBuiltin) Validate(with map[string]any) error {
	cmd := getStringParam(with, "cmd", "")
	if cmd == "" {
		return errors.New("missing required param 'cmd'")
	}
	if _, err := getDurationParam(with, "timeout", shellDefaultTimeout); err != nil {
		return err
	}
	return nil
}

func (shellBuiltin) Describe(with map[string]any) string {
	cmd := getStringParam(with, "cmd", "")
	return fmt.Sprintf("builtin: shell(cmd=%s)", cmd)
}

func (shellBuiltin) Run(ctx context.Context, with map[string]any, _ ExecContext) error {
	cmdStr := getStringParam(with, "cmd", "")
	timeout, err := getDurationParam(with, "timeout", shellDefaultTimeout)
	if err != nil {
		return err
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var stderr bytes.Buffer
	// Hardcoded sh -c matches deploy/condition `when:` convention (see CLAUDE.md).
	c := exec.CommandContext(runCtx, "sh", "-c", cmdStr)
	c.Stderr = &stderr
	// On timeout, exec.CommandContext SIGKILLs only the direct `sh`. Orphan
	// descendants (e.g. `sh -c "sleep 5"` leaves `sleep` running) keep
	// stderr's read end alive and Wait() blocks until they exit. WaitDelay
	// caps that wait — after the grace period Go closes the pipes itself
	// and Wait returns.
	c.WaitDelay = 100 * time.Millisecond

	err = c.Run()
	if runCtx.Err() == context.DeadlineExceeded {
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
