package docker

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"devbox-cli/internal/render"
)

// HealthGetFn returns the Docker health status string for a container by ID.
// Known return values: "healthy", "unhealthy", "starting", "none".
type HealthGetFn func(id string) (string, error)

// WaitContainersHealthy polls each container until all are healthy or times out.
// Containers with no healthcheck ("none" status) emit a one-time warning and are
// skipped (not counted as failures). Unhealthy containers return an error immediately.
func WaitContainersHealthy(ids []string, getHealth HealthGetFn, attempts int, interval time.Duration, w *render.Writer) error {
	warned := make(map[string]bool)

	for attempt := range attempts {
		if attempt > 0 {
			time.Sleep(interval)
		}

		allDone := true
		for _, id := range ids {
			status, err := getHealth(id)
			if err != nil {
				return fmt.Errorf("inspecting container %s: %w", id, err)
			}
			switch status {
			case "healthy":
				// ready
			case "unhealthy":
				return fmt.Errorf("container %s is unhealthy", id)
			case "none", "":
				if !warned[id] {
					if w != nil {
						w.Warning(fmt.Sprintf("container %s has no healthcheck, skipping", id))
					}
					warned[id] = true
				}
				// treat as done — no healthcheck configured
			default: // "starting" or any other transient state
				allDone = false
			}
		}

		if allDone {
			if w != nil {
				w.Success("all containers healthy")
			}
			return nil
		}
	}
	return fmt.Errorf("containers did not become healthy within timeout (%d attempts)", attempts)
}

// HealthStatus returns the health status of a single container by ID.
// Returns "none" when the container has no healthcheck configured.
// bin is the docker binary name (e.g. "docker" or "podman").
func HealthStatus(bin, id string) (string, error) {
	out, err := exec.Command(
		bin, "inspect",
		"--format", `{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}`,
		id,
	).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
