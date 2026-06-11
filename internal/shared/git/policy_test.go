package git

import "testing"

func TestDecide(t *testing.T) {
	clean := Status{IsRepo: true, HasUpstream: true, FetchOK: true}
	dirty := Status{IsRepo: true, HasUpstream: true, FetchOK: true, Dirty: true}
	noUpstream := Status{IsRepo: true, HasUpstream: false}
	fetchFailed := Status{IsRepo: true, HasUpstream: true, FetchOK: false, FetchErr: "connection refused"}
	behind := Status{IsRepo: true, HasUpstream: true, FetchOK: true, Behind: 3}
	diverged := Status{IsRepo: true, HasUpstream: true, FetchOK: true, Behind: 2, Ahead: 1}
	notRepo := Status{IsRepo: false}

	tests := []struct {
		name          string
		status        Status
		mode          UpdateMode
		isInteractive bool
		wantAction    Action
		wantMsgEmpty  bool
	}{
		// mode=off → always skip, no message
		{"off/not-repo", notRepo, ModeOff, true, ActionSkip, true},
		{"off/clean-behind", behind, ModeOff, true, ActionSkip, true},
		{"off/dirty", dirty, ModeOff, true, ActionSkip, true},
		{"off/behind-ci", behind, ModeOff, false, ActionSkip, true},

		// not a repo → skip
		{"on/not-repo", notRepo, ModeOn, true, ActionSkip, true},

		// dirty worktree → warn
		{"on/dirty", dirty, ModeOn, true, ActionWarn, false},

		// no upstream → warn
		{"on/no-upstream", noUpstream, ModeOn, true, ActionWarn, false},

		// fetch failed → warn
		{"on/fetch-failed", fetchFailed, ModeOn, true, ActionWarn, false},

		// up to date → skip
		{"on/up-to-date", clean, ModeOn, true, ActionSkip, true},

		// behind, mode=on + TTY → prompt
		{"on/behind-tty", behind, ModeOn, true, ActionPullPrompt, false},
		// behind, mode=on + CI → warn
		{"on/behind-ci", behind, ModeOn, false, ActionWarn, false},

		// diverged → warn
		{"on/diverged", diverged, ModeOn, true, ActionWarn, false},
		{"on/diverged-ci", diverged, ModeOn, false, ActionWarn, false},

		// unknown/legacy mode string (e.g. a stale "auto"/"prompt" surviving an old
		// config) is neither ModeOn nor ModeOff → falls through to the safe default
		// skip rather than pulling unprompted.
		{"unknown/behind", behind, UpdateMode("auto"), true, ActionSkip, true},
		{"unknown/clean", clean, UpdateMode("prompt"), true, ActionSkip, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action, msg := Decide(tt.status, tt.mode, tt.isInteractive)
			if action != tt.wantAction {
				t.Errorf("action=%v, want %v", action, tt.wantAction)
			}
			if tt.wantMsgEmpty && msg != "" {
				t.Errorf("expected empty message, got %q", msg)
			}
			if !tt.wantMsgEmpty && msg == "" {
				t.Errorf("expected non-empty message for action=%v", action)
			}
		})
	}
}
