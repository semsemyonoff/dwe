// Package snapshot provides validators for the snapshot subsystem:
// devbox/snapshot.yml shape and template scope, plus per-snapshot
// integrity checks against the on-disk manifest / artifacts.
package snapshot

import (
	"path/filepath"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/core/validate"
	coresnap "github.com/semsemyonoff/dwe/internal/core/workflow/snapshot"
)

// All returns the snapshot domain validators.
//
// snapCfg / snapCfgErr come from a single LoadSnapshotConfig call performed by
// the caller (runValidate). When snapCfg is nil and snapCfgErr is nil the
// snapshot.yml file is simply absent — every validator self-skips silently.
//
// verifyChecksums toggles the per-snapshot sha256 verification (off by default
// because it can be slow for multi-GB dumps; the `--verify` flag on
// `devbox validate snapshot` flips it on).
func All(cfg *config.DweConfig, snapCfg *config.SnapshotConfig, snapCfgErr error, baseDir string, _ *registry.Registry, verifyChecksums bool) []validate.Validator {
	out := []validate.Validator{
		&configLoadableValidator{err: snapCfgErr},
		&createDefinedValidator{cfg: snapCfg},
		&restoreDefinedValidator{cfg: snapCfg},
		&variantPairingValidator{cfg: snapCfg},
		&rollbackTargetExistsValidator{cfg: snapCfg, baseDir: baseDir},
		&templateScopeValidator{cfg: snapCfg},
	}
	// Per-snapshot validators: discovered by listing the snapshots directory.
	// Errors here are tolerated — ListSnapshots returns nil on missing dir.
	entries, _ := coresnap.ListSnapshots(baseDir, snapCfg)
	for i := range entries {
		name := filepath.Base(entries[i].Dir)
		out = append(out, &perSnapshotValidator{
			baseDir:         baseDir,
			cfg:             snapCfg,
			name:            name,
			entry:           entries[i],
			verifyChecksums: verifyChecksums,
		})
		out = append(out, &servicesDiffValidator{
			name:  name,
			entry: entries[i],
			cfg:   cfg,
		})
	}
	return out
}
