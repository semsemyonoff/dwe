package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/cli/info"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	lifecyclepkg "github.com/semsemyonoff/dwe/internal/core/workflow/lifecycle"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
	"github.com/semsemyonoff/dwe/internal/shared/promptcache"
	"github.com/semsemyonoff/dwe/internal/shared/render"

	"github.com/spf13/cobra"
)

// restartContainerFn is a seam for docker.RestartContainer so tests can inject
// fake docker behavior without a real Docker daemon.
var restartContainerFn = docker.RestartContainer

// RestartService restarts a single service's container directly via
// `docker restart`, bypassing docker compose and lifecycle hooks.
//
// This path is intentionally lightweight: it runs no preflight checks and
// acquires no project locks, so it is NOT symmetric with `dwe stop <name>`
// (which does both). Trade-off: faster and works on disabled services, but
// a concurrent `dwe deploy run` is not blocked and the user gets raw docker
// errors instead of curated preflight diagnostics when the daemon is down.
//
// baseDir is the project root (the directory containing workspace.yml). It is
// used to resolve the compose project name from workspace/docker.yml so the
// derived container name matches what docker compose actually created — see
// config.ResolveComposeProjectName.
//
// out receives a single "✓ container restarted: <name>" success line once the
// docker restart returns. nil is treated as io.Discard so callers that don't
// care (or tests) can opt out cheaply. Errors are NOT printed here — the cobra
// RunE surfaces them through cmd.PrintErrln / SilenceErrors machinery.
func RestartService(ctx context.Context, baseDir string, cfg *config.DweConfig, name string, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	containerName, err := resolveServiceContainer(baseDir, cfg, name)
	if err != nil {
		return err
	}
	dockerBin := config.DockerBin(cfg)
	if err := restartContainerFn(ctx, dockerBin, containerName, docker.DefaultStopTimeoutSec); err != nil {
		if errors.Is(err, docker.ErrNoSuchContainer) {
			return fmt.Errorf("service %q has no container — run `dwe deploy run` or `dwe run` first", name)
		}
		return err
	}
	render.NewWriter(out).Success("✓ container restarted: " + containerName)
	return nil
}

// NewRestartCmd builds the `dwe restart` cobra command.
func NewRestartCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	var yes bool
	var skipPreflight bool

	cmd := &cobra.Command{
		Use:   "restart [service]",
		Short: "Restart the project (stop, then run --no-update)",
		Long: `Restart the project by running the full stop lifecycle then the full run lifecycle.

The run leg always skips the git update probe (equivalent to 'dwe run --no-update').

When a service name is given, restarts only that service's container directly via
'docker restart', bypassing compose and lifecycle hooks. This works even after
the service has been disabled.`,
		Example: `  dwe restart
  dwe restart postgres`,
		Args:         cobra.MaximumNArgs(1),
		GroupID:      groupID,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				if err := lifecyclepkg.RunRestart(lifecyclepkg.RunContext{
					Ctx:        cmd.Context(),
					ConfigPath: flags.ConfigPath,
					Yes:        yes,
					// lifecycle commands are not migrated to JSON output; suppress
					// the chained info display in JSON mode to avoid a mixed
					// text+JSON stream on stdout.
					ShowInfo: func() error {
						if flags.Output == "json" {
							return nil
						}
						return info.Run(cmd, flags)
					},
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
				_ = promptcache.Write(flags.ProjectRoot(), promptcache.StateRunning)
				return nil
			}
			// Per-service restart: container-level, no preflight, no locks.
			name := args[0]
			cfg, err := config.LoadConfig(flags.ConfigPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			if err := RestartService(cmd.Context(), flags.ProjectRoot(), cfg, name, cmd.OutOrStdout()); err != nil {
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
