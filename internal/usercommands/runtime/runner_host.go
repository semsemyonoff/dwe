package runtime

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/shared/tpl"
	"devbox-cli/internal/usercommands/model"
	"devbox-cli/internal/usercommands/resolve"
)

// childTermDelay is the grace period exec.CommandContext gives a child after
// SIGTERM before sending SIGKILL when ctx is cancelled.
const childTermDelay = 5 * time.Second

// parallelColorForceEnv returns env entries that coerce common CLI tools to
// keep ANSI colours when the child's stdout is a pipe rather than a TTY.
// Inside a workflow parallel sub-step each child writes through a LineTee
// (no PTY is allocated — concurrent sub-steps cannot share one), so without
// these vars tools like lipgloss, npm/yarn, jest, chalk-based tools, BSD
// ls, brew, and others auto-disable colours and the captured failure /
// always_show_output dump on stderr ends up plain text.
//
// Returns nil outside parallel so non-parallel runs keep the existing
// auto-detection behaviour.
func parallelColorForceEnv(rc RunContext) []string {
	if !rc.UnderParallel {
		return nil
	}
	if os.Getenv("NO_COLOR") != "" {
		return nil
	}
	return []string{
		"CLICOLOR_FORCE=1",    // BSD ls, brew, lipgloss
		"FORCE_COLOR=1",       // Node ecosystem (npm/yarn/jest/eslint/chalk)
		"COLORTERM=truecolor", // anything that key-checks COLORTERM
	}
}

// bindCancel configures cmd to send SIGTERM (instead of the default SIGKILL)
// when its context is cancelled, and to force-kill after childTermDelay.
// Call this immediately after exec.CommandContext to give children a chance
// to clean up.
func bindCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = childTermDelay
}

// DevboxRunner executes type=devbox commands by invoking the current devbox
// executable with the run: string as its arguments.
type DevboxRunner struct{}

