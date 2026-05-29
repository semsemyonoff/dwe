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

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/ui/render"
	"devbox-cli/internal/core/ui/styles"
	"devbox-cli/internal/core/usercommands"
	"devbox-cli/internal/core/validate"
	valchecks "devbox-cli/internal/core/validate/checks"
	valconfig "devbox-cli/internal/core/validate/config"
	valenv "devbox-cli/internal/core/validate/env"
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

// preflightHeader returns a user-facing message shown above the diagnostics
// table. Preflight runs implicitly before lifecycle commands, so the header
// must make it clear *why* a table suddenly appeared. The blocking flag
// switches the tone between "this stops the command" and "FYI, here are
// some non-blocking notices" so we never tell a user they're blocked when
// they aren't.
func preflightHeader(stage string, blocking bool) string {
	action := preflightActionLabel(stage)
	if blocking {
		return fmt.Sprintf("Devbox can't %s — please fix the issues below and try again:", action)
	}
	return fmt.Sprintf("Devbox is about to %s. A few things to know:", action)
}

// preflightActionLabel translates the internal stage name into a short
// human verb so the header reads naturally.
func preflightActionLabel(stage string) string {
	switch stage {
	case "deploy":
		return "deploy the project"
	case "run":
		return "start the project"
	case "stop":
		return "stop the project"
	case "command":
		return "run this command"
	case "":
		return "continue"
	default:
		return "continue (" + stage + ")"
	}
}

// RunFn is the signature of Run. Commands that want a swappable preflight
// (e.g. for tests, or to override the implementation) accept a RunFn and
// default to Run when nil.
type RunFn = func(ctx context.Context, cfg *config.DevboxConfig, cmdRegistry *usercommands.Registry, baseDir, stage string, skip bool, errOut io.Writer) error

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
func Run(ctx context.Context, cfg *config.DevboxConfig, cmdRegistry *usercommands.Registry, baseDir, stage string, skip bool, errOut io.Writer) error {
	if errOut == nil {
		errOut = io.Discard
	}
	if skip {
		_, _ = fmt.Fprintln(errOut, "preflight skipped (--skip-preflight)")
		return nil
	}

	validateCfg, warnings, loadErr := config.LoadValidateConfig(config.ValidateConfigPath(baseDir))

	vctx := validate.Context{
		Ctx:                 ctx,
		ProjectRoot:         baseDir,
		Cfg:                 cfg,
		CommandRegistry:     cmdRegistry,
		ValidateCfg:         validateCfg,
		ValidateCfgWarnings: warnings,
		ValidateCfgLoadErr:  loadErr,
		Stage:               stage,
	}

	reg := validate.NewRegistry()
	for _, v := range valenv.All(cfg) {
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
	// Pass nil loadErr: config.validate (registered above) already emits the
	// parse error diagnostic. Passing it here too would produce a duplicate
	// row in the preflight table and double-count it in the summary.
	for _, v := range valchecks.AllForStage(validateCfg, nil, baseDir, cmdRegistry, stage) {
		reg.Register(v)
	}

	diags := reg.Run(vctx)
	// quiet=false so SeverityInfo rows (e.g. unknown-stage warnings from
	// config.validate) are not suppressed. Filter only SeverityOK rows: they
	// represent passing checks and are noise in preflight output, but info/
	// warning/error diagnostics must reach the user.
	rows := render.FormatDiagnostics(diags, false)
	var filtered []render.DiagnosticRow
	for _, r := range rows {
		if r.Severity != validate.SeverityOK {
			filtered = append(filtered, r)
		}
	}
	summary := validate.Aggregate(diags)
	blocking := validate.ExitCode(summary, false) != 0
	if len(filtered) > 0 {
		header := preflightHeader(stage, blocking)
		if blocking {
			header = styles.StyleFailed(header)
		} else {
			header = styles.StyleWarning(header)
		}
		_, _ = fmt.Fprintln(errOut, header)
		_, _ = fmt.Fprintln(errOut, render.DiagnosticsTable(filtered))
	}
	if blocking {
		return &Error{Summary: summary}
	}
	return nil
}
