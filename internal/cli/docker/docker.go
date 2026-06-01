package docker

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	dockerpkg "github.com/semsemyonoff/dwe/internal/shared/docker"
	"github.com/semsemyonoff/dwe/internal/shared/envfile"
	"github.com/semsemyonoff/dwe/internal/shared/render"

	"github.com/spf13/cobra"
)

// NewCmd builds the `devbox docker` command tree.
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
}

// envRegenCommands lists docker commands that trigger automatic .env regeneration.
var envRegenCommands = []string{"up", "run", "exec", "restart", "build"}

func newDockerPipeline(flags *cmdctx.RootFlags, command string) (*dockerPipeline, error) {
	cfg, err := config.LoadConfig(flags.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	baseDir := flags.ProjectRoot()
	dockerCfg, err := config.LoadDockerConfig(baseDir, cfg)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("loading docker config: %w", err)
		}
		dockerCfg = &config.DockerConfig{}
	}

	// Auto-generate .env before these commands.
	if slices.Contains(envRegenCommands, command) {
		if _, err := envfile.Regenerate(flags.ConfigPath); err != nil {
			return nil, fmt.Errorf("generating .env: %w", err)
		}
	}

	// Ensure declared Docker resources exist before the command runs.
	if err := dockerpkg.EnsureVolumes(dockerCfg.Resources, dockerCfg.ProjectName, command, config.DockerBin(cfg), render.Stdout()); err != nil {
		return nil, fmt.Errorf("ensuring volumes: %w", err)
	}

	compose := dockerpkg.NewCompose(cfg, dockerCfg)

	return &dockerPipeline{
		cfg:       cfg,
		dockerCfg: dockerCfg,
		compose:   compose,
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

	devbox docker exec app-main -- php artisan migrate

The -- separator allows flags in the command itself to be passed through without
being consumed by devbox's parser.`,
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

	devbox docker run app-main -- composer install

The -- separator allows flags in the command itself to be passed through without
being consumed by devbox's parser.`,
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
func resolvePullInvocation(cfg *config.DweConfig, dockerCfg *config.DockerConfig, all bool, services []string) (*dockerpkg.Compose, []string) {
	if all {
		return dockerpkg.NewComposeAll(cfg, dockerCfg), services
	}
	return dockerpkg.NewCompose(cfg, dockerCfg), services
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

			compose, extraArgs := resolvePullInvocation(p.cfg, p.dockerCfg, all, args)
			return compose.Exec("pull", extraArgs...)
		},
		SilenceUsage: true,
	}

	cmd.Flags().BoolVar(&all, "all", false, "pull images from all configured overlays, not just enabled ones")
	return cmd
}

// resolveBuildInvocation returns the Compose instance and extra args for a build command.
// When all is true, uses ComposeFilesAll(); otherwise uses ComposeFiles().
// When force is true, prepends --no-cache --pull to the extra args.
// The returned extra args include the force flags (if applicable) and service names.
func resolveBuildInvocation(cfg *config.DweConfig, dockerCfg *config.DockerConfig, all, force bool, services []string) (*dockerpkg.Compose, []string) {
	var compose *dockerpkg.Compose
	if all {
		compose = dockerpkg.NewComposeAll(cfg, dockerCfg)
	} else {
		compose = dockerpkg.NewCompose(cfg, dockerCfg)
	}

	extraArgs := make([]string, 0, len(services)+2)
	if force {
		extraArgs = append(extraArgs, "--no-cache", "--pull")
	}
	extraArgs = append(extraArgs, services...)
	return compose, extraArgs
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

			compose, extraArgs := resolveBuildInvocation(p.cfg, p.dockerCfg, all, force, args)
			return compose.Exec("build", extraArgs...)
		},
		SilenceUsage: true,
	}

	cmd.Flags().BoolVar(&all, "all", false, "build images from all configured overlays, not just enabled ones")
	cmd.Flags().BoolVar(&force, "force", false, "rebuild without cache and re-pull base images (--no-cache --pull)")
	return cmd
}

func newDockerProjectNameCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "project-name",
		Short: "Print the resolved compose project name",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.ConfigPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			baseDir := flags.ProjectRoot()
			dockerCfg, err := config.LoadDockerConfig(baseDir, cfg)
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("loading docker config: %w", err)
				}
				dockerCfg = &config.DockerConfig{}
			}

			name := dockerCfg.ProjectName
			if name == "" {
				name = cfg.Project.FullName()
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), name)
			return err
		},
		SilenceUsage: true,
	}
}
