package command

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"devbox-cli/internal/command/cmdctx"
	"devbox-cli/internal/config"
	"devbox-cli/internal/docker"

	"github.com/spf13/cobra"
)

func newComposeCmd(flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "compose",
		Short:        "Low-level Docker Compose diagnostics",
		SilenceUsage: true,
	}
	cmd.AddCommand(newComposeFilesCmd(flags))
	cmd.AddCommand(newComposeRawCmd(flags))
	cmd.AddCommand(newComposeArgvCmd(flags))
	return cmd
}

// newComposeRawCmd creates the `devbox compose raw` command (formerly `devbox compose run`).
// It resolves the compose file list and project name from config, then delegates
// to `docker compose` with the user-supplied arguments. All docker compose flags
// and subcommands can be passed after `--`.
//
// With --bare, only the project name is injected (no default compose files).
// This is useful for commands that use a standalone compose file like the installer.
func newComposeRawCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:                "raw [--bare] [-- docker-compose-args...]",
		Short:              "Run docker compose directly with resolved file list and project name (escape hatch)",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse --bare flag manually (DisableFlagParsing is on).
			bare, passArgs := extractBareFlag(args)

			cfg, err := config.LoadConfig(flags.ConfigPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			// Use docker policy project name for consistency with devbox docker commands.
			baseDir := flags.ProjectRoot()
			dockerCfg, err := config.LoadDockerConfig(baseDir, cfg)
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("loading docker config: %w", err)
				}
				dockerCfg = &config.DockerConfig{}
			}

			composeArgs := []string{"-p", dockerCfg.ProjectName}
			if !bare {
				for _, f := range cfg.ComposeFiles() {
					composeArgs = append(composeArgs, "-f", f)
				}
			}
			composeArgs = append(composeArgs, passArgs...)

			dockerCmd := exec.Command(config.DockerBin(cfg), append([]string{"compose"}, composeArgs...)...) //nolint:gosec
			dockerCmd.Env = docker.MergeEnv(dockerCfg.ProcessEnv)
			dockerCmd.Stdin = cmd.InOrStdin()
			dockerCmd.Stdout = cmd.OutOrStdout()
			dockerCmd.Stderr = cmd.ErrOrStderr()
			return dockerCmd.Run()
		},
		SilenceUsage: true,
	}
}

// extractBareFlag checks for --bare in the arg list and returns the remaining
// args with the leading "--" separator stripped. Only a "--" that appears before
// any positional argument (i.e. before args that are not --bare) is treated as
// the devbox separator and removed. Once positional args have started, all "--"
// tokens are preserved so they pass through to docker compose unchanged.
func extractBareFlag(args []string) (bare bool, rest []string) {
	separatorSkipped := false
	positionalStarted := false
	for _, arg := range args {
		switch {
		case arg == "--bare" && !positionalStarted:
			bare = true
		case arg == "--" && !separatorSkipped && !positionalStarted:
			separatorSkipped = true
		default:
			positionalStarted = true
			rest = append(rest, arg)
		}
	}
	return
}

// newComposeArgvCmd creates the `devbox compose argv` command.
// It shows the full `docker compose` command that `devbox docker <command>` would
// execute, without running it. Useful for diagnostics and debugging.
func newComposeArgvCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "argv <command> [args...]",
		Short: "Show the full docker compose command that would be executed",
		Args:  cobra.MinimumNArgs(1),
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

			compose := docker.NewCompose(cfg, dockerCfg)

			command := args[0]
			extraArgs := args[1:]
			fullArgs := compose.BuildArgs(command, extraArgs...)

			// Print as: <bin> compose -p ... -f ... <command> ...
			_, err = fmt.Fprintln(cmd.OutOrStdout(), compose.BinName()+" "+strings.Join(fullArgs, " "))
			return err
		},
		SilenceUsage: true,
	}
}

func newComposeFilesCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "files",
		Short: "Print resolved compose file list (base + enabled overlays), one per line",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.ConfigPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			for _, f := range cfg.ComposeFiles() {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), f)
			}
			return nil
		},
		SilenceUsage: true,
	}
}
