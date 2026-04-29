package commands

import (
	"fmt"
	"maps"
	"os/exec"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/docker"
	"devbox-cli/internal/tpl"
)

// ServiceExecRunner executes type=service_exec commands via `docker compose exec`.
// When Mode is ExecModeExecOrRun it checks whether the target container is
// running and falls back to `docker compose run --rm` if it is not.
type ServiceExecRunner struct{}

// BuildCommand constructs the exec.Cmd for the given context.
// When mode is exec-or-run the container state is checked at build time:
// if the container is not running the command will use `run --rm` instead.
func (r *ServiceExecRunner) BuildCommand(ctx RunContext, compose *docker.Compose) (*exec.Cmd, error) {
	svc, user, workdir, mode, err := resolveServiceFields(ctx)
	if err != nil {
		return nil, err
	}
	if mode == "" {
		mode = ExecModeExec
	}

	argv, err := buildServiceArgv(ctx)
	if err != nil {
		return nil, err
	}

	envVars, err := buildRenderedEnv(ctx.Cmd, ctx)
	if err != nil {
		return nil, err
	}

	// Determine exec vs run.
	useExec := true
	switch mode {
	case ExecModeRun:
		useExec = false
	case ExecModeExecOrRun:
		running, checkErr := isContainerRunning(compose, svc)
		if checkErr != nil {
			// On check failure, fall back to exec (let docker report the real error).
			running = true
		}
		useExec = running
	}

	composeArgs, err := buildRenderedComposeArgs(ctx)
	if err != nil {
		return nil, err
	}

	return buildDockerComposeCmd(ctx, compose, svc, user, workdir, argv, envVars, composeArgs, useExec), nil
}

// Run executes the command inside the container.
func (r *ServiceExecRunner) Run(ctx RunContext) error {
	compose := ctx.Compose()
	c, err := r.BuildCommand(ctx, compose)
	if err != nil {
		return err
	}
	c.Stdout = stdout(ctx)
	c.Stderr = stderr(ctx)
	c.Stdin = stdinOrOS(ctx)
	return c.Run()
}

// ServiceRunRunner executes type=service_run commands via `docker compose run --rm`.
type ServiceRunRunner struct{}

// BuildCommand constructs the exec.Cmd for the given context.
func (r *ServiceRunRunner) BuildCommand(ctx RunContext, compose *docker.Compose) (*exec.Cmd, error) {
	svc, user, workdir, _, err := resolveServiceFields(ctx)
	if err != nil {
		return nil, err
	}

	argv, err := buildServiceArgv(ctx)
	if err != nil {
		return nil, err
	}

	envVars, err := buildRenderedEnv(ctx.Cmd, ctx)
	if err != nil {
		return nil, err
	}

	composeArgs, err := buildRenderedComposeArgs(ctx)
	if err != nil {
		return nil, err
	}

	return buildDockerComposeCmd(ctx, compose, svc, user, workdir, argv, envVars, composeArgs, false), nil
}

// Run executes the command in a one-off container.
func (r *ServiceRunRunner) Run(ctx RunContext) error {
	compose := ctx.Compose()
	c, err := r.BuildCommand(ctx, compose)
	if err != nil {
		return err
	}
	c.Stdout = stdout(ctx)
	c.Stderr = stderr(ctx)
	c.Stdin = stdinOrOS(ctx)
	return c.Run()
}

// resolveServiceFields returns the effective service, user, workdir, and mode
// for the command, applying runner overrides when present.
func resolveServiceFields(ctx RunContext) (svc string, user UserMode, workdir string, mode ExecMode, err error) {
	cmd := ctx.Cmd

	// Start with top-level fields.
	svc = cmd.Service
	user = cmd.User
	workdir = cmd.Workdir
	mode = cmd.Mode

	// Apply runner override when present.
	if cmd.Runner != nil {
		if cmd.Runner.Service != "" {
			svc = cmd.Runner.Service
		}
		if cmd.Runner.User != "" {
			user = cmd.Runner.User
		}
		if cmd.Runner.Mode != "" {
			mode = cmd.Runner.Mode
		}
		// runner.workdir takes precedence over runner.workdir_from.
		if cmd.Runner.Workdir != "" {
			workdir = cmd.Runner.Workdir
		} else if cmd.Runner.WorkdirFrom != "" {
			workdir, err = resolveWorkdirFrom(cmd.Runner.WorkdirFrom, ctx)
			if err != nil {
				return
			}
		}
	} else if cmd.WorkdirFrom != "" && workdir == "" {
		// Top-level workdir_from fallback.
		workdir, err = resolveWorkdirFrom(cmd.WorkdirFrom, ctx)
		if err != nil {
			return
		}
	}

	if svc == "" {
		err = fmt.Errorf("service name is empty")
	}
	return
}

