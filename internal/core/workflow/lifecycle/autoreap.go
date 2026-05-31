package lifecycle

import "github.com/semsemyonoff/devbox/internal/core/project/config"

// AutoReapPhaseName is the synthetic phase name prepended to every lifecycle
// stop pipeline. It is intentionally visible in plan output (leading
// underscore) so operators can see that reaping happens automatically and is
// not configurable.
const AutoReapPhaseName = "_auto_reap_daemons"

// autoReapPhase returns the synthetic phase that runs the daemons_reap
// builtin. The phase is shaped as a normal DeployPhase so it flows through
// the standard pipeline executor.
func autoReapPhase() config.DeployPhase {
	return config.DeployPhase{
		Name:        AutoReapPhaseName,
		Description: "stop any running daemons started by this project",
		Steps: []config.DeployStep{
			{
				Name: "reap-daemons",
				Type: "builtin",
				Cmd:  "daemons_reap",
			},
		},
	}
}

// EnsureStopConfig returns a stop pipeline with the auto-reap phase prepended,
// and reports whether a built-in default was used (defaulted=true when cfg is
// nil or has no stop: section). A nil LifecycleConfig (file absent) or a
// config with no stop: section uses DefaultStopConfig(); otherwise the user's
// stop pipeline is returned with the reap phase prepended.
//
// The returned value is always safe to dereference and never aliases the
// caller's phases slice (a fresh slice is allocated).
func EnsureStopConfig(cfg *config.LifecycleConfig) (*config.LifecycleStopConfig, bool) {
	if cfg == nil || cfg.Stop == nil {
		d := DefaultStopConfig()
		phases := make([]config.DeployPhase, 0, len(d.Phases)+1)
		phases = append(phases, autoReapPhase())
		phases = append(phases, d.Phases...)
		return &config.LifecycleStopConfig{
			FinalMessage: d.FinalMessage,
			Phases:       phases,
		}, true
	}
	final := cfg.Stop.FinalMessage
	if final == "" {
		final = DefaultStopConfig().FinalMessage
	}
	phases := make([]config.DeployPhase, 0, len(cfg.Stop.Phases)+1)
	phases = append(phases, autoReapPhase())
	phases = append(phases, cfg.Stop.Phases...)
	return &config.LifecycleStopConfig{
		FinalMessage: final,
		Log:          cfg.Stop.Log,
		Phases:       phases,
	}, false
}
