package runtime

import (
	"fmt"
	"maps"
	"os/exec"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/docker"
	"devbox-cli/internal/tpl"
	"devbox-cli/internal/usercommands/model"
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
		mode = model.ExecModeExec
	}

	argv, err := buildServiceArgv(ctx)
	if err != nil {
		return nil, err
	}

	envVars, err := buildRenderedEnv(ctx.Cmd, ctx)
	if err != nil {
		return nil, err
	}

	useExec := true
	switch mode {
	case model.ExecModeRun:
		useExec = false
	case model.ExecModeExecOrRun:
		running, checkErr := isContainerRunning(compose, svc)
		if checkErr != nil {
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
func resolveServiceFields(ctx RunContext) (svc string, user model.UserMode, workdir string, mode model.ExecMode, err error) {
	cmd := ctx.Cmd

	svc = cmd.Service
	user = cmd.User
	mode = cmd.Mode

	wdLiteral := cmd.Workdir
	wdFrom := cmd.WorkdirFrom
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
		if cmd.Runner.Workdir != "" {
			wdLiteral = cmd.Runner.Workdir
		}
		if cmd.Runner.WorkdirFrom != "" {
			wdFrom = cmd.Runner.WorkdirFrom
		}
	}

	if wdFrom != "" {
		var resolved string
		resolved, err = resolveWorkdirFrom(wdFrom, ctx)
		if err != nil {
			return
		}
		workdir = resolved
	}
	if workdir == "" {
		workdir = wdLiteral
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
		return "", nil
	}
	v, found := config.ResolvePath(ctx.Config.Raw, dotPath)
	if !found {
		return "", nil
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
// argument slice.
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
	compose *docker.Compose,
	svc string,
	user model.UserMode,
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

	args = append(args, compose.GlobalArgs...)

	if useExec {
		args = append(args, "exec")
		if defaults, ok := compose.CommandArgs["exec"]; ok {
			args = append(args, defaults...)
		}
	} else {
		args = append(args, "run")
		if defaults, ok := compose.CommandArgs["run"]; ok {
			args = append(args, defaults...)
		}
		args = append(args, "--no-deps", "--entrypoint", "")
	}

	args = append(args, composeArgs...)

	switch user {
	case model.UserModeCurrent:
		if ctx.Render != nil {
			args = append(args, "--user", ctx.Render.Host.UID+":"+ctx.Render.Host.GID)
		}
	case model.UserModeRoot:
		args = append(args, "--user", "root")
	case "":
		// No user flag.
	default:
		args = append(args, "--user", string(user))
	}

	if workdir != "" {
		args = append(args, "--workdir", workdir)
	}

	for k := range envVars {
		args = append(args, "-e", k)
	}

	args = append(args, svc)
	args = append(args, serviceArgv...)

	cmd := exec.Command("docker", append([]string{"compose"}, args...)...) //nolint:gosec
	combined := make(map[string]string, len(compose.ProcessEnv)+len(envVars))
	maps.Copy(combined, compose.ProcessEnv)
	maps.Copy(combined, envVars)
	cmd.Env = docker.MergeEnv(combined)
	return cmd
}

// isContainerRunning checks whether the named service container is running.
func isContainerRunning(compose *docker.Compose, service string) (bool, error) {
	args := compose.BuildInternalArgs("ps", "--status", "running", "--format", "json", service)

	cmd := exec.Command("docker", args...) //nolint:gosec
	cmd.Env = compose.BuildEnv()
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("docker compose ps: %w", err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}
