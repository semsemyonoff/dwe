// Package service implements the runtime runners for type=service commands:
// service.ExecRunner (mode=exec / exec-or-fail / exec-or-run) and
// service.RunRunner (mode=run, ephemeral container). Both drive
// `docker compose` and share helpers for argv rendering, env construction,
// and user/workdir resolution.
package service

import (
	"context"
	"fmt"
	"maps"
	"os/exec"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/internal/runio"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/spec"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
	"github.com/semsemyonoff/dwe/internal/shared/render"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// ExecRunner executes type=service_exec commands via `docker compose exec`.
// The behaviour when the target container is not running depends on Mode:
//   - exec-or-fail (default): refuses with a clear devbox error.
//   - exec-or-run: silently falls back to `docker compose run --rm` (with a
//     warning written to stderr so the ephemeral-container behaviour is visible).
//   - exec / run: forced; exec lets compose emit its own error if the container
//     is missing.
type ExecRunner struct{}

// BuildCommand constructs the exec.Cmd for the given context.
// See ExecRunner for mode semantics. The supplied ctx is attached to
// the returned *exec.Cmd so callers can cancel the child by cancelling ctx.
func (e *ExecRunner) BuildCommand(ctx context.Context, rc spec.RunContext, compose *docker.Compose) (*exec.Cmd, error) {
	svc, user, workdir, mode, err := resolveServiceFields(rc)
	if err != nil {
		return nil, err
	}
	if mode == "" {
		mode = model.DefaultExecMode
	}

	argv, err := buildServiceArgv(rc)
	if err != nil {
		return nil, err
	}

	envVars, err := runio.BuildRenderedEnv(rc.Cmd, rc)
	if err != nil {
		return nil, err
	}

	useExec := true
	switch mode {
	case model.ExecModeRun:
		useExec = false
	case model.ExecModeExecOrFail:
		// Pre-check so that "service not running" surfaces as a clean devbox
		// error rather than a raw compose stderr trace.
		running, checkErr := isContainerRunning(compose, svc)
		if checkErr == nil && !running {
			return nil, fmt.Errorf("service %q is not running (mode: exec-or-fail). Start it with `dwe docker up %s`, or set `mode: exec-or-run` if a one-off ephemeral container is acceptable", svc, svc)
		}
		// On probe error we proceed; compose will fail with its own error if needed.
	case model.ExecModeExecOrRun:
		running, checkErr := isContainerRunning(compose, svc)
		if checkErr != nil {
			running = true
		}
		useExec = running
		if !running {
			render.NewWriter(runio.StderrOf(rc)).Warning(fmt.Sprintf("service %q is not running — falling back to ephemeral `docker compose run --rm`; state will not persist between invocations", svc))
		}
	}

	composeArgs, err := buildRenderedComposeArgs(rc)
	if err != nil {
		return nil, err
	}

	return buildDockerComposeCmd(ctx, rc, compose, svc, user, workdir, argv, envVars, composeArgs, useExec), nil
}

// Run executes the command inside the container.
func (e *ExecRunner) Run(ctx context.Context, rc spec.RunContext) error {
	compose := rc.Compose()
	c, err := e.BuildCommand(ctx, rc, compose)
	if err != nil {
		return err
	}
	used, cleanup := runio.ParallelChildIO(rc, c, runio.StdoutOf(rc))
	defer cleanup()
	if !used {
		c.Stdout = runio.StdoutOf(rc)
		c.Stderr = runio.StderrOf(rc)
		c.Stdin = runio.StdinOrOS(rc)
	}
	return c.Run()
}

// resolveServiceFields returns the effective service, user, workdir, and mode
// for the command, applying runner overrides when present.
//
// The string-valued fields (service, workdir, workdir_from) are rendered as
// command-template expressions so they can reference ${param.*}, ${context.*},
// or any other entry in the command template space — same contract as argv,
// cmd, and compose_args. This unblocks generic per-service commands such as
// `service: app-${param.service}` invoked from a per-service deploy pipeline.
func resolveServiceFields(ctx spec.RunContext) (svc string, user model.UserMode, workdir string, mode model.ExecMode, err error) {
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

	if svc, err = tpl.RenderCommand(svc, ctx.Render); err != nil {
		err = fmt.Errorf("render service: %w", err)
		return
	}
	if wdLiteral, err = tpl.RenderCommand(wdLiteral, ctx.Render); err != nil {
		err = fmt.Errorf("render workdir: %w", err)
		return
	}
	if wdFrom, err = tpl.RenderCommand(wdFrom, ctx.Render); err != nil {
		err = fmt.Errorf("render workdir_from: %w", err)
		return
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

	if user == "" {
		if cliUser := lookupServiceCLIUser(ctx.Config, svc); cliUser != "" {
			user = model.UserMode(cliUser)
		}
	}

	if svc == "" {
		err = fmt.Errorf("service name is empty")
	}
	return
}

// lookupServiceCLIUser returns services.<svc>.cli.user for the service whose
// Container field matches the given compose service name, or "" when no match
// is found (or the matched entry has no cli.user set).
func lookupServiceCLIUser(cfg *config.DweConfig, container string) string {
	if cfg == nil || container == "" {
		return ""
	}
	for _, s := range cfg.Services {
		if s.Container == container {
			return s.CLI.User
		}
	}
	return ""
}

// resolveWorkdirFrom resolves a dot-path into the config Raw map and returns
// the string value.
func resolveWorkdirFrom(dotPath string, ctx spec.RunContext) (string, error) {
	v, err := config.LookupDotPath(ctx.Config, dotPath)
	if err != nil {
		return "", fmt.Errorf("workdir_from %q: %w", dotPath, err)
	}
	if v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("workdir_from %q: resolved value is not a string", dotPath)
	}
	return s, nil
}

// buildRenderedComposeArgs renders each compose_args entry via templates and returns the slice.
func buildRenderedComposeArgs(ctx spec.RunContext) ([]string, error) {
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

// buildServiceArgv renders the cmd/argv fields of the command and returns the
// argument slice.
func buildServiceArgv(ctx spec.RunContext) ([]string, error) {
	cmd := ctx.Cmd
	if cmd.Cmd != "" {
		rendered, err := tpl.RenderCommand(cmd.Cmd, ctx.Render)
		if err != nil {
			return nil, fmt.Errorf("render cmd: %w", err)
		}
		return []string{config.ShellBin(ctx.Config), "-c", rendered}, nil
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
	ctx context.Context,
	rc spec.RunContext,
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
		if rc.Render != nil {
			args = append(args, "--user", rc.Render.Host.UID+":"+rc.Render.Host.GID)
		}
	case model.UserModeRoot:
		args = append(args, "--user", "root")
	case "", model.UserModeInternal:
		// No user flag — image's built-in USER is used.
	default:
		args = append(args, "--user", string(user))
	}

	if workdir != "" {
		args = append(args, "--workdir", workdir)
	}

	// Forward colour-forcing env vars into the container when running as a
	// workflow parallel sub-step. The captured stdout is then dumped to the
	// user's terminal verbatim (failure dumps + always_show_output), so we
	// want the child to keep its colours even though docker compose attaches
	// a pipe rather than a TTY.
	if envVars == nil {
		envVars = make(map[string]string)
	}
	for _, kv := range runio.ParallelColorForceEnv(rc) {
		// kv is "KEY=VALUE"; split once.
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			k, v := kv[:eq], kv[eq+1:]
			if _, exists := envVars[k]; !exists {
				envVars[k] = v
			}
		}
	}

	for k := range envVars {
		args = append(args, "-e", k)
	}

	args = append(args, svc)
	args = append(args, serviceArgv...)

	cmd := exec.CommandContext(ctx, compose.BinName(), append([]string{"compose"}, args...)...) //nolint:gosec
	runio.BindCancel(cmd)
	combined := make(map[string]string, len(compose.ProcessEnv)+len(envVars))
	maps.Copy(combined, compose.ProcessEnv)
	maps.Copy(combined, envVars)
	cmd.Env = docker.MergeEnv(combined)
	return cmd
}

// isContainerRunning checks whether the named service container is running.
func isContainerRunning(compose *docker.Compose, service string) (bool, error) {
	args := compose.BuildInternalArgs("ps", "--status", "running", "--format", "json", service)

	cmd := exec.Command(compose.BinName(), args...) //nolint:gosec
	cmd.Env = compose.BuildEnv()
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("%s compose ps: %w", compose.BinName(), err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}
