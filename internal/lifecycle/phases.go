package lifecycle

import (
	"errors"
	"fmt"
	"os"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/pipeline"
	"devbox-cli/internal/shared/i18n"
	"devbox-cli/internal/usercommands"
)

// RunPhases resolves and executes a set of lifecycle pipeline phases.
//
// name is the human-readable label passed to the reporter (e.g. "run", "stop").
// logFileName is the base name (without extension) for the log file written to .devbox/logs/.
// logEnabled toggles file logging at .devbox/logs/<logFileName>.log; when false, output
// goes only to stdout and no log file is created.
// Phases are resolved with an empty service (lifecycle is orchestrator-only).
//
// Returns pipeline.ErrSilent when any aborting step fails (reporter has already printed
// the failure). Returns other errors for config/IO failures.
func RunPhases(
	cfg *config.DevboxConfig,
	reg *usercommands.Registry,
	workDir string,
	phases []config.DeployPhase,
	name string,
	logFileName string,
	skipConfirm bool,
	logEnabled bool,
	translator i18n.Translator,
	locale string,
) error {
	var steps []pipeline.ResolvedStep
	for _, phase := range phases {
		resolved, err := pipeline.ResolvePhaseSteps(cfg, reg, phase, "")
		if err != nil {
			return fmt.Errorf("resolving lifecycle phases: %w", err)
		}
		steps = append(steps, resolved...)
	}

	w, logWriter, termOut, logPath, cleanup, err := pipeline.OpenPipelineLog(workDir, logFileName, logEnabled)
	if err != nil {
		return err
	}
	defer cleanup()

	rep := pipeline.NewPlainReporter(w, logWriter, termOut)
	defer rep.Close()

	dockerCfg, err := config.LoadDockerConfig(workDir, cfg)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("loading docker config: %w", err)
		}
		dockerCfg = &config.DockerConfig{}
	}

	if err := pipeline.RunWithOptions(pipeline.RunOptions{
		Steps:        steps,
		Reporter:     rep,
		Name:         name,
		Config:       cfg,
		DockerConfig: dockerCfg,
		Registry:     reg,
		WorkDir:      workDir,
		LogWriter:    logWriter,
		SkipConfirm:  skipConfirm,
		Translator:   translator,
		Locale:       locale,
	}); err != nil {
		if errors.Is(err, pipeline.ErrSilent) && logEnabled {
			w.Warning("Full output saved to: " + logPath)
		}
		return err
	}

	if logEnabled {
		w.Info("Log saved to: " + logPath)
	}
	return nil
}