// resolveWorkdirFrom resolves a dot-path into the config Raw map and returns
// the string value.
func resolveWorkdirFrom(dotPath string, ctx RunContext) (string, error) {
	if ctx.Config == nil {
		return "", fmt.Errorf("workdir_from %q: config is nil", dotPath)
	}
	v, found := config.ResolvePath(ctx.Config.Raw, dotPath)
	if !found {
		return "", fmt.Errorf("workdir_from %q: path not found in config", dotPath)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("workdir_from %q: value is not a string", dotPath)
	}
	return s, nil
}

// buildRenderedComposeArgs renders each compose_args entry via templates and returns the slice.
func buildRenderedComposeArgs(ctx RunContext) ([]string, error) {
	cmd := ctx.Cmd
	if len(cmd.ComposeArgs) == 0 {
		return nil, nil
	}

	rendered := make([]string, len(cmd.ComposeArgs))
	for i, arg := range cmd.ComposeArgs {
		r, err := tpl.RenderCommand(arg, ctx.Render)
		if err != nil {
			return nil, fmt.Errorf("render compose_args[%d]: %w", i, err)
		}
		rendered[i] = r
	}
	return rendered, nil
}

// buildServiceArgv renders the run/argv fields of the command and returns the
// argument slice (the part that follows the service name in the compose command).
func buildServiceArgv(ctx RunContext) ([]string, error) {
	cmd := ctx.Cmd
	if cmd.Run != "" {
		rendered, err := tpl.RenderCommand(cmd.Run, ctx.Render)
		if err != nil {
			return nil, fmt.Errorf("render run: %w", err)
		}
		return []string{"sh", "-c", rendered}, nil
	}
	rendered := make([]string, len(cmd.Argv))
	for i, arg := range cmd.Argv {
		r, err := tpl.RenderCommand(arg, ctx.Render)
		if err != nil {
			return nil, fmt.Errorf("render argv[%d]: %w", i, err)
		}
		rendered[i] = r
	}
	return rendered, nil
}

// buildDockerComposeCmd assembles the full docker compose exec/run command
// using the shared Compose struct for project name, file list, and global args.
func buildDockerComposeCmd(
	ctx RunContext,
	compose *docker.Compose,
	svc string,
	user UserMode,
	workdir string,
	serviceArgv []string,
	envVars map[string]string,
	composeArgs []string,
	useExec bool,
) *exec.Cmd {
	var args []string

	if compose.ProjectName != "" {
		args = append(args, "-p", compose.ProjectName)
	}
	for _, f := range compose.Files {
		args = append(args, "-f", f)
	}

	// Global args from docker policy (e.g. --ansi always --progress tty).
	args = append(args, compose.GlobalArgs...)

	if useExec {
		args = append(args, "exec")
		// Per-command default args from docker policy.
		if defaults, ok := compose.CommandArgs["exec"]; ok {
			args = append(args, defaults...)
		}
	} else {
		args = append(args, "run")
		// Per-command default args from docker policy.
		if defaults, ok := compose.CommandArgs["run"]; ok {
			args = append(args, defaults...)
		}
		args = append(args, "--no-deps", "--entrypoint", "")
	}

	// Custom compose args from command definition (e.g. -T, --name, -d, --rm).
	args = append(args, composeArgs...)

	// User flag.
	switch user {
	case UserModeCurrent:
		if ctx.Render != nil {
			args = append(args, "--user", ctx.Render.Host.UID+":"+ctx.Render.Host.GID)
		}
	case UserModeRoot:
		args = append(args, "--user", "root")
	case "":
		// No user flag.
	default:
		args = append(args, "--user", string(user))
	}

	// Workdir flag.
	if workdir != "" {
		args = append(args, "--workdir", workdir)
	}

	// Env vars: inject into the docker process environment and pass -e KEY
	// (name only) so docker compose forwards them into the container.
	// This avoids exposing secret values in argv (visible via ps/procfs).
	for k := range envVars {
		args = append(args, "-e", k)
	}

	// Service name.
	args = append(args, svc)

	// Command arguments.
	args = append(args, serviceArgv...)

	cmd := exec.Command("docker", append([]string{"compose"}, args...)...) //nolint:gosec
	// Merge compose process env with command-level env vars so that -e KEY
	// (name only) above picks up the actual values from the process environment.
	combined := make(map[string]string, len(compose.ProcessEnv)+len(envVars))
	maps.Copy(combined, compose.ProcessEnv)
	maps.Copy(combined, envVars)
	cmd.Env = docker.MergeEnv(combined)
	return cmd
}

// isContainerRunning checks whether the named service container is running
// in the given compose project using the shared Compose struct.
func isContainerRunning(compose *docker.Compose, service string) (bool, error) {
	args := compose.BuildInternalArgs("ps", "--status", "running", "--format", "json", service)

	cmd := exec.Command("docker", args...) //nolint:gosec
	cmd.Env = compose.BuildEnv()
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("docker compose ps: %w", err)
	}
	// If output contains any non-whitespace the container is running.
	return strings.TrimSpace(string(out)) != "", nil
}
