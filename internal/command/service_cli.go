package command

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"

	"github.com/spf13/cobra"
)

func newServicesCLICmd(flags *rootFlags) *cobra.Command {
	var asRoot bool

	cmd := &cobra.Command{
		Use:   "cli <service>",
		Short: "Open a shell in the service container (exec if running, run if stopped)",
		Long: `Open an interactive shell in the specified service container.

If the container is running, connects via 'docker exec'.
If the container does not exist, starts a new one via 'docker compose run --rm'.
If the container is stopped (exited), an error is returned.

Shell, user, and working directory defaults are read from the cli: section
in devbox/services.yml and can be overridden with flags.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			return runServicesCLI(cfg, args[0], asRoot)
		},
	}

	cmd.Flags().BoolVar(&asRoot, "root", false, "run as root user")
	return cmd
}

// runServicesCLI resolves the container state and either execs into a running
// container or starts a new one via docker compose run.
func runServicesCLI(cfg *config.DevboxConfig, serviceName string, asRoot bool) error {
	svc, ok := cfg.Services[serviceName]
	if !ok {
		return fmt.Errorf("service %q not found", serviceName)
	}
	if svc.Container == "" {
		return fmt.Errorf("service %q has no container defined", serviceName)
	}

	shell := svc.CLI.Shell
	if shell == "" {
		shell = "bash"
	}

	workDir := svc.CLI.WorkDir
	if workDir == "" {
		workDir = svc.WorkDirInternal
	}
	if workDir == "" {
		workDir = svc.DirInternal
	}

	u := resolveUser(svc.CLI.User, asRoot)

	// Container name matches the container_name field in compose.yaml:
	// <project-full-name>-<container>, e.g. devbox-laravel-app-main.
	fullContainerName := cfg.Project.FullName() + "-" + svc.Container

	status, err := containerStateStatus(fullContainerName)
	if err != nil {
		// Container does not exist — start a new one via compose run.
		composeFiles := buildComposeFileList(cfg)
		return composeRunCLI(cfg.Project.FullName(), composeFiles, svc.Container, shell, u, workDir)
	}

	switch status {
	case "running":
		return dockerExecCLI(fullContainerName, shell, u, workDir)
	default:
		return fmt.Errorf(
			"container %q is %s — start it first with 'make up'",
			fullContainerName, status,
		)
	}
}

// resolveUser returns the effective user string for -u flag.
// --root overrides everything; otherwise uses configured user or current UID.
func resolveUser(configured string, asRoot bool) string {
	if asRoot {
		return "root"
	}
	if configured != "" {
		return configured
	}
	if u, err := user.Current(); err == nil {
		return u.Uid
	}
	return ""
}

// containerStateStatus returns the Docker state status string for a container
// (e.g. "running", "exited", "paused"). Returns an error when the container
// does not exist.
func containerStateStatus(containerName string) (string, error) {
	out, err := exec.Command(
		"docker", "inspect",
		"--format", "{{.State.Status}}",
		containerName,
	).Output()
	if err != nil {
		return "", fmt.Errorf("container %q not found", containerName)
	}
	return strings.TrimSpace(string(out)), nil
}

// dockerExecCLI runs an interactive shell in a running container via docker exec.
func dockerExecCLI(containerName, shell, u, workDir string) error {
	args := []string{"exec", "-it"}
	if u != "" {
		args = append(args, "-u", u)
	}
	if workDir != "" {
		args = append(args, "-w", workDir)
	}
	args = append(args, containerName, shell)

	render.Stdout().Info(fmt.Sprintf("exec → %s", containerName))
	return runInteractive("docker", args...)
}

// composeRunCLI starts a new temporary container via docker compose run --rm.
func composeRunCLI(projectName string, composeFiles []string, serviceName, shell, u, workDir string) error {
	args := []string{"compose", "-p", projectName}
	for _, f := range composeFiles {
		args = append(args, "-f", f)
	}
	args = append(args, "run", "--rm", "-it")
	if u != "" {
		args = append(args, "-u", u)
	}
	if workDir != "" {
		args = append(args, "-w", workDir)
	}
	args = append(args, serviceName, shell)

	render.Stdout().Info(fmt.Sprintf("run → %s (new container)", serviceName))
	return runInteractive("docker", args...)
}

// runInteractive executes a command with the current process's stdin/stdout/stderr,
// allowing full interactive terminal use.
func runInteractive(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
