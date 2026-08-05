package docker

import (
	"fmt"
	"io"
	"slices"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	dockerpkg "github.com/semsemyonoff/dwe/internal/shared/docker"
	"github.com/semsemyonoff/dwe/internal/shared/envfile"
	"github.com/semsemyonoff/dwe/internal/shared/render"

	"github.com/spf13/cobra"
)

// NewCmd builds the `dwe docker` command tree.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "docker",
		Short:        "Docker Compose lifecycle commands",
		GroupID:      groupID,
		SilenceUsage: true,
	}
	cmd.AddCommand(newDockerUpCmd(flags))
	cmd.AddCommand(newDockerDownCmd(flags))
	cmd.AddCommand(newDockerStopCmd(flags))
	cmd.AddCommand(newDockerRestartCmd(flags))
	cmd.AddCommand(newDockerLogsCmd(flags))
	cmd.AddCommand(newDockerPsCmd(flags))
	cmd.AddCommand(newDockerExecCmd(flags))
	cmd.AddCommand(newDockerRunCmd(flags))
	cmd.AddCommand(newDockerPullCmd(flags))
	cmd.AddCommand(newDockerBuildCmd(flags))
	cmd.AddCommand(newDockerProjectNameCmd(flags))
	return cmd
}

// dockerPipeline loads config and docker policy, builds a Compose struct,
// and optionally generates .env. It is the shared setup for all docker commands.
type dockerPipeline struct {
	cfg       *config.DweConfig
	dockerCfg *config.DockerConfig
	compose   *dockerpkg.Compose
	baseDir   string
}

// envRegenCommands lists docker commands that trigger automatic .env regeneration.
var envRegenCommands = []string{"up", "run", "exec", "restart", "build"}

func newDockerPipeline(flags *cmdctx.RootFlags, command string) (*dockerPipeline, error) {
	cfg, err := config.LoadConfigOrWrap(flags.ConfigPath)
	if err != nil {
		return nil, err
	}

	baseDir := flags.ProjectRoot()
	dockerCfg, err := config.LoadDockerConfigOrEmpty(baseDir, cfg)
	if err != nil {
		return nil, err
	}

	// Auto-generate .env before these commands.
	if slices.Contains(envRegenCommands, command) {
		if _, err := envfile.Regenerate(flags.ConfigPath); err != nil {
			return nil, fmt.Errorf("generating .env: %w", err)
		}
	}

	// Ensure declared Docker resources exist before the command runs. Use the
	// resolved compose project name (docker.yml project_name -> else FullName) so
	// non-shared volumes are prefixed with the SAME project name compose uses;
	// passing the raw (possibly empty) dockerCfg.ProjectName would create bare-
	// named volumes that diverge from the compose/-p and reset scopes.
	projectName, err := config.ResolveComposeProjectName(baseDir, cfg)
	if err != nil {
		return nil, fmt.Errorf("resolving compose project name: %w", err)
	}
	if err := dockerpkg.EnsureVolumes(dockerCfg.Resources, projectName, command, config.DockerBin(cfg), render.Stdout()); err != nil {
		return nil, fmt.Errorf("ensuring volumes: %w", err)
	}

	compose := dockerpkg.NewCompose(cfg, dockerCfg, baseDir)

	return &dockerPipeline{
		cfg:       cfg,
		dockerCfg: dockerCfg,
		compose:   compose,
		baseDir:   baseDir,
	}, nil
}

func newDockerUpCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var wait bool

	cmd := &cobra.Command{
		Use:   "up [services...]",
		Short: "Start compose services",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newDockerPipeline(flags, "up")
			if err != nil {
				return err
			}
			if p.dockerCfg.Build.PrepullBases {
				// Pass nil (not args): `docker compose up <svc>` also builds
				// <svc>'s depends_on services, whose base images must be
				// prepulled too — narrowing to args would skip them.
				prepullBases(cmd.ErrOrStderr(), p.compose, nil, false)
			}
			extra := args
			if wait {
				extra = append([]string{"--wait"}, args...)
			}
			return p.compose.Exec("up", extra...)
		},
		SilenceUsage: true,
	}

	cmd.Flags().BoolVar(&wait, "wait", false, "block until all services are healthy (forwards --wait to docker compose up; requires healthchecks on every started service)")
	return cmd
}

func newDockerDownCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Stop and remove compose services",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newDockerPipeline(flags, "down")
			if err != nil {
				return err
			}
			return p.compose.Exec("down", args...)
		},
		SilenceUsage: true,
	}
}

func newDockerStopCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "stop [services...]",
		Short: "Stop compose services",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newDockerPipeline(flags, "stop")
			if err != nil {
				return err
			}
			return p.compose.Exec("stop", args...)
		},
		SilenceUsage: true,
	}
}

func newDockerRestartCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "restart [services...]",
		Short: "Restart compose services",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newDockerPipeline(flags, "restart")
			if err != nil {
				return err
			}
			return p.compose.Exec("restart", args...)
		},
		SilenceUsage: true,
	}
}

func newDockerLogsCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "logs [services...]",
		Short: "View compose service logs",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newDockerPipeline(flags, "logs")
			if err != nil {
				return err
			}
			return p.compose.Exec("logs", args...)
		},
		SilenceUsage: true,
	}
}

func newDockerPsCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "ps",
		Short: "List compose containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newDockerPipeline(flags, "ps")
			if err != nil {
				return err
			}
			return p.compose.Exec("ps", args...)
		},
		SilenceUsage: true,
	}
}

func newDockerExecCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:                "exec <service> [-- cmd...]",
		Short:              "Execute a command in a running compose service",
		DisableFlagParsing: true,
		Long: `Execute a command in a running compose service.

All arguments after the service name (including --) are forwarded verbatim to
docker compose exec. Use -- to separate the service name from the command:

	dwe docker exec app-main -- php artisan migrate

The -- separator allows flags in the command itself to be passed through without
being consumed by dwe's parser.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newDockerPipeline(flags, "exec")
			if err != nil {
				return err
			}
			return p.compose.Exec("exec", stripDockerCommandSeparator(args)...)
		},
		SilenceUsage: true,
	}
}

func newDockerRunCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:                "run <service> [-- cmd...]",
		Short:              "Run a one-off command in a compose service",
		DisableFlagParsing: true,
		Long: `Run a one-off command in a compose service.

All arguments after the service name (including --) are forwarded verbatim to
docker compose run. Use -- to separate the service name from the command:

	dwe docker run app-main -- composer install

The -- separator allows flags in the command itself to be passed through without
being consumed by dwe's parser.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newDockerPipeline(flags, "run")
			if err != nil {
				return err
			}
			return p.compose.Exec("run", stripDockerCommandSeparator(args)...)
		},
		SilenceUsage: true,
	}
}

func stripDockerCommandSeparator(args []string) []string {
	for i, arg := range args {
		if arg == "--" {
			stripped := make([]string, 0, len(args)-1)
			stripped = append(stripped, args[:i]...)
			stripped = append(stripped, args[i+1:]...)
			return stripped
		}
	}
	return args
}

// resolvePullInvocation returns the Compose instance and extra args for a pull command.
// When all is true, uses ComposeFilesAll(); otherwise uses ComposeFiles().
// The returned extra args are just the service names (pull doesn't have flags like --force).
func resolvePullInvocation(cfg *config.DweConfig, dockerCfg *config.DockerConfig, baseDir string, all bool, services []string) (*dockerpkg.Compose, []string) {
	if all {
		return dockerpkg.NewComposeAll(cfg, dockerCfg, baseDir), services
	}
	return dockerpkg.NewCompose(cfg, dockerCfg, baseDir), services
}

func newDockerPullCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var all bool

	cmd := &cobra.Command{
		Use:   "pull [services...]",
		Short: "Pull compose service images",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newDockerPipeline(flags, "pull")
			if err != nil {
				return err
			}

			compose, extraArgs := resolvePullInvocation(p.cfg, p.dockerCfg, p.baseDir, all, args)
			return compose.Exec("pull", extraArgs...)
		},
		SilenceUsage: true,
	}

	cmd.Flags().BoolVar(&all, "all", false, "pull images from all configured overlays, not just enabled ones")
	return cmd
}

