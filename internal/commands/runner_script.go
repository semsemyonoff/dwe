package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"devbox-cli/internal/tpl"
)

// ScriptRunner executes type=script commands by running one or more script files.
//
// Two modes are supported:
//   - Simple mode: script.path is set — a single script file is executed.
//   - Phased mode: script.run (and optionally script.plan, script.cleanup) are set.
//     In phased mode, cleanup is always run even if plan or run fails.
//
// The following contract environment variables are injected into every script:
//
//	DEVBOX_ROOT            absolute project root path
//	DEVBOX_COMMAND_ID      full command ID (e.g. "services.main.bootstrap")
//	DEVBOX_TEMP_DIR        writable temp directory scoped to this invocation
//	DEVBOX_NONINTERACTIVE  "1" when running without a TTY / in CI, "0" otherwise
//	DEVBOX_PARAMS_JSON     resolved params as a JSON object
//	DEVBOX_CONTEXT_JSON    resolved context values as a JSON object
//	DEVBOX_BIN             absolute path to the devbox executable
//	DEVBOX_FILES_JSON      JSON object mapping file IDs to resolved paths
type ScriptRunner struct{}

// Run executes the script command described by ctx.
func (r *ScriptRunner) Run(ctx RunContext) error {
	s := ctx.Cmd.Script
	if s == nil {
		return fmt.Errorf("script runner: script block is nil")
	}

	shell := s.Shell
	if shell == "" {
		shell = "sh"
	}

	// Create a temp directory for DEVBOX_TEMP_DIR and ensure cleanup.
	tmpDir, err := os.MkdirTemp("", "devbox-script-*")
	if err != nil {
		return fmt.Errorf("script runner: create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck

	contractEnv, err := r.buildContractEnv(ctx, tmpDir)
	if err != nil {
		return err
	}

	if s.Path != "" {
		// Simple mode — single script file.
		return r.execScript(ctx, shell, s.Path, contractEnv)
	}

	// Phased mode.
	if s.Plan != "" {
		if err := r.execScript(ctx, shell, s.Plan, contractEnv); err != nil {
			return fmt.Errorf("script plan phase: %w", err)
		}
	}

	runErr := r.execScript(ctx, shell, s.Run, contractEnv)

	if s.Cleanup != "" {
		if cleanErr := r.execScript(ctx, shell, s.Cleanup, contractEnv); cleanErr != nil {
			// Log cleanup failure but do not mask the run error.
			_, _ = fmt.Fprintf(stderr(ctx), "script runner: cleanup phase error: %v\n", cleanErr)
		}
	}

	return runErr
}

// buildContractEnv constructs the slice of NAME=VALUE contract env vars.
func (r *ScriptRunner) buildContractEnv(ctx RunContext, tmpDir string) ([]string, error) {
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
	} else if v := os.Getenv("DEVBOX_NONINTERACTIVE"); v == "1" || v == "true" {
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

	// Resolve devbox binary path.
	devboxBin, err := os.Executable()
	if err != nil {
		// Fallback to os.Args[0] (the command name) and make it absolute.
		devboxBin = os.Args[0]
		if !filepath.IsAbs(devboxBin) {
			absPath, err := filepath.Abs(devboxBin)
			if err == nil {
				devboxBin = absPath
			}
		}
	}

	// Build DEVBOX_FILES_JSON from ctx.Render.Files.
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
		"DEVBOX_ROOT=" + root,
		"DEVBOX_COMMAND_ID=" + ctx.Cmd.ID,
		"DEVBOX_TEMP_DIR=" + tmpDir,
		"DEVBOX_NONINTERACTIVE=" + nonInteractive,
		"DEVBOX_PARAMS_JSON=" + string(paramsJSON),
		"DEVBOX_CONTEXT_JSON=" + string(contextJSON),
		"DEVBOX_BIN=" + devboxBin,
		"DEVBOX_FILES_JSON=" + string(filesJSON),
	}, nil
}

// execScript runs a single script file using the given shell interpreter.
// Note: script.path is always resolved against ctx.ProjectRoot, not against workdir.
func (r *ScriptRunner) execScript(ctx RunContext, shell, scriptPath string, contractEnv []string) error {
	if !filepath.IsAbs(scriptPath) && ctx.ProjectRoot != "" {
		scriptPath = filepath.Join(ctx.ProjectRoot, scriptPath)
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

	c := exec.Command(shell, scriptPath) //nolint:gosec

	// Determine working directory: render workdir if set, otherwise use project root.
	workdir := ctx.ProjectRoot
	if ctx.Cmd != nil && ctx.Cmd.Workdir != "" {
		rendered, err := tpl.RenderCommand(ctx.Cmd.Workdir, ctx.Render)
		if err != nil {
			return fmt.Errorf("script runner: render workdir: %w", err)
		}
		// Normalize relative paths against project root.
		if !filepath.IsAbs(rendered) && ctx.ProjectRoot != "" {
			workdir = filepath.Join(ctx.ProjectRoot, rendered)
		} else {
			workdir = rendered
		}
	}

	if workdir != "" {
		c.Dir = workdir
	}

	// Inherit host env, overlay command env, then apply contract vars.
	envMap, err := buildRenderedEnv(ctx.Cmd, ctx)
	if err != nil {
		return err
	}
	c.Env = os.Environ()
	for k, v := range envMap {
		c.Env = append(c.Env, k+"="+v)
	}
	c.Env = append(c.Env, contractEnv...)

	c.Stdout = stdout(ctx)
	c.Stderr = stderr(ctx)
	c.Stdin = stdinOrOS(ctx)

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
