// Package promptcache provides an atomic-write API for the prompt stack-state
// cache file at <root>/.dwe/prompt-cache.yml. The file is read by the prompt
// hot path in internal/shared/prompt and written by authoritative call sites
// (lifecycle commands, dwe status, dwe service --apply, dwe snapshot restore).
//
// This package is a leaf: it MUST NOT import anything from internal/core. The
// Health → state-string mapping lives in core/project/stack.HealthState; sites
// translate to a string before invoking Write.
package promptcache

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Cache-state strings — part of the on-disk schema at <root>/.dwe/prompt-cache.yml.
const (
	StateRunning = "running"
	StatePartial = "partial"
	StateStopped = "stopped"
)

const cacheRelPath = ".dwe/prompt-cache.yml"

// Write atomically updates <root>/.dwe/prompt-cache.yml with the given state.
// state MUST be one of StateRunning, StatePartial, StateStopped — invalid
// values return an error and DO NOT write. Best-effort: callers should ignore
// the error rather than failing the command. Creates <root>/.dwe/ if missing.
func Write(root, state string) error {
	switch state {
	case StateRunning, StatePartial, StateStopped:
	default:
		return fmt.Errorf("promptcache: invalid state %q", state)
	}
	path := filepath.Join(root, cacheRelPath)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := "updated_at: " + time.Now().UTC().Format(time.RFC3339) + "\nstate: " + state + "\n"
	tmp, err := os.CreateTemp(dir, "prompt-cache-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// Remove deletes <root>/.dwe/prompt-cache.yml. Idempotent: returns nil when
// the file is already absent. Used by scoped lifecycle commands and snapshot
// restore/rollback to invalidate stale aggregate state.
func Remove(root string) error {
	path := filepath.Join(root, cacheRelPath)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
