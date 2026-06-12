package bridge

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	corebridge "github.com/semsemyonoff/dwe/internal/core/bridge"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/project/project"
	"github.com/semsemyonoff/dwe/internal/shared/lock"

	"github.com/spf13/cobra"
)

// newDaemonCmd builds the hidden `dwe bridge daemon` foreground entry. It is
// the process the lifecycle ensure step spawns detached (stdout/stderr
// redirected to .dwe/bridge/daemon.log); running it manually in a terminal
// works the same, with diagnostics on stderr.
func newDaemonCmd(_ *cmdctx.RootFlags) *cobra.Command {
	var projectRoot string
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run the bridge daemon in the foreground (internal)",
		Long: `Run the host bridge daemon in the foreground.

Internal command: lifecycle commands spawn it detached via the bridge ensure
step. The daemon binds the project's .dwe/bridge transports, proxies shim
connections to forked dwe subprocesses, and exits on SIGTERM or once the
project stack has no running containers left (auto-stop).`,
		Hidden:       true,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemon(cmd, projectRoot)
		},
	}
	cmd.Flags().StringVar(&projectRoot, "project-root", "",
		"absolute path to the project root (required)")
	_ = cmd.MarkFlagRequired("project-root")
	return cmd
}

// runDaemon is the daemon process body: pidfile flock (single instance per
// project), transport listeners, auto-stop watcher, graceful shutdown on
// SIGTERM/SIGINT (design D6).
func runDaemon(cmd *cobra.Command, projectRoot string) error {
	if !filepath.IsAbs(projectRoot) {
		return fmt.Errorf("--project-root must be absolute, got %q", projectRoot)
	}
	logf := log.New(cmd.ErrOrStderr(), "", log.LstdFlags).Printf

	bridgeDir := corebridge.DefaultBridgeDir(projectRoot)
	// 0700 per design D3; created before the flock so lock.Acquire's more
	// permissive MkdirAll never decides the directory mode.
	if err := os.MkdirAll(bridgeDir, 0o700); err != nil {
		return fmt.Errorf("creating bridge dir: %w", err)
	}

	pidLock, err := lock.Acquire(corebridge.PidPath(bridgeDir))
	if err != nil {
		if held, ok := errors.AsType[*lock.HeldError](err); ok {
			// Another daemon won the spawn race — single instance is
			// guaranteed by this flock, so exit cleanly (design D6).
			logf("bridge daemon: already running (pid %d), exiting", held.PID)
			return nil
		}
		return fmt.Errorf("acquiring daemon pidfile: %w", err)
	}
	defer func() { _ = pidLock.Release() }()

	// The auto-stop watcher needs the compose project identity and docker
	// binary; the daemon itself stays a stateless forwarder.
	cfg, err := config.LoadConfigOrWrap(filepath.Join(projectRoot, project.ConfigFilename))
	if err != nil {
		return err
	}
	projectName, err := config.ResolveComposeProjectName(projectRoot, cfg)
	if err != nil {
		return fmt.Errorf("resolving compose project name: %w", err)
	}
	dockerBin := config.DockerBin(cfg)

	daemon := corebridge.New(corebridge.Config{
		ProjectRoot:  projectRoot,
		BridgeDir:    bridgeDir,
		BindOverride: os.Getenv(corebridge.BindOverrideEnv),
		GatewayIP:    dockerGatewayIP(dockerBin),
		Logf:         logf,
	})
	if err := daemon.Start(); err != nil {
		return err
	}
	logf("bridge daemon: listening for project %q (pid %d, tcp port %d, sock %s)",
		projectName, os.Getpid(), daemon.Port(), daemon.SocketPath())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	subscribe, countRunning := corebridge.DockerAutoStopHooks(dockerBin, projectName)
	err = corebridge.RunAutoStop(ctx, corebridge.AutoStopConfig{
		Subscribe:    subscribe,
		CountRunning: countRunning,
		Logf:         logf,
	})
	switch {
	case err == nil:
		logf("bridge daemon: project stack is down, shutting down")
	case errors.Is(err, context.Canceled):
		logf("bridge daemon: received shutdown signal")
	default:
		logf("bridge daemon: auto-stop watcher failed: %v", err)
	}

	// Graceful shutdown (design D6): Close removes host.sock and the port
	// file but keeps the token; the deferred Release drops the flock.
	if err := daemon.Close(); err != nil {
		return err
	}
	logf("bridge daemon: stopped")
	return nil
}

// dockerGatewayIP returns a resolver for the default docker bridge network
// gateway — the extra TCP bind address needed on native Linux (design D3).
// Failures are non-fatal: the daemon logs and skips the extra bind (macOS
// has no such host interface at all).
func dockerGatewayIP(dockerBin string) func() (string, error) {
	return func() (string, error) {
		out, err := exec.Command(dockerBin, "network", "inspect", "bridge", //nolint:gosec
			"--format", "{{range .IPAM.Config}}{{println .Gateway}}{{end}}").Output()
		if err != nil {
			if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && len(exitErr.Stderr) > 0 {
				return "", errors.New(strings.TrimSpace(string(exitErr.Stderr)))
			}
			return "", err
		}
		// Multiple IPAM configs are possible (IPv4 + IPv6); the first
		// non-empty line is the IPv4 gateway.
		for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				return line, nil
			}
		}
		return "", nil
	}
}
