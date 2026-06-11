package bridge

import (
	"fmt"

	"github.com/semsemyonoff/dwe/internal/core/bridge/shimassets"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// Bridge prepare hook (design D8/D6). Every command that performs
// `compose up` (deploy run, whole-stack run/restart, services --apply via
// their deploy/restart legs) calls Prepare AFTER preflight and the project
// locks, BEFORE compose args are built. The overlay step ALWAYS runs — it
// must delete a stale overlay when bridge got fully disabled, or compose
// would keep consuming a fragment for services that no longer define an
// image. Shim materialization and the daemon step run only when at least one
// enabled service has bridge enabled.

// PrepareOptions configures one Prepare call. BaseDir and Cfg are required.
type PrepareOptions struct {
	// BaseDir is the absolute project root (the directory of workspace.yml).
	BaseDir string
	// Cfg is the loaded project config the overlay is generated from.
	Cfg *config.DweConfig
	// DockerBin resolves per-service image architectures for the overlay's
	// shim mounts (config.DockerBin). Ignored when Arch is set.
	DockerBin string
	// CycleDaemon selects the daemon step: false = Ensure (idempotent),
	// true = Cycle (SIGTERM → ensure — design D6: `dwe deploy run` and
	// whole-stack restart must never keep a daemon from an older dwe build).
	// Cycle REPLACES ensure; it is never run in addition to it.
	CycleDaemon bool
	// Logf receives non-fatal diagnostics (arch fallback warnings); nil
	// discards them.
	Logf func(format string, args ...any)
	// Arch overrides the production DockerArchResolver (tests).
	Arch ArchResolver
	// Spawn overrides daemon process creation (tests — the production spawn
	// detaches a real `dwe bridge daemon`).
	Spawn SpawnFunc
}

// AnyBridgeEnabled reports whether at least one ENABLED service has the host
// bridge enabled — the gate for the daemon-touching steps of the prepare
// hook and for the best-effort ensure in `dwe status` (design D6).
func AnyBridgeEnabled(cfg *config.DweConfig) bool {
	for _, name := range config.DeployOrder(cfg, []string{"app", "tool", "infra"}) {
		if cfg.Services[name].BridgeEnabled() {
			return true
		}
	}
	return false
}

// Prepare runs the bridge prepare hook: regenerate-or-delete the compose
// overlay (always), then materialize the shim binaries and ensure (or cycle)
// the daemon — skipped entirely when no enabled service has bridge enabled.
// Any failure is returned to the caller: a stale overlay or a half-prepared
// bridge would break the compose up that follows.
func Prepare(opts PrepareOptions) error {
	arch := opts.Arch
	if arch == nil {
		arch = DockerArchResolver(nil, opts.DockerBin)
	}
	spec := BuildOverlaySpec(opts.BaseDir, opts.Cfg, arch, opts.Logf)
	if _, err := regenerateOverlayFromSpec(opts.BaseDir, spec); err != nil {
		return err
	}
	if len(spec.Services) == 0 {
		return nil
	}

	if _, err := shimassets.Materialize(opts.BaseDir); err != nil {
		return fmt.Errorf("bridge: materializing shim binaries: %w", err)
	}

	ecfg := EnsureConfig{ProjectRoot: opts.BaseDir, Spawn: opts.Spawn}
	daemonStep := Ensure
	if opts.CycleDaemon {
		daemonStep = Cycle
	}
	if _, err := daemonStep(ecfg); err != nil {
		return err
	}
	return nil
}
