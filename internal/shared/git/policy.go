package git

import "fmt"

// UpdateMode controls how the update probe behaves.
type UpdateMode string

// Update mode constants.
const (
	ModeOn  UpdateMode = "on"
	ModeOff UpdateMode = "off"
)

// Action is the decision made by Decide.
type Action int

const (
	// ActionSkip means no update action should be taken (silent).
	ActionSkip Action = iota
	// ActionWarn means the user should be warned but no pull performed.
	ActionWarn
	// ActionPullPrompt means the user should be asked before pulling.
	ActionPullPrompt
)

// Decide encodes the update mode safety matrix and returns the action to take
// and a human-readable message (empty for ActionSkip).
func Decide(status Status, mode UpdateMode, isInteractive bool) (Action, string) {
	if mode == ModeOff {
		return ActionSkip, ""
	}
	if !status.IsRepo {
		return ActionSkip, ""
	}
	if status.Dirty {
		return ActionWarn, "working tree has uncommitted changes — skipping update"
	}
	if !status.HasUpstream {
		return ActionWarn, "no upstream branch configured — skipping update"
	}
	if !status.FetchOK {
		msg := "could not contact remote"
		if status.FetchErr != "" {
			msg += ": " + status.FetchErr
		}
		return ActionWarn, msg
	}
	// Up to date.
	if status.Behind == 0 {
		return ActionSkip, ""
	}
	// Diverged.
	if status.Ahead > 0 {
		return ActionWarn, "branch has diverged from upstream — skipping update"
	}
	// Behind and clean. mode=on: prompt on a TTY, warn in non-interactive.
	if mode == ModeOn {
		if isInteractive {
			return ActionPullPrompt, fmt.Sprintf("branch is %d commit(s) behind upstream", status.Behind)
		}
		return ActionWarn, fmt.Sprintf("branch is %d commit(s) behind upstream — skipping update in non-interactive mode", status.Behind)
	}
	return ActionSkip, ""
}
