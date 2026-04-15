package command

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"devbox-cli/internal/config"
	"devbox-cli/internal/docker"
	"devbox-cli/internal/render"

	"github.com/spf13/cobra"
)

func newComposeCmd(flags *rootFlags) *cobra.Command {
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
func newComposeRawCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:                "raw [-- docker-compose-args...]",
		Short:              "Run docker compose directly with resolved file list and project name (escape hatch)",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			composeArgs := []string{"-p", cfg.Project.FullName()}
			for _, f := range buildComposeFileList(cfg) {
				composeArgs = append(composeArgs, "-f", f)
			}
			// Strip leading "--" separator if present.
			passArgs := args
			if len(passArgs) > 0 && passArgs[0] == "--" {
				passArgs = passArgs[1:]
			}
			composeArgs = append(composeArgs, passArgs...)

			dockerCmd := exec.Command("docker", append([]string{"compose"}, composeArgs...)...)
			dockerCmd.Stdin = cmd.InOrStdin()
			dockerCmd.Stdout = cmd.OutOrStdout()
			dockerCmd.Stderr = cmd.ErrOrStderr()
			return dockerCmd.Run()
		},
		SilenceUsage: true,
	}
}

// newComposeArgvCmd creates the `devbox compose argv` command.
// It shows the full `docker compose` command that `devbox docker <command>` would
// execute, without running it. Useful for diagnostics and debugging.
func newComposeArgvCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "argv <command> [args...]",
		Short: "Show the full docker compose command that would be executed",
		Args:  cobra.MinimumNArgs(1),
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

			compose := docker.NewCompose(cfg, dockerCfg)

			command := args[0]
			extraArgs := args[1:]
			fullArgs := compose.BuildArgs(command, extraArgs...)

			// Print as: docker compose -p ... -f ... <command> ...
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "docker "+strings.Join(fullArgs, " "))
			return err
		},
		SilenceUsage: true,
	}
}

func newComposeFilesCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "files",
		Short: "Print resolved compose file list (base + enabled overlays), one per line",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			for _, f := range buildComposeFileList(cfg) {
				fmt.Println(f)
			}
			return nil
		},
		SilenceUsage: true,
	}
}

// buildComposeFileList returns the ordered list of compose files.
// Delegates to cfg.ComposeFiles() which is the canonical implementation.
func buildComposeFileList(cfg *config.DevboxConfig) []string {
	return cfg.ComposeFiles()
}

// healthGetFn returns the Docker health status string for a container by ID.
// Known return values: "healthy", "unhealthy", "starting", "none".
type healthGetFn func(id string) (string, error)

// waitContainersHealthy polls each container until all are healthy or times out.
// Containers with no healthcheck ("none" status) emit a one-time warning and are
// skipped (not counted as failures). Unhealthy containers return an error immediately.
func waitContainersHealthy(ids []string, getHealth healthGetFn, attempts int, interval time.Duration, w *render.Writer) error {
	warned := make(map[string]bool)

	for attempt := range attempts {
		if attempt > 0 {
			time.Sleep(interval)
		}

		allDone := true
		for _, id := range ids {
			status, err := getHealth(id)
			if err != nil {
				return fmt.Errorf("inspecting container %s: %w", id, err)
			}
			switch status {
			case "healthy":
				// ready
			case "unhealthy":
				return fmt.Errorf("container %s is unhealthy", id)
			case "none", "":
				if !warned[id] {
					w.Warning(fmt.Sprintf("container %s has no healthcheck, skipping", id))
					warned[id] = true
				}
				// treat as done — no healthcheck configured
			default: // "starting" or any other transient state
				allDone = false
			}
		}

		if allDone {
			w.Success("all containers healthy")
			return nil
		}
	}
	return fmt.Errorf("containers did not become healthy within timeout (%d attempts)", attempts)
}

// dockerHealthStatus returns the health status of a single container by ID.
// Returns "none" when the container has no healthcheck configured.
func dockerHealthStatus(id string) (string, error) {
	out, err := exec.Command(
		"docker", "inspect",
		"--format", `{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}`,
		id,
	).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
