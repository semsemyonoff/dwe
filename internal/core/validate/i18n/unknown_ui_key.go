package i18n

import (
	"fmt"

	"github.com/semsemyonoff/devbox/internal/core/validate"
	"github.com/semsemyonoff/devbox/internal/shared/i18n"
)

// unknownUIKeyValidator emits a diagnostic for each unknown ui.* key
// (one not in the canonical KnownUIKeys list).
type unknownUIKeyValidator struct {
	pf i18n.ProjectFile
}

func (v *unknownUIKeyValidator) ID() string {
	return fmt.Sprintf("%s/unknown_ui_key", v.pf.Locale)
}

func (v *unknownUIKeyValidator) Domain() string {
	return "i18n"
}

func (v *unknownUIKeyValidator) Run(_ validate.Context) []validate.Diagnostic {
	if v.pf.Bundle == nil || v.pf.Bundle.UI == nil {
		return nil
	}

	knownSet := make(map[string]struct{})
	for _, key := range i18n.KnownUIKeys {
		knownSet[key] = struct{}{}
	}

	var diags []validate.Diagnostic
	for uiKey := range v.pf.Bundle.UI {
		if _, ok := knownSet[uiKey]; !ok {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityWarning,
				Domain:   "i18n",
				Target:   fmt.Sprintf("%s/ui.%s", v.pf.Locale, uiKey),
				File:     v.pf.Path,
				Message:  fmt.Sprintf("unknown ui key: %s", uiKey),
				Hint:     "if intentional, file a request to add it to the canonical set",
			})
		}
	}

	return diags
}
