package commands

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"devbox-cli/internal/tpl"
)

// DevboxRunner executes type=devbox commands by invoking the current devbox
// executable with the run: string as its arguments. This avoids hard-coding
// the binary path (./bin/devbox) in command definitions.
//
// The run: field contains only the subcommand and its arguments, e.g.:
//
//	run: "compose run -- up -d db"
//
// At runtime this becomes: <devbox-executable> compose run -- up -d db
type DevboxRunner struct{}

// Run executes the devbox subcommand described by ctx.Run.
func (r *DevboxRunner) Run(ctx RunContext) error {
	bin, err := os.Executable()
	if err != nil {
		bin = "./bin/devbox"
	}

	rendered, err := tpl.RenderCommand(ctx.Cmd.Run, ctx.Render)
	if err != nil {
		return fmt.Errorf("render run: %w", err)
	}

	cmd := exec.Command("sh", "-c", bin+" "+rendered) //nolint:gosec
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
// It supports two execution modes:
//   - run:  the command string is passed to `sh -c` for shell evaluation.
//   - argv: the argument vector is exec'd directly without a shell.
//
// The workdir and env fields support ${...} template interpolation.
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

	if len(argv) == 0 {
		return nil, fmt.Errorf("argv is empty")
	}
	c := exec.Command(argv[0], argv[1:]...) //nolint:gosec

	// Resolve working directory.
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
	c.Stdin = stdinOrOS(ctx)
	return c.Run()
}

// buildRenderedEnv renders all env values (which may contain ${...} expressions)
// and returns the final string→string map.
func buildRenderedEnv(cmd *CommandDef, ctx RunContext) (map[string]string, error) {
	files := make(map[string]tpl.ResolvedFile)
	if ctx.Render != nil && ctx.Render.Files != nil {
		files = ctx.Render.Files
	}
	raw, err := BuildEnv(cmd, ctx.Params, ctx.Context, files)
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
