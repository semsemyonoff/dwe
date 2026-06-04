package setup

import (
	"github.com/semsemyonoff/dwe/internal/core/validate"
	"github.com/semsemyonoff/dwe/internal/core/workflow/setup"
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
		&parseValidator{baseValidator: baseValidator{id: "parse"}, err: setupErr, path: setupPath},
		&typeKnownValidator{newCfg("type_known", setupCfg)},
		&idRequiredValidator{newCfg("id_required", setupCfg)},
		&idUniqueValidator{newCfg("id_unique", setupCfg)},
		&writesRequiredValidator{newCfg("writes_required", setupCfg)},
		&writesUniqueValidator{newCfg("writes_unique", setupCfg)},
		&writesSyntaxValidator{newCfg("writes_syntax", setupCfg)},
		&writesScopeValidator{newCfg("writes_scope", setupCfg)},
		&optionsValidValidator{newCfg("options_valid", setupCfg)},
		&validateExclusiveValidator{newCfg("validate_exclusive", setupCfg)},
		&validateOnlyOnInputValidator{newCfg("validate_only_on_input", setupCfg)},
		&validatePresetKnownValidator{newCfg("validate_preset_known", setupCfg)},
		&validateRegexCompilesValidator{newCfg("validate_regex_compiles", setupCfg)},
		&typeWritesConsistentValidator{newCfg("type_writes_consistent", setupCfg)},
		&requiredConsistentValidator{newCfg("required_consistent", setupCfg)},
	}
}
