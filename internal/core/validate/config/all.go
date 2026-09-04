package config

import (
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// All returns all config validators.
func All() []validate.Validator {
	return []validate.Validator{
		&workspaceValidator{},
		&validateYmlValidator{},
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
		&generatedValidator{},
		&deprecationsValidator{},
		&parallelGroupsValidator{},
		&lifecycleParallelGroupsValidator{},
		&resetParallelGroupsValidator{},
		&iconsValidator{},
		&composeProjectNameValidator{},
		&containerNameValidator{},
		&formalBlocksValidator{},
		&templateRefsValidator{},
		&portsExportsValidator{},
	}
}
