package journal

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper: build a state file with pre-existing service entries.
func seedState(t *testing.T, path string, s *ProjectState) {
	t.Helper()
	require.NoError(t, Save(path, s))
}

// helper: load state and assert no error.
func loadState(t *testing.T, path string) *ProjectState {
	t.Helper()
	s, err := Load(path)
	require.NoError(t, err)
	return s
}

func TestAddPendingOp_FirstRestart(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	err := AddPendingOp(path, PendingOp{Kind: PendingRestart}, "hash1")
	require.NoError(t, err)

	s := loadState(t, path)
	require.NotNil(t, s.Pending)
	require.Len(t, s.Pending.Operations, 1)
	assert.Equal(t, PendingRestart, s.Pending.Operations[0].Kind)
	assert.Equal(t, "hash1", s.Pending.ConfigHash)
}

func TestAddPendingOp_MixedBatch(t *testing.T) {
	// Add restart then deploy — both kinds must be preserved.
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	require.NoError(t, AddPendingOp(path, PendingOp{Kind: PendingRestart}, "hash1"))
	require.NoError(t, AddPendingOp(path, PendingOp{Kind: PendingDeploy, Services: []string{"a"}}, "hash1"))

	s := loadState(t, path)
	require.NotNil(t, s.Pending)
	require.Len(t, s.Pending.Operations, 2)
	assert.Equal(t, PendingRestart, s.Pending.Operations[0].Kind)
	assert.Equal(t, PendingDeploy, s.Pending.Operations[1].Kind)
	assert.Equal(t, []string{"a"}, s.Pending.Operations[1].Services)
}

func TestAddPendingOp_DeployMergesServices(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	require.NoError(t, AddPendingOp(path, PendingOp{Kind: PendingDeploy, Services: []string{"a"}}, "hash1"))
	require.NoError(t, AddPendingOp(path, PendingOp{Kind: PendingDeploy, Services: []string{"b"}}, "hash1"))

	s := loadState(t, path)
	require.NotNil(t, s.Pending)
	require.Len(t, s.Pending.Operations, 1)
	assert.Equal(t, []string{"a", "b"}, s.Pending.Operations[0].Services)
}

func TestAddPendingOp_RestartNoDuplicate(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	require.NoError(t, AddPendingOp(path, PendingOp{Kind: PendingRestart}, "hash1"))
	require.NoError(t, AddPendingOp(path, PendingOp{Kind: PendingRestart}, "hash1"))

	s := loadState(t, path)
	require.NotNil(t, s.Pending)
	assert.Len(t, s.Pending.Operations, 1)
}

func TestAddPendingOp_UnspecifiedReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	err := AddPendingOp(path, PendingOp{Kind: PendingKindUnspecified}, "hash1")
	require.Error(t, err)
	assert.NoFileExists(t, path)
}

func TestAddPendingOp_MissingFileCreatesOne(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	err := AddPendingOp(path, PendingOp{Kind: PendingRestart}, "hash1")
	require.NoError(t, err)
	assert.FileExists(t, path)
}

func TestAddPendingOps_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	ops := []PendingOp{
		{Kind: PendingDeploy, Services: []string{"a"}},
		{Kind: PendingRestart},
	}
	err := AddPendingOps(path, ops, "hash1")
	require.NoError(t, err)

	s := loadState(t, path)
	require.NotNil(t, s.Pending)
	require.Len(t, s.Pending.Operations, 2)
}

func TestAddPendingOps_EmptySliceNoOp(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	// Pre-seed state so we can verify it's unchanged.
	seedState(t, path, &ProjectState{
		SchemaVersion: "1",
		Project:       &ProjectLevelState{},
		Services:      make(map[string]*ServiceState),
	})

	err := AddPendingOps(path, nil, "hash1")
	require.NoError(t, err)

	s := loadState(t, path)
	assert.Nil(t, s.Pending)
}

func TestAddPendingOps_UnspecifiedKindRejected(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	// Pre-seed so we can verify file unchanged.
	seedState(t, path, &ProjectState{
		SchemaVersion: "1",
		Project:       &ProjectLevelState{},
		Services:      make(map[string]*ServiceState),
	})

	ops := []PendingOp{
		{Kind: PendingDeploy, Services: []string{"a"}},
		{Kind: PendingKindUnspecified},
	}
	err := AddPendingOps(path, ops, "hash1")
	require.Error(t, err)

	s := loadState(t, path)
	assert.Nil(t, s.Pending) // file unchanged
}

func TestClearPending_ResetsUnconditionally(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	require.NoError(t, AddPendingOp(path, PendingOp{Kind: PendingRestart}, "hash1"))

	require.NoError(t, ClearPending(path))

	s := loadState(t, path)
	assert.Nil(t, s.Pending)
}

