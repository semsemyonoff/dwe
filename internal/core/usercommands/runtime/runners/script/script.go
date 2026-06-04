// Package script implements the runtime runner for type=script commands.
// A Runner executes one (simple mode) or up to three (plan/run/cleanup phased
// mode) script files with a stable DWE_* contract env injected into each
// child shell.
package script

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/internal/runio"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/spec"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// Runner executes type=script commands by running one or more script files.
//
// Two modes are supported:
//   - Simple mode: script.path is set — a single script file is executed.
//   - Phased mode: script.run (and optionally script.plan, script.cleanup) are set.
//     In phased mode, cleanup is always run even if plan or run fails.
//
// The following contract environment variables are injected into every script:
//
//	DWE_ROOT            absolute project root path
//	DWE_COMMAND_ID      full command ID (e.g. "services.main.bootstrap")
//	DWE_TEMP_DIR        writable temp directory scoped to this invocation
//	DWE_NONINTERACTIVE  "1" when running without a TTY / in CI, "0" otherwise
//	DWE_PARAMS_JSON     resolved params as a JSON object
//	DWE_CONTEXT_JSON    resolved context values as a JSON object
//	DWE_BIN             absolute path to the dwe executable
//	DWE_FILES_JSON      JSON object mapping file IDs to resolved paths
type Runner struct{}

// Run executes the script command described by rc.
func (s *Runner) Run(ctx context.Context, rc spec.RunContext) error {
	sd := rc.Cmd.Script
	if sd == nil {
		return fmt.Errorf("script runner: script block is nil")
	}

	shell := sd.Shell
	if shell == "" {
		shell = "sh"
	}

	tmpDir, err := os.MkdirTemp("", "dwe-script-*")
	if err != nil {
		return fmt.Errorf("script runner: create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck

	contractEnv, err := s.buildContractEnv(rc, tmpDir)
	if err != nil {
		return err
	}

	if sd.Path != "" {
		return s.execScript(ctx, rc, shell, sd.Path, contractEnv)
	}

	if sd.Plan != "" {
		if err := s.execScript(ctx, rc, shell, sd.Plan, contractEnv); err != nil {
			return fmt.Errorf("script plan phase: %w", err)
		}
	}

	runErr := s.execScript(ctx, rc, shell, sd.Run, contractEnv)

	if sd.Cleanup != "" {
		if cleanErr := s.execScript(ctx, rc, shell, sd.Cleanup, contractEnv); cleanErr != nil {
			_, _ = fmt.Fprintf(runio.StderrOf(rc), "script runner: cleanup phase error: %v\n", cleanErr)
		}
	}

	return runErr
}

// buildContractEnv constructs the slice of NAME=VALUE contract env vars.
func (s *Runner) buildContractEnv(ctx spec.RunContext, tmpDir string) ([]string, error) {
	root := ctx.ProjectRoot
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("script runner: resolve project root: %w", err)
		}
		root = wd
	}

	nonInteractive := "0"
	if ctx.NonInteractive {
		nonInteractive = "1"
	} else if v := os.Getenv("DWE_NONINTERACTIVE"); v == "1" || v == "true" {
		nonInteractive = "1"
	}

	params := ctx.Params
	if params == nil {
		params = map[string]any{}
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("script runner: marshal params: %w", err)
	}

	ctxVals := ctx.Context
	if ctxVals == nil {
		ctxVals = map[string]any{}
	}
	contextJSON, err := json.Marshal(ctxVals)
	if err != nil {
		return nil, fmt.Errorf("script runner: marshal context: %w", err)
	}

	dweBin, err := os.Executable()
	if err != nil {
		dweBin = os.Args[0]
		if !filepath.IsAbs(dweBin) {
			absPath, err := filepath.Abs(dweBin)
			if err == nil {
				dweBin = absPath
			}
		}
	}

	filesMap := map[string]map[string]string{}
	if ctx.Render != nil && ctx.Render.Files != nil {
		for id, resolved := range ctx.Render.Files {
			filesMap[id] = map[string]string{"path": resolved.Path}
		}
	}
	filesJSON, err := json.Marshal(filesMap)
	if err != nil {
		return nil, fmt.Errorf("script runner: marshal files: %w", err)
	}

	return []string{
		"DWE_ROOT=" + root,
		"DWE_COMMAND_ID=" + ctx.Cmd.ID,
		"DWE_TEMP_DIR=" + tmpDir,
		"DWE_NONINTERACTIVE=" + nonInteractive,
		"DWE_PARAMS_JSON=" + string(paramsJSON),
		"DWE_CONTEXT_JSON=" + string(contextJSON),
		"DWE_BIN=" + dweBin,
		"DWE_FILES_JSON=" + string(filesJSON),
	}, nil
}

// execScript runs a single script file using the given shell interpreter.
// Note: script.path is always resolved against rc.ProjectRoot, not against workdir.
func (s *Runner) execScript(ctx context.Context, rc spec.RunContext, shell, scriptPath string, contractEnv []string) error {
	if !filepath.IsAbs(scriptPath) && rc.ProjectRoot != "" {
		scriptPath = filepath.Join(rc.ProjectRoot, scriptPath)
	}

	if _, err := exec.LookPath(shell); err != nil {
		return fmt.Errorf("script runner: shell %q not found in PATH: %w", shell, err)
	}
	if _, err := os.Stat(scriptPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("script runner: script not found: %s", scriptPath)
		}
		return fmt.Errorf("script runner: stat script %s: %w", scriptPath, err)
	}

	c := exec.CommandContext(ctx, shell, scriptPath) //nolint:gosec
	runio.BindCancel(c)

	workdir := rc.ProjectRoot
	if rc.Cmd != nil && rc.Cmd.Workdir != "" {
		rendered, err := tpl.RenderCommand(rc.Cmd.Workdir, rc.Render)
		if err != nil {
			return fmt.Errorf("script runner: render workdir: %w", err)
		}
		if !filepath.IsAbs(rendered) && rc.ProjectRoot != "" {
			workdir = filepath.Join(rc.ProjectRoot, rendered)
		} else {
			workdir = rendered
		}
	}

	if workdir != "" {
		c.Dir = workdir
	}

	envMap, err := runio.BuildRenderedEnv(rc.Cmd, rc)
	if err != nil {
		return err
	}
	c.Env = os.Environ()
	for k, v := range envMap {
		c.Env = append(c.Env, k+"="+v)
	}
	c.Env = append(c.Env, contractEnv...)
	for _, kv := range runio.ParallelColorForceEnv(rc) {
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			if _, exists := envMap[kv[:eq]]; !exists {
				c.Env = append(c.Env, kv)
			}
		}
	}

	defer runio.WireChildIO(rc, c)()

	if err := c.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 127 {
				return fmt.Errorf("script %s failed: %w (exit 127 usually means a command was not found; check the script and commands it invokes)", scriptPath, err)
			}
			return fmt.Errorf("script %s failed: %w", scriptPath, err)
		}
		return fmt.Errorf("script %s failed: %w", scriptPath, err)
	}
	return nil
}
