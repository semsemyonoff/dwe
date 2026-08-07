package envtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/bridge"
	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/containers"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
)

// TeardownDeps holds the external actions Teardown drives, one seam per step,
// so tests can record calls and ordering without touching the real Docker
// daemon, bridge daemon, or filesystem. NewTeardownDeps wires the real
// implementations; tests construct their own recording TeardownDeps directly.
type TeardownDeps struct {
	// ComposeDown runs `docker compose down` (never -v) for the copy at
	// m.CopyPath. skipped=true means the copy's compose file set could not be
	// built (copy already gone, or its config no longer loads) — Teardown
	// reports this via warn and still runs every later step (manifest-driven
	// resilience, spec §6).
	ComposeDown func(ctx context.Context, m *Manifest) (skipped bool, err error)

	// ReapContainers removes every container labelled
	// com.docker.compose.project=<m.ComposeProject> (docker ps -aq -a, then
	// docker rm -f each) — exact identity, never a name-pattern guess.
	ReapContainers func(ctx context.Context, m *Manifest) error

	// RemoveVolumes removes every volume prefixed "<m.ComposeProject>_"
	// (shared, unprefixed volumes survive).
	RemoveVolumes func(ctx context.Context, m *Manifest) error

	// StopBridge stops the copy's bridge daemon (m.BridgeDir).
	StopBridge func(m *Manifest) error

	// RemoveCopy deletes the disposable copy directory tree (m.CopyPath).
	RemoveCopy func(m *Manifest) error

	// DeleteManifest removes the manifest file itself. Bound to the concrete
	// manifest path at construction time (Manifest carries no path of its own).
	DeleteManifest func(m *Manifest) error
}

// NewTeardownDeps builds the real TeardownDeps for a scenario run's manifest
// at manifestPath. Compose-down subprocess output is streamed to log (the
// run log); pass nil to discard it.
func NewTeardownDeps(manifestPath string, log io.Writer) TeardownDeps {
	return TeardownDeps{
		ComposeDown:    func(ctx context.Context, m *Manifest) (bool, error) { return composeDownReal(ctx, m, log) },
		ReapContainers: reapContainersReal,
		RemoveVolumes:  removeVolumesReal,
		StopBridge:     stopBridgeReal,
		RemoveCopy:     removeCopyReal,
		DeleteManifest: func(m *Manifest) error { return DeleteManifest(manifestPath) },
	}
}

// Teardown runs the spec §6 teardown sequence for a completed or aborted
// scenario run, driven ONLY by m and the copy's own contents — never the
// original scenario definition — so a half-dead run is still sweepable from
// the manifest alone (this is also what `dwe test clean` reuses).
//
// Order: compose down -> reap containers by exact label -> remove volumes by
// exact project prefix -> stop the bridge daemon -> remove the copy -> delete
// the manifest. Volumes are removed AFTER containers are reaped (spec §6
// lists the reverse order; an in-use volume cannot be removed, so the swap is
// an implementation-level fix that preserves every spec guarantee).
//
// Every step is best-effort: a failure is reported via warn and joined into
// the returned error, but every later step still runs. Callers MUST pass a
// fresh context — never the scenario's expired deadline, since teardown must
// still run after a timeout or Ctrl+C.
func Teardown(ctx context.Context, m *Manifest, deps TeardownDeps, warn func(string)) error {
	if m == nil {
		return fmt.Errorf("envtest: cannot tear down nil manifest")
	}
	if warn == nil {
		warn = func(string) {}
	}

	var errs []error
	fail := func(step string, err error) {
		warn(fmt.Sprintf("teardown: %s: %v", step, err))
		errs = append(errs, fmt.Errorf("%s: %w", step, err))
	}

	if deps.ComposeDown != nil {
		if skipped, err := deps.ComposeDown(ctx, m); skipped {
			warn(fmt.Sprintf("teardown: skipping compose down: %v", err))
		} else if err != nil {
			fail("compose down", err)
		}
	}

	if deps.ReapContainers != nil {
		if err := deps.ReapContainers(ctx, m); err != nil {
			fail("reap containers", err)
		}
	}

	if deps.RemoveVolumes != nil {
		if err := deps.RemoveVolumes(ctx, m); err != nil {
			fail("remove volumes", err)
		}
	}

	if deps.StopBridge != nil {
		if err := deps.StopBridge(m); err != nil {
			fail("stop bridge daemon", err)
		}
	}

	if deps.RemoveCopy != nil {
		if err := deps.RemoveCopy(m); err != nil {
			fail("remove copy", err)
		}
	}

	if deps.DeleteManifest != nil {
		if err := deps.DeleteManifest(m); err != nil {
			fail("delete manifest", err)
		}
	}

	return errors.Join(errs...)
}

// loadCopyConfig loads the disposable copy's own config (workspace.yml +
// defaults.yml + local.yml, exactly like any other dwe command). Failure here
// is what drives the compose-down degradation: a removed copy or a config
// that no longer loads means there is no compose file set to build.
func loadCopyConfig(copyPath string) (*config.DweConfig, *config.DockerConfig, error) {
	cfg, err := config.LoadConfig(filepath.Join(copyPath, "workspace.yml"))
	if err != nil {
		return nil, nil, fmt.Errorf("loading copy config: %w", err)
	}
	dockerCfg, err := config.LoadDockerConfigOrEmpty(copyPath, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("loading copy docker config: %w", err)
	}
	return cfg, dockerCfg, nil
}

