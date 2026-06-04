package shell

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

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/shared/daemon"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
	"github.com/semsemyonoff/dwe/internal/shared/render"
)

// ttyDetector is the seam tests use to drive TTY auto-detect branches without
// touching real stdio. Defaults to widgets.IsInteractiveFn.
var ttyDetector = widgets.IsInteractiveFn

// stdioInteractive returns true when both host stdin and host stdout are TTYs.
func stdioInteractive() bool {
	return ttyDetector(os.Stdin) && ttyDetector(os.Stdout)
}

// dockerExecTTYFlags returns ["-i", "-t"] when interactive, else ["-i"].
// Stdin is always wired so piped input still reaches the child.
func dockerExecTTYFlags() []string {
	if stdioInteractive() {
		return []string{"-i", "-t"}
	}
	return []string{"-i"}
}

// composeRunTTYFlags returns the TTY flag vector for `docker compose run`.
// Compose allocates a TTY by default; -T disables it. Stdin stays wired.
func composeRunTTYFlags() []string {
	if stdioInteractive() {
		return []string{"-i"}
	}
	return []string{"-i", "-T"}
}

// shellCommandExitError carries a child command's exact exit code through
// cobra/fang. cmd/dwe/main.go extracts `interface{ ExitCode() int }` and calls
// os.Exit with that code; its errHandler also suppresses Fang's "Error:" banner
// for ExitCode-bearing errors so only the child's stdout/stderr is visible.
type shellCommandExitError struct {
	code       int
	underlying error
}

func (e *shellCommandExitError) Error() string { return e.underlying.Error() }
func (e *shellCommandExitError) ExitCode() int { return e.code }
func (e *shellCommandExitError) Unwrap() error { return e.underlying }

