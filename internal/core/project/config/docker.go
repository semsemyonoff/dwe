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
// Loaded separately from workspace/docker.yml + workspace/docker.local.yml.
type DockerConfig struct {
	// ProjectName is the resolved compose project name.
	ProjectName string `yaml:"project_name"`
	// Args holds per-command default arguments for docker compose.
	Args DockerArgs `yaml:"args"`
	// ProcessEnv holds additional environment variables passed to every
	// `docker compose` process launched by dwe. Use this to suppress
	// unwanted Docker CLI output, e.g.:
	//   process_env:
	//     DOCKER_CLI_HINTS: "false"
	ProcessEnv map[string]string `yaml:"process_env"`
	// Resources declares managed Docker resources (volumes, etc.).
	Resources DockerResourcesConfig `yaml:"resources"`
	// Topology controls topology display and health calculation.
	Topology DockerTopologyConfig `yaml:"topology"`
	// Build holds image-build policy for compose build/up paths.
	Build DockerBuildConfig `yaml:"build"`
}

// DockerBuildConfig holds image-build policy for compose build/up paths.
type DockerBuildConfig struct {
	// PrepullBases pulls external FROM base images via the daemon before
	// buildkit builds, working around builders that cannot reach LAN
	// registries. Missing-only by default; see dwe docker build --force.
	PrepullBases bool `yaml:"prepull_bases"`
}

// DockerTopologyConfig controls which compose services appear in the
// topology tree and are counted toward stack health.
type DockerTopologyConfig struct {
	// Hidden lists compose service names to exclude from the topology
	// tree and from the stack health calculation. Useful for init
	// containers that run once and then exit (e.g. redis-insight-setup).
	Hidden []string `yaml:"hidden"`
}

// DockerResourcesConfig holds declarations for Docker resources managed by dwe.
type DockerResourcesConfig struct {
	// Volumes is a map of logical name → volume config.
	Volumes map[string]DockerVolumeConfig `yaml:"volumes"`
}

// DockerVolumeConfig describes a Docker volume that dwe should ensure exists.
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
	// EnsureBefore lists the dwe docker/deploy commands that trigger idempotent creation.
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
// Extend here when adding new docker subcommands wrapped by dwe.
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

// LoadDockerConfigOrEmpty loads Docker Compose execution policy like
// LoadDockerConfig, but treats a missing docker.yml as an empty config instead
// of an error. Returns (&DockerConfig{}, nil) when docker.yml does not exist,
// and a wrapped error (with prefix "loading docker config: ") for any other
// failure.
func LoadDockerConfigOrEmpty(baseDir string, cfg *DweConfig) (*DockerConfig, error) {
	dcfg, err := LoadDockerConfig(baseDir, cfg)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &DockerConfig{}, nil
		}
		return nil, fmt.Errorf("loading docker config: %w", err)
	}
	return dcfg, nil
}

// LoadDockerConfig loads Docker Compose execution policy from
// workspace/docker.yml (base) and workspace/docker.local.yml (optional overrides).
// The project_name field is resolved as a ${...} template against cfg.Raw.
//
// Per-key defaults are applied for args: up, logs, run, down. These defaults
// are applied only when the key is absent from both layers; explicit empty lists
// ([]) opt out of the default.
//
// .env is auto-regenerated before {up, run, exec, restart, build} unconditionally.
func LoadDockerConfig(baseDir string, cfg *DweConfig) (*DockerConfig, error) {
	dockerPath := filepath.Join(baseDir, "workspace", "docker.yml")
	base, err := loadRawYAML(dockerPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dockerPath, err)
	}

	// Track which args keys were explicitly set in the YAML.
	presentKeys := detectPresentArgsKeys(dockerPath)

	// Merge local overrides if present.
	localPath := filepath.Join(baseDir, "workspace", "docker.local.yml")
	if local, err := loadRawYAML(localPath); err == nil {
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

// normalizeComposeProjectName lowercases a compose project name. Docker
// Compose requires project names to match [a-z0-9][a-z0-9_-]*, rejecting
// uppercase — the one violation dwe cannot construct around, since
// project.name/project.prefix and docker.yml's project_name are free-form
// user text. ResolveComposeProjectName and ComposeProjectName both route
// through this single helper so the two entry points can never diverge on
// casing, and every other site that needs the name — docker.buildCompose (the
// -p value itself), `dwe docker project-name`, the reset stop/remove builtin,
// spec.RunContext.Compose, the ports preflight — calls one of those two
// rather than re-deriving the precedence. The one deliberate exception is
// internal/shared/prompt.readComposeProjectName, a standalone hot-path reader
// that repeats the lowercasing itself.
func normalizeComposeProjectName(name string) string {
	return strings.ToLower(name)
}

// ResolveComposeProjectName returns the authoritative compose project name —
// the same identifier `docker compose -p <name>` uses to scope containers,
// networks, and volumes for the project. It is the single source of truth for
// any code path that derives a container name as "<project>-<container>"
// while bypassing compose (per-service stop/restart, per-service reset, log
// tailing, status probes).
//
// Precedence:
//  1. workspace/docker.yml's resolved project_name (e.g. "${project.prefix}_${project.name}").
//  2. cfg.Project.FullName() as a fallback when docker.yml is absent or its
//     project_name is empty — preserves behaviour for minimal projects.
//
// Reads ONLY the project_name field (via readDockerProjectName) — does NOT
// roundtrip through the full DockerConfig schema. This keeps the lightweight
// per-service paths (restart/stop/logs/reset --service) immune to unrelated
// schema errors elsewhere in docker.yml (malformed args:, bad volumes:, etc.):
// a typo in the args block must not break `dwe restart <svc>` when only
// project_name is needed. Template-resolution errors on project_name itself
// (e.g. ${project.naem} typo) ARE propagated. os.ErrNotExist on docker.yml
// triggers the fallback to FullName().
//
// baseDir is the project root (the directory containing workspace.yml); an
// empty baseDir skips the docker.yml read and returns FullName().
func ResolveComposeProjectName(baseDir string, cfg *DweConfig) (string, error) {
	if baseDir == "" {
		return normalizeComposeProjectName(cfg.Project.FullName()), nil
	}
	name, err := readDockerProjectName(baseDir, cfg)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return normalizeComposeProjectName(cfg.Project.FullName()), nil
		}
		return "", err
	}
	if name == "" {
		return normalizeComposeProjectName(cfg.Project.FullName()), nil
	}
	return normalizeComposeProjectName(name), nil
}

