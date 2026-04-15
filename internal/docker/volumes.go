package docker

import (
	"os/exec"
	"slices"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
)

// EnsureVolumes creates any declared volumes that do not yet exist.
// It is idempotent: existing volumes are left untouched.
// Only volumes whose ensure_before list contains command are processed.
func EnsureVolumes(resources config.DockerResourcesConfig, command string, w *render.Writer) error {
	for _, vol := range resources.Volumes {
		if !slices.Contains(vol.EnsureBefore, command) {
			continue
		}
		exists, err := volumeExists(vol.Name)
		if err != nil {
			return err
		}
		if exists {
			w.Success("Volume " + vol.Name + " exists")
			continue
		}
		w.Info("Creating volume " + vol.Name + "...")
		if err := exec.Command("docker", "volume", "create", vol.Name).Run(); err != nil {
			return err
		}
		w.Success("Volume " + vol.Name + " created")
	}
	return nil
}

// volumeExists reports whether a Docker volume with the given name exists.
func volumeExists(name string) (bool, error) {
	err := exec.Command("docker", "volume", "inspect", name).Run()
	if err == nil {
		return true, nil
	}
	if _, ok := err.(*exec.ExitError); ok {
		return false, nil
	}
	return false, err
}
