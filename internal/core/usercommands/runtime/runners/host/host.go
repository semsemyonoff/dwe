// Package host implements the runtime runners for type=shell (host.Runner)
// and type=devbox (host.DevboxRunner) commands. Both run on the host machine
// and share helpers for env contract injection and parallel-aware colour
// forcing.
package host

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/internal/runio"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/spec"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// Runner executes type=shell commands on the host machine.
type Runner struct{}

// BuildCommand constructs the exec.Cmd that would be run for the given context.
// It is exported for testing without actual execution. The supplied ctx is
// attached to the returned *exec.Cmd via exec.CommandContext so callers can
// cancel the child by cancelling ctx.
func (r *Runner) BuildCommand(ctx context.Context, rc spec.RunContext) (*exec.Cmd, error) {
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
	runio.BindCancel(c)

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

	envMap, err := runio.BuildRenderedEnv(cmd, rc)
	if err != nil {
		return nil, err
	}
	contractEnv := hostContractEnv(rc)
	colorEnv := runio.ParallelColorForceEnv(rc)
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
func hostContractEnv(rc spec.RunContext) []string {
	var out []string

	devboxBin, err := os.Executable()
	if err != nil || devboxBin == "" {
		devboxBin = config.DweBin(rc.Config)
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
func (r *Runner) Run(ctx context.Context, rc spec.RunContext) error {
	c, err := r.BuildCommand(ctx, rc)
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
