package deploy

import (
	_ "embed"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

//go:embed default_deploy.yml
var defaultDeployYAML string

// DefaultDeployYAML returns the built-in deploy pipeline as an authorable,
// commented deploy.yml document — the payload `dwe deploy eject` emits.
//
// It is an asset rather than a marshalled DefaultDeployConfig() because a
// marshaller would have to stay in sync with DeployStep's custom UnmarshalYAML
// and deployStepKnownFields (which nothing cross-checks) and would drop the
// comments, which are the point of handing a human a file to edit. What keeps
// the asset from drifting from the constructor is the round-trip test in
// asset_test.go: it loads these bytes through the real strict loader and
// requires the result to equal DefaultDeployConfig(). Edit one, run that test.
//
// The returned slice is a fresh copy; callers may mutate it.
func DefaultDeployYAML() []byte {
	return []byte(defaultDeployYAML)
}

// DefaultDeployConfig returns a freshly-allocated default deploy pipeline. Callers may mutate the result safely.
// Log defaults to true to match LoadProjectDeployConfig's behavior — a project with no deploy.yml gets the
// same .dwe/logs/deploy.log artifact as a project that authors deploy.yml without an explicit log: field.
func DefaultDeployConfig() *config.ProjectDeployConfig {
	logOn := true
	return &config.ProjectDeployConfig{
		Log: &logOn,
		Phases: []config.DeployPhase{
			{
				Name:           "services",
				Description:    "Deploy all enabled services (resolved by dependency order)",
				DeployServices: true,
			},
			{
				Name:        "start",
				Description: "Start containers and wait for health",
				Steps: []config.DeployStep{
					{
						Name:        "up",
						Type:        "dwe",
						Cmd:         "docker up --wait",
						Description: "Start all containers and wait until healthy",
						Untracked:   true,
					},
				},
			},
			{
				Name:        "post-deploy",
				Description: "Post-deploy summary (runs only if all prior phases succeeded)",
				Untracked:   true,
				Steps: []config.DeployStep{
					{
						Name:        "info",
						Type:        "dwe",
						Cmd:         "info",
						Description: "Show environment summary",
					},
					{
						Name: "success",
						Type: "builtin",
						Cmd:  "message",
						// The With shapes here must be the shapes yaml.v3 decodes
						// into — map[string]any for a mapping, []any for a
						// sequence. A []string or a map[string]string reads the
						// same to a human but fails the asset round-trip test in
						// asset_test.go for a reason that has nothing to do with
						// the asset.
						With: map[string]any{
							"level": "success",
							"text":  "Deploy completed successfully",
						},
					},
				},
			},
		},
	}
}

// EnsureDeployConfig returns the loaded config unchanged when it contains phases;
// otherwise it returns DefaultDeployConfig and defaulted=true.
// An empty-phases config is treated as "no user pipeline" (today's silent-noop
// case) and is replaced with the default.
func EnsureDeployConfig(loaded *config.ProjectDeployConfig) (*config.ProjectDeployConfig, bool) {
	if loaded == nil || len(loaded.Phases) == 0 {
		return DefaultDeployConfig(), true
	}
	return loaded, false
}