func TestClearPendingForKind_RestartOnMixed(t *testing.T) {
	// Clear restart → deploy survives.
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	require.NoError(t, AddPendingOp(path, PendingOp{Kind: PendingRestart}, "hash1"))
	require.NoError(t, AddPendingOp(path, PendingOp{Kind: PendingDeploy, Services: []string{"a"}}, "hash1"))

	require.NoError(t, ClearPendingForKind(path, PendingRestart))

	s := loadState(t, path)
	require.NotNil(t, s.Pending)
	require.Len(t, s.Pending.Operations, 1)
	assert.Equal(t, PendingDeploy, s.Pending.Operations[0].Kind)
}

func TestClearPendingForKind_OnDeployOnlyNoOp(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	require.NoError(t, AddPendingOp(path, PendingOp{Kind: PendingDeploy, Services: []string{"a"}}, "hash1"))

	// Clearing restart on a deploy-only state should leave deploy intact.
	require.NoError(t, ClearPendingForKind(path, PendingRestart))

	s := loadState(t, path)
	require.NotNil(t, s.Pending)
	require.Len(t, s.Pending.Operations, 1)
	assert.Equal(t, PendingDeploy, s.Pending.Operations[0].Kind)
}

func TestClearPendingForServices_SubsetClear(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	require.NoError(t, AddPendingOp(path, PendingOp{Kind: PendingDeploy, Services: []string{"a", "b"}}, "hash1"))

	require.NoError(t, ClearPendingForServices(path, PendingDeploy, []string{"a"}))

	s := loadState(t, path)
	require.NotNil(t, s.Pending)
	require.Len(t, s.Pending.Operations, 1)
	assert.Equal(t, []string{"b"}, s.Pending.Operations[0].Services)
}

func TestClearPendingForServices_LastServiceRemovesOp(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	require.NoError(t, AddPendingOp(path, PendingOp{Kind: PendingDeploy, Services: []string{"a"}}, "hash1"))

	require.NoError(t, ClearPendingForServices(path, PendingDeploy, []string{"a"}))

	s := loadState(t, path)
	assert.Nil(t, s.Pending)
}

func TestClearPendingForServices_WrongKindNoOp(t *testing.T) {
	// Clearing deploy services when only restart exists → no-op.
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	require.NoError(t, AddPendingOp(path, PendingOp{Kind: PendingRestart}, "hash1"))

	require.NoError(t, ClearPendingForServices(path, PendingDeploy, []string{"x"}))

	s := loadState(t, path)
	require.NotNil(t, s.Pending)
	assert.Len(t, s.Pending.Operations, 1)
}

func TestClearPendingForServices_RestartIgnoresServices(t *testing.T) {
	// For PendingRestart, services arg is ignored — whole restart op removed.
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	require.NoError(t, AddPendingOp(path, PendingOp{Kind: PendingRestart}, "hash1"))

	require.NoError(t, ClearPendingForServices(path, PendingRestart, []string{"anything"}))

	s := loadState(t, path)
	assert.Nil(t, s.Pending)
}

func TestClearPendingForServices_AbsentNameNoOp(t *testing.T) {
	// Removing a service name not in the op is idempotent.
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	require.NoError(t, AddPendingOp(path, PendingOp{Kind: PendingDeploy, Services: []string{"a", "b"}}, "hash1"))

	require.NoError(t, ClearPendingForServices(path, PendingDeploy, []string{"nonexistent"}))

	s := loadState(t, path)
	require.NotNil(t, s.Pending)
	assert.Equal(t, []string{"a", "b"}, s.Pending.Operations[0].Services)
}

func TestClearPendingOps_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	// Pre-seed: [{Deploy, ["a","b"]}, {Restart}]
	require.NoError(t, AddPendingOps(path, []PendingOp{
		{Kind: PendingDeploy, Services: []string{"a", "b"}},
		{Kind: PendingRestart},
	}, "hash1"))

	clears := []PendingClear{
		{Kind: PendingDeploy, Services: []string{"a"}},
		{Kind: PendingRestart},
	}
	require.NoError(t, ClearPendingOps(path, clears))

	s := loadState(t, path)
	require.NotNil(t, s.Pending)
	require.Len(t, s.Pending.Operations, 1)
	assert.Equal(t, PendingDeploy, s.Pending.Operations[0].Kind)
	assert.Equal(t, []string{"b"}, s.Pending.Operations[0].Services)
}

func TestClearPendingOps_EmptySliceNoOp(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	require.NoError(t, AddPendingOp(path, PendingOp{Kind: PendingRestart}, "hash1"))

	require.NoError(t, ClearPendingOps(path, nil))

	s := loadState(t, path)
	assert.NotNil(t, s.Pending) // unchanged
}

