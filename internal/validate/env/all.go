package env

import (
	"devbox-cli/internal/config"
	"devbox-cli/internal/validate"
)

// All returns the built-in env probes. cfg is consulted via the nil-safe
// BinariesConfig accessors (config.DockerBin / config.GitBin / config.ShellBin),
// so a nil cfg yields the defaults ("docker" / "git" / "sh").
func All(cfg *config.DevboxConfig) []validate.Validator {
	return []validate.Validator{
		&dockerBinValidator{cfg: cfg},
		&dockerDaemonValidator{cfg: cfg},
		&dockerComposeValidator{cfg: cfg},
		&gitBinValidator{cfg: cfg},
		&shellBinValidator{cfg: cfg},
		&projectPermsValidator{},
		&portsFreeValidator{cfg: cfg},
	}
}
