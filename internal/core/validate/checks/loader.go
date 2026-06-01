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
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime"
	"github.com/semsemyonoff/dwe/internal/core/validate"
	"github.com/semsemyonoff/dwe/internal/shared/render"
)

// diagFile is the canonical path reported in diagnostics. Matches what the
// loader and config.validate validator report so users see a consistent
// reference regardless of where the file was located on disk.
const diagFile = "workspace/validate.yml"

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
// contains stage. An empty stage returns all entries.
//
// When cfg is nil and loadErr is a real parse error (not os.ErrNotExist), a
// synthetic error validator in the "checks" domain is returned so callers
// scoped to "checks" still surface the validate.yml failure via the normal
// diagnostic table rather than silently producing zero results.
func AllForStage(cfg *config.ValidateConfig, loadErr error, baseDir string, cmdRegistry *registry.Registry, stage string) []validate.Validator {
	if cfg == nil {
		if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
			return []validate.Validator{&validateYmlErrValidator{err: loadErr}}
		}
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
		if _, ok := builtin.Get(entry.Cmd, builtin.CtxPredicate); !ok {
			// Distinguish unknown from kind-mismatch for a clearer error
			if err := builtin.Validate(entry.Cmd, entry.With, builtin.CtxPredicate); err != nil {
				return cached(entry, err.Error())
			}
			return cached(entry, fmt.Sprintf("unknown builtin: %s", entry.Cmd))
		}
		if _, ok := allowedBuiltinCmds[entry.Cmd]; !ok {
			return cached(entry, fmt.Sprintf(
				"checks may only use builtins: shell, file_exists, executable_in_path, env_keys_present, tcp_reachable (got: %s)", entry.Cmd))
		}
		if err := builtin.Validate(entry.Cmd, entry.With, builtin.CtxPredicate); err != nil {
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
	d := toDiagnostic(v.entry, v.message)
	d.Severity = validate.SeverityError
	return []validate.Diagnostic{d}
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
	runCtx := ctx.Ctx
	if runCtx == nil {
		runCtx = context.Background()
	}
	if err := builtin.Run(runCtx, v.entry.Cmd, v.entry.With, ectx, builtin.CtxPredicate); err != nil {
		return []validate.Diagnostic{toDiagnostic(v.entry, err.Error())}
	}
	return []validate.Diagnostic{okDiagnostic(v.entry)}
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
	if ctx.Cfg == nil {
		d := toDiagnostic(v.entry, "config not loaded: cannot run command check")
		d.Severity = validate.SeverityError
		return []validate.Diagnostic{d}
	}
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

	runCtx := ctx.Ctx
	if runCtx == nil {
		runCtx = context.Background()
	}
	if err := runtime.RunCommand(runCtx, rc); err != nil {
		msg := err.Error()
		if tail := lastLine(stderrBuf.String()); tail != "" {
			msg = fmt.Sprintf("%s: %s", msg, tail)
		}
		return []validate.Diagnostic{toDiagnostic(v.entry, msg)}
	}
	return []validate.Diagnostic{okDiagnostic(v.entry)}
}

func toDiagnostic(entry config.CheckEntry, msg string) validate.Diagnostic {
	return validate.Diagnostic{
		Severity: entry.Severity,
		Domain:   "checks",
		Target:   targetWithStages(entry),
		File:     diagFile,
		Line:     entry.SourceLine,
		Message:  msg,
		Hint:     entry.Hint,
	}
}

func okDiagnostic(entry config.CheckEntry) validate.Diagnostic {
	return validate.Diagnostic{
		Severity: validate.SeverityOK,
		Domain:   "checks",
		Target:   targetWithStages(entry),
		File:     diagFile,
		Line:     entry.SourceLine,
	}
}

// targetWithStages renders the check id with the stages it applies to on a
// second line so the diagnostics table makes the stage scope visible without
// a dedicated column (no other domain has stages). A check with no stages
// is rendered as just the id.
func targetWithStages(entry config.CheckEntry) string {
	if len(entry.Stages) == 0 {
		return entry.ID
	}
	return entry.ID + "\n(" + strings.Join(entry.Stages, ", ") + ")"
}

// validateYmlErrValidator surfaces a validate.yml parse failure inside the
// "checks" domain. It is returned by AllForStage when cfg is nil due to a
// real load error (not os.ErrNotExist), so scoped runs still produce a
// visible diagnostic rather than zero rows.
//
// It implements validate.DomainLevelValidator so that Registry.Run includes
// it even when a two-part scope ["checks", "<id>"] is requested — the parse
// error prevents any specific check from being resolved.
//
// It also implements validate.GlobalValidator so that the error is surfaced
// even when the requested scope does not include "config" or "checks" (e.g.
// "dwe validate env" with a broken validate.yml must not silently succeed).
// Duplication with config.validate is prevented upstream: buildRegistry sets
// checksLoadErr to nil when config.validate is already in scope, so this
// validator is only created when config.validate will not run.
type validateYmlErrValidator struct{ err error }

func (v *validateYmlErrValidator) ID() string          { return "_config" }
func (v *validateYmlErrValidator) Domain() string      { return "checks" }
func (v *validateYmlErrValidator) IsDomainLevel() bool { return true }
func (v *validateYmlErrValidator) IsGlobal() bool      { return true }
func (v *validateYmlErrValidator) Run(_ validate.Context) []validate.Diagnostic {
	return []validate.Diagnostic{{
		Severity: validate.SeverityError,
		Domain:   "checks",
		Target:   "_config",
		File:     diagFile,
		Message:  v.err.Error(),
	}}
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
