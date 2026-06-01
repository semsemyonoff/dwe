package deploy

import "github.com/semsemyonoff/dwe/internal/core/project/config"

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
						Type:        "devbox",
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
						Type:        "devbox",
						Cmd:         "info",
						Description: "Show environment summary",
					},
					{
						Name: "success",
						Type: "builtin",
						Cmd:  "message",
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
