package i18n

import (
	"devbox-cli/internal/i18n"
	"devbox-cli/internal/usercommands/registry"
	"devbox-cli/internal/validate"
)

// All produces validators for i18n translation files.
// projectFiles is the result of i18n.LoadProjectBundles.
// loadErr is the aggregate error from LoadProjectBundles (non-nil only for programmer errors).
// reg is the user-command registry used to validate that translation entries reference real commands/groups.
//
// When loadErr is non-nil, an error validator is returned in addition to per-file validators.
// Missing devbox/i18n/ directory returns zero validators (no error).
func All(projectFiles []i18n.ProjectFile, loadErr error, reg *registry.Registry) []validate.Validator {
	var validators []validate.Validator

	if loadErr != nil {
		validators = append(validators, &loadErrValidator{err: loadErr})
	}

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
