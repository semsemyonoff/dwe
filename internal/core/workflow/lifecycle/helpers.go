package lifecycle

import (
	"log/slog"
	"path/filepath"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
)

// loadRegistryWithVisibility loads the user-command registry from configPath and
// applies hide: visibility against cfg so the workflow runner's Hidden-target
// skip fires consistently for commands invoked from lifecycle phases.
//
// It is nil-tolerant by design: on load failure it returns (nil, err) so the
// caller can run preflight with a nil registry and surface the deferred error
// later, preserving each entry point's regErr-vs-lock ordering. Visibility eval
// failures are swallowed (fail-open — the command stays visible).
func loadRegistryWithVisibility(configPath string, cfg *config.DweConfig, workDir string) (*usercommands.Registry, error) {
	reg, regErr := usercommands.LoadRegistryFromConfigPath(configPath)
	if regErr != nil {
		return nil, regErr
	}
	if reg != nil {
		_ = reg.ApplyVisibility(cfg, workDir)
	}
	return reg, nil
}

// clearPendingRestart clears any pending restart entry from the deploy journal,
// logging a warning (with the caller-supplied context message) on failure.
// Best-effort: a clear failure never aborts the lifecycle operation.
func clearPendingRestart(workDir, warnMsg string) {
	statePath := filepath.Join(workDir, journal.DefaultRelPath)
	if clearErr := journal.ClearPendingForKind(statePath, journal.PendingRestart); clearErr != nil {
		slog.Warn(warnMsg, "err", clearErr)
	}
}
