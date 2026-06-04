package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
)

// symlinkFn is the indirection point for os.Symlink so the copy-fallback path can
// be exercised in tests without depending on the host's symlink support (Windows
// without developer mode, restrictive filesystems, etc.). Production code uses the
// real os.Symlink; tests override it to force the fallback.
var symlinkFn = os.Symlink

// linkClaudeMd makes CLAUDE.md inside dir point at the sibling AGENTS.md. It first
// attempts a relative symlink (CLAUDE.md → AGENTS.md, matching this repo's own
// convention); if the symlink cannot be created — the platform does not support
// symlinks, or CLAUDE.md already exists — it falls back to writing CLAUDE.md as a
// verbatim copy of AGENTS.md and returns fallback=true.
//
// AGENTS.md is the canonical file and must already exist in dir; it is read for the
// copy fallback. linkClaudeMd never overwrites an existing CLAUDE.md: a pre-existing
// target is treated as a satisfied fill-gaps result (fallback=false, no error).
func linkClaudeMd(dir string) (fallback bool, err error) {
	claudePath := filepath.Join(dir, "CLAUDE.md")
	if _, statErr := os.Stat(claudePath); statErr == nil {
		// CLAUDE.md already present (symlink or copy from a prior run): leave it be.
		return false, nil
	} else if !os.IsNotExist(statErr) {
		return false, fmt.Errorf("scaffold: stat %s: %w", claudePath, statErr)
	}

	// The symlink target is relative so the link survives the project being moved.
	if linkErr := symlinkFn("AGENTS.md", claudePath); linkErr == nil {
		return false, nil
	}

	// Symlink failed — write CLAUDE.md as a verbatim copy of AGENTS.md instead.
	agentsPath := filepath.Join(dir, "AGENTS.md")
	data, readErr := os.ReadFile(agentsPath)
	if readErr != nil {
		return false, fmt.Errorf("scaffold: read %s for copy fallback: %w", agentsPath, readErr)
	}
	if _, writeErr := writeFile(claudePath, data, false); writeErr != nil {
		return false, fmt.Errorf("scaffold: write CLAUDE.md copy fallback: %w", writeErr)
	}
	return true, nil
}
