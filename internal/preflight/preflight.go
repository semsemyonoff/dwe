// Package preflight runs env probes + project checks before lifecycle
// operations (deploy run, run, stop, restart) so user-actionable problems
// surface BEFORE any side effect on Docker, git, or the filesystem.
//
// The preflight assembler is its own package so the command layer (Cobra
// wrappers) and the lifecycle layer (lifecycle.RunRun / RunStop) can both
// invoke it without an import cycle.
package preflight

import (
	"context"
	"fmt"
	"io"

	"devbox-cli/internal/config"
	"devbox-cli/internal/ui"
	"devbox-cli/internal/usercommands"
	"devbox-cli/internal/validate"
	valchecks "devbox-cli/internal/validate/checks"
	valconfig "devbox-cli/internal/validate/config"
	valenv "devbox-cli/internal/validate/env"
)

// Error is returned when preflight surfaces any error-severity diagnostic.
// Implements ExitCode() so main.go renders exit code 1 without a fang prefix.
type Error struct {
	Summary validate.Summary
}

func (e *Error) Error() string { return "preflight failed" }

// ExitCode returns 1 so main.go translates a preflight failure to exit code 1
// without printing fang's "Error: ..." prefix.
func (e *Error) ExitCode() int { return 1 }

// Run executes env + checks (filtered by stage) and renders diagnostics to
// errOut. Returns *Error on any error-severity diagnostic.
//
// When skip is true, prints a one-line skip notice and returns nil without
// loading validate.yml or executing any validator — the flag is a true
// bypass (type: command checks invoke arbitrary user scripts that could
// mutate state).
//
// cmdRegistry is nil-tolerant: checks.AllForStage produces unknown-command
// diagnostics for any type: command entry when nil.
func Run(_ context.Context, cfg *config.DevboxConfig, cmdRegistry *usercommands.Registry, baseDir, stage string, skip bool, errOut io.Writer) error {
	if errOut == nil {
		errOut = io.Discard
	}
	if skip {
		_, _ = fmt.Fprintln(errOut, "preflight skipped (--skip-preflight)")
		return nil
	}

	validateCfg, warnings, loadErr := config.LoadValidateConfig(config.ValidateConfigPath(baseDir))

	vctx := validate.Context{
		ProjectRoot:         baseDir,
		Cfg:                 cfg,
		CommandRegistry:     cmdRegistry,
		ValidateCfg:         validateCfg,
		ValidateCfgWarnings: warnings,
		ValidateCfgLoadErr:  loadErr,
	}

	reg := validate.NewRegistry()
	for _, v := range valenv.All(cfg) {
		reg.Register(v)
	}
	for _, v := range valchecks.AllForStage(validateCfg, baseDir, cmdRegistry, stage) {
		reg.Register(v)
	}
	// Surface a malformed validate.yml inline as part of preflight, not
	// silently — pick the single config.validate validator out of the
	// roster (it reads ValidateCfgLoadErr / Warnings from the Context).
	for _, v := range valconfig.All() {
		if v.Domain() == "config" && v.ID() == "validate" {
			reg.Register(v)
			break
		}
	}

	diags := reg.Run(vctx)
	rows := ui.FormatDiagnostics(diags, false)
	if len(rows) > 0 {
		_, _ = fmt.Fprintln(errOut, ui.RenderDiagnosticsTable(rows))
	}
	summary := validate.Aggregate(diags)
	if validate.ExitCode(summary, false) != 0 {
		return &Error{Summary: summary}
	}
	return nil
}
