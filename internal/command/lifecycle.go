package command

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"devbox-cli/internal/commands"
	"devbox-cli/internal/config"
	pipeline "devbox-cli/internal/pipeline"
	"devbox-cli/internal/render"
)

// runLifecyclePhases resolves and executes a set of lifecycle pipeline phases.
//
// name is the human-readable label passed to the reporter (e.g. "run", "stop").
// logFileName is the base name (without extension) for the log file written to logs/.
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
) error {
	var steps []resolvedStep
	for _, phase := range phases {
		resolved, err := resolvePhaseSteps(cfg, phase, "")
		if err != nil {
			return fmt.Errorf("resolving lifecycle phases: %w", err)
		}
		steps = append(steps, resolved...)
	}

	logsDir := filepath.Join(workDir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return fmt.Errorf("creating logs directory %s: %w", logsDir, err)
	}
	logPath := filepath.Join(logsDir, logFileName+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("creating lifecycle log %s: %w", logPath, err)
	}
	defer func() { _ = logFile.Close() }()

	tee := io.MultiWriter(os.Stdout, &ansiStripper{logFile})
	w := render.NewWriter(tee)
	rep := pipeline.NewPlainReporter(w)

	if err := runPipeline(steps, rep, name, cfg, reg, workDir, logFile, skipConfirm, nil); err != nil {
		if errors.Is(err, ErrSilent) {
			w.Warning("Full output saved to: " + logPath)
		}
		return err
	}

	w.Info("Log saved to: " + logPath)
	return nil
}
