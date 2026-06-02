package docker

import (
	"errors"
	"strings"
)

// ErrNoSuchContainer is returned by docker helpers that surface the
// "No such container" condition as a typed error so callers can detect it
// with errors.Is and add domain-specific guidance.
var ErrNoSuchContainer = errors.New("docker: no such container")

// IsNoSuchContainerErr reports whether the stderr output from a docker command
// indicates that the target container does not exist.
func IsNoSuchContainerErr(stderr string) bool {
	return strings.Contains(stderr, "No such container")
}

// IsDaemonUnavailableErr reports whether the stderr output from a docker command
// indicates that the Docker daemon is not reachable.
func IsDaemonUnavailableErr(stderr string) bool {
	return strings.Contains(stderr, "Cannot connect to the Docker daemon")
}
