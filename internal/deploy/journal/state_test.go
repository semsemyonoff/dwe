package journal

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoad_MissingFile tests that Load returns a zero-value with defaults when file is absent.
func TestLoad_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "nonexistent.yml")

	state, err := Load(statePath)

	require.NoError(t, err)
	assert.NotNil(t, state)
	assert.Equal(t, "1", state.SchemaVersion)
	assert.NotNil(t, state.Project)
	assert.NotNil(t, state.Services)
	assert.Empty(t, state.Services)
}

// TestLoad_RoundTrip tests that Save + Load preserves data.
func TestLoad_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	original := &ProjectState{
		SchemaVersion: "1",
		Project: &ProjectLevelState{
			Status:     StatusDeployed,
			ConfigHash: "abc123",
			LastRun: &LastRun{
				Status:    StatusOk,
				StartedAt: time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC),
			},
			Phases: map[string]*PhaseState{
				"setup": {
					Status: StatusOk,
					Steps: map[string]*StepState{
						"create-dirs": {
							Status:     StatusOk,
							ActionHash: "hash1",
							DurationMs: 10,
						},
					},
				},
			},
		},
		Services: map[string]*ServiceState{
			"main": {
				Status:     StatusDeployed,
				ConfigHash: "def456",
				DeployedAt: time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC),
				LastRun: &LastRun{
					Status: StatusOk,
				},
				Phases: map[string]*PhaseState{
					"setup": {
						Status: StatusOk,
						Steps: map[string]*StepState{
							"install": {
								Status:     StatusOk,
								ActionHash: "hash2",
								DurationMs: 20,
							},
						},
					},
				},
			},
		},
	}

	// Save
	err := Save(statePath, original)
	require.NoError(t, err)
	assert.FileExists(t, statePath)

	// Load
	loaded, err := Load(statePath)
	require.NoError(t, err)

	// Verify
	assert.Equal(t, original.SchemaVersion, loaded.SchemaVersion)
	assert.Equal(t, original.Project.Status, loaded.Project.Status)
	assert.Equal(t, original.Project.ConfigHash, loaded.Project.ConfigHash)
	assert.Equal(t, original.Services["main"].Status, loaded.Services["main"].Status)
	assert.Equal(t, original.Services["main"].ConfigHash, loaded.Services["main"].ConfigHash)
	assert.Equal(t, "hash1", loaded.Project.Phases["setup"].Steps["create-dirs"].ActionHash)
	assert.Equal(t, "hash2", loaded.Services["main"].Phases["setup"].Steps["install"].ActionHash)
}

// TestLoad_UnknownFields tests that state.yml uses lenient YAML decoding —
// unknown fields are silently ignored so a newer devbox version's state file
// can be read by an older version without error.
func TestLoad_UnknownFields(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	content := `schema_version: "1"
project:
  status: deployed
  unknown_field: should_be_ignored
`
	err := os.WriteFile(statePath, []byte(content), 0o644)
	require.NoError(t, err)

	state, err := Load(statePath)
	assert.NoError(t, err, "unknown fields must be ignored (lenient decode)")
	require.NotNil(t, state)
	assert.Equal(t, StatusDeployed, state.Project.Status)
}

// TestLoad_MalformedYAML tests that malformed YAML produces an error.
func TestLoad_MalformedYAML(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "malformed.yml")

	content := `schema_version: "1"
  this is not valid yaml: [
`
	err := os.WriteFile(statePath, []byte(content), 0o644)
	require.NoError(t, err)

	_, err = Load(statePath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse state file")
}

