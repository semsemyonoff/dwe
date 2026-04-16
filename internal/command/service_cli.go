package command

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/docker"
	"devbox-cli/internal/render"
)

// runServicesCLI resolves the container state and either execs into a running
// container or starts a new one via docker compose run.
func runServicesCLI(cfg *config.DevboxConfig, compose *docker.Compose, serviceName string, asRoot bool) error {
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
	fullContainerName := compose.ProjectName + "-" + svc.Container

	status, err := containerStateStatus(fullContainerName)
	if err != nil {
		// Container does not exist — start a new one via compose run.
		return composeRunCLI(compose, svc.Container, shell, u, workDir)
	}

	switch status {
	case "running":
		return dockerExecCLI(fullContainerName, shell, u, workDir)
	default:
		return fmt.Errorf(
			"container %q is %s — start it first with 'devbox up'",
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
// It uses the shared Compose struct for project name, file list, and global args.
func composeRunCLI(compose *docker.Compose, serviceName, shell, u, workDir string) error {
	args := []string{"compose"}
	if compose.ProjectName != "" {
		args = append(args, "-p", compose.ProjectName)
	}
	for _, f := range compose.Files {
		args = append(args, "-f", f)
	}
	args = append(args, compose.GlobalArgs...)
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
