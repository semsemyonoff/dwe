package journal

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultRelPath is the relative path to the deploy state file.
const DefaultRelPath = ".dwe/deploy/state.yml"

// Status represents the deployment status of a step or service.
type Status string

// Status constants.
const (
	StatusOk          Status = "ok"
	StatusFailed      Status = "failed"
	StatusPartial     Status = "partial"
	StatusInProgress  Status = "in_progress"
	StatusSkipped     Status = "skipped"
	StatusNotDeployed Status = "not_deployed"
	StatusDeployed    Status = "deployed"
)

// LastRun tracks the timing and outcome of a deploy run.
type LastRun struct {
	Status     Status    `yaml:"status"`
	StartedAt  time.Time `yaml:"started_at,omitempty"`
	FinishedAt time.Time `yaml:"finished_at,omitempty"`
}

// StepState tracks a single step's outcome, hash, and duration.
type StepState struct {
	Status     Status    `yaml:"status"`
	FinishedAt time.Time `yaml:"finished_at,omitempty"`
	ActionHash string    `yaml:"action_hash"`
	DurationMs int64     `yaml:"duration_ms"`
}

// PhaseState aggregates step outcomes for a phase.
type PhaseState struct {
	Status Status                `yaml:"status"`
	Steps  map[string]*StepState `yaml:"steps,omitempty"`
}

// ServiceState tracks a service's deployment status, config hash, and phases.
type ServiceState struct {
	Status     Status                 `yaml:"status"`
	DeployedAt time.Time              `yaml:"deployed_at,omitempty"`
	ConfigHash string                 `yaml:"config_hash"`
	LastRun    *LastRun               `yaml:"last_run,omitempty"`
	Phases     map[string]*PhaseState `yaml:"phases,omitempty"`
}

// ProjectState is the top-level state file structure.
type ProjectState struct {
	SchemaVersion string                   `yaml:"schema_version"`
	Project       *ProjectLevelState       `yaml:"project,omitempty"`
	Services      map[string]*ServiceState `yaml:"services,omitempty"`
	Pending       *PendingApply            `yaml:"pending,omitempty"`
}

// ProjectLevelState tracks the project-wide state including project-scope phases.
type ProjectLevelState struct {
	DeployedAt time.Time              `yaml:"deployed_at,omitempty"`
	ConfigHash string                 `yaml:"config_hash"`
	Status     Status                 `yaml:"status"`
	LastRun    *LastRun               `yaml:"last_run,omitempty"`
	Phases     map[string]*PhaseState `yaml:"phases,omitempty"`
}

// Load reads the state file from disk. Returns a zero-value-with-defaults
// (SchemaVersion="1") when the file is absent; returns an error for malformed YAML
// or unknown fields.
func Load(path string) (*ProjectState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Return zero-value with defaults
			return &ProjectState{
				SchemaVersion: "1",
				Project:       &ProjectLevelState{},
				Services:      make(map[string]*ServiceState),
			}, nil
		}
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	// Lenient decode: state.yml is machine-generated; ignore unknown fields so
	// a newer dwe version's state file can be read by an older version.
	var state ProjectState
	if err := yaml.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state file: %w", err)
	}

	// Initialize defaults
	if state.SchemaVersion == "" {
		state.SchemaVersion = "1"
	}
	if state.Project == nil {
		state.Project = &ProjectLevelState{}
	}
	if state.Services == nil {
		state.Services = make(map[string]*ServiceState)
	}

	return &state, nil
}

// Save writes the state atomically to disk using write-temp + rename.
// Ensures the parent directory exists with mode 0o755 and the file is written with mode 0o644.
func Save(path string, s *ProjectState) error {
	if s == nil {
		return fmt.Errorf("cannot save nil state")
	}

	// Ensure parent directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	// Marshal state to YAML
	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Write to temp file
	tmpFile, err := os.CreateTemp(dir, ".state-*.yml")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpPath) // Clean up on error
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Set file permissions
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return fmt.Errorf("failed to set file permissions: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// Remove deletes the state file. No-op if the file doesn't exist.
func Remove(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove state file: %w", err)
	}
	return nil
}

// RemoveService removes a single service from the state file and recomputes
// the project aggregate. If no services remain after removal, the file is deleted.
func RemoveService(path string, name string) error {
	state, err := Load(path)
	if err != nil {
		return err
	}

	// Delete the service
	delete(state.Services, name)

	// If no services remain and no project-level phases exist, remove the file entirely.
	// Preserve the file if project-scope steps were deployed.
	if len(state.Services) == 0 {
		if state.Project == nil || len(state.Project.Phases) == 0 {
			return Remove(path)
		}
	}

	// Recompute project aggregate before saving
	Recompute(state)
	return Save(path, state)
}

// Recompute derives status aggregates from per-service and per-phase outcomes.
// Status only, never hashes. Hashes are owned by the caller.
func Recompute(p *ProjectState) {
	if p == nil || p.Project == nil {
		return
	}

	// Derive project status from per-service statuses
	var (
		hasDeployed    bool
		hasFailed      bool
		hasNotDeployed bool
	)

	for _, svc := range p.Services {
		switch svc.Status {
		case StatusDeployed:
			hasDeployed = true
		case StatusFailed:
			hasFailed = true
		case StatusNotDeployed:
			hasNotDeployed = true
		case StatusPartial:
			hasFailed = true
			hasDeployed = true
		case StatusInProgress:
			// in_progress means the pipeline crashed before completing — treat as failure.
			hasFailed = true
		case StatusSkipped:
			// Skipped at the service level is treated the same as not deployed.
			hasNotDeployed = true
		}
	}

	// Determine project status based on aggregated service statuses
	switch {
	case hasFailed:
		p.Project.Status = StatusFailed
	case hasNotDeployed && hasDeployed:
		p.Project.Status = StatusPartial
	case hasNotDeployed:
		p.Project.Status = StatusNotDeployed
	case hasDeployed:
		p.Project.Status = StatusDeployed
	default:
		// No services tracked — project-level-only deploy.
		// Use LastRun outcome to set status so project-only deploys don't show not_deployed.
		if p.Project.LastRun != nil && p.Project.LastRun.Status == StatusOk {
			p.Project.Status = StatusDeployed
		} else {
			p.Project.Status = StatusNotDeployed
		}
	}

	// Fix LastRun.Status when stuck in_progress (e.g. process crashed mid-deploy).
	// Always mark failed: an in_progress status means the pipeline never completed cleanly.
	if p.Project.LastRun != nil && p.Project.LastRun.Status == StatusInProgress {
		p.Project.LastRun.Status = StatusFailed
	}

	// Fix service-level LastRun.Status stuck in_progress for the same reason.
	for _, svc := range p.Services {
		if svc.LastRun != nil && svc.LastRun.Status == StatusInProgress {
			svc.LastRun.Status = StatusFailed
		}
	}
}
