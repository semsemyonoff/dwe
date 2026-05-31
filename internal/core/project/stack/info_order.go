package stack

import (
	"github.com/semsemyonoff/devbox/internal/core/project/config"
)

// DeployOrder returns services ordered by deployment dependencies, grouped by type.
// Delegates to config.DeployOrder; kept here for callers that import stack.
func DeployOrder(cfg *config.DevboxConfig, types []string) []string {
	return config.DeployOrder(cfg, types)
}
