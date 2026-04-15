package builtin

import (
	"fmt"
	"os"
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

func (dockerRemoveProjectVolumesBuiltin) Run(with map[string]any, ctx ExecContext) error {
	dockerCfg, err := config.LoadDockerConfig(ctx.ProjectRoot, ctx.Config)
	if err != nil {
		return fmt.Errorf("loading docker config: %w", err)
	}
	projectName := dockerCfg.ProjectName
	if projectName == "" {
		return fmt.Errorf("could not resolve project name — cannot remove volumes safely")
	}

	// List all volumes.
	out, err := exec.Command("docker", "volume", "ls", "-q").Output()
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
		ctx.Output.Info(fmt.Sprintf("no volumes found with prefix %q", prefix))
		return nil
	}

	ctx.Output.Info(fmt.Sprintf("removing %d volume(s) with prefix %q", len(toRemove), prefix))
	args := append([]string{"volume", "rm"}, toRemove...)
	cmd := exec.Command("docker", args...) //nolint:gosec
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker volume rm: %w", err)
	}
	return nil
}
