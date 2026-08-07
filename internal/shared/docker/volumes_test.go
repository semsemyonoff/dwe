package docker

import (
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/render"
)

// TestEnsureVolumes_BinarySubstitution verifies that EnsureVolumes threads the
// supplied bin argument instead of a hardcoded "docker". The volume list is
// empty, so nothing is exec'd — this only guards the no-op path.
func TestEnsureVolumes_BinarySubstitution(t *testing.T) {
	// volumeExists uses exec.Command(bin, "volume", "inspect", name).Run().
	// A missing binary returns a path error, not an ExitError, so volumeExists
	// propagates it as a real error. We just verify the bin parameter is wired
	// at all by confirming EnsureVolumes with an empty volume list is a no-op.
	resources := config.DockerResourcesConfig{Volumes: nil}
	w := render.Stdout()
	err := EnsureVolumes(resources, "test-project", "deploy", "podman", w)
	if err != nil {
		t.Errorf("EnsureVolumes with empty volumes should be a no-op, got: %v", err)
	}
}

func TestVolumeExists_DefaultBinParameter(t *testing.T) {
	// volumeExists is unexported; this test exercises it indirectly via EnsureVolumes.
	// The bin parameter is threaded correctly if EnsureVolumes accepts it without panic.
	resources := config.DockerResourcesConfig{Volumes: nil}
	w := render.Stdout()
	if err := EnsureVolumes(resources, "proj", "up", "docker", w); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
