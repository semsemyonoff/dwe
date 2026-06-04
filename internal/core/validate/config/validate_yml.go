package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// validateYmlValidator surfaces the outcome of the single LoadValidateConfig
// parse performed by the caller (runValidate or RunPreflight). It does NOT
// load validate.yml itself — it reads ctx.ValidateCfgLoadErr and
// ctx.ValidateCfgWarnings, which are populated upstream.
type validateYmlValidator struct{}

var _ validate.Validator = (*validateYmlValidator)(nil)

func (v *validateYmlValidator) ID() string {
	return "validate"
}

func (v *validateYmlValidator) Domain() string {
	return "config"
}

func (v *validateYmlValidator) Run(ctx validate.Context) []validate.Diagnostic {
	const file = "workspace/validate.yml"

	if ctx.ValidateCfgLoadErr != nil {
		// Absent file: silently tolerated (validate.yml is optional).
		if errors.Is(ctx.ValidateCfgLoadErr, os.ErrNotExist) {
			return nil
		}
		// Any other load error surfaces as a single error diagnostic.
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "config",
			Target:   "validate",
			File:     file,
			Message:  ctx.ValidateCfgLoadErr.Error(),
		}}
	}

	// Successful load: pass through any soft warnings produced by the loader,
	// then append semantic checks that need the merged project config (the
	// loader has no view of cfg.Services).
	extra := validateUnknownServiceRefs(ctx, file)
	if len(extra) == 0 {
		return ctx.ValidateCfgWarnings
	}
	// Defensive copy so appending the extras cannot mutate the caller's
	// warnings slice via shared capacity. Today LoadValidateConfig builds the
	// warnings slice with `var warnings []Diagnostic` (cap=0 — safe), but the
	// alias would be silently dangerous if that ever changes.
	out := make([]validate.Diagnostic, 0, len(ctx.ValidateCfgWarnings)+len(extra))
	out = append(out, ctx.ValidateCfgWarnings...)
	out = append(out, extra...)
	return out
}

// validateUnknownServiceRefs walks checks[].services[] and emits one error
// per unknown service name. The loader cannot perform this check (no view of
// cfg.Services); it lives here so a single `dwe validate` pass surfaces typos
// like services: [api-server] when only "api" exists.
func validateUnknownServiceRefs(ctx validate.Context, file string) []validate.Diagnostic {
	if ctx.ValidateCfg == nil || ctx.Cfg == nil {
		return nil
	}
	var diags []validate.Diagnostic
	for _, entry := range ctx.ValidateCfg.Checks {
		for _, svc := range entry.Services {
			if _, ok := ctx.Cfg.Services[svc]; ok {
				continue
			}
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "validate",
				File:     file,
				Line:     entry.SourceLine,
				Message:  fmt.Sprintf("check %q: unknown service %q in services: list", entry.ID, svc),
				Hint:     "Service names must match a folder under workspace/services/ (each folder has a service.yml).",
			})
		}
	}
	return diags
}
