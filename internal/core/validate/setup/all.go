package setup

import (
	"devbox-cli/internal/core/validate"
	"devbox-cli/internal/core/workflow/setup"
)

// All returns the setup domain validators. The setupCfg and setupErr come from
// a single setup.LoadSetupYAML call performed by the caller. When setupCfg is
// nil and setupErr is nil, the setup.yml file is simply absent (not an error).
//
// The setupErr is handled by the setup.parse validator, which is the SOLE
// emitter of load-error diagnostics for this domain. This matches the pattern
// in validate/snapshot and validate/checks.
func All(setupCfg *setup.Config, setupErr error, setupPath string) []validate.Validator {
	return []validate.Validator{
		&parseValidator{err: setupErr, path: setupPath},
		&typeKnownValidator{cfg: setupCfg},
		&idRequiredValidator{cfg: setupCfg},
		&idUniqueValidator{cfg: setupCfg},
		&writesRequiredValidator{cfg: setupCfg},
		&writesUniqueValidator{cfg: setupCfg},
		&writesSyntaxValidator{cfg: setupCfg},
		&writesScopeValidator{cfg: setupCfg},
		&optionsValidValidator{cfg: setupCfg},
		&validateExclusiveValidator{cfg: setupCfg},
		&validateOnlyOnInputValidator{cfg: setupCfg},
		&validatePresetKnownValidator{cfg: setupCfg},
		&validateRegexCompilesValidator{cfg: setupCfg},
		&typeWritesConsistentValidator{cfg: setupCfg},
		&requiredConsistentValidator{cfg: setupCfg},
	}
}
