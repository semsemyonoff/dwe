package lifecycle

import (
	"errors"
	"fmt"
	"os"

	"github.com/semsemyonoff/devbox/internal/core/execution/pipeline"
	"github.com/semsemyonoff/devbox/internal/core/project/config"
	"github.com/semsemyonoff/devbox/internal/core/usercommands"
	"github.com/semsemyonoff/devbox/internal/shared/i18n"
)

// RunPhasesFunc is a package-level variable so tests can stub phase execution
// without executing actual devbox/shell commands. Same pattern as PreflightFunc
// and GitProbeFunc.
var RunPhasesFunc = runPhases

// RunPhases resolves and executes a set of lifecycle pipeline phases.
// It delegates to RunPhasesFunc, which tests may replace with a no-op stub.
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
	return RunPhasesFunc(cfg, reg, workDir, phases, name, logFileName, skipConfirm, logEnabled, translator, locale)
}

// runPhases is the real implementation of phase resolution and execution.
func runPhases(
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
