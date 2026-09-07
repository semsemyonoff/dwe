package reset

import (
	_ "embed"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

//go:embed default_reset.yml
var defaultResetYAML string

// DefaultResetYAML returns the built-in reset pipeline as an authorable,
// commented reset.yml document — the payload `dwe reset eject` emits.
//
// It is an asset rather than a marshalled DefaultResetConfig() because a
// marshaller would have to stay in sync with DeployStep's custom UnmarshalYAML
// and deployStepKnownFields (which nothing cross-checks) and would drop the
// comments, which are the point of handing a human a file to edit. What keeps
// the asset from drifting from the constructor is the round-trip test in
// asset_test.go: it loads these bytes through the real strict loader and
// requires the result to equal DefaultResetConfig(). Edit one, run that test.
//
// The returned slice is a fresh copy; callers may mutate it.
func DefaultResetYAML() []byte {
	return []byte(defaultResetYAML)
}

// DefaultResetConfig returns a freshly-allocated default reset pipeline. Callers may mutate the result safely.
// Log is left nil on purpose: LoadResetConfig fills an absent log: key with false, so nil here and a loaded
// reset.yml without the key describe the same behaviour. The asset spells `log: false` out anyway — see
// asset_test.go, which normalises the asymmetry rather than hiding it.
func DefaultResetConfig() *config.ProjectDeployConfig {
	return &config.ProjectDeployConfig{
		Phases: []config.DeployPhase{
			{
				Name:        "pre",
				Description: "Confirm destructive reset",
				Untracked:   true,
				Steps: []config.DeployStep{
					{
						Name: "confirm",
						Type: "builtin",
						Cmd:  "confirm",
						With: map[string]any{
							"message": "This will stop containers, remove project volumes, and delete generated data.",
						},
						Description: "Confirm before proceeding with destructive operations",
					},
				},
			},
			{
				Name:        "stop",
				Description: "Stop all containers",
				Steps: []config.DeployStep{
					{
						Name:        "down",
						Type:        "dwe",
						Cmd:         "docker down",
						Description: "Stop and remove all project containers",
					},
				},
			},
			{
				Name:        "cleanup",
				Description: "Remove project volumes and generated service data",
				Steps: []config.DeployStep{
					{
						Name: "remove-volumes",
						Type: "builtin",
						Cmd:  "docker_remove_project_volumes",
						// No continue_on_error: the builtin already makes individual
						// `docker volume rm` failures best-effort (a stuck volume is
						// reported and skipped, not fatal), while keeping project-name
						// resolution and `docker volume ls` failures fatal so a broken
						// docker setup aborts the reset instead of silently clearing
						// the journal with volumes left behind.
						Description: "Remove all Docker volumes belonging to this project",
					},
					{
						Name: "remove-services",
						Type: "builtin",
						Cmd:  "remove_paths",
						// The With shapes here must be the shapes yaml.v3 decodes
						// into — map[string]any for a mapping, []any for a
						// sequence. A []string or a map[string]string reads the
						// same to a human but fails the asset round-trip test in
						// asset_test.go for a reason that has nothing to do with
						// the asset.
						With: map[string]any{
							"paths": []any{"services/"},
						},
						Description: "Remove generated service hub directories",
					},
				},
			},
		},
	}
}

// EnsureResetConfig returns the loaded config unchanged when it contains phases;
// otherwise it returns DefaultResetConfig and defaulted=true.
// An empty-phases config is treated as "no user pipeline" and is replaced with the default.
func EnsureResetConfig(loaded *config.ProjectDeployConfig) (*config.ProjectDeployConfig, bool) {
	if loaded == nil || len(loaded.Phases) == 0 {
		return DefaultResetConfig(), true
	}
	return loaded, false
}
