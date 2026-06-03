package promptcache

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

type cacheFile struct {
	UpdatedAt time.Time `yaml:"updated_at"`
	State     string    `yaml:"state"`
}

func readCacheFile(t *testing.T, root string) cacheFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".dwe", "prompt-cache.yml"))
	if err != nil {
		t.Fatalf("read cache file: %v", err)
	}
	var got cacheFile
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode cache yaml: %v", err)
	}
	return got
}

func TestWrite_CreatesFile_WithRightShape(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".dwe"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC().Add(-time.Second)
	if err := Write(root, StateRunning); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := readCacheFile(t, root)
	if got.State != "running" {
		t.Errorf("state = %q, want %q", got.State, "running")
	}
	if got.UpdatedAt.Before(before) {
		t.Errorf("updated_at = %v, want >= %v", got.UpdatedAt, before)
	}
	if got.UpdatedAt.Location() != time.UTC {
		t.Errorf("updated_at location = %v, want UTC", got.UpdatedAt.Location())
	}
}

func TestWrite_CreatesDweDirIfMissing(t *testing.T) {
	root := t.TempDir()
	if _, err := os.Stat(filepath.Join(root, ".dwe")); !os.IsNotExist(err) {
		t.Fatalf("expected .dwe missing, got err=%v", err)
	}
	if err := Write(root, StateStopped); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".dwe", "prompt-cache.yml")); err != nil {
		t.Fatalf("cache file not created: %v", err)
	}
}

func TestWrite_RejectsInvalidState(t *testing.T) {
	root := t.TempDir()
	cases := []string{"", "unknown", "RUNNING", "stoped"}
	for _, s := range cases {
		t.Run(s, func(t *testing.T) {
			if err := Write(root, s); err == nil {
				t.Fatalf("Write(%q) = nil, want error", s)
			}
			if _, err := os.Stat(filepath.Join(root, ".dwe", "prompt-cache.yml")); !os.IsNotExist(err) {
				t.Fatalf("cache file created for invalid state %q (err=%v)", s, err)
			}
		})
	}
}

func TestWrite_AllValidStates(t *testing.T) {
	for _, s := range []string{StateRunning, StatePartial, StateStopped} {
		t.Run(s, func(t *testing.T) {
			root := t.TempDir()
			if err := Write(root, s); err != nil {
				t.Fatalf("Write(%q): %v", s, err)
			}
			got := readCacheFile(t, root)
			if got.State != s {
				t.Errorf("state = %q, want %q", got.State, s)
			}
		})
	}
}

func TestWrite_Atomic_OnRenameFailure_OriginalUntouched(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission semantics")
	}
	if os.Geteuid() == 0 {
		t.Skip("requires non-root: chmod 0500 is bypassed by root")
	}
	root := t.TempDir()
	dweDir := filepath.Join(root, ".dwe")
	if err := os.MkdirAll(dweDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-seed an existing cache file.
	if err := Write(root, StateRunning); err != nil {
		t.Fatalf("seed Write: %v", err)
	}
	original, err := os.ReadFile(filepath.Join(dweDir, "prompt-cache.yml"))
	if err != nil {
		t.Fatal(err)
	}
	// Make .dwe read-only so CreateTemp / Rename fails.
	if err := os.Chmod(dweDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dweDir, 0o755) })

	if err := Write(root, StateStopped); err == nil {
		t.Fatalf("Write expected to fail under read-only .dwe, got nil")
	}
	// Restore perms so we can read.
	if err := os.Chmod(dweDir, 0o755); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(dweDir, "prompt-cache.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Errorf("original cache file modified after failed write:\nbefore=%q\nafter=%q", original, after)
	}
}

func TestWrite_ConcurrentSafeAtomicRename(t *testing.T) {
	root := t.TempDir()
	const n = 10
	states := []string{StateRunning, StatePartial, StateStopped}
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		state := states[i%len(states)]
		go func(s string) {
			defer wg.Done()
			_ = Write(root, s)
		}(state)
	}
	wg.Wait()
	got := readCacheFile(t, root)
	switch got.State {
	case StateRunning, StatePartial, StateStopped:
	default:
		t.Errorf("final state = %q, want one of running/partial/stopped", got.State)
	}
	// Ensure no leftover tmp files clutter .dwe (best-effort: at most a few may
	// remain if a goroutine was interrupted between CreateTemp and Rename, but
	// none should outlive the goroutine in normal execution).
	entries, err := os.ReadDir(filepath.Join(root, ".dwe"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if name == "prompt-cache.yml" {
			continue
		}
		// .tmp leftovers from failed renames are tolerated but should be rare.
		// If they exist, they must not interfere with subsequent reads.
		if filepath.Ext(name) != ".tmp" {
			t.Errorf("unexpected file in .dwe/: %s", name)
		}
	}
}

func TestRemove_DeletesExistingFile(t *testing.T) {
	root := t.TempDir()
	if err := Write(root, StateRunning); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".dwe", "prompt-cache.yml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("seed file missing: %v", err)
	}
	if err := Remove(root); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still present after Remove (err=%v)", err)
	}
}

func TestRemove_IdempotentWhenAbsent(t *testing.T) {
	root := t.TempDir()
	if err := Remove(root); err != nil {
		t.Fatalf("Remove on absent file: %v", err)
	}
	// Call again — still nil.
	if err := Remove(root); err != nil {
		t.Fatalf("second Remove on absent file: %v", err)
	}
}

func TestRemove_DotDweMissing_ReturnsNil(t *testing.T) {
	root := t.TempDir()
	if _, err := os.Stat(filepath.Join(root, ".dwe")); !os.IsNotExist(err) {
		t.Fatalf("expected .dwe missing, got err=%v", err)
	}
	if err := Remove(root); err != nil {
		t.Fatalf("Remove with no .dwe: %v", err)
	}
}
