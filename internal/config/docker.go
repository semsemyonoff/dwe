package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// DockerConfig holds Docker Compose execution policy.
// Loaded separately from devbox/docker.yml + devbox/docker.local.yml.
type DockerConfig struct {
	// ProjectName is the resolved compose project name.
	ProjectName string `yaml:"project_name"`
	// Args holds per-command default arguments for docker compose.
	Args DockerArgs `yaml:"args"`
	// Env controls automatic .env generation before lifecycle commands.
	Env DockerEnvConfig `yaml:"env"`
	// ProcessEnv holds additional environment variables passed to every
	// `docker compose` process launched by devbox. Use this to suppress
	// unwanted Docker CLI output, e.g.:
	//   process_env:
	//     DOCKER_CLI_HINTS: "false"
	ProcessEnv map[string]string `yaml:"process_env"`
	// Resources declares managed Docker resources (volumes, etc.).
	Resources DockerResourcesConfig `yaml:"resources"`
	// Topology controls topology display and health calculation.
	Topology DockerTopologyConfig `yaml:"topology"`
}

// DockerTopologyConfig controls which compose services appear in the
// topology tree and are counted toward stack health.
type DockerTopologyConfig struct {
	// Hidden lists compose service names to exclude from the topology
	// tree and from the stack health calculation. Useful for init
	// containers that run once and then exit (e.g. redis-insight-setup).
	Hidden []string `yaml:"hidden"`
}

// DockerResourcesConfig holds declarations for Docker resources managed by devbox.
type DockerResourcesConfig struct {
	// Volumes is a map of logical name → volume config.
	Volumes map[string]DockerVolumeConfig `yaml:"volumes"`
}

// DockerVolumeConfig describes a Docker volume that devbox should ensure exists.
type DockerVolumeConfig struct {
	// Name is the base volume name as declared in the YAML.
	// For shared volumes this is the literal Docker volume name.
	// For non-shared (project-scoped) volumes the runtime prefixes Name with
	// the compose project name to match Docker Compose's own naming convention
	// — see DockerVolumeConfig.ResolveName.
	Name string `yaml:"name"`
	// Shared marks the volume as project-independent. When true, the volume
	// uses Name verbatim and persists across project resets and across
	// projects that declare the same name. When false (default), the volume
	// is scoped to the current project and the resolved Docker name is
	// "<project_name>_<Name>" — same scheme that Docker Compose uses for
	// named volumes declared inside compose.yaml.
	Shared bool `yaml:"shared"`
	// EnsureBefore lists the devbox docker/deploy commands that trigger idempotent creation.
	// Supported values: up, deploy.
	EnsureBefore []string `yaml:"ensure_before"`
}

// ResolveName returns the actual Docker volume name to create / look up, given
// the compose project name. Shared volumes return Name verbatim; non-shared
// volumes are prefixed with "<projectName>_" so they share their lifecycle and
// scope with the project. An empty projectName disables prefixing (useful for
// tests and as a defensive fallback when policy resolution failed upstream).
func (v DockerVolumeConfig) ResolveName(projectName string) string {
	if v.Shared || projectName == "" {
		return v.Name
	}
	return projectName + "_" + v.Name
}

// DockerArgs holds global and per-command default arguments.
type DockerArgs struct {
	Global  []string `yaml:"global"`
	Up      []string `yaml:"up"`
	Down    []string `yaml:"down"`
	Stop    []string `yaml:"stop"`
	Restart []string `yaml:"restart"`
	Logs    []string `yaml:"logs"`
	Ps      []string `yaml:"ps"`
	Exec    []string `yaml:"exec"`
	Run     []string `yaml:"run"`
}

// DockerEnvConfig controls automatic .env generation.
type DockerEnvConfig struct {
	AutoGenerate bool     `yaml:"auto_generate"`
	Commands     []string `yaml:"commands"`
}

// ShouldGenerateEnv reports whether .env should be regenerated before the given command.
func (e *DockerEnvConfig) ShouldGenerateEnv(command string) bool {
	if !e.AutoGenerate {
		return false
	}
	return slices.Contains(e.Commands, command)
}

// LoadDockerConfig loads Docker Compose execution policy from
// devbox/docker.yml (base) and devbox/docker.local.yml (optional overrides).
// The project_name field is resolved as a ${...} template against cfg.Raw.
func LoadDockerConfig(baseDir string, cfg *DevboxConfig) (*DockerConfig, error) {
	dockerPath := filepath.Join(baseDir, "devbox", "docker.yml")
	base, err := loadRawYAML(dockerPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dockerPath, err)
	}

	// Merge local overrides if present.
	localPath := filepath.Join(baseDir, "devbox", "docker.local.yml")
	if local, err := loadRawYAML(localPath); err == nil {
		deepMerge(base, local)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", localPath, err)
	}

	data, err := yaml.Marshal(base)
	if err != nil {
		return nil, fmt.Errorf("marshal docker config: %w", err)
	}
	var dcfg DockerConfig
	if err := yaml.Unmarshal(data, &dcfg); err != nil {
		return nil, fmt.Errorf("unmarshal docker config: %w", err)
	}

	// Resolve ${...} template expressions in project_name.
	dcfg.ProjectName, err = resolveVarTemplate(dcfg.ProjectName, cfg.Raw)
	if err != nil {
		return nil, fmt.Errorf("resolve project_name: %w", err)
	}

	return &dcfg, nil
}

// resolveVarTemplate resolves ${dot.path} expressions in s against raw config.
// This is a lightweight resolver that doesn't need the full tpl package —
// it only handles the ${key} → value substitution pattern.
func resolveVarTemplate(s string, raw map[string]any) (string, error) {
	const maxIter = 10
	result := s
	for i := range maxIter {
		start := strings.Index(result, "${")
		if start == -1 {
			break
		}
		end := strings.Index(result[start:], "}")
		if end == -1 {
			return "", fmt.Errorf("unclosed ${ in %q", s)
		}
		end += start
		path := result[start+2 : end]
		val, ok := ResolvePath(raw, path)
		if !ok {
			return "", fmt.Errorf("unresolved path %q in template %q", path, s)
		}
		result = result[:start] + fmt.Sprintf("%v", val) + result[end+1:]
		if i == maxIter-1 && strings.Contains(result, "${") {
			return "", fmt.Errorf("too many template substitutions in %q (possible circular reference)", s)
		}
	}
	return result, nil
}