// dockerBinForCopy resolves the docker binary override from the copy's own
// config, falling back to the plain "docker" default when the copy's config
// no longer loads (degradation — containers/volumes may still exist under
// the manifest-recorded project name even though the copy is gone).
func dockerBinForCopy(copyPath string) string {
	cfg, _, err := loadCopyConfig(copyPath)
	if err != nil {
		return config.DockerBin(nil)
	}
	return config.DockerBin(cfg)
}

// composeDownReal builds and runs `docker compose down` for the copy at
// m.CopyPath. It NEVER appends -v/--volumes (the default down args already
// carry --remove-orphans; teardown removes volumes itself, by exact project
// prefix, in a later step). skipped=true means the copy's compose file set
// could not be built at all.
func composeDownReal(ctx context.Context, m *Manifest, log io.Writer) (skipped bool, err error) {
	cfg, dockerCfg, err := loadCopyConfig(m.CopyPath)
	if err != nil {
		return true, err
	}

	compose := docker.NewCompose(cfg, dockerCfg, m.CopyPath)
	// BuildInternalArgs (not BuildArgs) so the copy's docker.yml args.down
	// policy is bypassed entirely: a project that sets args.down: ["-v"] must
	// NOT be able to turn teardown into `compose down -v` and delete shared
	// named volumes before the prefix-scoped volume cleanup runs. Teardown is
	// always exactly `compose down --remove-orphans`, never -v.
	args := compose.BuildInternalArgs("down", "--remove-orphans")
	if err := runComposeDownFn(ctx, compose.BinName(), args, compose.BuildEnv(), m.CopyPath, log); err != nil {
		return false, fmt.Errorf("docker compose down: %w", err)
	}
	return false, nil
}

// runComposeDownProcess is the real subprocess runner behind runComposeDownFn.
// Deliberately NOT Compose.Exec: that method has no context parameter (a hung
// `down` would ignore the teardown deadline) and hard-wires os.Stdout/Stderr/
// Stdin, which would leak compose output into JSON stdout.
func runComposeDownProcess(ctx context.Context, bin string, args, env []string, dir string, log io.Writer) error {
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec
	cmd.Dir = dir
	cmd.Env = env
	if log != nil {
		cmd.Stdout = log
		cmd.Stderr = log
	}
	return cmd.Run()
}

// runComposeDownFn is a test seam over runComposeDownProcess: production runs
// the real subprocess; tests inject a recording stub to assert on the built
// args (e.g. never -v) without touching Docker.
var runComposeDownFn = runComposeDownProcess

// reapContainersReal removes every container labelled with the manifest's
// exact compose project name — the identity dwe itself created, never a
// name-pattern guess. -a is required: exited/one-off containers still hold
// volume references, so a running-only listing would miss them and volume
// removal (the next step) would then fail on those volumes.
func reapContainersReal(ctx context.Context, m *Manifest) error {
	dockerBin := dockerBinForCopy(m.CopyPath)
	filterArg := fmt.Sprintf("label=%s=%s", docker.ComposeProjectLabel, m.ComposeProject)

	ids, err := listContainersFn(ctx, dockerBin, []string{"ps", "-aq", "--filter", filterArg})
	if err != nil {
		return fmt.Errorf("listing containers: %w", err)
	}

	var errs []error
	for _, id := range ids {
		if id == "" {
			continue
		}
		if err := removeContainerFn(ctx, dockerBin, id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// listContainersByLabel is the real subprocess runner behind listContainersFn.
func listContainersByLabel(ctx context.Context, dockerBin string, args []string) ([]string, error) {
	out, err := exec.CommandContext(ctx, dockerBin, args...).Output() //nolint:gosec
	if err != nil {
		return nil, err
	}
	var ids []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if id := strings.TrimSpace(line); id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// listContainersFn / removeContainerFn are test seams: production shells out
// to `docker ps` / `docker rm -f <id>`; tests inject stubs to assert on the
// recorded args (the exact label filter, -a) without spawning real processes.
var (
	listContainersFn  = listContainersByLabel
	removeContainerFn = docker.RemoveContainer
)

// removeVolumesReal removes every volume prefixed "<m.ComposeProject>_" via
// the shared extracted helper (Task 5) — shared, unprefixed volumes survive.
func removeVolumesReal(ctx context.Context, m *Manifest) error {
	dockerBin := dockerBinForCopy(m.CopyPath)
	_, _, err := containers.RemoveVolumesByProjectPrefix(ctx, dockerBin, m.ComposeProject, nil)
	return err
}

// stopBridgeReal stops the copy's bridge daemon, if any. A missing/stale
// pidfile is a clean no-op (bridge.StopDaemon's own contract).
func stopBridgeReal(m *Manifest) error {
	_, err := bridge.StopDaemon(m.BridgeDir)
	return err
}

// removeCopyReal deletes the disposable copy directory tree.
func removeCopyReal(m *Manifest) error {
	if err := os.RemoveAll(m.CopyPath); err != nil {
		return fmt.Errorf("removing copy: %w", err)
	}
	return nil
}
