package statusview

import (
	"time"

	"devbox-cli/internal/deploy/journal"
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
