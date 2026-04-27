package builtin

import (
	"strings"
	"testing"
)

// --- dockerRemoveProjectVolumesBuiltin ---

func TestDockerRemoveVolumes_Validate(t *testing.T) {
	b := dockerRemoveProjectVolumesBuiltin{}
	if err := b.Validate(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := b.Validate(map[string]any{"extra": "ignored"}); err != nil {
		t.Fatalf("unexpected error with extra params: %v", err)
	}
}

func TestDockerRemoveVolumes_Describe(t *testing.T) {
	b := dockerRemoveProjectVolumesBuiltin{}
	desc := b.Describe(nil)
	if !strings.Contains(desc, "docker_remove_project_volumes") {
		t.Errorf("expected builtin name in describe, got %q", desc)
	}
}
