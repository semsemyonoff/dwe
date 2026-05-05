package runtime

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/tpl"
	"devbox-cli/internal/usercommands/model"
	"devbox-cli/internal/usercommands/resolve"
)

// DevboxRunner executes type=devbox commands by invoking the current devbox
// executable with the run: string as its arguments.
type DevboxRunner struct{}

// Run executes the devbox subcommand described by ctx.Cmd.
func (r *DevboxRunner) Run(ctx RunContext) error {
	bin, err := os.Executable()
	if err != nil {
		bin = config.DevboxBin(ctx.Config)
	}

	rendered, err := tpl.RenderCommand(ctx.Cmd.Cmd, ctx.Render)
	if err != nil {
		return fmt.Errorf("render cmd: %w", err)
	}

	cmd := exec.Command(config.ShellBin(ctx.Config), "-c", shellQuote(bin)+" "+rendered) //nolint:gosec
	if ctx.ProjectRoot != "" {
		cmd.Dir = ctx.ProjectRoot
	}

	envMap, err := buildRenderedEnv(ctx.Cmd, ctx)
	if err != nil {
		return err
	}
	if len(envMap) > 0 {
		cmd.Env = os.Environ()
		for k, v := range envMap {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}

	cmd.Stdout = stdout(ctx)
	cmd.Stderr = stderr(ctx)
	cmd.Stdin = stdinOrOS(ctx)
	return cmd.Run()
}

// HostRunner executes type=command commands on the host machine.
type HostRunner struct{}

// BuildCommand constructs the exec.Cmd that would be run for the given context.
// It is exported for testing without actual execution.
func (r *HostRunner) BuildCommand(ctx RunContext) (*exec.Cmd, error) {
	cmd := ctx.Cmd

	var argv []string
	if cmd.Cmd != "" {
		rendered, err := tpl.RenderCommand(cmd.Cmd, ctx.Render)
		if err != nil {
			return nil, fmt.Errorf("render cmd: %w", err)
		}
		argv = []string{config.ShellBin(ctx.Config), "-c", rendered}
	} else {
		rendered := make([]string, len(cmd.Argv))
		for i, arg := range cmd.Argv {
			r, err := tpl.RenderCommand(arg, ctx.Render)
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
	c := exec.Command(argv[0], argv[1:]...) //nolint:gosec

	if cmd.Workdir != "" {
		rendered, err := tpl.RenderCommand(cmd.Workdir, ctx.Render)
		if err != nil {
			return nil, fmt.Errorf("render workdir: %w", err)
		}
		if !filepath.IsAbs(rendered) && ctx.ProjectRoot != "" {
			rendered = filepath.Join(ctx.ProjectRoot, rendered)
		}
		c.Dir = rendered
	} else if ctx.ProjectRoot != "" {
		c.Dir = ctx.ProjectRoot
	}

	envMap, err := buildRenderedEnv(cmd, ctx)
	if err != nil {
		return nil, err
	}
	if len(envMap) > 0 {
		c.Env = os.Environ()
		for k, v := range envMap {
			c.Env = append(c.Env, k+"="+v)
		}
	}

	return c, nil
}

// Run executes the command on the host.
func (r *HostRunner) Run(ctx RunContext) error {
	c, err := r.BuildCommand(ctx)
	if err != nil {
		return err
	}
	c.Stdout = stdout(ctx)
	c.Stderr = stderr(ctx)
	c.Stdin = stdinOrOS(ctx)
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