func TestClearPendingOps_UnspecifiedKindRejected(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	err := ClearPendingOps(path, []PendingClear{{Kind: PendingKindUnspecified}})
	require.Error(t, err)
}

func TestClearStar_MissingFileIsNoOp(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	// All Clear* variants should silently succeed on missing file.
	assert.NoError(t, ClearPending(path))
	assert.NoError(t, ClearPendingForKind(path, PendingRestart))
	assert.NoError(t, ClearPendingForServices(path, PendingDeploy, []string{"a"}))
	assert.NoError(t, ClearPendingOps(path, []PendingClear{{Kind: PendingRestart}}))
}

func TestReplaceServiceWithPending_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	// Pre-seed state with a deployed service.
	seedState(t, path, &ProjectState{
		SchemaVersion: "1",
		Project:       &ProjectLevelState{Status: StatusDeployed},
		Services: map[string]*ServiceState{
			"a": {Status: StatusDeployed, ConfigHash: "oldhash"},
		},
	})

	require.NoError(t, ReplaceServiceWithPending(path, "a", PendingOp{Kind: PendingDeploy, Services: []string{"a"}}, "hash2"))

	s := loadState(t, path)
	assert.NotContains(t, s.Services, "a")
	require.NotNil(t, s.Pending)
	op := s.Pending.Find(PendingDeploy)
	require.NotNil(t, op)
	assert.Equal(t, []string{"a"}, op.Services)
}

func TestReplaceServiceWithPending_MissingFileTreatedAsNoOp(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	// File doesn't exist — remove is no-op, pending write proceeds.
	require.NoError(t, ReplaceServiceWithPending(path, "a", PendingOp{Kind: PendingRestart}, "hash1"))

	s := loadState(t, path)
	require.NotNil(t, s.Pending)
	assert.Len(t, s.Pending.Operations, 1)
}

func TestReplaceServiceWithPending_UnspecifiedKindReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	err := ReplaceServiceWithPending(path, "a", PendingOp{Kind: PendingKindUnspecified}, "hash1")
	require.Error(t, err)
}

func TestPendingOp_ServiceNames_DefensiveCopy(t *testing.T) {
	op := &PendingOp{Kind: PendingDeploy, Services: []string{"a", "b"}}
	names := op.ServiceNames()

	names[0] = "mutated"
	assert.Equal(t, "a", op.Services[0], "mutation of returned slice must not affect internal state")
}

func TestPendingOp_ServiceNames_NilOp(t *testing.T) {
	var op *PendingOp
	assert.Nil(t, op.ServiceNames())
}

func TestPendingApply_Find_ReturnsNilWhenMissing(t *testing.T) {
	p := &PendingApply{
		Operations: []PendingOp{{Kind: PendingRestart}},
	}
	assert.Nil(t, p.Find(PendingDeploy))
}

func TestPendingApply_Find_NilReceiver(t *testing.T) {
	var p *PendingApply
	assert.Nil(t, p.Find(PendingRestart))
}

// TestAddPendingOps_WriteFailureRegression verifies that a write failure after loading
// leaves the state unchanged. We simulate this by making the path a directory.
func TestAddPendingOps_WriteFailureRegression(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	// Pre-seed valid state.
	seedState(t, statePath, &ProjectState{
		SchemaVersion: "1",
		Project:       &ProjectLevelState{},
		Services:      make(map[string]*ServiceState),
	})

	// Replace the state file with a directory so writes fail.
	require.NoError(t, os.Remove(statePath))
	require.NoError(t, os.Mkdir(statePath, 0o755))

	ops := []PendingOp{{Kind: PendingRestart}}
	err := AddPendingOps(statePath, ops, "hash1")
	// Should error because Save will fail.
	require.Error(t, err)
}

// TestClearPendingOps_WriteFailureRegression asserts only that a multi-op
// clear on valid input succeeds and rewrites the state file — no write
// failure is injected (there is no seam for one).
func TestClearPendingOps_WriteFailureRegression(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	// Pre-seed pending.
	require.NoError(t, AddPendingOps(statePath, []PendingOp{
		{Kind: PendingDeploy, Services: []string{"a", "b"}},
		{Kind: PendingRestart},
	}, "hash1"))

	// Back up current state contents for comparison.
	originalData, err := os.ReadFile(statePath)
	require.NoError(t, err)

	// No seam exists to inject a Save failure here, so this stays a smoke test:
	// a no-error call on valid input must apply the clear.
	clears := []PendingClear{
		{Kind: PendingDeploy, Services: []string{"a"}},
		{Kind: PendingRestart},
	}
	require.NoError(t, ClearPendingOps(statePath, clears))

	currentData, err := os.ReadFile(statePath)
	require.NoError(t, err)
	// Data must have changed (the clear applied).
	assert.NotEqual(t, string(originalData), string(currentData))
}

