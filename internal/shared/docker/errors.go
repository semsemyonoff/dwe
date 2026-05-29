package docker

import "strings"

// IsNoSuchContainerErr reports whether the stderr output from a docker command
// indicates that the target container does not exist.
func IsNoSuchContainerErr(stderr string) bool {
	return strings.Contains(stderr, "No such container")
}
