package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	lifecyclepkg "github.com/semsemyonoff/dwe/internal/core/workflow/lifecycle"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
	"github.com/semsemyonoff/dwe/internal/shared/lock"
	"github.com/semsemyonoff/dwe/internal/shared/promptcache"

	"github.com/spf13/cobra"
)

// ErrUnknownService is returned by StopService (and per-service reset) when
// the named service does not exist in the config.
var ErrUnknownService = errors.New("unknown service")

// stopContainerFn is a seam for docker.StopContainer so tests can inject
// fake docker behavior without a real Docker daemon.
var stopContainerFn = docker.StopContainer

// lookupContainerFn resolves a service's real container name from the compose
// project + service labels (docker.LookupServiceContainer). It is a seam so
// tests can resolve names without spawning docker. Returns "" when the service
// has no container (never deployed or already removed).
var lookupContainerFn = docker.LookupServiceContainer

// StopServiceDeps carries all state needed by StopService and stopServiceLocked.
type StopServiceDeps struct {
	Cfg            *config.DweConfig
	CmdRegistry    *registry.Registry // nil-tolerant; preflight surfaces unknown-command diagnostics
	CmdRegistryErr error              // deferred registry-load error; checked after preflight succeeds
	BaseDir        string
	ErrOut         io.Writer // preflight diagnostics writer
	// SkipPreflight honors the --skip-preflight flag (ignored by stopServiceLocked
	// because the caller already ran preflight).
	SkipPreflight bool
}

// StopService is the public entry point for `dwe stop <name>`.
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
	if err := preflightRun(ctx, deps.Cfg, deps.CmdRegistry, deps.BaseDir, "stop", deps.SkipPreflight, errOut); err != nil {
		return err
	}
	if deps.CmdRegistryErr != nil {
		return fmt.Errorf("loading command registry: %w", deps.CmdRegistryErr)
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
	containerName, err := resolveServiceContainer(deps.BaseDir, deps.Cfg, name)
	if err != nil {
		return err
	}
	if containerName == "" {
		// No container exists for this service (never deployed or already
		// removed). Stop is idempotent — nothing to do.
		return nil
	}
	dockerBin := config.DockerBin(deps.Cfg)
	return stopContainerFn(ctx, dockerBin, containerName, docker.DefaultStopTimeoutSec)
}

// resolveServiceContainer validates that name is a known service and resolves
// its real docker container name by matching the compose project + service
// labels (NOT by guessing "<project>-<container>", which only works when the
// user pins container_name to that exact string). Shared by the per-service
// stop and restart paths (both bypass compose and act on the container
// directly). Returns "" when the service is known but has no container.
func resolveServiceContainer(baseDir string, cfg *config.DweConfig, name string) (string, error) {
	svc, ok := cfg.Services[name]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownService, name)
	}
	projectFull, err := config.ResolveComposeProjectName(baseDir, cfg)
	if err != nil {
		return "", fmt.Errorf("resolving compose project name: %w", err)
	}
	// nil processEnv: the follow-up docker stop/restart (via StopContainer /
	// RestartContainer) run with the inherited environment, so the probe must
	// too — otherwise probe and action could target different daemons.
	containerName, err := lookupContainerFn(config.DockerBin(cfg), nil, projectFull, svc.Container)
	if err != nil {
		return "", fmt.Errorf("resolving container for service %q: %w", name, err)
	}
	return containerName, nil
}

// NewStopCmd builds the `dwe stop` cobra command.
func NewStopCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	var yes bool
	var skipPreflight bool

	cmd := &cobra.Command{
		Use:   "stop [service]",
		Short: "Stop the project (full lifecycle: before-stop hooks → docker down → after-stop hooks)",
		Long: `Stop the project driven by workspace/lifecycle.yml.

Execution order: before-stop hooks → docker down → after-stop hooks → final message.

When a service name is given, stops only that service's container directly via
'docker stop', bypassing compose. This works even after the service has been disabled.

Use 'dwe docker down' for a bare Docker Compose stop-and-remove without hooks.
Use 'dwe docker stop' for the low-level compose stop (no container removal).`,
		Example: `  dwe stop
  dwe stop postgres`,
		Args:         cobra.MaximumNArgs(1),
		GroupID:      groupID,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				// Full-stack stop via lifecycle.
				if err := lifecyclepkg.RunStop(lifecyclepkg.StopContext{
					Ctx:           cmd.Context(),
					ConfigPath:    flags.ConfigPath,
					Yes:           yes,
					SkipPreflight: skipPreflight,
					ErrOut:        cmd.ErrOrStderr(),
					Translator:    flags.I18n,
					Locale:        flags.Locale,
					OnDefaultUsed: func(p lifecyclepkg.DefaultedPipeline) {
						cmdctx.EmitDefaultNotice(cmd, flags, string(p), "lifecycle")
					},
				}); err != nil {
					return err
				}
				_ = promptcache.Write(flags.ProjectRoot(), promptcache.StateStopped)
				return nil
			}
			// Per-service stop.
			name := args[0]
			cfg, err := config.LoadConfigOrWrap(flags.ConfigPath)
			if err != nil {
				return err
			}
			reg, regErr := usercommands.LoadRegistryFromConfigPath(flags.ConfigPath)
			if regErr != nil {
				reg = nil
			}
			baseDir := filepath.Dir(flags.ConfigPath)
			if reg != nil {
				_ = reg.ApplyVisibility(cfg, baseDir)
			}
			deps := StopServiceDeps{
				Cfg:            cfg,
				CmdRegistry:    reg,
				CmdRegistryErr: regErr,
				BaseDir:        baseDir,
				ErrOut:         cmd.ErrOrStderr(),
				SkipPreflight:  skipPreflight,
			}
			if err := StopService(cmd.Context(), deps, name); err != nil {
				return err
			}
			_ = promptcache.Remove(flags.ProjectRoot())
			return nil
		},
		ValidArgsFunction: cmdctx.ServiceNameCompletion(flags),
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation prompts inside hook steps")
	cmdctx.AddSkipPreflight(cmd, &skipPreflight)
	return cmd
}
