package lifecycle

import "github.com/semsemyonoff/dwe/internal/core/project/config"

// DefaultedPipeline identifies which built-in default pipeline was used
// in place of a missing or empty YAML section. Used as the argument to
// RunContext.OnDefaultUsed and StopContext.OnDefaultUsed.
type DefaultedPipeline string

// DefaultedRun and DefaultedStop identify which pipeline was substituted with
// a built-in default when the corresponding YAML section was absent.
const (
	DefaultedRun  DefaultedPipeline = "run"
	DefaultedStop DefaultedPipeline = "stop"
)

// DefaultRunConfig returns a freshly-allocated default run pipeline. Callers may mutate the result safely.
func DefaultRunConfig() *config.LifecycleRunConfig {
	return &config.LifecycleRunConfig{
		Update:       &config.LifecycleUpdate{Mode: "off"},
		ShowInfo:     true,
		FinalMessage: "Project is ready for work!",
		Phases: []config.DeployPhase{
			{
				Name:        "start",
				Description: "Start containers and wait for health",
				Steps: []config.DeployStep{
					{
						Name:        "up",
						Type:        "devbox",
						Cmd:         "docker up --wait",
						Description: "Start all containers and wait until healthy",
					},
				},
			},
		},
	}
}

// EnsureRunConfig returns the loaded run config when a run section is present;
// otherwise it returns DefaultRunConfig and defaulted=true.
// A nil LifecycleConfig (file absent) or a config with no run: section is
// treated as "no user run pipeline" and is replaced with the default.
func EnsureRunConfig(loaded *config.LifecycleConfig) (*config.LifecycleRunConfig, bool) {
	if loaded == nil || loaded.Run == nil {
		return DefaultRunConfig(), true
	}
	return loaded.Run, false
}

// DefaultStopConfig returns a freshly-allocated default stop pipeline. Callers may mutate the result safely.
// The auto-reap phase is NOT included — EnsureStopConfig always prepends it regardless of defaulting.
func DefaultStopConfig() *config.LifecycleStopConfig {
	return &config.LifecycleStopConfig{
		FinalMessage: "Project is stopped. Have a nice day!",
		Phases: []config.DeployPhase{
			{
				Name:        "stop",
				Description: "Stop containers",
				Steps: []config.DeployStep{
					{
						Name:        "down",
						Type:        "devbox",
						Cmd:         "docker down",
						Description: "Stop all containers",
					},
				},
			},
		},
	}
}
