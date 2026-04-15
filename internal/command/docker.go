package command

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"devbox-cli/internal/config"
	"devbox-cli/internal/docker"
	"devbox-cli/internal/render"

	"github.com/spf13/cobra"
)

func newDockerCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "docker",
		Short:        "Docker Compose lifecycle commands",
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
	cmd.AddCommand(newDockerWaitCmd(flags))
	cmd.AddCommand(newDockerProjectNameCmd(flags))
	return cmd
}

// dockerPipeline loads config and docker policy, builds a Compose struct,
// and optionally generates .env. It is the shared setup for all docker commands.
type dockerPipeline struct {
	cfg       *config.DevboxConfig
	dockerCfg *config.DockerConfig
	compose   *docker.Compose
}

func newDockerPipeline(flags *rootFlags, command string) (*dockerPipeline, error) {
	cfg, err := config.LoadConfig(flags.configPath)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	baseDir := filepath.Dir(flags.configPath)
	dockerCfg, err := config.LoadDockerConfig(baseDir, cfg)
	if err != nil {
		return nil, fmt.Errorf("loading docker config: %w", err)
	}

	// Auto-generate .env if configured for this command.
	if dockerCfg.Env.ShouldGenerateEnv(command) {
		if err := generateEnv(flags); err != nil {
			return nil, fmt.Errorf("generating .env: %w", err)
		}
	}

	// Ensure declared Docker resources exist before the command runs.
	if err := docker.EnsureVolumes(dockerCfg.Resources, command, render.Stdout()); err != nil {
		return nil, fmt.Errorf("ensuring volumes: %w", err)
	}

	compose := docker.NewCompose(cfg, dockerCfg)

	return &dockerPipeline{
		cfg:       cfg,
		dockerCfg: dockerCfg,
		compose:   compose,
	}, nil
}

// generateEnv runs the equivalent of `devbox render env -o .env`.
// The output path is resolved relative to the config file's directory
// so that it works correctly when invoked from a different working directory.
func generateEnv(flags *rootFlags) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable: %w", err)
	}
	envPath := filepath.Join(filepath.Dir(flags.configPath), ".env")
	cmd := exec.Command(exe, "-c", flags.configPath, "render", "env", "-o", envPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func newDockerUpCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "up [services...]",
		Short: "Start compose services",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newDockerPipeline(flags, "up")
			if err != nil {
				return err
			}
			return p.compose.Exec("up", args...)
		},
		SilenceUsage: true,
	}
}

func newDockerDownCmd(flags *rootFlags) *cobra.Command {
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

func newDockerStopCmd(flags *rootFlags) *cobra.Command {
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

func newDockerRestartCmd(flags *rootFlags) *cobra.Command {
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

func newDockerLogsCmd(flags *rootFlags) *cobra.Command {
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

func newDockerPsCmd(flags *rootFlags) *cobra.Command {
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

func newDockerExecCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:                "exec <service> [-- cmd...]",
		Short:              "Execute a command in a running compose service",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newDockerPipeline(flags, "exec")
			if err != nil {
				return err
			}
			return p.compose.Exec("exec", args...)
		},
		SilenceUsage: true,
	}
}

func newDockerRunCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:                "run <service> [-- cmd...]",
		Short:              "Run a one-off command in a compose service",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newDockerPipeline(flags, "run")
			if err != nil {
				return err
			}
			return p.compose.Exec("run", args...)
		},
		SilenceUsage: true,
	}
}

func newDockerWaitCmd(flags *rootFlags) *cobra.Command {
	var timeout time.Duration
	var interval time.Duration

	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Wait for all compose containers to become healthy",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newDockerPipeline(flags, "wait")
			if err != nil {
				return err
			}

			ids, err := p.compose.ContainerIDs()
			if err != nil {
				return fmt.Errorf("getting container IDs: %w", err)
			}
			if len(ids) == 0 {
				render.Stdout().Warning("no containers found")
				return nil
			}

			if interval <= 0 {
				return fmt.Errorf("--interval must be greater than zero")
			}
			attempts := max(int(timeout/interval), 1)

			return waitContainersHealthy(ids, dockerHealthStatus, attempts, interval, render.Stdout())
		},
		SilenceUsage: true,
	}

	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "total wait timeout")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "poll interval")
	return cmd
}

func newDockerProjectNameCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "project-name",
		Short: "Print the resolved compose project name",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			baseDir := filepath.Dir(flags.configPath)
			dockerCfg, err := config.LoadDockerConfig(baseDir, cfg)
			if err != nil {
				return fmt.Errorf("loading docker config: %w", err)
			}

			_, err = fmt.Fprintln(cmd.OutOrStdout(), dockerCfg.ProjectName)
			return err
		},
		SilenceUsage: true,
	}
}
