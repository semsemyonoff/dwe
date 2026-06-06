package docker

import (
	"os/exec"
	"slices"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/render"
)

// EnsureVolumes creates any declared volumes that do not yet exist.
// It is idempotent: existing volumes are left untouched.
// Only volumes whose ensure_before list contains command are processed.
//
// projectName MUST be the resolved compose project name — pass
// config.ResolveComposeProjectName(baseDir, cfg) (docker.yml project_name, else
// "<prefix>-<name>"), NOT the raw DockerConfig.ProjectName, which is empty when
// project_name is omitted. It prefixes non-shared volume names so the runtime
// matches the "<project>_<name>" scheme Docker Compose itself applies to named
// volumes; passing an empty projectName would create bare-named volumes that
// diverge from the compose/-p and reset scopes. Shared volumes ignore the prefix.
//
// bin is the Docker-compatible binary (e.g. "docker", "podman"); pass
// config.DockerBin(cfg) at the call site.
func EnsureVolumes(resources config.DockerResourcesConfig, projectName, command, bin string, w *render.Writer) error {
	for _, vol := range resources.Volumes {
		if !slices.Contains(vol.EnsureBefore, command) {
			continue
		}
		name := vol.ResolveName(projectName)
		exists, err := volumeExists(bin, name)
		if err != nil {
			return err
		}
		if exists {
			w.Success("Volume " + name + " exists")
			continue
		}
		w.Info("Creating volume " + name + "...")
		if err := exec.Command(bin, "volume", "create", name).Run(); err != nil { //nolint:gosec
			return err
		}
		w.Success("Volume " + name + " created")
	}
	return nil
}

// volumeExists reports whether a Docker volume with the given name exists.
func volumeExists(bin, name string) (bool, error) {
	err := exec.Command(bin, "volume", "inspect", name).Run() //nolint:gosec
	if err == nil {
		return true, nil
	}
	if _, ok := err.(*exec.ExitError); ok {
		return false, nil
	}
	return false, err
}
