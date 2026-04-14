package commands

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"devbox-cli/internal/tpl"
)

// HostRunner executes type=command commands on the host machine.
// It supports two execution modes:
//   - run:  the command string is passed to `sh -c` for shell evaluation.
//   - argv: the argument vector is exec'd directly without a shell.
//
// The cwd and env fields support ${...} template interpolation.
type HostRunner struct{}

// BuildCommand constructs the exec.Cmd that would be run for the given context.
// It is exported for testing without actual execution.
func (r *HostRunner) BuildCommand(ctx RunContext) (*exec.Cmd, error) {
	cmd := ctx.Cmd

	// Determine effective run/argv (runner field has no effect for type=command).
	var argv []string
	if cmd.Run != "" {
		rendered, err := tpl.RenderCommand(cmd.Run, ctx.Render)
		if err != nil {
			return nil, fmt.Errorf("render run: %w", err)
		}
		argv = []string{"sh", "-c", rendered}
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

	c := exec.Command(argv[0], argv[1:]...) //nolint:gosec

	// Resolve working directory.
	if cmd.Cwd != "" {
		rendered, err := tpl.RenderCommand(cmd.Cwd, ctx.Render)
		if err != nil {
			return nil, fmt.Errorf("render cwd: %w", err)
		}
		if !filepath.IsAbs(rendered) && ctx.ProjectRoot != "" {
			rendered = filepath.Join(ctx.ProjectRoot, rendered)
		}
		c.Dir = rendered
	} else if ctx.ProjectRoot != "" {
		c.Dir = ctx.ProjectRoot
	}

	// Build environment: inherit host env, then overlay command env.
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
	c.Stdin = os.Stdin
	return c.Run()
}

// buildRenderedEnv renders all env values (which may contain ${...} expressions)
// and returns the final string→string map.
func buildRenderedEnv(cmd *CommandDef, ctx RunContext) (map[string]string, error) {
	raw := BuildEnv(cmd, ctx.Params, ctx.Context)
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
