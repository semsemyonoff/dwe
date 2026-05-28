package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"os/user"
	"slices"
	"strings"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/shared/docker"
	"devbox-cli/internal/shared/render"
)

// shellOptions holds fully resolved shell session parameters after applying
// flag -> config -> default priority.
type shellOptions struct {
	Mode    string            // "auto", "exec", or "run"
	Shell   string            // shell binary, e.g. "bash"
	User    string            // container user or UID
	WorkDir string            // working directory inside container
	Env     map[string]string // environment variables to pass into the container
}

// resolveShellOptions merges CLI flags, service CLI config, and built-in defaults
// using the priority: flag (non-empty) > config (non-empty) > built-in default.
//
// Built-in defaults:
//   - Mode:    "auto"
//   - Shell:   "bash"
//   - User:    current OS UID (empty string if unavailable)
//   - WorkDir: svc.WorkDirInternal, then svc.DirInternal, then ""
//   - Env:     empty map
//
// --root (flags.asRoot) overrides User to "root" at the highest priority level.
func resolveShellOptions(flags shellCLIFlags, svcCLI config.ServiceCLIConfig, svc config.ServiceConfig) (shellOptions, error) {
	// --- Mode ---
	mode := flags.mode
	if mode == "" {
		mode = svcCLI.Mode
	}
	if mode == "" {
		mode = "auto"
	}

	// --- Shell ---
	shell := flags.shell
	if shell == "" {
		shell = svcCLI.Shell
	}
	if shell == "" {
		shell = "bash"
	}

	// --- WorkDir ---
	workDir := flags.workDir
	if workDir == "" {
		workDir = svcCLI.WorkDir
	}
	if workDir == "" {
		workDir = svc.WorkDirInternal
	}
	if workDir == "" {
		workDir = svc.DirInternal
	}

	// --- User ---
	u := resolveUser(flags.user, svcCLI.User, flags.asRoot)

	// --- Env: start from config env, then apply flag env on top (flag wins). ---
	env := make(map[string]string)
	maps.Copy(env, svcCLI.Env)
	for _, kv := range flags.envVars {
		k, v, found := strings.Cut(kv, "=")
		if !found || k == "" {
			return shellOptions{}, fmt.Errorf("--env %q: expected KEY=VALUE format", kv)
		}
		env[k] = v
	}

	return shellOptions{
		Mode:    mode,
		Shell:   shell,
		User:    u,
		WorkDir: workDir,
		Env:     env,
	}, nil
}

// shellExecFunc is the function signature for executing a shell in a running container.
type shellExecFunc func(containerName, shell, u, workDir string, env map[string]string) error

// shellRunFunc is the function signature for starting a new container shell.
type shellRunFunc func(compose *docker.Compose, serviceName, shell, u, workDir string, env map[string]string) error

// runServicesCLI resolves the container state and either execs into a running
// container or starts a new one via docker compose run.
// getState, execCLI, and runCLI are injected for testability.
func runServicesCLI(
	cfg *config.DevboxConfig,
	compose *docker.Compose,
	serviceName string,
	flags shellCLIFlags,
	getState func(string) (string, error),
	execCLI shellExecFunc,
	runCLI shellRunFunc,
) error {
	svc, ok := cfg.Services[serviceName]
	if !ok {
		return fmt.Errorf("service %q not found", serviceName)
	}
	if svc.Container == "" {
		return fmt.Errorf("service %q has no container defined", serviceName)
	}

	opts, err := resolveShellOptions(flags, svc.CLI, svc)
	if err != nil {
		return err
	}

	// Validate the resolved mode — catches typos in devbox/services.yml or defaults.yml.
	if !validModes[opts.Mode] {
		return fmt.Errorf("invalid cli.mode %q for service %q: must be auto, exec, or run", opts.Mode, serviceName)
	}

	// Container name matches the container_name field in compose.yaml:
	// <project-full-name>-<container>, e.g. devbox-laravel-app-main.
	fullContainerName := compose.ProjectName + "-" + svc.Container

	switch opts.Mode {
	case "exec":
		// Always exec; error if container is not running.
		status, stateErr := getState(fullContainerName)
		if stateErr != nil {
			return fmt.Errorf("container %q: %w", fullContainerName, stateErr)
		}
		if status != "running" {
			return fmt.Errorf("container %q is not running — start it with 'devbox run'", fullContainerName)
		}
		return execCLI(fullContainerName, opts.Shell, opts.User, opts.WorkDir, opts.Env)
	case "run":
		// Always start a new container, regardless of current state.
		return runCLI(compose, svc.Container, opts.Shell, opts.User, opts.WorkDir, opts.Env)
	default: // "auto"
		status, stateErr := getState(fullContainerName)
		switch {
		case errors.Is(stateErr, errContainerNotFound):
			// Container does not exist — start a new one via compose run.
			return runCLI(compose, svc.Container, opts.Shell, opts.User, opts.WorkDir, opts.Env)
		case stateErr != nil:
			// Real Docker error (daemon down, permission denied, etc.) — surface it.
			return fmt.Errorf("container %q: %w", fullContainerName, stateErr)
		case status == "running":
			return execCLI(fullContainerName, opts.Shell, opts.User, opts.WorkDir, opts.Env)
		default:
			return fmt.Errorf(
				"container %q is %s — start it first with 'devbox run'",
				fullContainerName, status,
			)
		}
	}
}

