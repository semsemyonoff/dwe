package stack

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/docker"
	"devbox-cli/internal/ui"
)

// FetchComposeTopology runs `docker compose config` with the given compose files
// and parses the service dependency graph. Returns nil on any error (docker not
// available, no compose files, etc.) so callers can degrade gracefully.
// processEnv is applied to the docker process so that DOCKER_HOST / DOCKER_CONTEXT
// overrides from docker.yml process_env are honoured.
// bin is the Docker-compatible binary (e.g. "docker", "podman"); pass
// config.DockerBin(cfg) at the call site.
func FetchComposeTopology(composeFiles []string, projectName string, processEnv []string, bin string) map[string][]string {
	if len(composeFiles) == 0 {
		return nil
	}
	args := BuildComposeArgs(projectName, composeFiles, "config")
	cmd := exec.Command(bin, args...) //nolint:gosec
	cmd.Env = processEnv
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	deps, err := ui.ParseComposeTopology(out)
	if err != nil {
		return nil
	}
	return deps
}

// ParseTopologyFromFiles builds a dependency map by reading and parsing compose
// YAML files directly, without invoking docker. Used as a fallback when docker
// is not available. Each file is parsed independently; services are merged across files.
func ParseTopologyFromFiles(composeFiles []string) map[string][]string {
	if len(composeFiles) == 0 {
		return nil
	}
	result := make(map[string][]string)
	for _, f := range composeFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		deps, err := ui.ParseComposeTopology(data)
		if err != nil {
			continue
		}
		// Later files replace earlier deps for the same service, matching
		// Docker Compose override semantics (depends_on is not additive
		// across files; a later file can clear or replace it).
		maps.Copy(result, deps)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// ComposeNodeStatuses runs `docker compose ps` with the given compose files and
// returns a map of compose service name → NodeStatus. Returns nil on any error.
// processEnv is applied to the docker process so that DOCKER_HOST / DOCKER_CONTEXT
// overrides from docker.yml process_env are honoured.
// bin is the Docker-compatible binary (e.g. "docker", "podman"); pass
// config.DockerBin(cfg) at the call site.
func ComposeNodeStatuses(composeFiles []string, projectName string, processEnv []string, bin string) map[string]ui.NodeStatus {
	if len(composeFiles) == 0 {
		return nil
	}
	runningArgs := BuildComposeArgs(projectName, composeFiles, "ps", "--format", "{{.Service}}", "--filter", "status=running")
	runningCmd := exec.Command(bin, runningArgs...) //nolint:gosec
	runningCmd.Env = processEnv
	runningOut, err := runningCmd.Output()
	if err != nil {
		return nil
	}

	running := make(map[string]bool)
	for line := range strings.SplitSeq(strings.TrimSpace(string(runningOut)), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			running[s] = true
		}
	}

	allArgs := BuildComposeArgs(projectName, composeFiles, "ps", "--format", "{{.Service}}", "--all")
	allCmd := exec.Command(bin, allArgs...) //nolint:gosec
	allCmd.Env = processEnv
	allOut, err := allCmd.Output()
	if err != nil {
		result := make(map[string]ui.NodeStatus, len(running))
		for name := range running {
			result[name] = ui.NodeRunning
		}
		return result
	}

	result := make(map[string]ui.NodeStatus)
	for line := range strings.SplitSeq(strings.TrimSpace(string(allOut)), "\n") {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		if running[s] {
			result[s] = ui.NodeRunning
		} else {
			result[s] = ui.NodeStopped
		}
	}
	return result
}

// DisabledNodes returns the compose service names for services that
// are neither mandatory nor enabled in the current config.
func DisabledNodes(cfg *config.DevboxConfig) []string {
	var names []string
	for _, svc := range cfg.Services {
		if !svc.Mandatory && !svc.Enabled && svc.Container != "" {
			names = append(names, svc.Container)
		}
	}
	sort.Strings(names)
	return names
}

// AugmentWithDisabled adds disabled services and tools as isolated nodes to topo
// and marks them NodeDisabled in the status map. If topo is nil, it is initialised
// to an empty map so disabled nodes are still shown.
func AugmentWithDisabled(cfg *config.DevboxConfig, topo map[string][]string, topoStatus map[string]ui.NodeStatus) (map[string][]string, map[string]ui.NodeStatus) {
	disabled := DisabledNodes(cfg)
	if len(disabled) == 0 {
		return topo, topoStatus
	}
	if topo == nil {
		topo = make(map[string][]string)
	}
	if topoStatus == nil {
		topoStatus = make(map[string]ui.NodeStatus)
	}
	for _, name := range disabled {
		if _, exists := topo[name]; !exists {
			topo[name] = nil
		}
		topoStatus[name] = ui.NodeDisabled
	}
	return topo, topoStatus
}