// wrapExitError converts an *exec.ExitError into a *shellCommandExitError so the
// child's exit code propagates to os.Exit. Non-exit errors are returned unchanged.
func wrapExitError(err error) error {
	if err == nil {
		return nil
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return &shellCommandExitError{code: exitErr.ExitCode(), underlying: exitErr}
	}
	return err
}

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
	cfg *config.DweConfig,
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

	// Validate the resolved mode — catches typos in workspace/services.yml or defaults.yml.
	if !validModes[opts.Mode] {
		return fmt.Errorf("invalid cli.mode %q for service %q: must be auto, exec, or run", opts.Mode, serviceName)
	}

	// Resolve the authoritative compose project name (handles absent docker.yml).
	projectFull, err := config.ResolveComposeProjectName(compose.BaseDir, cfg)
	if err != nil {
		return fmt.Errorf("resolving compose project name: %w", err)
	}
	fullContainerName, err := daemon.ResolveContainerName(projectFull, svc.Container)
	if err != nil {
		return err
	}

	switch opts.Mode {
	case "exec":
		// Always exec; error if container is not running.
		status, stateErr := getState(fullContainerName)
		if stateErr != nil {
			return fmt.Errorf("container %q: %w", fullContainerName, stateErr)
		}
		if status != "running" {
			return fmt.Errorf("container %q is not running — start it with 'dwe run'", fullContainerName)
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
				"container %q is %s — start it first with 'dwe run'",
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
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
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
	if err := json.Unmarshal(out, &items); err != nil {
		return "", fmt.Errorf("parsing docker inspect output: %w", err)
	}
	if len(items) == 0 || items[0].State == nil {
		// Object exists but has no usable State — treat as not found.
		return "", errContainerNotFound
	}
	return items[0].State.Status, nil
}

// dockerExecCLI runs an interactive shell in a running container via docker exec.
// processEnv is the OS-level environment for the docker process itself (e.g. DOCKER_CLI_HINTS=false).
func dockerExecCLI(containerName, shell, u, workDir string, env map[string]string, processEnv []string, dockerBin string) error {
	args := []string{"exec"}
	args = append(args, dockerExecTTYFlags()...)
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
	// docker exec does not read compose files, so cwd is irrelevant — inherit parent's.
	return runInteractive(processEnv, "", dockerBin, args...)
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
	args = append(args, "run")
	runArgs := compose.CommandArgs["run"]
	if !slices.Contains(runArgs, "--rm") {
		args = append(args, "--rm")
	}
	args = append(args, runArgs...)
	args = append(args, composeRunTTYFlags()...)
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
	return runInteractive(compose.BuildEnv(), compose.BaseDir, compose.BinName(), args...)
}

// runInteractive executes a command with the current process's stdin/stdout/stderr,
// allowing full interactive terminal use. processEnv overrides the OS environment
// for the child process (nil means inherit unchanged). workDir is the cwd for the
// child (empty inherits the parent CWD); compose-aware callers must pass
// compose.BaseDir so relative `-f` paths resolve against the project root.
//
// Exposed as a package variable so tests can inject a fake without spawning a
// real child process (used by one-shot helpers to drive exit-code wrapping).
var runInteractive = func(processEnv []string, workDir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = workDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = processEnv
	return cmd.Run()
}

// dockerExecOneShot runs a single command in a running container via
// `docker exec <ttyFlags> [-u u] [-w workDir] [-e K=V ...] <container> <shell> -c "<command>"`
// and exits without printing a banner. On *exec.ExitError, the error is wrapped
// as *shellCommandExitError so main.go can preserve the child's exit code.
func dockerExecOneShot(containerName, shell, u, workDir string, env map[string]string, command string, processEnv []string, dockerBin string) error {
	args := []string{"exec"}
	args = append(args, dockerExecTTYFlags()...)
	if u != "" {
		args = append(args, "-u", u)
	}
	if workDir != "" {
		args = append(args, "-w", workDir)
	}
	for _, k := range slices.Sorted(maps.Keys(env)) {
		args = append(args, "-e", k+"="+env[k])
	}
	args = append(args, containerName, shell, "-c", command)
	// Silent: no render.Stdout().Info — script stdout must stay clean.
	return wrapExitError(runInteractive(processEnv, "", dockerBin, args...))
}

// composeRunOneShot starts a fresh container via `docker compose run --rm` and
// runs `<shell> -c "<command>"` inside it, silently.
func composeRunOneShot(compose *docker.Compose, serviceName, shell, u, workDir string, env map[string]string, command string) error {
	args := []string{"compose"}
	if compose.ProjectName != "" {
		args = append(args, "-p", compose.ProjectName)
	}
	for _, f := range compose.Files {
		args = append(args, "-f", f)
	}
	args = append(args, compose.GlobalArgs...)
	args = append(args, "run")
	runArgs := compose.CommandArgs["run"]
	if !slices.Contains(runArgs, "--rm") {
		args = append(args, "--rm")
	}
	args = append(args, runArgs...)
	args = append(args, composeRunTTYFlags()...)
	if u != "" {
		args = append(args, "-u", u)
	}
	if workDir != "" {
		args = append(args, "-w", workDir)
	}
	for _, k := range slices.Sorted(maps.Keys(env)) {
		args = append(args, "-e", k+"="+env[k])
	}
	args = append(args, serviceName, shell, "-c", command)
	// Silent: no render.Stdout().Info.
	return wrapExitError(runInteractive(compose.BuildEnv(), compose.BaseDir, compose.BinName(), args...))
}

// dispatchShell routes to the one-shot path when flags.command is set, else to
// the interactive path. It is the single seam tests use to drive both branches
// without going through cobra plumbing.
func dispatchShell(
	cfg *config.DweConfig,
	compose *docker.Compose,
	serviceName string,
	flags shellCLIFlags,
	processEnv []string,
	dockerBin string,
) error {
	stateFn := func(name string) (string, error) {
		return containerStateStatus(name, processEnv, dockerBin)
	}
	if flags.command != "" {
		execOneFn := func(c, sh, u, w string, env map[string]string, cmd string) error {
			return dockerExecOneShot(c, sh, u, w, env, cmd, processEnv, dockerBin)
		}
		return runOneShotCommand(cfg, compose, serviceName, flags, stateFn, execOneFn, composeRunOneShot)
	}
	execFn := func(c, sh, u, w string, env map[string]string) error {
		return dockerExecCLI(c, sh, u, w, env, processEnv, dockerBin)
	}
	return runServicesCLI(cfg, compose, serviceName, flags, stateFn, execFn, composeRunCLI)
}

// oneShotExecFunc executes a single command in a running container.
type oneShotExecFunc func(containerName, shell, u, workDir string, env map[string]string, command string) error

// oneShotRunFunc starts a fresh container and runs a single command in it.
type oneShotRunFunc func(compose *docker.Compose, serviceName, shell, u, workDir string, env map[string]string, command string) error

// runOneShotCommand mirrors runServicesCLI's mode switch but invokes the one-shot
// helpers. getState/execOneShot/runOneShot are injected for testability.
func runOneShotCommand(
	cfg *config.DweConfig,
	compose *docker.Compose,
	serviceName string,
	flags shellCLIFlags,
	getState func(string) (string, error),
	execOneShot oneShotExecFunc,
	runOneShot oneShotRunFunc,
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

	if !validModes[opts.Mode] {
		return fmt.Errorf("invalid cli.mode %q for service %q: must be auto, exec, or run", opts.Mode, serviceName)
	}

	projectFull, err := config.ResolveComposeProjectName(compose.BaseDir, cfg)
	if err != nil {
		return fmt.Errorf("resolving compose project name: %w", err)
	}
	fullContainerName, err := daemon.ResolveContainerName(projectFull, svc.Container)
	if err != nil {
		return err
	}

	switch opts.Mode {
	case "exec":
		status, stateErr := getState(fullContainerName)
		if stateErr != nil {
			return fmt.Errorf("container %q: %w", fullContainerName, stateErr)
		}
		if status != "running" {
			return fmt.Errorf("container %q is not running — start it with 'dwe run'", fullContainerName)
		}
		return execOneShot(fullContainerName, opts.Shell, opts.User, opts.WorkDir, opts.Env, flags.command)
	case "run":
		return runOneShot(compose, svc.Container, opts.Shell, opts.User, opts.WorkDir, opts.Env, flags.command)
	default: // "auto"
		status, stateErr := getState(fullContainerName)
		switch {
		case errors.Is(stateErr, errContainerNotFound):
			return runOneShot(compose, svc.Container, opts.Shell, opts.User, opts.WorkDir, opts.Env, flags.command)
		case stateErr != nil:
			return fmt.Errorf("container %q: %w", fullContainerName, stateErr)
		case status == "running":
			return execOneShot(fullContainerName, opts.Shell, opts.User, opts.WorkDir, opts.Env, flags.command)
		default:
			return fmt.Errorf(
				"container %q is %s — start it first with 'dwe run'",
				fullContainerName, status,
			)
		}
	}
}