// Run executes the devbox subcommand described by rc.Cmd.
func (r *DevboxRunner) Run(ctx context.Context, rc RunContext) error {
	bin, err := os.Executable()
	if err != nil {
		bin = config.DevboxBin(rc.Config)
	}

	rendered, err := tpl.RenderCommand(rc.Cmd.Cmd, rc.Render)
	if err != nil {
		return fmt.Errorf("render cmd: %w", err)
	}

	cmd := exec.CommandContext(ctx, config.ShellBin(rc.Config), "-c", shellQuote(bin)+" "+rendered) //nolint:gosec
	bindCancel(cmd)
	if rc.ProjectRoot != "" {
		cmd.Dir = rc.ProjectRoot
	}

	envMap, err := buildRenderedEnv(rc.Cmd, rc)
	if err != nil {
		return err
	}
	colorEnv := parallelColorForceEnv(rc)
	if len(envMap) > 0 || len(colorEnv) > 0 {
		cmd.Env = os.Environ()
		for k, v := range envMap {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		for _, kv := range colorEnv {
			if eq := strings.IndexByte(kv, '='); eq > 0 {
				if _, exists := envMap[kv[:eq]]; !exists {
					cmd.Env = append(cmd.Env, kv)
				}
			}
		}
	}

	used, cleanup := parallelChildIO(rc, cmd, stdout(rc))
	defer cleanup()
	if !used {
		cmd.Stdout = stdout(rc)
		cmd.Stderr = stderr(rc)
		cmd.Stdin = stdinOrOS(rc)
	}
	return cmd.Run()
}

// HostRunner executes type=shell commands on the host machine.
type HostRunner struct{}

// BuildCommand constructs the exec.Cmd that would be run for the given context.
// It is exported for testing without actual execution. The supplied ctx is
// attached to the returned *exec.Cmd via exec.CommandContext so callers can
// cancel the child by cancelling ctx.
func (r *HostRunner) BuildCommand(ctx context.Context, rc RunContext) (*exec.Cmd, error) {
	cmd := rc.Cmd

	var argv []string
	if cmd.Cmd != "" {
		rendered, err := tpl.RenderCommand(cmd.Cmd, rc.Render)
		if err != nil {
			return nil, fmt.Errorf("render cmd: %w", err)
		}
		argv = []string{config.ShellBin(rc.Config), "-c", rendered}
	} else {
		rendered := make([]string, len(cmd.Argv))
		for i, arg := range cmd.Argv {
			r, err := tpl.RenderCommand(arg, rc.Render)
			if err != nil {
				return nil, fmt.Errorf("render argv[%d]: %w", i, err)
			}
			rendered[i] = r
		}
		argv = rendered
	}

	if len(argv) == 0 {
		return nil, fmt.Errorf("argv is empty")
	}
	c := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec
	bindCancel(c)

	if cmd.Workdir != "" {
		rendered, err := tpl.RenderCommand(cmd.Workdir, rc.Render)
		if err != nil {
			return nil, fmt.Errorf("render workdir: %w", err)
		}
		if !filepath.IsAbs(rendered) && rc.ProjectRoot != "" {
			rendered = filepath.Join(rc.ProjectRoot, rendered)
		}
		c.Dir = rendered
	} else if rc.ProjectRoot != "" {
		c.Dir = rc.ProjectRoot
	}

	envMap, err := buildRenderedEnv(cmd, rc)
	if err != nil {
		return nil, err
	}
	contractEnv := hostContractEnv(rc)
	colorEnv := parallelColorForceEnv(rc)
	if len(envMap) > 0 || len(contractEnv) > 0 || len(colorEnv) > 0 {
		c.Env = os.Environ()
		for k, v := range envMap {
			c.Env = append(c.Env, k+"="+v)
		}
		c.Env = append(c.Env, contractEnv...)
		for _, kv := range colorEnv {
			if eq := strings.IndexByte(kv, '='); eq > 0 {
				if _, exists := envMap[kv[:eq]]; !exists {
					c.Env = append(c.Env, kv)
				}
			}
		}
	}

	return c, nil
}

// hostContractEnv returns the contract environment variables exported into
// every type:shell subprocess so shell snippets can talk back to the host
// devbox CLI and the active docker compose project without rediscovery:
//
//	DEVBOX_BIN            absolute path to the running devbox binary
//	COMPOSE_PROJECT_NAME  active compose project name (e.g. devbox-laravel)
//	COMPOSE_FILE          colon-joined list of active overlay paths
//	                      (absolute when ProjectRoot is known)
//
// These mirror the equivalent contract used by type:script, scoped to what
// makes sense for a shell snippet (no params/context JSON, no temp dir).
// Variables already exported by the parent process or the user's env: block
// remain visible — Go's os/exec uses the last entry for duplicate keys, so
// contract values placed after user env take precedence (matching script).
func hostContractEnv(rc RunContext) []string {
	var out []string

	devboxBin, err := os.Executable()
	if err != nil || devboxBin == "" {
		devboxBin = config.DevboxBin(rc.Config)
	}
	out = append(out, "DEVBOX_BIN="+devboxBin)

	compose := rc.Compose()
	if compose.ProjectName != "" {
		out = append(out, "COMPOSE_PROJECT_NAME="+compose.ProjectName)
	}
	if len(compose.Files) > 0 {
		joined := make([]string, len(compose.Files))
		for i, f := range compose.Files {
			if filepath.IsAbs(f) || rc.ProjectRoot == "" {
				joined[i] = f
			} else {
				joined[i] = filepath.Join(rc.ProjectRoot, f)
			}
		}
		out = append(out, "COMPOSE_FILE="+strings.Join(joined, ":"))
	}

	return out
}

// Run executes the command on the host.
func (r *HostRunner) Run(ctx context.Context, rc RunContext) error {
	c, err := r.BuildCommand(ctx, rc)
	if err != nil {
		return err
	}
	used, cleanup := parallelChildIO(rc, c, stdout(rc))
	defer cleanup()
	if !used {
		c.Stdout = stdout(rc)
		c.Stderr = stderr(rc)
		c.Stdin = stdinOrOS(rc)
	}
	return c.Run()
}

// buildRenderedEnv renders all env values (which may contain ${...} expressions)
// and returns the final string→string map.
func buildRenderedEnv(cmd *model.CommandDef, ctx RunContext) (map[string]string, error) {
	files := make(map[string]tpl.ResolvedFile)
	if ctx.Render != nil && ctx.Render.Files != nil {
		files = ctx.Render.Files
	}
	raw, err := resolve.BuildEnv(cmd, ctx.Params, ctx.Context, files)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(raw))
	for k, v := range raw {
		rendered, err := tpl.RenderCommand(v, ctx.Render)
		if err != nil {
			return nil, fmt.Errorf("render env %q: %w", k, err)
		}
		result[k] = rendered
	}
	return result, nil
}

// stdout returns the writer to use for stdout, defaulting to os.Stdout.
func stdout(ctx RunContext) io.Writer {
	if ctx.Stdout != nil {
		return ctx.Stdout
	}
	return os.Stdout
}

// stderr returns the writer to use for stderr, defaulting to os.Stderr.
func stderr(ctx RunContext) io.Writer {
	if ctx.Stderr != nil {
		return ctx.Stderr
	}
	return os.Stderr
}

// shellQuote wraps a path in single quotes for safe inclusion in a sh -c string.
// Embedded single quotes are escaped via the '\\” idiom.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