// RemoveHiddenNodes removes the listed compose service names from the topology
// graph and status map. Hidden nodes are also pruned from dependency lists so
// they don't leave dangling references in the tree.
func RemoveHiddenNodes(topo map[string][]string, status map[string]ui.NodeStatus, hidden []string) (map[string][]string, map[string]ui.NodeStatus) {
	hide := make(map[string]bool, len(hidden))
	for _, h := range hidden {
		hide[h] = true
	}
	for name := range hide {
		delete(topo, name)
		delete(status, name)
	}
	for name, deps := range topo {
		filtered := make([]string, 0, len(deps))
		for _, d := range deps {
			if !hide[d] {
				filtered = append(filtered, d)
			}
		}
		topo[name] = filtered
	}
	return topo, status
}

// BuildNodeCategories maps compose service names to topology categories
// based on the actual service Type in the devbox config:
//
//   - app   → CatService
//   - tool  → CatTool
//   - infra → CatInfra
//
// Services without a Container are skipped. Unknown types fall through to CatInfra.
func BuildNodeCategories(cfg *config.DevboxConfig) map[string]ui.NodeCategory {
	cats := make(map[string]ui.NodeCategory)
	for _, svc := range cfg.Services {
		if svc.Container == "" {
			continue
		}
		switch svc.Type {
		case config.ServiceTypeApp:
			cats[svc.Container] = ui.CatService
		case config.ServiceTypeTool:
			cats[svc.Container] = ui.CatTool
		case config.ServiceTypeInfra:
			cats[svc.Container] = ui.CatInfra
		default:
			cats[svc.Container] = ui.CatInfra
		}
	}
	return cats
}

// BuildComposeArgs constructs `["compose", ["-p", projectName,] "-f", file..., command, extraArgs...]`.
func BuildComposeArgs(projectName string, composeFiles []string, command string, extraArgs ...string) []string {
	args := []string{"compose"}
	if projectName != "" {
		args = append(args, "-p", projectName)
	}
	for _, f := range composeFiles {
		args = append(args, "-f", f)
	}
	args = append(args, command)
	args = append(args, extraArgs...)
	return args
}

// ResolveProjectAndDocker returns both the compose project name and the full
// docker config. If docker.yml does not exist, project name falls back to the
// config default and dockerCfg is nil (no error).
func ResolveProjectAndDocker(configPath string, cfg *config.DevboxConfig) (string, *config.DockerConfig, error) {
	baseDir := filepath.Dir(configPath)
	dockerCfg, err := config.LoadDockerConfig(baseDir, cfg)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg.Project.FullName(), nil, nil
		}
		return "", nil, fmt.Errorf("loading docker config: %w", err)
	}
	projectName := cfg.Project.FullName()
	if dockerCfg.ProjectName != "" {
		projectName = dockerCfg.ProjectName
	}
	return projectName, dockerCfg, nil
}

// ResolveTopology builds the compose topology graph and per-node status map.
// It queries docker compose where available, falling back to YAML-only parsing
// when docker is unavailable. Disabled nodes are then added as isolated
// entries, and any nodes hidden via docker.yml are removed.
func ResolveTopology(cfg *config.DevboxConfig, dockerCfg *config.DockerConfig, projectName string) (map[string][]string, map[string]ui.NodeStatus) {
	composeFiles := cfg.ComposeFiles()
	var processEnv []string
	if dockerCfg != nil {
		processEnv = docker.MergeEnv(dockerCfg.ProcessEnv)
	}
	dockerBin := config.DockerBin(cfg)

	topo := FetchComposeTopology(composeFiles, projectName, processEnv, dockerBin)
	var topoStatus map[string]ui.NodeStatus
	if topo == nil {
		topo = ParseTopologyFromFiles(composeFiles)
	} else {
		topoStatus = ComposeNodeStatuses(composeFiles, projectName, processEnv, dockerBin)
		if topoStatus != nil {
			for name := range topo {
				if _, ok := topoStatus[name]; !ok {
					topoStatus[name] = ui.NodeStopped
				}
			}
		}
	}

	topo, topoStatus = AugmentWithDisabled(cfg, topo, topoStatus)
	if dockerCfg != nil && len(dockerCfg.Topology.Hidden) > 0 {
		topo, topoStatus = RemoveHiddenNodes(topo, topoStatus, dockerCfg.Topology.Hidden)
	}
	return topo, topoStatus
}
