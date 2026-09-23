package config

import (
	"fmt"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// healthcheckStartPeriodValidator notes compose services in the active chain
// whose healthcheck runs a test but declares no start_period. dwe brings the
// stack up with `docker compose up --wait`, so one slow-starting service whose
// failed boot-time probes count against retries can fail the whole `dwe run`.
//
// Info, not warning: most services start well inside interval × retries, so an
// absent start_period is a hint about a possible failure mode, not a defect —
// a warning would fail `--strict` for nearly every project with a healthcheck.
type healthcheckStartPeriodValidator struct{}

var _ validate.Validator = (*healthcheckStartPeriodValidator)(nil)

func (v *healthcheckStartPeriodValidator) ID() string     { return "healthcheck_start_period" }
func (v *healthcheckStartPeriodValidator) Domain() string { return "config" }

func (v *healthcheckStartPeriodValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if ctx.Cfg == nil {
		return nil
	}
	var diags []validate.Diagnostic
	for _, f := range config.ScanComposeHealthchecks(ctx.Cfg, ctx.ProjectRoot) {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityInfo,
			Domain:   "config",
			Target:   "config.healthcheck_start_period",
			File:     relPath(ctx.ProjectRoot, f.File),
			Message:  fmt.Sprintf("compose service %s has a healthcheck without start_period", f.Service),
			Hint: "`dwe run` waits for healthchecks (`docker compose up --wait`); probes a slow-starting service fails " +
				"while booting count against retries, so it can be marked unhealthy and fail the run — " +
				"set healthcheck.start_period to cover its startup time",
		})
	}
	return diags
}
