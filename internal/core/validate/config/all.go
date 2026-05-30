package config

import (
	"devbox-cli/internal/core/validate"
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
		&serviceHooksValidator{},
		&parallelGroupsValidator{},
		&lifecycleParallelGroupsValidator{},
		&resetParallelGroupsValidator{},
		&iconsValidator{},
	}
}
