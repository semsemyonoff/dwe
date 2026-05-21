package builtin

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"devbox-cli/internal/config"
)

type dockerRemoveProjectVolumesBuiltin struct{}

func (dockerRemoveProjectVolumesBuiltin) Validate(with map[string]any) error {
	return nil
}

func (dockerRemoveProjectVolumesBuiltin) Describe(with map[string]any) string {
	return "builtin: docker_remove_project_volumes()"
}

func (dockerRemoveProjectVolumesBuiltin) Run(ctx context.Context, with map[string]any, ectx ExecContext) error {
	// Use the pre-loaded docker config from ExecContext; callers normalise
	// os.ErrNotExist to &config.DockerConfig{} so we never load it here.
	dockerCfg := ectx.DockerConfig
	if dockerCfg == nil {
		dockerCfg = &config.DockerConfig{}
	}
	projectName := dockerCfg.ProjectName
	if projectName == "" {
		return fmt.Errorf("could not resolve project name — cannot remove volumes safely")
	}

	dockerBin := config.DockerBin(ectx.Config)

	// List all volumes.
	out, err := exec.CommandContext(ctx, dockerBin, "volume", "ls", "-q").Output() //nolint:gosec
	if err != nil {
		return fmt.Errorf("listing docker volumes: %w", err)
	}

	prefix := projectName + "_"
	var toRemove []string
	for vol := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		vol = strings.TrimSpace(vol)
		if vol != "" && strings.HasPrefix(vol, prefix) {
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
	args := append([]string{"volume", "rm"}, toRemove...)
	cmd := exec.CommandContext(ctx, dockerBin, args...) //nolint:gosec
	cmdOut := ectx.Output.Writer()
	cmd.Stdout = cmdOut
	cmd.Stderr = cmdOut
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker volume rm: %w", err)
	}
	return nil
}
