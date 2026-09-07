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

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/validate"
	valchecks "github.com/semsemyonoff/dwe/internal/core/validate/checks"
	valconfig "github.com/semsemyonoff/dwe/internal/core/validate/config"
	valenv "github.com/semsemyonoff/dwe/internal/core/validate/env"
	valsecrets "github.com/semsemyonoff/dwe/internal/core/validate/secrets"
	"github.com/semsemyonoff/dwe/internal/shared/trace"
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
		return fmt.Sprintf("DWE can't %s — please fix the issues below and try again:", action)
	}
	return fmt.Sprintf("DWE is about to %s. A few things to know:", action)
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

// stagesForPreflight maps a lifecycle stage to the set of validate.yml check
// stages this final preflight runs. For "deploy" it returns {deploy,
// post-setup} so post-setup checks (skipped at the early pre-wizard gate) run
// here. Every other stage maps to itself; an empty stage maps to nil, which
// AllForStages treats as "match every check" (preserving the prior
// empty-stage behavior).
func stagesForPreflight(stage string) []string {
	switch stage {
	case "":
		return nil
	case "deploy":
		return []string{"deploy", "post-setup"}
	default:
		return []string{stage}
	}
}

// RunFn is the signature of Run. Commands that want a swappable preflight
// (e.g. for tests, or to override the implementation) accept a RunFn and
// default to Run when nil.
type RunFn = func(ctx context.Context, cfg *config.DweConfig, cmdRegistry *usercommands.Registry, baseDir, stage string, skip bool, errOut io.Writer) error

// Run executes env + checks (filtered by stage) and renders diagnostics to
// errOut. Returns *Error on any error-severity diagnostic.
//
// When skip is true, prints a one-line skip notice and returns nil without
// loading validate.yml or executing any validator — the flag is a true
// bypass (type: command checks invoke arbitrary user scripts that could
// mutate state).
//
// cmdRegistry is nil-tolerant: checks.AllForStages produces unknown-command
// diagnostics for any type: command entry when nil.
func Run(ctx context.Context, cfg *config.DweConfig, cmdRegistry *usercommands.Registry, baseDir, stage string, skip bool, errOut io.Writer) error {
	if errOut == nil {
		errOut = io.Discard
	}
	if skip {
		trace.Decision(ctx, "preflight skipped (--skip-preflight) for stage %q", stage)
		_, _ = fmt.Fprintln(errOut, "preflight skipped (--skip-preflight)")
		return nil
	}
	trace.Decision(ctx, "preflight running for stage %q", stage)

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
	// Second (and only other) cherry-pick: secrets.unresolved is READINESS, not
	// content. A lifecycle command that runs with an undecryptable secret would
	// either write ciphertext into a rendered file or fail deep inside a
	// pipeline; blocking here reports the missing key once, with the fix. The
	// rest of the secrets domain (secrets.recipient) stays in `dwe validate`
	// only, like every other content validator.
	reg.Register(valsecrets.UnresolvedValidator())
	// Pass nil loadErr: config.validate (registered above) already emits the
	// parse error diagnostic. Passing it here too would produce a duplicate
	// row in the preflight table and double-count it in the summary.
	//
	// The deploy flow has two preflight moments: an early pre-wizard gate
	// (cli/deploy/menu.go, which queries only the "deploy" stage) and this
	// final run immediately before the pipeline. For the deploy stage we also
	// query "post-setup" so checks tagged stages: [post-setup] run here — they
	// are skipped at the early gate (they don't carry "deploy") and execute
	// only after the setup wizard has populated local.yml, or right before
	// deploy when no wizard runs. See docs/reference/config/validate.md.
	for _, v := range valchecks.AllForStages(validateCfg, nil, baseDir, cmdRegistry, stagesForPreflight(stage), cfg.Services, false) {
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
	trace.Decision(ctx, "preflight result for stage %q: %d error(s), %d warning(s), %d info(s) — blocking=%t",
		stage, summary.Errors, summary.Warnings, summary.Infos, blocking)
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