// ComposeProjectName returns the compose project name from an already-loaded
// DockerConfig, without re-reading docker.yml from disk. It is the in-memory
// sibling of ResolveComposeProjectName for callers that already hold the
// resolved DockerConfig (pipeline builtins via spec.ExecContext.DockerConfig,
// status collectors, completion paths): the resolved docker.yml project_name
// when set, else the canonical "<prefix>-<name>" (cfg.Project.FullName()).
//
// Both arguments are nil-safe. Use this for any compose-bypass path that
// derives a container, network, or volume name as "<project>-<...>" (e.g. the
// daemon builtins and daemon status/completion) so daemons land on the same
// project name as compose-managed services. dockerCfg.ProjectName is assumed
// already template-resolved (LoadDockerConfig resolves it); this helper does
// no template expansion.
func ComposeProjectName(dockerCfg *DockerConfig, cfg *DweConfig) string {
	if dockerCfg != nil && dockerCfg.ProjectName != "" {
		return normalizeComposeProjectName(dockerCfg.ProjectName)
	}
	if cfg != nil {
		return normalizeComposeProjectName(cfg.Project.FullName())
	}
	return ""
}

// ComposeProjectNameCandidates returns the compose project name(s) a
// compose-bypass daemon lookup should consider, canonical name first:
//
//  1. ComposeProjectName(dockerCfg, cfg) — the normalized (lowercase) name
//     daemons are created under now.
//  2. dockerCfg.ProjectName — the PRE-NORMALIZATION spelling, included only
//     when it differs from (1). Compose-bypass paths (daemon builtins,
//     StopContainer/RemoveContainer, label filters) build "<project>-<name>"
//     directly, so a daemon container created under an explicit
//     docker.yml project_name before case normalization landed is named with
//     the original spelling, not the lowercased one — this candidate lets
//     stop/reap/status/completion still find it.
//  3. cfg.Project.FullName() — the LEGACY scope, included only when it
//     differs from the candidates already collected. Before docker.yml
//     project_name was honored, daemon containers were named/labeled with
//     FullName even when project_name was set; this candidate lets
//     stop/reap/status/completion still find those daemons across the
//     one-time naming transition, including the case-only difference
//     introduced by normalization itself.
//
// Empty and duplicate values are dropped, so the result is never empty unless
// every candidate is empty (a project with no project.name). When all three
// sources agree the slice has a single element — there is no legacy scope to
// sweep.
func ComposeProjectNameCandidates(dockerCfg *DockerConfig, cfg *DweConfig) []string {
	primary := ComposeProjectName(dockerCfg, cfg)
	seen := make(map[string]bool)
	var out []string
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	add(primary)
	if dockerCfg != nil {
		add(dockerCfg.ProjectName)
	}
	if cfg != nil {
		add(cfg.Project.FullName())
	}
	return out
}

// readDockerProjectName reads workspace/docker.yml (+ optional
// docker.local.yml override) as raw maps, deep-merges them, extracts the
// project_name field, and resolves ${...} templates against cfg.Raw.
// Intentionally narrow: never decodes into DockerConfig, so unrelated YAML
// schema errors elsewhere in the file cannot break callers that only need
// project_name. Returns os.ErrNotExist if docker.yml is absent; empty string
// if the field is absent or empty; the resolved name otherwise.
func readDockerProjectName(baseDir string, cfg *DweConfig) (string, error) {
	base, err := loadRawYAML(filepath.Join(baseDir, "workspace", "docker.yml"))
	if err != nil {
		return "", err
	}
	localPath := filepath.Join(baseDir, "workspace", "docker.local.yml")
	if local, lerr := loadRawYAML(localPath); lerr == nil {
		deepMerge(base, local)
	} else if !errors.Is(lerr, os.ErrNotExist) {
		return "", fmt.Errorf("read %s: %w", localPath, lerr)
	}
	raw, _ := base["project_name"].(string)
	if raw == "" {
		return "", nil
	}
	return resolveVarTemplate(raw, cfg.Raw)
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
