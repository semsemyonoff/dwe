package containers

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// listVolumesFn / removeVolumesFn are test seams: production shells out to
// `docker volume ls -q` / `docker volume rm`; tests inject stubs to capture
// invocations and feed fake volume listings without spawning real subprocesses.
var (
	listVolumesFn   = listDockerVolumes
	removeVolumesFn = removeDockerVolumes
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

	// List all volumes.
	all, err := listVolumesFn(ctx, dockerBin)
	if err != nil {
		return fmt.Errorf("listing docker volumes: %w", err)
	}

	prefix := projectName + "_"
	var toRemove []string
	for _, vol := range all {
		if strings.HasPrefix(vol, prefix) {
			toRemove = append(toRemove, vol)
		}
	}

	if len(toRemove) == 0 {
		ectx.Output.Info(fmt.Sprintf("no volumes found with prefix %q", prefix))
		return nil
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	ectx.Output.Info(fmt.Sprintf("removing %d volume(s) with prefix %q", len(toRemove), prefix))
	if err := removeVolumesFn(ctx, dockerBin, toRemove, ectx.Output.Writer()); err != nil {
		return fmt.Errorf("docker volume rm: %w", err)
	}
	return nil
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

// removeDockerVolumes removes the given volumes (`docker volume rm <vols...>`),
// streaming docker's output to w.
func removeDockerVolumes(ctx context.Context, dockerBin string, vols []string, w io.Writer) error {
	args := append([]string{"volume", "rm"}, vols...)
	cmd := exec.CommandContext(ctx, dockerBin, args...) //nolint:gosec
	cmd.Stdout = w
	cmd.Stderr = w
	return cmd.Run()
}
