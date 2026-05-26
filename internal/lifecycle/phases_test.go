package lifecycle

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/pipeline"
)

func TestRunPhases_HappyPath(t *testing.T) {
	workDir := t.TempDir()
	cfg := &config.DevboxConfig{Raw: map[string]any{}}

	phases := []config.DeployPhase{
		{
			Name: "start",
			Steps: []config.DeployStep{
				{Name: "noop", Type: "shell", Cmd: "echo lifecycle-marker"},
			},
		},
	}

	err := RunPhases(cfg, nil, workDir, phases, "run", "run", false, true, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logPath := filepath.Join(workDir, ".devbox", "logs", "run.log")
	data, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("log file not created at %s: %v", logPath, readErr)
	}
	if len(data) == 0 {
		t.Fatal("log file is empty")
	}
	if strings.Contains(string(data), "\x1b[") {
		t.Errorf("log file contains ANSI escape sequences; got:\n%s", string(data))
	}
}

func TestRunPhases_AbortingStepFails(t *testing.T) {
	workDir := t.TempDir()
	cfg := &config.DevboxConfig{Raw: map[string]any{}}

	phases := []config.DeployPhase{
		{
			Name: "start",
			Steps: []config.DeployStep{
				{Name: "fail", Type: "shell", Cmd: "exit 1"},
			},
		},
	}

	err := RunPhases(cfg, nil, workDir, phases, "run", "run", false, true, nil, "")
	if !errors.Is(err, pipeline.ErrSilent) {
		t.Fatalf("want pipeline.ErrSilent, got %v", err)
	}

	logPath := filepath.Join(workDir, ".devbox", "logs", "run.log")
	if _, statErr := os.Stat(logPath); statErr != nil {
		t.Errorf("log file not created on failure: %v", statErr)
	}
}

func TestRunPhases_ContinueOnError(t *testing.T) {
	workDir := t.TempDir()
	cfg := &config.DevboxConfig{Raw: map[string]any{}}

	phases := []config.DeployPhase{
		{
			Name: "hooks",
			Steps: []config.DeployStep{
				{Name: "optional", Type: "shell", Cmd: "exit 1", ContinueOnError: true},
				{Name: "main", Type: "shell", Cmd: "true"},
			},
		},
	}

	err := RunPhases(cfg, nil, workDir, phases, "run", "run", false, true, nil, "")
	if err != nil {
		t.Fatalf("want nil (continue_on_error), got %v", err)
	}
}

func TestRunPhases_LogFileNameUsed(t *testing.T) {
	workDir := t.TempDir()
	cfg := &config.DevboxConfig{Raw: map[string]any{}}

	phases := []config.DeployPhase{
		{Name: "stop", Steps: []config.DeployStep{{Name: "noop", Type: "shell", Cmd: "true"}}},
	}

	err := RunPhases(cfg, nil, workDir, phases, "stop", "stop", false, true, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logPath := filepath.Join(workDir, ".devbox", "logs", "stop.log")
	if _, statErr := os.Stat(logPath); statErr != nil {
		t.Errorf("expected log at %s, got: %v", logPath, statErr)
	}
	runLog := filepath.Join(workDir, ".devbox", "logs", "run.log")
	if _, statErr := os.Stat(runLog); !os.IsNotExist(statErr) {
		t.Errorf("run.log should not exist for a stop pipeline")
	}
}

func TestRunPhases_EmptyPhases(t *testing.T) {
	workDir := t.TempDir()
	cfg := &config.DevboxConfig{Raw: map[string]any{}}

	err := RunPhases(cfg, nil, workDir, nil, "run", "run", false, true, nil, "")
	if err != nil {
		t.Fatalf("unexpected error with empty phases: %v", err)
	}

	logPath := filepath.Join(workDir, ".devbox", "logs", "run.log")
	if _, statErr := os.Stat(logPath); statErr != nil {
		t.Errorf("log file not created for empty phases: %v", statErr)
	}
}

func TestRunPhases_LogDisabled(t *testing.T) {
	workDir := t.TempDir()
	cfg := &config.DevboxConfig{Raw: map[string]any{}}

	phases := []config.DeployPhase{
		{Name: "start", Steps: []config.DeployStep{{Name: "noop", Type: "shell", Cmd: "true"}}},
	}

	err := RunPhases(cfg, nil, workDir, phases, "run", "run", false, false, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logPath := filepath.Join(workDir, ".devbox", "logs", "run.log")
	if _, statErr := os.Stat(logPath); !os.IsNotExist(statErr) {
		t.Errorf("log file should not exist when logging is disabled, got stat err: %v", statErr)
	}
	logsDir := filepath.Join(workDir, ".devbox", "logs")
	if _, statErr := os.Stat(logsDir); !os.IsNotExist(statErr) {
		t.Errorf(".devbox/logs/ dir should not be created when logging is disabled, got stat err: %v", statErr)
	}
}

func TestRunPhases_LogDisabledFailingStep(t *testing.T) {
	workDir := t.TempDir()
	cfg := &config.DevboxConfig{Raw: map[string]any{}}

	phases := []config.DeployPhase{
		{Name: "start", Steps: []config.DeployStep{{Name: "fail", Type: "shell", Cmd: "exit 1"}}},
	}

	err := RunPhases(cfg, nil, workDir, phases, "run", "run", false, false, nil, "")
	if !errors.Is(err, pipeline.ErrSilent) {
		t.Fatalf("want pipeline.ErrSilent, got %v", err)
	}
	logPath := filepath.Join(workDir, ".devbox", "logs", "run.log")
	if _, statErr := os.Stat(logPath); !os.IsNotExist(statErr) {
		t.Errorf("log file should not exist when logging is disabled, got stat err: %v", statErr)
	}
}
