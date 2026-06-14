// Package logs provides the `dwe logs <service>` command.
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

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/project/services"
	"github.com/semsemyonoff/dwe/internal/shared/docker"

	"github.com/spf13/cobra"
)

type logsOptions struct {
	tail   int
	since  string
	follow bool
}

// LookupContainerFn resolves a service's real container name from the compose
// project + service labels (docker.LookupServiceContainer). It is a seam so
// tests can resolve names without spawning `docker ps`. Returns "" when the
// service has no container. Exported only because the logs tests live in an
// external test package.
var LookupContainerFn = docker.LookupServiceContainer

// logTarget pairs a dwe service name with its resolved Docker container name,
// used by whole-stack (`dwe logs` with no argument) NDJSON multiplexing.
type logTarget struct {
	service   string
	container string
}

// NewCmd builds the `dwe logs <service>` command.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	var opts logsOptions

	cmd := &cobra.Command{
		Use:   "logs [service]",
		Short: "Stream container logs for a service (or the whole stack)",
		Long: `Stream Docker container logs for a project service.

With a service name, streams that one service's container. With NO argument,
streams the logs of the whole stack (all enabled services), like
'docker compose logs'.

The target container is resolved by its compose project + service labels, so it
works regardless of any container_name override or compose's default
"<project>-<service>-<index>" naming.

In text mode (default), output is passed through unchanged. With --output json,
emits NDJSON: one {"ts","stream","msg"} object per line (whole-stack mode adds a
"service" field).`,
		Example:           "  dwe logs\n  dwe logs myapp\n  dwe logs myapp --tail 100 --follow\n  dwe logs --output json --since 5m",
		SilenceUsage:      true,
		GroupID:           groupID,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: cmdctx.ServiceNameCompletion(flags),
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
// returns the real Docker container name (resolved by the compose project +
// service labels, NOT guessed as "<project>-<container>") and the loaded config.
// A known service with no running/exited container yields a container_not_found
// error rather than a name that docker logs would later reject.
func ResolveLogsTarget(flags *cmdctx.RootFlags, serviceName string) (containerName string, cfg *config.DweConfig, err error) {
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
	projectFull, err := config.ResolveComposeProjectName(flags.ProjectRoot(), cfg)
	if err != nil {
		return "", cfg, cmdctx.ErrWrap("project_invalid_config", err)
	}
	// nil processEnv: the single-service docker logs action (runLogsText /
	// runLogsJSON) runs with the inherited environment, so the probe matches.
	containerName, err = LookupContainerFn(config.DockerBin(cfg), nil, projectFull, svc.Container)
	if err != nil {
		if docker.IsDaemonUnavailableErr(err.Error()) {
			return "", cfg, cmdctx.Err("docker_unavailable", "cannot connect to Docker daemon — is Docker running?")
		}
		return "", cfg, cmdctx.ErrWrap("docker_error", err)
	}
	if containerName == "" {
		return "", cfg, cmdctx.Err("container_not_found",
			fmt.Sprintf("container for service %q not found — is the project deployed?", serviceName)).
			WithDetail("service", serviceName)
	}
	return containerName, cfg, nil
}

// logsFlagArgs renders the --tail/--since/--follow flag portion shared by the
// per-container `docker logs` argv and the whole-stack `docker compose logs`
// argv. It contains neither the "logs" subcommand nor any container/service.
func logsFlagArgs(opts logsOptions) []string {
	var args []string
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
	return args
}

func buildDockerLogsArgs(containerName string, opts logsOptions) []string {
	args := append([]string{"logs"}, logsFlagArgs(opts)...)
	return append(args, containerName)
}

// validateLogsFlags checks --tail and --since values before invoking docker.
func validateLogsFlags(opts logsOptions) error {
	if opts.tail < 0 {
		return cmdctx.Err("invalid_tail", fmt.Sprintf("--tail must be >= 0, got %d", opts.tail)).
			WithDetail("value", opts.tail)
	}
	if opts.since != "" {
		_, dErr := time.ParseDuration(opts.since)
		_, tErr := time.Parse(time.RFC3339, opts.since)
		if dErr != nil && tErr != nil {
			return cmdctx.Err("invalid_since", fmt.Sprintf("--since %q is not a valid duration (e.g. 5m) or RFC3339 timestamp", opts.since)).
				WithHint("use a duration like '5m' or '1h', or an RFC3339 timestamp like '2026-01-01T00:00:00Z'").
				WithDetail("value", opts.since)
		}
	}
	return nil
}

func runLogs(cmd *cobra.Command, flags *cmdctx.RootFlags, args []string, opts logsOptions) error {
	if err := validateLogsFlags(opts); err != nil {
		return err
	}

	ctx := cmd.Context()
	if opts.follow {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
	}

	// No service argument → stream the whole stack (all enabled services).
	if len(args) == 0 {
		return runLogsAllServices(cmd, ctx, flags, opts)
	}

	containerName, cfg, err := ResolveLogsTarget(flags, args[0])
	if err != nil {
		return err
	}

	if flags.Output == "json" {
		return runLogsJSON(cmd, ctx, args[0], opts, containerName, cfg)
	}
	return runLogsText(cmd, ctx, args[0], opts, containerName, cfg)
}

// runLogsAllServices streams logs for the whole stack (every enabled service).
//
// The two output modes deliberately use different mechanisms, but cover the same
// "enabled stack" service set: TEXT delegates to `docker compose logs` (which
// streams the services in the enabled compose overlays, with native colored
// prefixes and follow handling — not worth re-implementing), while JSON
// multiplexes per-container `docker logs` over the enabled services
// (collectLogTargets) so each NDJSON record keeps a clean per-service/per-stream
// attribution that `docker compose logs`' prefixed text cannot give reliably.
func runLogsAllServices(cmd *cobra.Command, ctx context.Context, flags *cmdctx.RootFlags, opts logsOptions) error {
	cfg, err := config.LoadConfig(flags.ConfigPath)
	if err != nil {
		return cmdctx.ErrWrap("project_invalid_config", err)
	}
	baseDir := flags.ProjectRoot()
	dockerCfg, err := config.LoadDockerConfigOrEmpty(baseDir, cfg)
	if err != nil {
		return cmdctx.ErrWrap("project_invalid_config", err)
	}
	compose := docker.NewCompose(cfg, dockerCfg, baseDir)

	if flags.Output == "json" {
		return runLogsAllJSON(cmd, ctx, opts, cfg, compose)
	}
	return runLogsAllText(cmd, ctx, opts, compose)
}

func runLogsText(cmd *cobra.Command, ctx context.Context, serviceName string, opts logsOptions, containerName string, cfg *config.DweConfig) error {
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
		if opts.follow && isCleanFollowExit(runErr, ctx) {
			return nil
		}
		captured := stderrBuf.String()
		if docker.IsNoSuchContainerErr(captured) {
			return cmdctx.Err("container_not_found",
				fmt.Sprintf("container for service %q not found — is the project deployed?", serviceName)).
				WithDetail("container", containerName)
		}
		if docker.IsDaemonUnavailableErr(captured) {
			return cmdctx.Err("docker_unavailable", "cannot connect to Docker daemon — is Docker running?")
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

func runLogsJSON(cmd *cobra.Command, ctx context.Context, serviceName string, opts logsOptions, containerName string, cfg *config.DweConfig) error {
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

	// Tee stderr so we can scan it for known error patterns after the command
	// exits, while still streaming its content as NDJSON records.
	var stderrCapture strings.Builder
	teedStderr := io.TeeReader(stderrPipe, &stderrCapture)

	ch := make(chan logLineJSON, 16)
	eg, egCtx := errgroup.WithContext(ctx)

	// Single-service mode: no service tag (envelope stays {ts,stream,msg}).
	eg.Go(streamReaderInto(ch, egCtx, stdoutPipe, "stdout", ""))
	eg.Go(streamReaderInto(ch, egCtx, teedStderr, "stderr", ""))

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
	// Check the *parent* ctx, not egCtx: errgroup auto-cancels egCtx whenever
	// eg.Wait returns, so egCtx.Err() != nil on every error path and would
	// silently swallow real reader errors. Only suppress when the user-facing
	// ctx (signal.NotifyContext for --follow, or cmd.Context() otherwise) was
	// actually cancelled.
	if egErr != nil && ctx.Err() == nil {
		return fmt.Errorf("docker logs: stream reader: %w", egErr)
	}

	if cmdErr := dockerCmd.Wait(); cmdErr != nil {
		if opts.follow && isCleanFollowExit(cmdErr, ctx) {
			return nil
		}
		captured := stderrCapture.String()
		if docker.IsNoSuchContainerErr(captured) {
			return cmdctx.Err("container_not_found",
				fmt.Sprintf("container for service %q not found — is the project deployed?", serviceName)).
				WithDetail("container", containerName)
		}
		if docker.IsDaemonUnavailableErr(captured) {
			return cmdctx.Err("docker_unavailable", "cannot connect to Docker daemon — is Docker running?")
		}
		return fmt.Errorf("docker logs: %w", cmdErr)
	}
	return nil
}

// streamReaderInto returns an errgroup-compatible reader that consumes lines
// from pipe and sends parsed NDJSON records (tagged with service, which is empty
// in single-service mode) into ch until EOF or egCtx cancellation. Lines longer
// than 1 MB are truncated rather than buffered unbounded.
func streamReaderInto(ch chan<- logLineJSON, egCtx context.Context, pipe io.Reader, stream, service string) func() error {
	return func() (retErr error) {
		defer func() {
			if r := recover(); r != nil {
				retErr = fmt.Errorf("reader panic in %s stream: %v", stream, r)
			}
		}()
		const maxLineBytes = 1024 * 1024
		const truncSuffix = "<truncated: line exceeded 1MB>"
		send := func(msg string) error {
			rec := parseLine(stream, msg)
			rec.Service = service
			select {
			case ch <- rec:
				return nil
			case <-egCtx.Done():
				return egCtx.Err()
			}
		}
		br := bufio.NewReaderSize(pipe, maxLineBytes)
		for {
			if egCtx.Err() != nil {
				return egCtx.Err()
			}
			chunk, err := br.ReadSlice('\n')
			if errors.Is(err, bufio.ErrBufferFull) {
				prefix := append([]byte(nil), chunk...)
			discard:
				for {
					_, dErr := br.ReadSlice('\n')
					switch {
					case dErr == nil:
						break discard
					case errors.Is(dErr, bufio.ErrBufferFull):
						continue
					case errors.Is(dErr, io.EOF):
						break discard
					default:
						return dErr
					}
				}
				if serr := send(strings.TrimRight(string(prefix), "\r\n") + truncSuffix); serr != nil {
					return serr
				}
				continue
			}
			if len(chunk) > 0 {
				if serr := send(strings.TrimRight(string(chunk), "\r\n")); serr != nil {
					return serr
				}
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
		}
	}
}

// runLogsAllText streams the whole stack's logs via `docker compose logs`, which
// natively multiplexes and prefixes every enabled service. The container-name
// label mismatch is a non-issue here: compose resolves its own services.
func runLogsAllText(cmd *cobra.Command, ctx context.Context, opts logsOptions, compose *docker.Compose) error {
	bin := compose.BinName()
	args := compose.BuildArgs("logs", logsFlagArgs(opts)...)
	dockerCmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec
	dockerCmd.Dir = compose.BaseDir
	dockerCmd.Env = compose.BuildEnv()
	dockerCmd.Stdout = cmd.OutOrStdout()

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
		if opts.follow && isCleanFollowExit(runErr, ctx) {
			return nil
		}
		if docker.IsDaemonUnavailableErr(stderrBuf.String()) {
			return cmdctx.Err("docker_unavailable", "cannot connect to Docker daemon — is Docker running?")
		}
		return fmt.Errorf("docker compose logs: %w", runErr)
	}
	return nil
}

// collectLogTargets resolves the real container name for every enabled service,
// in deterministic order, skipping services that have no container. Each lookup
// spawns a `docker ps`, so they run concurrently (bounded) instead of serially.
// processEnv is the environment for those probes and MUST equal the env used for
// the subsequent `docker logs` action so probe and action target the same
// daemon. Returns an error only when a docker probe itself fails (e.g. daemon
// unreachable).
func collectLogTargets(cfg *config.DweConfig, processEnv []string, projectName, dockerBin string) ([]logTarget, error) {
	names := services.SortedNames(cfg.Services)
	resolved := make([]string, len(names))
	eg := new(errgroup.Group)
	eg.SetLimit(8)
	for i, name := range names {
		svc := cfg.Services[name]
		if !svc.Enabled && !svc.Required {
			continue
		}
		i, container := i, svc.Container
		eg.Go(func() error {
			c, err := LookupContainerFn(dockerBin, processEnv, projectName, container)
			if err != nil {
				return err
			}
			resolved[i] = c // "" when the service has no container
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	var targets []logTarget
	for i, name := range names {
		if resolved[i] != "" {
			targets = append(targets, logTarget{service: name, container: resolved[i]})
		}
	}
	return targets, nil
}

// runLogsAllJSON streams the whole stack as NDJSON by multiplexing a per-service
// `docker logs --timestamps`, tagging each record with its service. Subprocesses
// are all started first (phase 1) so a start failure never strands reader
// goroutines, then readers + closers run (phase 2) mirroring runLogsJSON.
func runLogsAllJSON(cmd *cobra.Command, ctx context.Context, opts logsOptions, cfg *config.DweConfig, compose *docker.Compose) error {
	dockerBin := compose.BinName()
	// The label probe and the docker-logs action below must share one daemon, so
	// both use the compose process env (DOCKER_HOST/DOCKER_CONTEXT from docker.yml).
	processEnv := compose.BuildEnv()
	targets, err := collectLogTargets(cfg, processEnv, compose.ProjectName, dockerBin)
	if err != nil {
		if docker.IsDaemonUnavailableErr(err.Error()) {
			return cmdctx.Err("docker_unavailable", "cannot connect to Docker daemon — is Docker running?")
		}
		return fmt.Errorf("resolving service containers: %w", err)
	}
	if len(targets) == 0 {
		// Nothing deployed — emit an empty (valid) NDJSON stream.
		return nil
	}

	// Phase 1: start every subprocess and grab its pipes. On any failure, tear
	// down what was started — no reader goroutines exist yet.
	type stream struct {
		pipe    io.ReadCloser
		name    string
		service string
	}
	var cmds []*exec.Cmd
	var streams []stream
	cleanup := func() {
		for _, c := range cmds {
			if c.Process != nil {
				_ = c.Process.Kill()
			}
			_ = c.Wait()
		}
	}
	for _, tgt := range targets {
		base := buildDockerLogsArgs(tgt.container, opts)
		dockerArgs := make([]string, 0, len(base)+1)
		dockerArgs = append(dockerArgs, base[0], "--timestamps")
		dockerArgs = append(dockerArgs, base[1:]...)

		dockerCmd := exec.CommandContext(ctx, dockerBin, dockerArgs...) //nolint:gosec
		dockerCmd.Env = processEnv
		if opts.follow {
			dockerCmd.Cancel = func() error {
				if dockerCmd.Process == nil {
					return nil
				}
				return dockerCmd.Process.Signal(os.Interrupt)
			}
			dockerCmd.WaitDelay = 3 * time.Second
		}
		stdoutPipe, perr := dockerCmd.StdoutPipe()
		if perr != nil {
			cleanup()
			return fmt.Errorf("docker logs: stdout pipe: %w", perr)
		}
		stderrPipe, perr := dockerCmd.StderrPipe()
		if perr != nil {
			// stdoutPipe is already open on this not-yet-started cmd; cleanup()
			// only reaps started cmds, so close it here to avoid an fd leak.
			_ = stdoutPipe.Close()
			cleanup()
			return fmt.Errorf("docker logs: stderr pipe: %w", perr)
		}
		if serr := dockerCmd.Start(); serr != nil {
			cleanup()
			return fmt.Errorf("docker logs: %w", serr)
		}
		cmds = append(cmds, dockerCmd)
		streams = append(streams,
			stream{pipe: stdoutPipe, name: "stdout", service: tgt.service},
			stream{pipe: stderrPipe, name: "stderr", service: tgt.service},
		)
	}

	// Phase 2: readers + coordinator goroutines.
	// All readers share one egCtx: a hard read error from any container cancels
	// the rest. That is intentional — reader errors are rare pipe failures (not
	// normal EOF), and records already drained to stdout are preserved; the
	// alternative (isolating each reader) would mask a genuine stream fault.
	ch := make(chan logLineJSON, 16)
	eg, egCtx := errgroup.WithContext(ctx)
	for _, s := range streams {
		eg.Go(streamReaderInto(ch, egCtx, s.pipe, s.name, s.service))
	}
	go func() {
		<-egCtx.Done()
		for _, s := range streams {
			_ = s.pipe.Close()
		}
	}()
	var egErr error
	go func() {
		egErr = eg.Wait()
		close(ch)
	}()

	enc := json.NewEncoder(cmd.OutOrStdout())
	for rec := range ch {
		_ = enc.Encode(rec)
	}

	// Reap EVERY subprocess before returning, on all paths — including the
	// reader-error path below — so a failed reader never strands the remaining
	// docker-logs children as zombies. egCtx is already cancelled here (eg.Wait
	// returned), so the pipe-closer goroutine has closed the pipes, which makes
	// each child exit promptly (broken pipe) and lets Wait return.
	var waitErr error
	for _, c := range cmds {
		if werr := c.Wait(); werr != nil {
			if opts.follow && isCleanFollowExit(werr, ctx) {
				continue
			}
			// Containers were resolved as existing; a Wait error here is a race
			// (container removed mid-stream) or transient. Record the first one
			// unless the user-facing ctx was cancelled.
			if ctx.Err() == nil && waitErr == nil {
				waitErr = werr
			}
		}
	}

	if egErr != nil && ctx.Err() == nil {
		return fmt.Errorf("docker logs: stream reader: %w", egErr)
	}
	if waitErr != nil {
		return fmt.Errorf("docker logs: %w", waitErr)
	}
	return nil
}
