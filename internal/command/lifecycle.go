package command

import (
	"errors"
	"fmt"

	"devbox-cli/internal/commands"
	"devbox-cli/internal/config"
	pipeline "devbox-cli/internal/pipeline"
)

// runLifecyclePhases resolves and executes a set of lifecycle pipeline phases.
//
// name is the human-readable label passed to the reporter (e.g. "run", "stop").
// logFileName is the base name (without extension) for the log file written to logs/.
// logEnabled toggles file logging at logs/<logFileName>.log; when false, output
// goes only to stdout and no log file is created.
// Phases are resolved with an empty service (lifecycle is orchestrator-only).
//
// Returns ErrSilent when any aborting step fails (reporter has already printed
// the failure). Returns other errors for config/IO failures.
func runLifecyclePhases(
	cfg *config.DevboxConfig,
	reg *commands.Registry,
	workDir string,
	phases []config.DeployPhase,
	name string,
	logFileName string,
	skipConfirm bool,
	logEnabled bool,
) error {
	var steps []pipeline.ResolvedStep
	for _, phase := range phases {
		resolved, err := pipeline.ResolvePhaseSteps(cfg, phase, "")
		if err != nil {
			return fmt.Errorf("resolving lifecycle phases: %w", err)
		}
		steps = append(steps, resolved...)
	}

	w, logWriter, logPath, cleanup, err := pipeline.OpenPipelineLog(workDir, logFileName, logEnabled)
	if err != nil {
		return err
	}
	defer cleanup()

	rep := pipeline.NewPlainReporter(w)

	if err := pipeline.Run(steps, rep, name, cfg, reg, workDir, logWriter, skipConfirm, nil); err != nil {
		if errors.Is(err, ErrSilent) && logEnabled {
			w.Warning("Full output saved to: " + logPath)
		}
		return err
	}

	if logEnabled {
		w.Info("Log saved to: " + logPath)
	}
	return nil
}
