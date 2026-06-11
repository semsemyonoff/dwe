package bridge

import (
	"bufio"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/semsemyonoff/dwe/internal/shared/docker"
)

// Auto-stop defaults (design D6): the daemon has NO connection-idle timeout
// — a dead daemon cannot be revived from inside a container, so an idle
// timeout would brick hooks for an active developer. The correct idle
// criterion is "the stack is actually down": zero labeled running
// containers.
const (
	// defaultAutoStopGrace delays the first check after daemon startup —
	// compose up needs time to start labeled containers.
	defaultAutoStopGrace = 10 * time.Second
	// defaultAutoStopPoll is the fallback `docker ps` cadence; the events
	// stream may drop without the daemon noticing.
	defaultAutoStopPoll = 60 * time.Second
)

// AutoStopConfig drives RunAutoStop. Subscribe and CountRunning are the
// injectable docker seams; only CountRunning is required.
type AutoStopConfig struct {
	// Subscribe opens the project-filtered docker events stream. Every
	// received value is a hint to recheck the container count; the channel
	// closing means the stream dropped (polling continues regardless).
	// nil or a failing Subscribe degrades to poll-only.
	Subscribe func(ctx context.Context) (<-chan struct{}, error)
	// CountRunning returns the number of running containers labeled with
	// the project's compose label. Errors are logged and the check retried
	// on the next wake-up — a docker hiccup must not stop the daemon.
	CountRunning func(ctx context.Context) (int, error)
	// Grace delays the first check; 0 means defaultAutoStopGrace.
	Grace time.Duration
	// PollInterval is the fallback poll cadence; 0 means defaultAutoStopPoll.
	PollInterval time.Duration
	// Logf receives watcher diagnostics; nil discards them.
	Logf func(format string, args ...any)
}

// RunAutoStop blocks until the project stack is down — zero labeled running
// containers (returns nil: the caller shuts the daemon down) — or ctx is
// cancelled (returns ctx.Err(): SIGTERM-driven shutdown).
func RunAutoStop(ctx context.Context, cfg AutoStopConfig) error {
	if cfg.CountRunning == nil {
		return errors.New("bridge: auto-stop requires a CountRunning probe")
	}
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	grace := cfg.Grace
	if grace <= 0 {
		grace = defaultAutoStopGrace
	}
	poll := cfg.PollInterval
	if poll <= 0 {
		poll = defaultAutoStopPoll
	}

	// Startup grace before the first check.
	graceTimer := time.NewTimer(grace)
	defer graceTimer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-graceTimer.C:
	}

	var events <-chan struct{}
	if cfg.Subscribe != nil {
		ch, err := cfg.Subscribe(ctx)
		if err != nil {
			logf("bridge: docker events subscription failed: %v; relying on ps polling", err)
		} else {
			events = ch
		}
	}

	stackDown := func() bool {
		n, err := cfg.CountRunning(ctx)
		if err != nil {
			if ctx.Err() == nil {
				logf("bridge: counting project containers: %v", err)
			}
			return false
		}
		return n == 0
	}

	if stackDown() {
		logf("bridge: no running project containers — auto-stop")
		return nil
	}

	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-events:
			if !ok {
				// Stream dropped; a nil channel blocks forever in select,
				// leaving the poll ticker as the only wake-up source.
				events = nil
				logf("bridge: docker events stream ended; falling back to ps polling")
				continue
			}
		case <-ticker.C:
		}
		if stackDown() {
			logf("bridge: no running project containers — auto-stop")
			return nil
		}
	}
}

// dockerEventsArgs builds the `docker events` argv watching container
// lifecycle events for one compose project.
func dockerEventsArgs(composeProject string) []string {
	return []string{
		"events",
		"--filter", "type=container",
		"--filter", "label=" + docker.ComposeProjectLabel + "=" + composeProject,
		"--format", "{{.Status}}",
	}
}

// dockerPSArgs builds the `docker ps` argv listing running container IDs for
// one compose project.
func dockerPSArgs(composeProject string) []string {
	return []string{
		"ps",
		"--quiet",
		"--filter", "status=running",
		"--filter", "label=" + docker.ComposeProjectLabel + "=" + composeProject,
	}
}

// DockerAutoStopHooks returns the production Subscribe/CountRunning probes
// for AutoStopConfig, both backed by the docker CLI and scoped to the
// compose project's label (the same identity every other dwe probe matches
// on — see internal/shared/docker labels).
func DockerAutoStopHooks(dockerBin, composeProject string) (
	subscribe func(ctx context.Context) (<-chan struct{}, error),
	countRunning func(ctx context.Context) (int, error),
) {
	subscribe = func(ctx context.Context) (<-chan struct{}, error) {
		cmd := exec.CommandContext(ctx, dockerBin, dockerEventsArgs(composeProject)...) //nolint:gosec
		out, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		ch := make(chan struct{}, 1)
		go func() {
			defer close(ch)
			scanner := bufio.NewScanner(out)
			for scanner.Scan() {
				// Coalesce: one pending wake-up is enough, the receiver
				// re-counts containers anyway.
				select {
				case ch <- struct{}{}:
				default:
				}
			}
			_ = cmd.Wait()
		}()
		return ch, nil
	}
	countRunning = func(ctx context.Context) (int, error) {
		out, err := exec.CommandContext(ctx, dockerBin, dockerPSArgs(composeProject)...).Output() //nolint:gosec
		if err != nil {
			if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && len(exitErr.Stderr) > 0 {
				return 0, errors.New(strings.TrimSpace(string(exitErr.Stderr)))
			}
			return 0, err
		}
		count := 0
		for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
			if strings.TrimSpace(line) != "" {
				count++
			}
		}
		return count, nil
	}
	return subscribe, countRunning
}
