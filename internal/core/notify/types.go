// Package notify fires native desktop notifications when long-running
// DWE operations finish. The public surface is intentionally small:
// one concrete *Notifier returned by New, one Notify method, and the
// Event / Op / Outcome value types. The backend abstraction is
// unexported and lives inside the package.
package notify

import "time"

// Op identifies which DWE operation produced an Event. The zero value
// OpUnknown lets tests catch unset-Kind bugs.
type Op int

// Operation kinds.
const (
	OpUnknown Op = iota
	OpDeploy
	OpRun
	OpCommand
)

// configKey returns the string key used by userpkg.Config.NotifyEnabledFor.
// The string-keyed form keeps userconfig from importing notify.
func (k Op) configKey() string {
	switch k {
	case OpDeploy:
		return "deploy"
	case OpRun:
		return "run"
	case OpCommand:
		return "command"
	default:
		return ""
	}
}

// Outcome is the success/failure result of the operation that produced
// the Event.
type Outcome int

// Outcome values.
const (
	OutcomeUnknown Outcome = iota
	OutcomeSuccess
	OutcomeFailure
)

// Event carries everything the backend needs to render a notification.
// Operation is a human-readable label (e.g. "deploy", "run",
// "command:db.migrate") used in the title; Project is the DWE
// project name from cfg.Project.Name (may be empty when main config
// failed to load).
type Event struct {
	Kind      Op
	Operation string
	Outcome   Outcome
	Duration  time.Duration
	Err       error
	Project   string
}

// OutcomeFromErr maps a Go error to an Outcome. Shared by all
// hookpoints so they format the defer identically.
func OutcomeFromErr(err error) Outcome {
	if err == nil {
		return OutcomeSuccess
	}
	return OutcomeFailure
}
