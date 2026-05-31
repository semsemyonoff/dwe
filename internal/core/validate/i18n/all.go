package i18n

import (
	"github.com/semsemyonoff/devbox/internal/core/usercommands/registry"
	"github.com/semsemyonoff/devbox/internal/core/validate"
	"github.com/semsemyonoff/devbox/internal/shared/i18n"
)

// All produces validators for i18n translation files.
// projectFiles is the result of i18n.LoadProjectBundles.
// reg is the user-command registry used to validate that translation entries reference real commands/groups.
//
// Missing devbox/i18n/ directory returns zero validators (no error).
func All(projectFiles []i18n.ProjectFile, reg *registry.Registry) []validate.Validator {
	var validators []validate.Validator

	for _, pf := range projectFiles {
		if pf.ParseErr != nil {
			validators = append(validators, &parseErrorValidator{pf: pf})
		}
	}

	for _, pf := range projectFiles {
		if pf.ParseErr == nil && pf.Bundle != nil {
			validators = append(validators, &orphanValidator{pf: pf, reg: reg})
		}
	}

	for _, pf := range projectFiles {
		if pf.ParseErr == nil && pf.Bundle != nil {
			validators = append(validators, &unknownUIKeyValidator{pf: pf})
		}
	}

	return validators
}
