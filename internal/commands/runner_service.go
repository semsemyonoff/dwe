package commands

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/tpl"
)

// ServiceExecRunner executes type=service_exec commands via `docker compose exec`.
// When Mode is ExecModeExecOrRun it checks whether the target container is
// running and falls back to `docker compose run --rm` if it is not.
type ServiceExecRunner struct{}

// BuildCommand constructs the exec.Cmd for the given context.
// When mode is exec-or-run the container state is checked at build time:
// if the container is not running the command will use `run --rm` instead.
func (r *ServiceExecRunner) BuildCommand(ctx RunContext, projectFull string) (*exec.Cmd, error) {
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

	var composeFiles []string
	if ctx.Config != nil {
		composeFiles = ctx.Config.ComposeFiles()
	}

	// Determine exec vs run.
	useExec := true
	switch mode {
	case ExecModeRun:
		useExec = false
	case ExecModeExecOrRun:
		running, checkErr := isContainerRunning(projectFull, composeFiles, svc)
		if checkErr != nil {
			// On check failure, fall back to exec (let docker report the real error).
			running = true
		}
		useExec = running
	}

	return buildDockerComposeCmd(ctx, projectFull, composeFiles, svc, user, workdir, argv, envVars, useExec), nil
}

// Run executes the command inside the container.
func (r *ServiceExecRunner) Run(ctx RunContext) error {
	projectFull := resolveProjectFull(ctx.Config)
	c, err := r.BuildCommand(ctx, projectFull)
	if err != nil {
		return err
	}
	c.Stdout = stdout(ctx)
	c.Stderr = stderr(ctx)
	c.Stdin = os.Stdin
	return c.Run()
}

// ServiceRunRunner executes type=service_run commands via `docker compose run --rm`.
type ServiceRunRunner struct{}

// BuildCommand constructs the exec.Cmd for the given context.
func (r *ServiceRunRunner) BuildCommand(ctx RunContext, projectFull string) (*exec.Cmd, error) {
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

	var composeFiles []string
	if ctx.Config != nil {
		composeFiles = ctx.Config.ComposeFiles()
	}

	return buildDockerComposeCmd(ctx, projectFull, composeFiles, svc, user, workdir, argv, envVars, false), nil
}

// Run executes the command in a one-off container.
func (r *ServiceRunRunner) Run(ctx RunContext) error {
	projectFull := resolveProjectFull(ctx.Config)
	c, err := r.BuildCommand(ctx, projectFull)
	if err != nil {
		return err
	}
	c.Stdout = stdout(ctx)
	c.Stderr = stderr(ctx)
	c.Stdin = os.Stdin
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

// buildDockerComposeCmd assembles the full docker compose exec/run command.
func buildDockerComposeCmd(
	ctx RunContext,
	projectFull string,
	composeFiles []string,
	svc string,
	user UserMode,
	workdir string,
	serviceArgv []string,
	envVars map[string]string,
	useExec bool,
) *exec.Cmd {
	var args []string

	if projectFull != "" {
		args = append(args, "-p", projectFull)
	}
	for _, f := range composeFiles {
		args = append(args, "-f", f)
	}

	if useExec {
		args = append(args, "exec")
	} else {
		args = append(args, "run", "--rm", "--no-deps", "--entrypoint", "")
	}

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

	// Env vars passed via -e flags (each value is a separate argument, never shell-interpreted).
	for k, v := range envVars {
		args = append(args, "-e", k+"="+v)
	}

	// Service name.
	args = append(args, svc)

	// Command arguments.
	args = append(args, serviceArgv...)

	return exec.Command("docker", append([]string{"compose"}, args...)...) //nolint:gosec
}

// isContainerRunning checks whether the named service container is running
// in the given compose project.
func isContainerRunning(projectFull string, composeFiles []string, service string) (bool, error) {
	args := []string{"compose"}
	if projectFull != "" {
		args = append(args, "-p", projectFull)
	}
	for _, f := range composeFiles {
		args = append(args, "-f", f)
	}
	args = append(args, "ps", "--status", "running", "--format", "json", service)

	out, err := exec.Command("docker", args...).Output() //nolint:gosec
	if err != nil {
		return false, fmt.Errorf("docker compose ps: %w", err)
	}
	// If output contains any non-whitespace the container is running.
	return strings.TrimSpace(string(out)) != "", nil
}

// resolveProjectFull returns the compose project name from config (prefix-name).
func resolveProjectFull(cfg *config.DevboxConfig) string {
	if cfg == nil {
		return ""
	}
	return cfg.Project.FullName()
}
