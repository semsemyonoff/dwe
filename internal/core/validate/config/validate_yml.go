package config

import (
	"errors"
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
	const file = "devbox/validate.yml"

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

	// Successful load: pass through any soft warnings produced by the loader.
	return ctx.ValidateCfgWarnings
}
