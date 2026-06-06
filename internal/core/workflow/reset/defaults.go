package reset

import "github.com/semsemyonoff/dwe/internal/core/project/config"

// DefaultResetConfig returns a freshly-allocated default reset pipeline. Callers may mutate the result safely.
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
