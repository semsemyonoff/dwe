package snapshot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/workflow/snapshot/meta"
)

// Entry is one element returned by ListSnapshots: the loaded manifest plus
// the on-disk directory size in bytes (computed by summing artifact sizes
// from the manifest — not a fresh disk walk; rendering callers want a
// quick header summary, not an integrity scan).
type Entry struct {
	Manifest *meta.Manifest
	// Dir is the absolute filesystem path of the snapshot directory.
	Dir string
	// TotalSize is the sum of Manifest.Artifacts[*].Size; it ignores the
	// captured devbox/ subtree (which is small and identical in shape).
	TotalSize int64
}

// ListSnapshots enumerates snapshots under SnapshotsDir(baseDir, cfg).
//
// Directories without a readable manifest.yml are skipped with an entry whose
// Manifest is nil and TotalSize is zero so callers can render a row marked
// "corrupt" without aborting the whole listing. The slice is sorted by
// CreatedAt descending (most recent first) and then by name for ties; entries
// with a nil manifest sort last.
//
// When the snapshots directory itself does not exist, the result is an empty
// slice with no error — a project that has never run snapshot create is a
// valid state.
func ListSnapshots(baseDir string, cfg *config.SnapshotConfig) ([]Entry, error) {
	root := meta.SnapshotsDir(baseDir, cfg)
	dents, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read snapshots dir: %w", err)
	}
	var out []Entry
	for _, d := range dents {
		if !d.IsDir() {
			continue
		}
		name := d.Name()
		// Skip hidden / staging directories (e.g. transient unpack staging).
		if len(name) > 0 && name[0] == '.' {
			continue
		}
		dir := filepath.Join(root, name)
		manifestPath := filepath.Join(dir, meta.ManifestFileName)
		m, mErr := meta.LoadManifest(manifestPath)
		entry := Entry{Dir: dir}
		if mErr == nil {
			entry.Manifest = m
			for _, a := range m.Artifacts {
				entry.TotalSize += a.Size
			}
		}
		out = append(out, entry)
	}
	sort.SliceStable(out, func(i, j int) bool {
		mi, mj := out[i].Manifest, out[j].Manifest
		switch {
		case mi == nil && mj == nil:
			return filepath.Base(out[i].Dir) < filepath.Base(out[j].Dir)
		case mi == nil:
			return false
		case mj == nil:
			return true
		case mi.CreatedAt.Equal(mj.CreatedAt):
			return mi.Name < mj.Name
		default:
			return mi.CreatedAt.After(mj.CreatedAt)
		}
	})
	return out, nil
}
