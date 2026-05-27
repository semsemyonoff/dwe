package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
// Extend here when adding new docker subcommands wrapped by devbox.
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
	Pull    []string `yaml:"pull"`
	Build   []string `yaml:"build"`
}


// LoadDockerConfig loads Docker Compose execution policy from
// devbox/docker.yml (base) and devbox/docker.local.yml (optional overrides).
// The project_name field is resolved as a ${...} template against cfg.Raw.
//
// Per-key defaults are applied for args: up, logs, run, down. These defaults
// are applied only when the key is absent from both layers; explicit empty lists
// ([]) opt out of the default.
//
// .env is auto-regenerated before {up, run, exec, restart, build} unconditionally.
// Old docker.yml files with an `env:` block will be rejected with a migration error.
func LoadDockerConfig(baseDir string, cfg *DevboxConfig) (*DockerConfig, error) {
	dockerPath := filepath.Join(baseDir, "devbox", "docker.yml")
	base, err := loadRawYAML(dockerPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dockerPath, err)
	}

	// Check for removed `env:` block in base layer.
	if _, hasEnv := base["env"]; hasEnv {
		return nil, fmt.Errorf("env: removed from docker.yml — .env auto-regenerates for {up, run, exec, restart, build} unconditionally; the env: customization is gone. See docs/reference/config/docker.md")
	}

	// Track which args keys were explicitly set in the YAML.
	presentKeys := detectPresentArgsKeys(dockerPath)

	// Merge local overrides if present.
	localPath := filepath.Join(baseDir, "devbox", "docker.local.yml")
	if local, err := loadRawYAML(localPath); err == nil {
		// Check for removed `env:` block in local layer.
		if _, hasEnv := local["env"]; hasEnv {
			return nil, fmt.Errorf("env: removed from docker.local.yml — .env auto-regenerates for {up, run, exec, restart, build} unconditionally; the env: customization is gone. See docs/reference/config/docker.md")
		}
		deepMerge(base, local)
		// Merge presence tracking from local layer
		localKeys := detectPresentArgsKeys(localPath)
		for k := range localKeys {
			presentKeys[k] = true
		}
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

	// Apply per-key defaults for args that were absent from both layers.
	applyDockerArgsDefaults(&dcfg.Args, presentKeys)

	// Resolve ${...} template expressions in project_name.
	dcfg.ProjectName, err = resolveVarTemplate(dcfg.ProjectName, cfg.Raw)
	if err != nil {
		return nil, fmt.Errorf("resolve project_name: %w", err)
	}

	return &dcfg, nil
}

// detectPresentArgsKeys detects which keys are explicitly present in the args
// block of a docker.yml or docker.local.yml file. Returns a map where keys
// are arg field names ("up", "down", "logs", "run") present in YAML.
// Returns empty map if file doesn't exist or has no args block.
func detectPresentArgsKeys(path string) map[string]bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return make(map[string]bool)
	}

	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return make(map[string]bool)
	}

	present := make(map[string]bool)
	if argsRaw, ok := raw["args"].(map[string]any); ok {
		for k := range argsRaw {
			present[k] = true
		}
	}
	return present
}

// applyDockerArgsDefaults applies per-key defaults for docker compose arguments.
// Defaults are applied only when the key was not explicitly set in YAML.
// The defaults are:
//   - up: ["-d", "--remove-orphans"]
//   - logs: ["-f"]
//   - run: ["--rm"]
//   - down: ["--remove-orphans"]
//
// Other keys (global, stop, restart, ps, exec, pull, build) have no defaults.
func applyDockerArgsDefaults(args *DockerArgs, presentKeys map[string]bool) {
	if !presentKeys["up"] && len(args.Up) == 0 {
		args.Up = []string{"-d", "--remove-orphans"}
	}
	if !presentKeys["logs"] && len(args.Logs) == 0 {
		args.Logs = []string{"-f"}
	}
	if !presentKeys["run"] && len(args.Run) == 0 {
		args.Run = []string{"--rm"}
	}
	if !presentKeys["down"] && len(args.Down) == 0 {
		args.Down = []string{"--remove-orphans"}
	}
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
