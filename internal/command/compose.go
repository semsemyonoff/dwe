package command

import (
	"fmt"
	"os/exec"
	"sort"
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
	return cmd
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
// The base file is always first; enabled tool overlays follow in sorted key order;
// then enabled service overlays (from services with compose_overlay) in sorted order.
func buildComposeFileList(cfg *config.DevboxConfig) []string {
	var files []string
	if cfg.Compose.Base != "" {
		files = append(files, cfg.Compose.Base)
	}

	// Tool overlays from compose.overlays.
	keys := make([]string, 0, len(cfg.Compose.Overlays))
	for k := range cfg.Compose.Overlays {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		if toolOverlayEnabled(cfg, key) {
			files = append(files, cfg.Compose.Overlays[key])
		}
	}

	// Service overlays from services with compose_overlay set.
	svcNames := make([]string, 0, len(cfg.Services))
	for name := range cfg.Services {
		svcNames = append(svcNames, name)
	}
	sort.Strings(svcNames)

	for _, name := range svcNames {
		svc := cfg.Services[name]
		if svc.Enabled && len(svc.Compose) > 0 {
			files = append(files, svc.Compose...)
		}
	}

	return files
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

// toolOverlayEnabled reports whether the given tool overlay key is active in cfg.
func toolOverlayEnabled(cfg *config.DevboxConfig, key string) bool {
	switch key {
	case "adminer":
		return cfg.Tools.Adminer.Enabled
	case "redis_insight":
		return cfg.Tools.RedisInsight.Enabled
	case "mailpit":
		return cfg.Tools.Mailpit.Enabled
	default:
		return false
	}
}