// resolveBuildInvocation returns the Compose instance and extra args for a build command.
// When all is true, uses ComposeFilesAll(); otherwise uses ComposeFiles().
// When force is true, prepends --no-cache --pull to the extra args — unless
// prepull is also true, in which case the daemon-side prepull step already
// re-pulls the derived bases and compose gets only --no-cache (buildkit
// --pull is broken for LAN registries, the same root cause prepull works
// around). The returned extra args include the force flags (if applicable)
// and service names.
func resolveBuildInvocation(cfg *config.DweConfig, dockerCfg *config.DockerConfig, baseDir string, all, force, prepull bool, services []string) (*dockerpkg.Compose, []string) {
	var compose *dockerpkg.Compose
	if all {
		compose = dockerpkg.NewComposeAll(cfg, dockerCfg, baseDir)
	} else {
		compose = dockerpkg.NewCompose(cfg, dockerCfg, baseDir)
	}

	extraArgs := make([]string, 0, len(services)+2)
	if force {
		if prepull {
			extraArgs = append(extraArgs, "--no-cache")
		} else {
			extraArgs = append(extraArgs, "--no-cache", "--pull")
		}
	}
	extraArgs = append(extraArgs, services...)
	return compose, extraArgs
}

// prepullBases best-effort pulls the external FROM base images derived from
// the given services (empty = all) via the daemon, so buildkit resolves
// every FROM locally. This is the whole point of build.prepull_bases:
// buildkit's own fetcher cannot reach LAN registries on some builders, while
// `docker pull` does.
//
// The entire step is advisory (constraint #3): any internal error (deriving
// bases, probing image existence) degrades to a warning on errOut and
// execution always proceeds to the compose command that follows. When force
// is true every derived base is pulled unconditionally; otherwise only bases
// missing from the local image store are pulled. A pull failure for a
// missing base is called out loudly, since the following compose
// build/up is likely to fail for the same reason.
func prepullBases(errOut io.Writer, compose *dockerpkg.Compose, services []string, force bool) {
	refs, err := compose.DeriveBuildBases(services)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "warning: deriving build base images: %v\n", err)
		return
	}

	for _, ref := range refs {
		label := ref.Ref
		if ref.Platform != "" {
			label = fmt.Sprintf("%s (%s)", ref.Ref, ref.Platform)
		}

		exists := compose.ImageExists(ref.Ref, ref.Platform)
		if exists && !force {
			continue
		}

		if err := compose.PullImage(ref.Ref, ref.Platform); err != nil {
			if !exists {
				_, _ = fmt.Fprintf(errOut, "warning: pulling base image %q failed, build will likely fail: %v\n", label, err)
			} else {
				_, _ = fmt.Fprintf(errOut, "warning: re-pulling base image %q failed: %v\n", label, err)
			}
		}
	}
}

func newDockerBuildCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var all bool
	var force bool

	cmd := &cobra.Command{
		Use:   "build [services...]",
		Short: "Build compose service images",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newDockerPipeline(flags, "build")
			if err != nil {
				return err
			}

			prepull := p.dockerCfg.Build.PrepullBases
			compose, extraArgs := resolveBuildInvocation(p.cfg, p.dockerCfg, p.baseDir, all, force, prepull, args)
			if prepull {
				prepullBases(cmd.ErrOrStderr(), compose, args, force)
			}
			return compose.Exec("build", extraArgs...)
		},
		SilenceUsage: true,
	}

	cmd.Flags().BoolVar(&all, "all", false, "build images from all configured overlays, not just enabled ones")
	cmd.Flags().BoolVar(&force, "force", false, "rebuild without cache and re-pull base images (--no-cache --pull; with build.prepull_bases set, bases are re-pulled by the daemon and compose gets --no-cache only)")
	return cmd
}

func newDockerProjectNameCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "project-name",
		Short: "Print the resolved compose project name",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfigOrWrap(flags.ConfigPath)
			if err != nil {
				return err
			}

			baseDir := flags.ProjectRoot()
			dockerCfg, err := config.LoadDockerConfigOrEmpty(baseDir, cfg)
			if err != nil {
				return err
			}

			// Must be the exact value dwe passes to `docker compose -p` —
			// scripts use this to build their own label filters — so it goes
			// through the shared resolver rather than re-deriving the
			// precedence (and losing the lowercasing) here.
			name := config.ComposeProjectName(dockerCfg, cfg)
			_, err = fmt.Fprintln(cmd.OutOrStdout(), name)
			return err
		},
		SilenceUsage: true,
	}
}
