package command

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"

	"github.com/spf13/cobra"
)

func newComposeCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "compose",
		Short:        "Docker Compose helpers",
		SilenceUsage: true,
	}
	cmd.AddCommand(newComposeFilesCmd(flags))
	cmd.AddCommand(newComposeWaitCmd(flags))
	cmd.AddCommand(newComposeRunCmd(flags))
	return cmd
}

// newComposeRunCmd creates the `devbox compose run` command.
// It resolves the compose file list and project name from config, then delegates
// to `docker compose` with the user-supplied arguments. All docker compose flags
// and subcommands can be passed after `--`.
func newComposeRunCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:                "run [-- docker-compose-args...]",
		Short:              "Run docker compose with the resolved file list and project name",
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

// newComposeWaitCmd creates the `devbox compose wait` command.
// It polls each container's health status until all are healthy (or times out).
func newComposeWaitCmd(flags *rootFlags) *cobra.Command {
	var timeout time.Duration
	var interval time.Duration

	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Wait for all compose containers to become healthy",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			composeFiles := buildComposeFileList(cfg)
			projectName := cfg.Project.FullName()

			ids, err := dockerComposeContainerIDs(projectName, composeFiles)
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

// dockerComposeContainerIDs returns the container IDs for the given compose project.
func dockerComposeContainerIDs(projectName string, composeFiles []string) ([]string, error) {
	args := []string{"compose", "-p", projectName}
	for _, f := range composeFiles {
		args = append(args, "-f", f)
	}
	args = append(args, "ps", "-q")

	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		return nil, err
	}

	var ids []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			ids = append(ids, line)
		}
	}
	return ids, nil
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