// TestSave_CreatesParentDirectory tests that Save creates parent directories with 0o755.
func TestSave_CreatesParentDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "subdir", "state.yml")

	state := &ProjectState{
		SchemaVersion: "1",
		Project:       &ProjectLevelState{},
		Services:      make(map[string]*ServiceState),
	}

	err := Save(statePath, state)
	require.NoError(t, err)

	// Check file exists and parent directory exists
	assert.FileExists(t, statePath)
	parentDir := filepath.Dir(statePath)
	info, err := os.Stat(parentDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

// TestSave_FilePermissions tests that Save creates files with 0o644 permissions.
func TestSave_FilePermissions(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	state := &ProjectState{
		SchemaVersion: "1",
		Project:       &ProjectLevelState{},
		Services:      make(map[string]*ServiceState),
	}

	err := Save(statePath, state)
	require.NoError(t, err)

	info, err := os.Stat(statePath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

// TestSave_NilState tests that Save returns error for nil state.
func TestSave_NilState(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	err := Save(statePath, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot save nil state")
}

// TestRemove_NoOp tests that Remove is a no-op for absent files.
func TestRemove_NoOp(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "nonexistent.yml")

	err := Remove(statePath)
	assert.NoError(t, err)
	assert.NoFileExists(t, statePath)
}

// TestRemove_DeletesFile tests that Remove deletes existing files.
func TestRemove_DeletesFile(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	// Create file
	state := &ProjectState{
		SchemaVersion: "1",
		Project:       &ProjectLevelState{},
		Services:      make(map[string]*ServiceState),
	}
	err := Save(statePath, state)
	require.NoError(t, err)
	assert.FileExists(t, statePath)

	// Remove
	err = Remove(statePath)
	require.NoError(t, err)
	assert.NoFileExists(t, statePath)
}

// TestRemoveService_LastServiceRemoved tests that removing the last service deletes the file.
func TestRemoveService_LastServiceRemoved(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	state := &ProjectState{
		SchemaVersion: "1",
		Project:       &ProjectLevelState{},
		Services: map[string]*ServiceState{
			"main": {
				Status:     StatusDeployed,
				ConfigHash: "abc123",
			},
		},
	}

	// Save
	err := Save(statePath, state)
	require.NoError(t, err)
	assert.FileExists(t, statePath)

	// Remove the only service
	err = RemoveService(statePath, "main")
	require.NoError(t, err)

	// File should be deleted
	assert.NoFileExists(t, statePath)
}

// TestRemoveService_PreservesOtherServices tests that removing one service keeps others.
func TestRemoveService_PreservesOtherServices(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	state := &ProjectState{
		SchemaVersion: "1",
		Project:       &ProjectLevelState{},
		Services: map[string]*ServiceState{
			"main": {
				Status:     StatusDeployed,
				ConfigHash: "abc123",
			},
			"db": {
				Status:     StatusDeployed,
				ConfigHash: "def456",
			},
		},
	}

	// Save
	err := Save(statePath, state)
	require.NoError(t, err)

	// Remove main
	err = RemoveService(statePath, "main")
	require.NoError(t, err)

	// Load and verify
	loaded, err := Load(statePath)
	require.NoError(t, err)
	assert.NotContains(t, loaded.Services, "main")
	assert.Contains(t, loaded.Services, "db")
	assert.Equal(t, StatusDeployed, loaded.Services["db"].Status)
}

// TestRemoveService_NonexistentFile tests that removing from nonexistent file returns zero-value.
func TestRemoveService_NonexistentFile(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "nonexistent.yml")

	// RemoveService on nonexistent file should load zero-value and then try to save empty
	// When no services, it should remove the file (which doesn't exist, so no-op)
	err := RemoveService(statePath, "main")
	require.NoError(t, err)
	assert.NoFileExists(t, statePath)
}

// TestRecompute_ProjectStatusFromServices tests Recompute derives project status.
func TestRecompute_ProjectStatusFromServices(t *testing.T) {
	tests := []struct {
		name     string
		services map[string]*ServiceState
		expected Status
	}{
		{
			name: "all deployed",
			services: map[string]*ServiceState{
				"main": {Status: StatusDeployed},
				"db":   {Status: StatusDeployed},
			},
			expected: StatusDeployed,
		},
		{
			name: "any failed",
			services: map[string]*ServiceState{
				"main":   {Status: StatusDeployed},
				"broken": {Status: StatusFailed},
			},
			expected: StatusFailed,
		},
		{
			name: "mixed deployed and not_deployed",
			services: map[string]*ServiceState{
				"main": {Status: StatusDeployed},
				"new":  {Status: StatusNotDeployed},
			},
			expected: StatusPartial,
		},
		{
			name: "all not_deployed",
			services: map[string]*ServiceState{
				"main": {Status: StatusNotDeployed},
				"db":   {Status: StatusNotDeployed},
			},
			expected: StatusNotDeployed,
		},
		{
			name: "partial includes mixed deployed+failed",
			services: map[string]*ServiceState{
				"main": {Status: StatusDeployed},
				"bad":  {Status: StatusPartial},
			},
			expected: StatusFailed,
		},
		{
			name:     "no services",
			services: map[string]*ServiceState{},
			expected: StatusNotDeployed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &ProjectState{
				SchemaVersion: "1",
				Project:       &ProjectLevelState{},
				Services:      tt.services,
			}

			Recompute(state)
			assert.Equal(t, tt.expected, state.Project.Status)
		})
	}
}

// TestRecompute_NilState is a no-op.
func TestRecompute_NilState(t *testing.T) {
	// Should not panic
	Recompute(nil)
}

// TestDefaultRelPath checks the constant is correct.
func TestDefaultRelPath(t *testing.T) {
	assert.Equal(t, ".devbox/deploy/state.yml", DefaultRelPath)
}
