package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"devbox-cli/internal/config"
	"devbox-cli/internal/daemon"
	"devbox-cli/internal/docker"
	"devbox-cli/internal/lifecycle"
	"devbox-cli/internal/lock"
	"devbox-cli/internal/preflight"
	"devbox-cli/internal/usercommands/registry"

	"github.com/spf13/cobra"
)

// ErrUnknownService is returned by StopService (and per-service reset) when
// the named service does not exist in the config.
var ErrUnknownService = errors.New("unknown service")

// stopContainerFn is a seam for docker.StopContainer so tests can inject
// fake docker behavior without a real Docker daemon.
var stopContainerFn = docker.StopContainer

// StopServiceDeps carries all state needed by StopService and stopServiceLocked.
type StopServiceDeps struct {
	Cfg         *config.DevboxConfig
	CmdRegistry *registry.Registry // required for type: command preflight checks
	BaseDir     string
	ErrOut      io.Writer // preflight diagnostics writer
	// SkipPreflight honors the --skip-preflight flag (ignored by stopServiceLocked
	// because the caller already ran preflight).
	SkipPreflight bool
}

// StopService is the public entry point for `devbox stop <name>`.
// It validates that name is a known service, runs stop-stage preflight, acquires
// project locks, then delegates to stopServiceLocked.
func StopService(ctx context.Context, deps StopServiceDeps, name string) error {
	if _, ok := deps.Cfg.Services[name]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownService, name)
	}
	errOut := deps.ErrOut
	if errOut == nil {
		errOut = io.Discard
	}
	if err := preflight.Run(ctx, deps.Cfg, deps.CmdRegistry, deps.BaseDir, "stop", deps.SkipPreflight, errOut); err != nil {
		return err
	}
	releaseLocks, err := lock.AcquireProjectLocks(deps.BaseDir)
	if err != nil {
		return err
	}
	defer releaseLocks()
	return stopServiceLocked(ctx, deps, name)
}

// stopServiceLocked is the package-internal core used by reset (Task 16), which
// has already run preflight and holds the project locks.
func stopServiceLocked(ctx context.Context, deps StopServiceDeps, name string) error {
	svc, ok := deps.Cfg.Services[name]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownService, name)
	}
	projectFull := deps.Cfg.Project.FullName()
	containerName, err := daemon.ResolveContainerName(projectFull, svc.Container)
	if err != nil {
		return fmt.Errorf("resolving container name for service %q: %w", name, err)
	}
	dockerBin := config.DockerBin(deps.Cfg)
	return stopContainerFn(ctx, dockerBin, containerName, docker.DefaultStopTimeoutSec)
}

func newStopCmd(flags *rootFlags) *cobra.Command {
	var yes bool
	var skipPreflight bool

	cmd := &cobra.Command{
		Use:   "stop [service]",
		Short: "Stop the project (full lifecycle: before-stop hooks → docker down → after-stop hooks)",
		Long: `Stop the project driven by devbox/lifecycle.yml.

Execution order: before-stop hooks → docker down → after-stop hooks → final message.

When a service name is given, stops only that service's container directly via
'docker stop', bypassing compose. This works even after the service has been disabled.

Use 'devbox docker down' for a bare Docker Compose stop-and-remove without hooks.
Use 'devbox docker stop' for the low-level compose stop (no container removal).`,
		Example:      `  devbox stop\n  devbox stop postgres`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				// Full-stack stop via lifecycle.
				return lifecycle.RunStop(lifecycle.StopContext{
					Ctx:           cmd.Context(),
					ConfigPath:    flags.configPath,
					Yes:           yes,
					SkipPreflight: skipPreflight,
					ErrOut:        cmd.ErrOrStderr(),
				})
			}
			// Per-service stop.
			name := args[0]
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			reg, regErr := loadCommandRegistry(flags.configPath)
			if regErr != nil {
				reg = nil
			}
			baseDir := filepath.Dir(flags.configPath)
			deps := StopServiceDeps{
				Cfg:           cfg,
				CmdRegistry:   reg,
				BaseDir:       baseDir,
				ErrOut:        cmd.ErrOrStderr(),
				SkipPreflight: skipPreflight,
			}
			if regErr != nil {
				return fmt.Errorf("loading command registry: %w", regErr)
			}
			if err := StopService(cmd.Context(), deps, name); err != nil {
				return err
			}
			return nil
		},
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			configPath, _, err := completionConfigPath(flags, cmd)
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			names := make([]string, 0, len(cfg.Services))
			for n := range cfg.Services {
				names = append(names, n)
			}
			return names, cobra.ShellCompDirectiveNoFileComp
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation prompts inside hook steps")
	addSkipPreflightFlag(cmd, &skipPreflight)
	return cmd
}
