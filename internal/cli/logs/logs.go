// Package logs provides the `devbox logs <service>` command.
package logs

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"devbox-cli/internal/cli/cmdctx"
	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/project/services"
	"devbox-cli/internal/shared/daemon"
	"devbox-cli/internal/shared/docker"

	"github.com/spf13/cobra"
)

type logsOptions struct {
	tail   int
	since  string
	follow bool
}

// NewCmd builds the `devbox logs <service>` command.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	var opts logsOptions

	cmd := &cobra.Command{
		Use:   "logs <service>",
		Short: "Stream container logs for a service",
		Long: `Stream Docker container logs for a named project service.

In text mode (default), output is passed through unchanged from docker logs.
With --output json, emits NDJSON: one {"ts","stream","msg"} object per line.`,
		Example:      "  devbox logs myapp\n  devbox logs myapp --tail 100 --follow\n  devbox logs myapp --output json --since 5m",
		SilenceUsage: true,
		GroupID:      groupID,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogs(cmd, flags, args, opts)
		},
	}

	cmd.Flags().IntVar(&opts.tail, "tail", 50, "number of trailing log lines to show (0 = all)")
	cmd.Flags().StringVar(&opts.since, "since", "", "show logs since duration (e.g. 5m, 1h) or RFC3339 timestamp")
	cmd.Flags().BoolVarP(&opts.follow, "follow", "f", false, "stream new log lines as they arrive")

	return cmd
}

// ResolveLogsTarget loads the project config, validates the service name, and
// returns the resolved Docker container name and loaded config.
func ResolveLogsTarget(flags *cmdctx.RootFlags, serviceName string) (containerName string, cfg *config.DevboxConfig, err error) {
	cfg, err = config.LoadConfig(flags.ConfigPath)
	if err != nil {
		return "", nil, cmdctx.ErrWrap("project_invalid_config", err)
	}
	svc, ok := cfg.Services[serviceName]
	if !ok {
		known := services.SortedNames(cfg.Services)
		return "", cfg, cmdctx.Err("service_unknown", fmt.Sprintf("no service %q in project", serviceName)).
			WithHint("available: "+strings.Join(known, ", ")).
			WithDetail("requested", serviceName)
	}
	containerName, err = daemon.ResolveContainerName(cfg.Project.FullName(), svc.Container)
	if err != nil {
		return "", cfg, cmdctx.ErrWrap("container_name_invalid", err)
	}
	return containerName, cfg, nil
}

func buildDockerLogsArgs(containerName string, opts logsOptions) []string {
	args := []string{"logs"}
	if opts.tail == 0 {
		args = append(args, "--tail", "all")
	} else {
		args = append(args, "--tail", strconv.Itoa(opts.tail))
	}
	if opts.since != "" {
		args = append(args, "--since", opts.since)
	}
	if opts.follow {
		args = append(args, "--follow")
	}
	args = append(args, containerName)
	return args
}

func runLogs(cmd *cobra.Command, flags *cmdctx.RootFlags, args []string, opts logsOptions) error {
	containerName, cfg, err := ResolveLogsTarget(flags, args[0])
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	if opts.follow {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
	}

	if flags.Output == "json" {
		return runLogsJSON(cmd, ctx, args[0], opts, containerName, cfg)
	}
	return runLogsText(cmd, ctx, args[0], opts, containerName, cfg)
}

func runLogsText(cmd *cobra.Command, ctx context.Context, serviceName string, opts logsOptions, containerName string, cfg *config.DevboxConfig) error {
	dockerArgs := buildDockerLogsArgs(containerName, opts)
	dockerCmd := exec.CommandContext(ctx, config.DockerBin(cfg), dockerArgs...) //nolint:gosec
	dockerCmd.Stdout = cmd.OutOrStdout()

	// Tee stderr: stream it to the caller's stderr AND capture it so we can
	// detect "No such container" after the process exits.
	var stderrBuf strings.Builder
	dockerCmd.Stderr = io.MultiWriter(cmd.ErrOrStderr(), &stderrBuf)

	if opts.follow {
		dockerCmd.Cancel = func() error {
			if dockerCmd.Process == nil {
				return nil
			}
			return dockerCmd.Process.Signal(os.Interrupt)
		}
		dockerCmd.WaitDelay = 3 * time.Second
	}

	if runErr := dockerCmd.Run(); runErr != nil {
		if isCleanFollowExit(runErr, ctx) {
			return nil
		}
		if docker.IsNoSuchContainerErr(stderrBuf.String()) {
			return cmdctx.Err("container_not_found",
				fmt.Sprintf("container for service %q not found — is the project deployed?", serviceName)).
				WithDetail("container", containerName)
		}
		return fmt.Errorf("docker logs: %w", runErr)
	}
	return nil
}

