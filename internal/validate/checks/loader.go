// Package checks turns project-level validate.yml entries into synthetic
// validate.Validators that dispatch to either a builtin or a locked-down
// user command at Run time. Load-time problems (unknown builtin, unknown
// command, bad with: shape, disallowed command type) are pre-baked into a
// failing validator that emits a single Diagnostic from Run — keeping the
// Validator interface free of an error return path while still surfacing
// every problem in one validate pass.
package checks

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"devbox-cli/internal/builtin"
	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	"devbox-cli/internal/usercommands/model"
	"devbox-cli/internal/usercommands/registry"
	"devbox-cli/internal/usercommands/runtime"
	"devbox-cli/internal/validate"
)

// diagFile is the canonical path reported in diagnostics. Matches what the
// loader and config.validate validator report so users see a consistent
// reference regardless of where the file was located on disk.
const diagFile = "devbox/validate.yml"

// allowedCommandTypes is the whitelist of user-command types invokable from
// a check. Heavyweight types (workflow, service_*, devbox, builtin-as-command)
// are rejected at load time.
var allowedCommandTypes = map[model.CommandType]struct{}{
	model.CommandTypeShell:  {},
	model.CommandTypeScript: {},
}

// allowedBuiltinCmds is the allowlist of builtins safe to invoke from check
// entries. Destructive builtins (docker_remove_project_volumes, daemons_reap,
// docker_daemon_start, docker_daemon_stop, remove_paths, service_configs_copy,
// service_dirs_ensure) and interactive ones (confirm, docker_daemon_logs) are
// intentionally excluded — checks must not produce side effects.
var allowedBuiltinCmds = map[string]struct{}{
	"shell":              {},
	"file_exists":        {},
	"executable_in_path": {},
	"env_keys_present":   {},
	"tcp_reachable":      {},
}

// AllForStage produces synthetic validators for every entry whose Stages
// contains stage. An empty stage returns all entries. A nil cfg yields an
// empty slice.
func AllForStage(cfg *config.ValidateConfig, baseDir string, cmdRegistry *registry.Registry, stage string) []validate.Validator {
	if cfg == nil {
		return nil
	}
	out := make([]validate.Validator, 0, len(cfg.Checks))
	for i := range cfg.Checks {
		entry := cfg.Checks[i]
		if !MatchStage(entry, stage) {
			continue
		}
		out = append(out, buildValidator(entry, baseDir, cmdRegistry))
	}
	return out
}

// buildValidator picks the runner vs cached-failure form based on whether the
// entry's dispatch target resolves at load time.
func buildValidator(entry config.CheckEntry, baseDir string, cmdRegistry *registry.Registry) validate.Validator {
	switch entry.Type {
	case "builtin":
		if _, ok := builtin.Get(entry.Cmd); !ok {
			return cached(entry, fmt.Sprintf("unknown builtin: %s", entry.Cmd))
		}
		if _, ok := allowedBuiltinCmds[entry.Cmd]; !ok {
			return cached(entry, fmt.Sprintf(
				"checks may only use builtins: shell, file_exists, executable_in_path, env_keys_present, tcp_reachable (got: %s)", entry.Cmd))
		}
		if err := builtin.Validate(entry.Cmd, entry.With); err != nil {
			return cached(entry, err.Error())
		}
		return &builtinRunner{entry: entry, baseDir: baseDir}
	case "command":
		if cmdRegistry == nil {
			return cached(entry, fmt.Sprintf("unknown command: %s", entry.Cmd))
		}
		def, err := cmdRegistry.Get(entry.Cmd)
		if err != nil {
			return cached(entry, fmt.Sprintf("unknown command: %s", entry.Cmd))
		}
		if _, ok := allowedCommandTypes[def.Type]; !ok {
			return cached(entry, fmt.Sprintf(
				"checks may only invoke user commands of type shell or script (got: %s)", def.Type))
		}
		return &commandRunner{entry: entry, baseDir: baseDir, registry: cmdRegistry, def: def}
	default:
		// LoadValidateConfig already rejects unknown types as hard errors, so
		// reaching this branch implies a caller built a CheckEntry directly
		// with an invalid type. Surface it as a load-time diagnostic anyway.
		return cached(entry, fmt.Sprintf("unknown check type: %s", entry.Type))
	}
}

// cached produces a validator that emits a single Diagnostic with the entry's
// own severity and hint. Used for problems detected at load time.
func cached(entry config.CheckEntry, msg string) validate.Validator {
	return &cachedValidator{entry: entry, message: msg}
}

type cachedValidator struct {
	entry   config.CheckEntry
	message string
}

func (v *cachedValidator) ID() string     { return v.entry.ID }
func (v *cachedValidator) Domain() string { return "checks" }
func (v *cachedValidator) Run(_ validate.Context) []validate.Diagnostic {
	return []validate.Diagnostic{toDiagnostic(v.entry, v.message)}
}

type builtinRunner struct {
	entry   config.CheckEntry
	baseDir string
}

func (v *builtinRunner) ID() string     { return v.entry.ID }
func (v *builtinRunner) Domain() string { return "checks" }
func (v *builtinRunner) Run(ctx validate.Context) []validate.Diagnostic {
	projectRoot := v.baseDir
	if projectRoot == "" {
		projectRoot = ctx.ProjectRoot
	}
	ectx := builtin.ExecContext{
		Config:      ctx.Cfg,
		ProjectRoot: projectRoot,
		Output:      render.NewWriter(io.Discard),
		SkipConfirm: true,
	}
	if err := builtin.Run(context.Background(), v.entry.Cmd, v.entry.With, ectx); err != nil {
		return []validate.Diagnostic{toDiagnostic(v.entry, err.Error())}
	}
	return nil
}

type commandRunner struct {
	entry    config.CheckEntry
	baseDir  string
	registry *registry.Registry
	def      *model.CommandDef
}

func (v *commandRunner) ID() string     { return v.entry.ID }
func (v *commandRunner) Domain() string { return "checks" }
func (v *commandRunner) Run(ctx validate.Context) []validate.Diagnostic {
	projectRoot := v.baseDir
	if projectRoot == "" {
		projectRoot = ctx.ProjectRoot
	}
	rc, err := runtime.BuildRunContext(ctx.Cfg, v.registry, v.def, v.entry.With, projectRoot)
	if err != nil {
		return []validate.Diagnostic{toDiagnostic(v.entry, err.Error())}
	}
	stderrBuf := &bytes.Buffer{}
	rc.SkipConfirm = true
	rc.NonInteractive = true
	rc.SkipNotify = true
	rc.Stdout = io.Discard
	rc.Stderr = stderrBuf
	rc.Stdin = nil

	if err := runtime.RunCommand(context.Background(), rc); err != nil {
		msg := err.Error()
		if tail := lastLine(stderrBuf.String()); tail != "" {
			msg = fmt.Sprintf("%s: %s", msg, tail)
		}
		return []validate.Diagnostic{toDiagnostic(v.entry, msg)}
	}
	return nil
}

func toDiagnostic(entry config.CheckEntry, msg string) validate.Diagnostic {
	return validate.Diagnostic{
		Severity: entry.Severity,
		Domain:   "checks",
		Target:   entry.ID,
		File:     diagFile,
		Line:     entry.SourceLine,
		Message:  msg,
		Hint:     entry.Hint,
	}
}

// lastLine returns the last non-empty trimmed line of s. Used to attach a
// short tail of captured stderr to diagnostic messages.
func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return strings.TrimSpace(s)
}
