package i18n

import (
	"fmt"

	"devbox-cli/internal/i18n"
	"devbox-cli/internal/validate"
)

// parseErrorValidator emits a diagnostic for a ProjectFile with a parse error.
type parseErrorValidator struct {
	pf i18n.ProjectFile
}

func (v *parseErrorValidator) ID() string {
	if v.pf.Locale == "" {
		return "_load_error"
	}
	return v.pf.Locale
}

func (v *parseErrorValidator) Domain() string {
	return "i18n"
}

func (v *parseErrorValidator) IsDomainLevel() bool {
	return v.pf.Locale == ""
}

func (v *parseErrorValidator) Run(_ validate.Context) []validate.Diagnostic {
	if v.pf.Locale == "" {
		// Directory-level failure
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "i18n",
			Target:   "_load_error",
			File:     v.pf.Path,
			Message:  v.pf.ParseErr.Error(),
		}}
	}

	// Per-file strict-decode failure
	return []validate.Diagnostic{{
		Severity: validate.SeverityError,
		Domain:   "i18n",
		Target:   fmt.Sprintf("%s (parse error)", v.pf.Locale),
		File:     v.pf.Path,
		Message:  v.pf.ParseErr.Error(),
	}}
}

// loadErrValidator surfaces a non-nil aggregate error from LoadProjectBundles.
type loadErrValidator struct {
	err error
}

func (v *loadErrValidator) ID() string          { return "_load_error" }
func (v *loadErrValidator) Domain() string      { return "i18n" }
func (v *loadErrValidator) IsDomainLevel() bool { return true }
func (v *loadErrValidator) IsGlobal() bool      { return true }

func (v *loadErrValidator) Run(_ validate.Context) []validate.Diagnostic {
	return []validate.Diagnostic{{
		Severity: validate.SeverityError,
		Domain:   "i18n",
		Target:   "_load_error",
		Message:  v.err.Error(),
	}}
}

var _ validate.DomainLevelValidator = (*parseErrorValidator)(nil)
var _ validate.GlobalValidator = (*loadErrValidator)(nil)
