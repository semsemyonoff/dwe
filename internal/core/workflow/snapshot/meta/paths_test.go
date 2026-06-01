package meta

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

func TestSnapshotsDir(t *testing.T) {
	base := "/proj"
	if runtime.GOOS == "windows" {
		base = `C:\proj`
	}

	t.Run("nil-cfg-defaults-relative", func(t *testing.T) {
		got := SnapshotsDir(base, nil)
		want := filepath.Join(base, "snapshots")
		if got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})
	t.Run("empty-cfg-defaults-relative", func(t *testing.T) {
		got := SnapshotsDir(base, &config.SnapshotConfig{})
		want := filepath.Join(base, "snapshots")
		if got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})
	t.Run("relative-overridden", func(t *testing.T) {
		got := SnapshotsDir(base, &config.SnapshotConfig{Dir: "var/snaps"})
		want := filepath.Join(base, "var/snaps")
		if got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})
	t.Run("absolute-not-joined", func(t *testing.T) {
		abs := "/tmp/snaps"
		if runtime.GOOS == "windows" {
			abs = `C:\tmp\snaps`
		}
		got := SnapshotsDir(base, &config.SnapshotConfig{Dir: abs})
		if got != filepath.Clean(abs) {
			t.Fatalf("got %q want %q", got, abs)
		}
	})
}

func TestSnapshotPaths(t *testing.T) {
	base := t.TempDir()
	if got, want := CurrentPointer(base), filepath.Join(base, ".dwe/snapshots/current"); got != want {
		t.Errorf("CurrentPointer: got %q want %q", got, want)
	}
	if got, want := LockPath(base), filepath.Join(base, ".dwe/snapshots/snapshot.lock"); got != want {
		t.Errorf("LockPath: got %q want %q", got, want)
	}
	if got, want := PreRestoreBackup(base), filepath.Join(base, ".dwe/snapshots/.pre-restore-backup"); got != want {
		t.Errorf("PreRestoreBackup: got %q want %q", got, want)
	}
	if got, want := SnapshotDir(base, nil, "foo"), filepath.Join(base, "snapshots/foo"); got != want {
		t.Errorf("SnapshotDir: got %q want %q", got, want)
	}
	if got, want := ManifestPath(base, nil, "foo"), filepath.Join(base, "snapshots/foo/manifest.yml"); got != want {
		t.Errorf("ManifestPath: got %q want %q", got, want)
	}
}