// resolveUser returns the effective user string for -u flag.
// --root overrides everything; flag user overrides config user; fallback is current UID.
func resolveUser(flagUser, configUser string, asRoot bool) string {
	if asRoot {
		return "root"
	}
	if flagUser != "" {
		return flagUser
	}
	if configUser != "" {
		return configUser
	}
	if u, err := user.Current(); err == nil {
		return u.Uid
	}
	return ""
}

// errContainerNotFound is returned by containerStateStatus when the container
// does not exist (docker inspect "No such object"). It is distinct from a real
// Docker error (daemon down, permission denied, etc.) so callers can choose to
// fall back to "docker compose run" only for the absent-container case.
var errContainerNotFound = fmt.Errorf("container not found")

// containerStateStatus returns the Docker state status string for a container
// (e.g. "running", "exited", "paused"). Returns errContainerNotFound when the
// container does not exist, or the original Docker error for all other failures
// (daemon unreachable, permission denied, etc.).
// processEnv is applied to the docker process so that DOCKER_HOST / DOCKER_CONTEXT
// overrides from docker.yml process_env are honoured for the probe.
//
// Uses raw JSON output from docker inspect to avoid Docker's template engine
// raising "map has no entry for key" errors on containers without a State field.
func containerStateStatus(containerName string, processEnv []string, dockerBin string) (string, error) {
	cmd := exec.Command(dockerBin, "inspect", containerName) //nolint:gosec
	cmd.Env = processEnv
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if strings.Contains(strings.ToLower(string(exitErr.Stderr)), "no such object") {
				return "", errContainerNotFound
			}
			if len(exitErr.Stderr) > 0 {
				return "", fmt.Errorf("%s", strings.TrimSpace(string(exitErr.Stderr)))
			}
		}
		return "", err
	}
	// docker inspect returns a JSON array; parse the first element's State.Status.
	var items []struct {
		State *struct {
			Status string `json:"Status"`
		} `json:"State"`
	}
	if err := json.Unmarshal(out, &items); err != nil || len(items) == 0 || items[0].State == nil {
		// Object exists but has no usable State — treat as not found.
		return "", errContainerNotFound
	}
	return items[0].State.Status, nil
}

// dockerExecCLI runs an interactive shell in a running container via docker exec.
// processEnv is the OS-level environment for the docker process itself (e.g. DOCKER_CLI_HINTS=false).
func dockerExecCLI(containerName, shell, u, workDir string, env map[string]string, processEnv []string, dockerBin string) error {
	args := []string{"exec", "-it"}
	if u != "" {
		args = append(args, "-u", u)
	}
	if workDir != "" {
		args = append(args, "-w", workDir)
	}
	for _, k := range slices.Sorted(maps.Keys(env)) {
		args = append(args, "-e", k+"="+env[k])
	}
	args = append(args, containerName, shell)

	render.Stdout().Info(fmt.Sprintf("exec → %s", containerName))
	return runInteractive(processEnv, dockerBin, args...)
}

// composeRunCLI starts a new temporary container via docker compose run --rm.
// It uses the shared Compose struct for project name, file list, and global args.
func composeRunCLI(compose *docker.Compose, serviceName, shell, u, workDir string, env map[string]string) error {
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
	for _, k := range slices.Sorted(maps.Keys(env)) {
		args = append(args, "-e", k+"="+env[k])
	}
	args = append(args, serviceName, shell)

	render.Stdout().Info(fmt.Sprintf("run → %s (new container)", serviceName))
	return runInteractive(compose.BuildEnv(), compose.BinName(), args...)
}

// runInteractive executes a command with the current process's stdin/stdout/stderr,
// allowing full interactive terminal use. processEnv overrides the OS environment
// for the child process (nil means inherit unchanged).
func runInteractive(processEnv []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = processEnv
	return cmd.Run()
}