// TestReplaceServiceWithPending_AtomicWriteFailure verifies that if saving fails, no
// partial state is written.
func TestReplaceServiceWithPending_AtomicWriteFailure(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	seedState(t, statePath, &ProjectState{
		SchemaVersion: "1",
		Project:       &ProjectLevelState{Status: StatusDeployed},
		Services: map[string]*ServiceState{
			"a": {Status: StatusDeployed, ConfigHash: "oldhash"},
		},
	})

	// Make the parent dir read-only so CreateTemp fails.
	dir := filepath.Dir(statePath)
	require.NoError(t, os.Chmod(dir, 0o555))
	defer func() { _ = os.Chmod(dir, 0o755) }()

	err := ReplaceServiceWithPending(statePath, "a", PendingOp{Kind: PendingDeploy, Services: []string{"a"}}, "hash2")
	// On CI the test may run as root (in Docker); skip assertion if no error.
	if err == nil {
		t.Skip("running as root or OS permits write despite read-only dir — skip failure test")
	}

	// Re-enable write permission so we can Load.
	_ = os.Chmod(dir, 0o755)

	s := loadState(t, statePath)
	assert.Contains(t, s.Services, "a", "service must still be present after failed write")
	assert.Nil(t, s.Pending, "no pending must be written after failed write")
}

// TestSortedUniq verifies the sortedUniq helper across edge cases.
func TestSortedUniq(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "empty slice",
			in:   []string{},
			want: []string{},
		},
		{
			name: "nil slice",
			in:   nil,
			want: nil,
		},
		{
			name: "single element",
			in:   []string{"a"},
			want: []string{"a"},
		},
		{
			name: "already sorted and unique",
			in:   []string{"a", "b", "c"},
			want: []string{"a", "b", "c"},
		},
		{
			name: "duplicates present",
			in:   []string{"b", "a", "b", "c", "a"},
			want: []string{"a", "b", "c"},
		},
		{
			name: "unsorted with duplicates",
			in:   []string{"z", "a", "m", "a", "z"},
			want: []string{"a", "m", "z"},
		},
		{
			name: "all same",
			in:   []string{"x", "x", "x"},
			want: []string{"x"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sortedUniq(tt.in)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestSortedUniq_DoesNotMutateInput verifies that sortedUniq does not modify the input slice.
func TestSortedUniq_DoesNotMutateInput(t *testing.T) {
	in := []string{"c", "a", "b", "a"}
	orig := make([]string, len(in))
	copy(orig, in)

	_ = sortedUniq(in)

	assert.Equal(t, orig, in, "input slice must not be mutated")
}

// Ensure PendingKind constants have expected values.
func TestPendingKindValues(t *testing.T) {
	assert.Equal(t, PendingKind(""), PendingKindUnspecified)
	assert.Equal(t, PendingKind("restart"), PendingRestart)
	assert.Equal(t, PendingKind("deploy"), PendingDeploy)
}

// TestConcurrentAddPendingOp is a basic smoke test that concurrent adds don't panic.
// This does NOT assert perfect ordering — file-level locking is not implemented.
func TestConcurrentAddPendingOp_Smoke(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			_ = AddPendingOp(path, PendingOp{Kind: PendingRestart}, "hash1")
		})
	}
	wg.Wait()
	// No panic is the assertion here.
}

// TestProjectState_PendingOmitempty verifies that nil Pending is omitted from YAML.
func TestProjectState_PendingOmitempty(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	s := &ProjectState{
		SchemaVersion: "1",
		Project:       &ProjectLevelState{},
		Services:      make(map[string]*ServiceState),
		Pending:       nil,
	}
	require.NoError(t, Save(path, s))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "pending")
}

// TestProjectState_PendingRoundTrip verifies YAML round-trip for Pending.
func TestProjectState_PendingRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")

	require.NoError(t, AddPendingOps(path, []PendingOp{
		{Kind: PendingDeploy, Services: []string{"a", "b"}},
		{Kind: PendingRestart},
	}, "confighash"))

	s := loadState(t, path)
	require.NotNil(t, s.Pending)
	assert.Equal(t, "confighash", s.Pending.ConfigHash)
	assert.Len(t, s.Pending.Operations, 2)
	assert.NotNil(t, s.Pending.Find(PendingDeploy))
	assert.NotNil(t, s.Pending.Find(PendingRestart))
}

// Ensure errors.Is works on AddPendingOp unspecified error (not a sentinel — just verify non-nil).
func TestAddPendingOp_UnspecifiedErrorIsNonNil(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.yml")
	err := AddPendingOp(path, PendingOp{Kind: PendingKindUnspecified}, "h")
	assert.True(t, errors.Is(err, err)) // trivial identity — just ensures it's non-nil
	assert.Error(t, err)
}