// isCleanFollowExit reports whether err represents a graceful termination
// from signal handling in --follow mode: SIGINT (exit 130), force-kill after
// WaitDelay expiry, or context cancellation with a negative exit code.
func isCleanFollowExit(err error, ctx context.Context) bool {
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		if exitErr.ExitCode() == 130 {
			return true
		}
		if exitErr.ExitCode() < 0 && ctx.Err() != nil {
			return true
		}
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		return true
	}
	if strings.Contains(err.Error(), "signal: interrupt") {
		return true
	}
	return false
}

func runLogsJSON(cmd *cobra.Command, ctx context.Context, _ string, opts logsOptions, containerName string, cfg *config.DevboxConfig) error {

	// Build base args and insert --timestamps right after "logs".
	base := buildDockerLogsArgs(containerName, opts)
	dockerArgs := make([]string, 0, len(base)+1)
	dockerArgs = append(dockerArgs, base[0]) // "logs"
	dockerArgs = append(dockerArgs, "--timestamps")
	dockerArgs = append(dockerArgs, base[1:]...)

	dockerCmd := exec.CommandContext(ctx, config.DockerBin(cfg), dockerArgs...) //nolint:gosec

	if opts.follow {
		dockerCmd.Cancel = func() error {
			if dockerCmd.Process == nil {
				return nil
			}
			return dockerCmd.Process.Signal(os.Interrupt)
		}
		dockerCmd.WaitDelay = 3 * time.Second
	}

	stdoutPipe, err := dockerCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("docker logs: stdout pipe: %w", err)
	}
	stderrPipe, err := dockerCmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("docker logs: stderr pipe: %w", err)
	}

	if err := dockerCmd.Start(); err != nil {
		return fmt.Errorf("docker logs: %w", err)
	}

	ch := make(chan logLineJSON, 16)
	eg, egCtx := errgroup.WithContext(ctx)

	readPipe := func(pipe io.ReadCloser, stream string) func() error {
		return func() (retErr error) {
			defer func() {
				if r := recover(); r != nil {
					retErr = fmt.Errorf("reader panic in %s stream: %v", stream, r)
				}
			}()
			sc := bufio.NewScanner(pipe)
			sc.Buffer(make([]byte, 64*1024), 1024*1024)
			for sc.Scan() {
				if egCtx.Err() != nil {
					return egCtx.Err()
				}
				select {
				case ch <- parseLine(stream, sc.Text()):
				case <-egCtx.Done():
					return egCtx.Err()
				}
			}
			if scErr := sc.Err(); scErr != nil {
				if errors.Is(scErr, bufio.ErrTooLong) {
					ts := time.Now().UTC().Format(time.RFC3339Nano)
					select {
					case ch <- logLineJSON{Ts: ts, Stream: stream, Msg: "<truncated: line exceeded 1MB>"}:
					case <-egCtx.Done():
					}
					return nil
				}
				return scErr
			}
			return nil
		}
	}

	eg.Go(readPipe(stdoutPipe, "stdout"))
	eg.Go(readPipe(stderrPipe, "stderr"))

	// Pipe-closer goroutine: when egCtx is done (cancelled by signal or after
	// all readers finish), close both pipes. This unblocks any sc.Scan() that
	// is waiting for data when the subprocess has orphaned child processes
	// (e.g. a shell script that forked a "sleep" child) still holding the
	// pipe write-end open. In the normal-exit case egCtx is cancelled by
	// errgroup.Wait() returning; closing already-EOF pipes is a safe no-op.
	go func() {
		<-egCtx.Done()
		_ = stdoutPipe.Close()
		_ = stderrPipe.Close()
	}()

	// Closer goroutine: waits for both readers, then closes ch exactly once.
	// The write to egErr before close(ch) happens-before the read after the
	// drain loop exits, satisfying the Go memory model.
	var egErr error
	go func() {
		egErr = eg.Wait()
		close(ch)
	}()

	enc := json.NewEncoder(cmd.OutOrStdout())
	for rec := range ch {
		_ = enc.Encode(rec) // drain always completes; ignore write errors
	}

	// egErr was written before close(ch) → happens-before the loop exit above.
	if egErr != nil {
		// Ignore context-cancellation errors; they are normal shutdown signals.
		var isCancelled bool
		if egCtx.Err() != nil {
			isCancelled = true
		}
		if !isCancelled {
			return fmt.Errorf("docker logs: stream reader: %w", egErr)
		}
	}

	if cmdErr := dockerCmd.Wait(); cmdErr != nil {
		if isCleanFollowExit(cmdErr, ctx) {
			return nil
		}
		return fmt.Errorf("docker logs: %w", cmdErr)
	}
	return nil
}
