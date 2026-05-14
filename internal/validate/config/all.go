package config

import (
	"devbox-cli/internal/validate"
)

// All returns all config validators.
func All() []validate.Validator {
	return []validate.Validator{
		&devboxValidator{},
		&servicesValidator{},
		&dockerValidator{},
		&infoValidator{},
		&stylesValidator{},
		&lifecycleValidator{},
		&deployValidator{},
		&resetValidator{},
		&serviceDeployValidator{},
	}
}
