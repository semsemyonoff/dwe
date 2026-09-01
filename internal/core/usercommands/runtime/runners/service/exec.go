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
//   - exec-or-fail (default): refuses with a clear dwe error.
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

	// The container probe runs BEFORE the argv is built, because building it may
	// execute the command's argv_append_from expression — a host side effect,
	// and one whose empty-output result short-circuits with
	// spec.ErrArgvAppendEmpty ("skipped: nothing to process"). Probing second
	// would report a stopped service as a clean skip and exit 0.
	useExec := true
	switch mode {
	case model.ExecModeRun:
		useExec = false
	case model.ExecModeExecOrFail:
		// Pre-check so that "service not running" surfaces as a clean dwe
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

	argv, err := buildServiceArgv(ctx, rc)
	if err != nil {
		return nil, err
	}

	envVars, err := runio.BuildRenderedEnv(rc.Cmd, rc)
	if err != nil {
		return nil, err
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
	defer runio.WireChildIO(rc, c)()
	return c.Run()
}

// workdirInternal is the workdir opt-out sentinel, mirroring
// model.UserModeInternal: emit no --workdir flag AND skip the service fallback,
// so the image's own WORKDIR applies.
const workdirInternal = "internal"

// resolveServiceFields returns the effective service, user, workdir, and mode
// for the command, applying runner overrides when present.
//
// The workdir chain, first non-empty wins:
//
//  1. workdir (or runner.workdir) == "internal" — no flag, fallback skipped, stop
//  2. runner.workdir_from → workdir_from
//  3. runner.workdir → workdir
//  4. services.<svc>.cli.workdir
//  5. services.<svc>.work_dir_internal
//  6. services.<svc>.dir_internal
//  7. no --workdir flag — the image's WORKDIR applies
//
// Rungs 4-6 are config.ContainerWorkdirFallback, the same chain `dwe shell`
// applies, so a shell session and a command into one service land together.
// The sentinel outranks workdir_from because opting out cannot be expressed
// any other way — for that one value the "workdir_from wins" rule inverts.
//
// The string-valued fields (service, workdir, workdir_from) are rendered as
// command-template expressions so they can reference ${param.*}, ${context.*},
// or any other entry in the command template space — same contract as argv,
// cmd, and compose_args. This unblocks generic per-service commands such as
// `service: app-${param.service}` invoked from a per-service deploy pipeline.
func resolveServiceFields(ctx spec.RunContext) (svc string, user model.UserMode, workdir string, mode model.ExecMode, err error) {
	cmd := ctx.Cmd

	svc = cmd.EffectiveService()
	user = cmd.EffectiveUser()
	mode = cmd.Mode
	if cmd.Runner != nil && cmd.Runner.Mode != "" {
		mode = cmd.Runner.Mode
	}

	wdLiteral := cmd.EffectiveWorkdir()
	wdFrom := cmd.EffectiveWorkdirFrom()

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

	if wdLiteral != workdirInternal {
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
		if workdir == "" {
			workdir = config.ContainerWorkdirFallback(ctx.Config, svc)
		}
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
	svc, ok := config.ServiceByContainer(cfg, container)
	if !ok {
		return ""
	}
	return svc.CLI.User
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
//
// ctx is the cancellation context rather than a decoration: an argv_append_from
// expression is executed here, on the host, and must die with the invocation.
func buildServiceArgv(ctx context.Context, rc spec.RunContext) ([]string, error) {
	cmd := rc.Cmd
	if cmd.Cmd != "" {
		script, positional, err := runio.RenderShellCommand(cmd.Cmd, rc.Render)
		if err != nil {
			return nil, err
		}
		// positional is nil unless the template has a ${args} slot; the shell
		// then binds them to "$@" without them ever entering the program text.
		return append([]string{config.ShellBin(rc.Config), "-c", script}, positional...), nil
	}
	argv, err := runio.RenderArgvWithArgs(cmd.Argv, rc.Render)
	if err != nil {
		return nil, err
	}
	return runio.AppendArgvFrom(ctx, rc, argv)
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

	// Forward colour-forcing env vars into the container when the output is
	// captured or piped but still lands on a terminal: workflow parallel
	// sub-steps (LineTee failure dumps + always_show_output) and color-forced
	// bridge runs alike. The child keeps its colours even though docker
	// compose attaches a pipe rather than a TTY.
	if envVars == nil {
		envVars = make(map[string]string)
	}
	for _, kv := range runio.ColorForceEnv(rc) {
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
	cmd.Dir = compose.BaseDir
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
	cmd.Dir = compose.BaseDir
	cmd.Env = compose.BuildEnv()
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("%s compose ps: %w", compose.BinName(), err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}
