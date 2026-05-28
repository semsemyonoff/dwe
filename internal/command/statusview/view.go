package statusview

import (
	"time"

	"devbox-cli/internal/core/workflow/deploy/journal"
)

// ConfigDelta represents the relationship between persisted and current config hashes.
type ConfigDelta string

// Constants for ConfigDelta values.
const (
	ConfigDeltaOK      ConfigDelta = "ok"
	ConfigDeltaChanged ConfigDelta = "changed"
	ConfigDeltaMissing ConfigDelta = "missing"
)

// DeployStatusRow holds data for one row in the deploy status table.
type DeployStatusRow struct {
	Service         string
	Status          journal.Status
	DeployedAt      time.Time
	ConfigDelta     ConfigDelta
	PrevHashShort   string
	CurrHashShort   string
	LastFailedPhase string
	LastFailedStep  string
}

// DeployStatusView holds the top-level deploy status and per-service breakdown.
type DeployStatusView struct {
	ProjectStatus     journal.Status
	ProjectDeployedAt time.Time
	Rows              []DeployStatusRow
}

// DeploySummary holds counts for the root summary line.
type DeploySummary struct {
	Deployed      int
	Total         int
	ProjectStatus journal.Status
}

// DaemonRow holds data for one row in the daemons status table.
// All string fields are sanitised (control characters stripped) by the
// collector before reaching the renderer.
type DaemonRow struct {
	ID        string        // devbox.daemon.id label (e.g. services.main.queue)
	Params    string        // params JSON or pretty key=value summary
	Container string        // docker container name
	Uptime    time.Duration // time since container start
	StartedAt time.Time     // raw container start time
}

// GitWorkspaceRow holds data for one row in the git workspace status table.
// Service is the devbox service name; Dir is the configured directory.
// When the service directory has no own `.git` (boundary check fails),
// Branch / SHA / AheadBehind are empty, Dirty is false, and Err is nil
// (the normal "service has no own repo" case — rendered as blank cells).
// When something actually went wrong (dir missing, shellout failure),
// Err is non-nil so the caller can count and emit a single warning.
type GitWorkspaceRow struct {
	Service     string
	Dir         string
	Branch      string
	SHA         string
	Dirty       bool
	AheadBehind string
	Err         error
}
