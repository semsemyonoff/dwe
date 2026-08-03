package shell

import (
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

// ttyMode is the resolved --tty / --no-tty selection. The zero value is the
// historical auto-detect behaviour, so an unset mode changes nothing.
type ttyMode int

const (
	// ttyAuto allocates a PTY only when both host stdin and stdout are TTYs.
	ttyAuto ttyMode = iota
	// ttyOn forces a PTY (--tty).
	ttyOn
	// ttyOff suppresses the PTY (--no-tty).
	ttyOff
)

// wantTTY reports whether a pseudo-TTY should be requested.
func (m ttyMode) wantTTY() bool {
	switch m {
	case ttyOn:
		return true
	case ttyOff:
		return false
	default:
		return stdioInteractive()
	}
}

// dockerExecTTYFlags returns the stdin/TTY flag vector for `docker exec`.
//
//	auto  → ["-i","-t"] when both host stdio are TTYs, else ["-i"]
//	--tty → ["-i","-t"] with a terminal stdin, else ["-t"]
//	--no-tty → ["-i"]
//
// The dropped -i under --tty is load-bearing, not an optimisation: `docker exec
// -i -t` hard-fails when host stdin is not a terminal — "cannot attach stdin to
// a TTY-enabled container because stdin is not a terminal" — which is exactly
// the situation a forced TTY exists for (an agent or script whose stdio are
// pipes). Dropping -i keeps the PTY, at the cost of the child not reading host
// stdin; a caller that needs to pipe input in should not be forcing a TTY.
func dockerExecTTYFlags(mode ttyMode) []string {
	if !mode.wantTTY() {
		return []string{"-i"}
	}
	if !ttyDetector(os.Stdin) {
		return []string{"-t"}
	}
	return []string{"-i", "-t"}
}

// composeRunTTYFlags returns the TTY flag vector for `docker compose run`.
// Compose allocates a TTY by default; -T disables it. Stdin stays wired.
//
// --tty cannot be honoured here when host stdin is not a terminal: compose
// refuses outright (`--no-tty=false` errors with "cannot attach stdin to a
// TTY-enabled container because stdin is not a terminal") and its permissive
// spellings silently hand the child a pipe anyway. Verified against Docker
// 29.6. So the forced case degrades to the non-TTY vector, and the caller
// warns — see warnComposeTTYUnavailable.
func composeRunTTYFlags(mode ttyMode) []string {
	if mode.wantTTY() && ttyDetector(os.Stdin) {
		return []string{"-i"}
	}
	return []string{"-i", "-T"}
}

// oneShotTTYMode collapses the auto default onto the one-shot (`-c`) path.
//
// One-shot has never allocated a PTY regardless of host stdio — it is the
// scripting entry point and its stdout must stay byte-clean — so auto means off
// here, unlike the interactive path where auto follows the terminal. An explicit
// --tty / --no-tty still wins.
func oneShotTTYMode(mode ttyMode) ttyMode {
	if mode == ttyAuto {
		return ttyOff
	}
	return mode
}

// composeTTYUnavailable reports a --tty that the compose path cannot deliver,
// so the caller can say so instead of leaving the user to wonder why output is
// still block-buffered.
func composeTTYUnavailable(mode ttyMode) bool {
	return mode == ttyOn && !ttyDetector(os.Stdin)
}

// warnComposeTTYUnavailable writes the one-line explanation to stderr. stdout
// stays clean — callers on this path may be piping the child's output.
func warnComposeTTYUnavailable(serviceName string) {
	render.NewWriter(os.Stderr).Warning(fmt.Sprintf(
		"--tty ignored for %s: it starts a new container via `docker compose run`, "+
			"which cannot allocate a PTY while stdin is not a terminal. "+
			"Start the service first so the shell can `docker exec` into it.",
		serviceName,
	))
}

// appendUserWorkdirEnvArgs appends the shared -u / -w / -e flags onto a docker
// exec or compose run argv. Env vars are emitted in sorted key order so the
// resulting argv is deterministic.
func appendUserWorkdirEnvArgs(args []string, u, workDir string, env map[string]string) []string {
	if u != "" {
		args = append(args, "-u", u)
	}
	if workDir != "" {
		args = append(args, "-w", workDir)
	}
	for _, k := range slices.Sorted(maps.Keys(env)) {
		args = append(args, "-e", k+"="+env[k])
	}
	return args
}

// composeRunArgv builds the shared
// `compose [-p name] [-f file...] <global args> run [--rm] <run args...>`
// prefix used by the interactive and one-shot compose-run flows.
func composeRunArgv(compose *docker.Compose) []string {
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
	return args
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

// resolveShellTarget resolves the shell options shared by the interactive
// (runServicesCLI) and one-shot (runOneShotCommand) flows. It validates the
// service exists, has a container, and carries a valid cli.mode.
//
// It no longer derives a "<project>-<container>" name: the running container is
// located by compose labels at probe time (see serviceContainerState), which is
// correct under custom container_name and compose's default
// "<project>-<service>-<index>" naming.
//
// It returns the compose service name (svc.Container) the probe and `docker
// compose run` must target — svc.Container is the value compose stamps into the
// com.docker.compose.service label and the service key dwe uses everywhere
// (topology, stop, logs); it defaults to the folder key but may be overridden in
// service.yml, so callers must use it rather than the user-facing serviceName.
func resolveShellTarget(cfg *config.DweConfig, serviceName string, flags shellCLIFlags) (shellOptions, string, error) {
	svc, ok := cfg.Services[serviceName]
	if !ok {
		return shellOptions{}, "", fmt.Errorf("service %q not found", serviceName)
	}
	if svc.Container == "" {
		return shellOptions{}, "", fmt.Errorf("service %q has no container defined", serviceName)
	}

	opts, err := resolveShellOptions(flags, svc.CLI, svc)
	if err != nil {
		return shellOptions{}, "", err
	}

	// Validate the resolved mode — catches typos in workspace/services.yml or defaults.yml.
	if !validModes[opts.Mode] {
		return shellOptions{}, "", fmt.Errorf("invalid cli.mode %q for service %q: must be auto, exec, or run", opts.Mode, serviceName)
	}

	return opts, svc.Container, nil
}

// runServicesCLI resolves the container state and either execs into a running
// container or starts a new one via docker compose run.
// probe, execCLI, and runCLI are injected for testability.
func runServicesCLI(
	cfg *config.DweConfig,
	compose *docker.Compose,
	serviceName string,
	flags shellCLIFlags,
	probe containerProbeFunc,
	execCLI shellExecFunc,
	runCLI shellRunFunc,
) error {
	opts, composeService, err := resolveShellTarget(cfg, serviceName, flags)
	if err != nil {
		return err
	}

	switch opts.Mode {
	case "exec":
		// Always exec; error if the service's container is not running.
		name, status, stateErr := probe(composeService)
		if stateErr != nil {
			return fmt.Errorf("service %q: %w", serviceName, stateErr)
		}
		if status != "running" {
			return fmt.Errorf("service %q is not running — start it with 'dwe run'", serviceName)
		}
		return execCLI(name, opts.Shell, opts.User, opts.WorkDir, opts.Env)
	case "run":
		// Always start a new container, regardless of current state.
		return runCLI(compose, composeService, opts.Shell, opts.User, opts.WorkDir, opts.Env)
	default: // "auto"
		name, status, stateErr := probe(composeService)
		switch {
		case errors.Is(stateErr, errContainerNotFound):
			// Container does not exist — start a new one via compose run.
			return runCLI(compose, composeService, opts.Shell, opts.User, opts.WorkDir, opts.Env)
		case stateErr != nil:
			// Real Docker error (daemon down, permission denied, etc.) — surface it.
			return fmt.Errorf("service %q: %w", serviceName, stateErr)
		case status == "running":
			return execCLI(name, opts.Shell, opts.User, opts.WorkDir, opts.Env)
		default:
			return fmt.Errorf(
				"service %q container is %s — start it first with 'dwe run'",
				serviceName, status,
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

// errContainerNotFound is returned by serviceContainerState when no container
// carries the compose labels for the service. It is distinct from a real Docker
// error (daemon down, permission denied, etc.) so callers can choose to fall
// back to "docker compose run" only for the absent-container case.
var errContainerNotFound = fmt.Errorf("container not found")

// containerProbeFunc resolves a compose service's running container name and
// Docker state. Injected so tests drive the state machine without spawning a
// real docker process.
type containerProbeFunc func(service string) (containerName, status string, err error)

// serviceContainerState locates the container for compose service `service` in
// project `projectName` by matching the compose project + service labels, and
// returns its actual name (for `docker exec`) plus a coarse state ("running" or
// "stopped"). Returns errContainerNotFound when no labelled container exists, or
// the original Docker error for all other failures (daemon unreachable,
// permission denied, etc.). processEnv is applied to the docker process so
// DOCKER_HOST / DOCKER_CONTEXT overrides from docker.yml process_env are honoured.
//
// It resolves the container via docker.ServiceContainerName, which probes by the
// compose project + service labels and prefers a long-lived service container
// over an ephemeral one-off `compose run` container (so a concurrent `dwe shell
// --mode run` session is never exec'd into by mistake). A first
// `--filter status=running` pass yields the running container to exec into; a
// second `--all` pass distinguishes "stopped" from "absent". Empty stdout from a
// zero-exit `docker ps` unambiguously means "no match", and a real Docker error
// exits non-zero and is surfaced — so auto mode never silently starts a
// duplicate container on a probe hiccup.
func serviceContainerState(projectName, service string, processEnv []string, dockerBin string) (string, string, error) {
	if projectName == "" || service == "" {
		// Without both labels we cannot identify the container; treat as absent
		// so auto mode falls through to `docker compose run` rather than guessing.
		return "", "", errContainerNotFound
	}

	running, err := docker.ServiceContainerName(dockerBin, processEnv, projectName, service, true)
	if err != nil {
		return "", "", err
	}
	if running != "" {
		return running, "running", nil
	}

	any, err := docker.ServiceContainerName(dockerBin, processEnv, projectName, service, false)
	if err != nil {
		return "", "", err
	}
	if any != "" {
		return any, "stopped", nil
	}
	return "", "", errContainerNotFound
}

// dockerExecCLI runs an interactive shell in a running container via docker exec.
// processEnv is the OS-level environment for the docker process itself (e.g. DOCKER_CLI_HINTS=false).
func dockerExecCLI(containerName, shell, u, workDir string, env map[string]string, processEnv []string, dockerBin string, tty ttyMode) error {
	args := []string{"exec"}
	args = append(args, dockerExecTTYFlags(tty)...)
	args = appendUserWorkdirEnvArgs(args, u, workDir, env)
	args = append(args, containerName, shell)

	render.Stdout().Info(fmt.Sprintf("exec → %s", containerName))
	// docker exec does not read compose files, so cwd is irrelevant — inherit parent's.
	return runInteractive(processEnv, "", dockerBin, args...)
}

// composeRunCLI starts a new temporary container via docker compose run --rm.
// It uses the shared Compose struct for project name, file list, and global args.
func composeRunCLI(compose *docker.Compose, serviceName, shell, u, workDir string, env map[string]string, tty ttyMode) error {
	if composeTTYUnavailable(tty) {
		warnComposeTTYUnavailable(serviceName)
	}
	args := composeRunArgv(compose)
	args = append(args, composeRunTTYFlags(tty)...)
	args = appendUserWorkdirEnvArgs(args, u, workDir, env)
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
// `docker exec -i [-u u] [-w workDir] [-e K=V ...] <container> <shell> -c "<command>"`
// and exits without printing a banner. On *exec.ExitError, the error is wrapped
// as *shellCommandExitError so main.go can preserve the child's exit code.
//
// No PTY by default (-t omitted) so stdout stays clean for piping — a PTY makes
// docker translate \n to \r\n, which corrupts strict downstream parsers. The
// cost of that default is that the child sees a pipe on stdout and therefore
// switches to block buffering, so a long-running command looks hung until it
// exits; --tty opts into a PTY to get incremental output back.
func dockerExecOneShot(containerName, shell, u, workDir string, env map[string]string, command string, processEnv []string, dockerBin string, tty ttyMode) error {
	args := []string{"exec"}
	args = append(args, dockerExecTTYFlags(oneShotTTYMode(tty))...)
	args = appendUserWorkdirEnvArgs(args, u, workDir, env)
	args = append(args, containerName, shell, "-c", command)
	// Silent: no render.Stdout().Info — script stdout must stay clean.
	return wrapExitError(runInteractive(processEnv, "", dockerBin, args...))
}

// composeRunOneShot starts a fresh container via `docker compose run --rm` and
// runs `<shell> -c "<command>"` inside it, silently.
func composeRunOneShot(compose *docker.Compose, serviceName, shell, u, workDir string, env map[string]string, command string, tty ttyMode) error {
	if composeTTYUnavailable(tty) {
		warnComposeTTYUnavailable(serviceName)
	}
	args := composeRunArgv(compose)
	args = append(args, composeRunTTYFlags(oneShotTTYMode(tty))...)
	args = appendUserWorkdirEnvArgs(args, u, workDir, env)
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
	probeFn := func(service string) (string, string, error) {
		return serviceContainerState(compose.ProjectName, service, processEnv, dockerBin)
	}
	if flags.command != "" {
		execOneFn := func(c, sh, u, w string, env map[string]string, cmd string) error {
			return dockerExecOneShot(c, sh, u, w, env, cmd, processEnv, dockerBin, flags.tty)
		}
		runOneFn := func(cp *docker.Compose, svc, sh, u, w string, env map[string]string, cmd string) error {
			return composeRunOneShot(cp, svc, sh, u, w, env, cmd, flags.tty)
		}
		return runOneShotCommand(cfg, compose, serviceName, flags, probeFn, execOneFn, runOneFn)
	}
	execFn := func(c, sh, u, w string, env map[string]string) error {
		return dockerExecCLI(c, sh, u, w, env, processEnv, dockerBin, flags.tty)
	}
	runFn := func(cp *docker.Compose, svc, sh, u, w string, env map[string]string) error {
		return composeRunCLI(cp, svc, sh, u, w, env, flags.tty)
	}
	return runServicesCLI(cfg, compose, serviceName, flags, probeFn, execFn, runFn)
}

// oneShotExecFunc executes a single command in a running container.
type oneShotExecFunc func(containerName, shell, u, workDir string, env map[string]string, command string) error

// oneShotRunFunc starts a fresh container and runs a single command in it.
type oneShotRunFunc func(compose *docker.Compose, serviceName, shell, u, workDir string, env map[string]string, command string) error

// runOneShotCommand mirrors runServicesCLI's mode switch but invokes the one-shot
// helpers. probe/execOneShot/runOneShot are injected for testability.
func runOneShotCommand(
	cfg *config.DweConfig,
	compose *docker.Compose,
	serviceName string,
	flags shellCLIFlags,
	probe containerProbeFunc,
	execOneShot oneShotExecFunc,
	runOneShot oneShotRunFunc,
) error {
	opts, composeService, err := resolveShellTarget(cfg, serviceName, flags)
	if err != nil {
		return err
	}

	switch opts.Mode {
	case "exec":
		name, status, stateErr := probe(composeService)
		if stateErr != nil {
			return fmt.Errorf("service %q: %w", serviceName, stateErr)
		}
		if status != "running" {
			return fmt.Errorf("service %q is not running — start it with 'dwe run'", serviceName)
		}
		return execOneShot(name, opts.Shell, opts.User, opts.WorkDir, opts.Env, flags.command)
	case "run":
		return runOneShot(compose, composeService, opts.Shell, opts.User, opts.WorkDir, opts.Env, flags.command)
	default: // "auto"
		name, status, stateErr := probe(composeService)
		switch {
		case errors.Is(stateErr, errContainerNotFound):
			return runOneShot(compose, composeService, opts.Shell, opts.User, opts.WorkDir, opts.Env, flags.command)
		case stateErr != nil:
			return fmt.Errorf("service %q: %w", serviceName, stateErr)
		case status == "running":
			return execOneShot(name, opts.Shell, opts.User, opts.WorkDir, opts.Env, flags.command)
		default:
			return fmt.Errorf(
				"service %q container is %s — start it first with 'dwe run'",
				serviceName, status,
			)
		}
	}
}
