package command

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/config"
)

// TestRunLifecyclePhases_HappyPath verifies that runLifecyclePhases returns nil,
// writes a non-empty log file at logs/<logFileName>.log, and the log contains
// ANSI-stripped output for the executed step.
func TestRunLifecyclePhases_HappyPath(t *testing.T) {
	workDir := t.TempDir()
	cfg := &config.DevboxConfig{Raw: map[string]any{}}

	phases := []config.DeployPhase{
		{
			Name: "start",
			Steps: []config.DeployStep{
				{Name: "noop", Run: "echo lifecycle-marker"},
			},
		},
	}

	err := runLifecyclePhases(cfg, nil, workDir, phases, "run", "run", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logPath := filepath.Join(workDir, "logs", "run.log")
	data, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("log file not created at %s: %v", logPath, readErr)
	}
	if len(data) == 0 {
		t.Fatal("log file is empty")
	}
	// Log must not contain raw ANSI escape sequences (ansiStripper removes them).
	if strings.Contains(string(data), "\x1b[") {
		t.Errorf("log file contains ANSI escape sequences; got:\n%s", string(data))
	}
}

// TestRunLifecyclePhases_AbortingStepFails verifies that a failing step without
// continue_on_error causes runLifecyclePhases to return ErrSilent.
func TestRunLifecyclePhases_AbortingStepFails(t *testing.T) {
	workDir := t.TempDir()
	cfg := &config.DevboxConfig{Raw: map[string]any{}}

	phases := []config.DeployPhase{
		{
			Name: "start",
			Steps: []config.DeployStep{
				{Name: "fail", Run: "exit 1"},
			},
		},
	}

	err := runLifecyclePhases(cfg, nil, workDir, phases, "run", "run", false)
	if !errors.Is(err, ErrSilent) {
		t.Fatalf("want ErrSilent, got %v", err)
	}

	// Log file must still be created even on failure.
	logPath := filepath.Join(workDir, "logs", "run.log")
	if _, statErr := os.Stat(logPath); statErr != nil {
		t.Errorf("log file not created on failure: %v", statErr)
	}
}

// TestRunLifecyclePhases_ContinueOnError verifies that a failing step with
// continue_on_error=true does not abort the pipeline — the next step runs
// and the function returns nil.
func TestRunLifecyclePhases_ContinueOnError(t *testing.T) {
	workDir := t.TempDir()
	cfg := &config.DevboxConfig{Raw: map[string]any{}}

	phases := []config.DeployPhase{
		{
			Name: "hooks",
			Steps: []config.DeployStep{
				{Name: "optional", Run: "exit 1", ContinueOnError: true},
				{Name: "main", Run: "true"},
			},
		},
	}

	err := runLifecyclePhases(cfg, nil, workDir, phases, "run", "run", false)
	if err != nil {
		t.Fatalf("want nil (continue_on_error), got %v", err)
	}
}

// TestRunLifecyclePhases_LogFileNameUsed verifies that the logFileName parameter
// determines the path: logs/<logFileName>.log, not logs/run.log for a stop pipeline.
func TestRunLifecyclePhases_LogFileNameUsed(t *testing.T) {
	workDir := t.TempDir()
	cfg := &config.DevboxConfig{Raw: map[string]any{}}

	phases := []config.DeployPhase{
		{Name: "stop", Steps: []config.DeployStep{{Name: "noop", Run: "true"}}},
	}

	err := runLifecyclePhases(cfg, nil, workDir, phases, "stop", "stop", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logPath := filepath.Join(workDir, "logs", "stop.log")
	if _, statErr := os.Stat(logPath); statErr != nil {
		t.Errorf("expected log at %s, got: %v", logPath, statErr)
	}
	// Ensure the run.log is NOT created.
	runLog := filepath.Join(workDir, "logs", "run.log")
	if _, statErr := os.Stat(runLog); !os.IsNotExist(statErr) {
		t.Errorf("run.log should not exist for a stop pipeline")
	}
}

// TestRunLifecyclePhases_EmptyPhases returns nil immediately and creates a log file.
func TestRunLifecyclePhases_EmptyPhases(t *testing.T) {
	workDir := t.TempDir()
	cfg := &config.DevboxConfig{Raw: map[string]any{}}

	err := runLifecyclePhases(cfg, nil, workDir, nil, "run", "run", false)
	if err != nil {
		t.Fatalf("unexpected error with empty phases: %v", err)
	}

	logPath := filepath.Join(workDir, "logs", "run.log")
	if _, statErr := os.Stat(logPath); statErr != nil {
		t.Errorf("log file not created for empty phases: %v", statErr)
	}
}
