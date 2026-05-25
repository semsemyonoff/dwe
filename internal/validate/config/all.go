package config

import (
	"devbox-cli/internal/validate"
)

// All returns all config validators.
func All() []validate.Validator {
	return []validate.Validator{
		&devboxValidator{},
		&validateYmlValidator{},
		&uiValidator{},
		&servicesValidator{},
		&dockerValidator{},
		&infoValidator{},
		&stylesValidator{},
		&lifecycleValidator{},
		&deployValidator{},
		&deployFilesGateValidator{},
		&lifecycleFilesGateValidator{},
		&resetValidator{},
		&resetFilesGateValidator{},
		&serviceDeployValidator{},
		&servicesFolderValidator{},
		&deployAfterValidator{},
		&parallelGroupsValidator{},
		&lifecycleParallelGroupsValidator{},
		&resetParallelGroupsValidator{},
	}
}
