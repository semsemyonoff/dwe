package containers

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// listVolumesFn / removeVolumeFn are test seams: production shells out to
// `docker volume ls -q` / `docker volume rm <vol>`; tests inject stubs to
// capture invocations and feed fake volume listings without spawning real
// subprocesses.
var (
	listVolumesFn  = listDockerVolumes
	removeVolumeFn = removeDockerVolume
)

// RemoveProjectVolumes implements docker_remove_project_volumes.
type RemoveProjectVolumes struct{}

// Validate checks that the with parameters are valid before the pipeline runs.
func (RemoveProjectVolumes) Validate(with map[string]any) error {
	return nil
}

// Describe returns a short human-readable description used in plan output.
func (RemoveProjectVolumes) Describe(with map[string]any) string {
	return "builtin: docker_remove_project_volumes()"
}

// Run executes the docker_remove_project_volumes builtin.
//
// Failure model: project-name resolution and volume LISTING are fatal (they
// indicate the step cannot run correctly, so the caller — e.g. reset — should
// abort rather than silently skip cleanup). Individual `docker volume rm`
// failures are best-effort: a volume that cannot be dropped (still in use, etc.)
// is reported and skipped so it never aborts the surrounding pipeline. This is
// why the default reset pipeline does NOT wrap this step in continue_on_error —
// that would also swallow the fatal listing/resolution errors.
func (RemoveProjectVolumes) Run(ctx context.Context, with map[string]any, ectx spec.ExecContext) error {
	if ectx.Config == nil {
		return fmt.Errorf("docker_remove_project_volumes: config not available")
	}

	// Resolve the compose project name through the single source of truth
	// (ResolveComposeProjectName) so a project without docker.yml — or with no
	// project_name field — falls back to the default "<prefix>-<name>", exactly
	// like `docker compose -p` and per-service reset. Reading the raw
	// DockerConfig.ProjectName here would hard-fail on those projects.
	projectName, err := config.ResolveComposeProjectName(ectx.ProjectRoot, ectx.Config)
	if err != nil {
		return fmt.Errorf("resolve project name: %w", err)
	}
	if projectName == "" {
		return fmt.Errorf("could not resolve project name — cannot remove volumes safely")
	}

	dockerBin := config.DockerBin(ectx.Config)

	removed, failed, err := RemoveVolumesByProjectPrefix(ctx, dockerBin, projectName, func(format string, args ...any) {
		ectx.Output.Error(fmt.Sprintf(format, args...))
	})
	if err != nil {
		return err
	}

	prefix := projectName + "_"
	total := len(removed) + len(failed)
	if total == 0 {
		ectx.Output.Info(fmt.Sprintf("no volumes found with prefix %q", prefix))
		return nil
	}

	ectx.Output.Info(fmt.Sprintf("removing %d volume(s) with prefix %q", total, prefix))

	if len(removed) > 0 {
		ectx.Output.Success(fmt.Sprintf("removed %d volume(s)", len(removed)))
	}
	if len(failed) > 0 {
		ectx.Output.Info(fmt.Sprintf("%d volume(s) left in place: %s", len(failed), strings.Join(failed, ", ")))
	}
	return nil
}

// RemoveVolumesByProjectPrefix removes every docker volume whose name carries
// the prefix "<projectName>_" (volumes without the prefix, e.g. `shared:`
// volumes, survive). Listing failure is fatal (returned as err, so a caller
// like reset or test teardown can abort rather than proceed blind); a
// per-volume removal failure is best-effort — it is reported via logf (nil is
// a valid no-op) and recorded in failed, but never aborts the batch. removed
// and failed are returned so callers can build their own summary/accounting
// on top (e.g. the builtin's Info/Success lines, or a teardown log line).
func RemoveVolumesByProjectPrefix(ctx context.Context, dockerBin, projectName string, logf func(format string, args ...any)) (removed, failed []string, err error) {
	prefix := projectName + "_"

	all, err := listVolumesFn(ctx, dockerBin)
	if err != nil {
		return nil, nil, fmt.Errorf("listing docker volumes: %w", err)
	}

	var toRemove []string
	for _, vol := range all {
		if strings.HasPrefix(vol, prefix) {
			toRemove = append(toRemove, vol)
		}
	}

	for _, vol := range toRemove {
		if err := ctx.Err(); err != nil {
			return removed, failed, err
		}
		if rmErr := removeVolumeFn(ctx, dockerBin, vol); rmErr != nil {
			failed = append(failed, vol)
			if logf != nil {
				logf("could not remove volume %q: %v", vol, rmErr)
			}
			continue
		}
		removed = append(removed, vol)
	}
	return removed, failed, nil
}

// listDockerVolumes returns every docker volume name (`docker volume ls -q`),
// with blank lines trimmed out.
func listDockerVolumes(ctx context.Context, dockerBin string) ([]string, error) {
	out, err := exec.CommandContext(ctx, dockerBin, "volume", "ls", "-q").Output() //nolint:gosec
	if err != nil {
		return nil, err
	}
	var names []string
	for vol := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if vol = strings.TrimSpace(vol); vol != "" {
			names = append(names, vol)
		}
	}
	return names, nil
}

// removeDockerVolume removes a single docker volume (`docker volume rm <vol>`).
// On failure it returns an error carrying docker's stderr so the caller can
// report exactly why the volume could not be removed.
func removeDockerVolume(ctx context.Context, dockerBin, vol string) error {
	out, err := exec.CommandContext(ctx, dockerBin, "volume", "rm", vol).CombinedOutput() //nolint:gosec
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}
