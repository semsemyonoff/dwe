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

		// not a repo
		{"prompt/not-repo", notRepo, ModePrompt, true, ActionSkip, true},
		{"auto/not-repo", notRepo, ModeAuto, true, ActionSkip, true},

		// dirty worktree → warn regardless of mode
		{"prompt/dirty", dirty, ModePrompt, true, ActionWarn, false},
		{"auto/dirty", dirty, ModeAuto, true, ActionWarn, false},
		{"check/dirty", dirty, ModeCheck, true, ActionWarn, false},

		// no upstream → warn
		{"prompt/no-upstream", noUpstream, ModePrompt, true, ActionWarn, false},
		{"auto/no-upstream", noUpstream, ModeAuto, true, ActionWarn, false},

		// fetch failed → warn
		{"prompt/fetch-failed", fetchFailed, ModePrompt, true, ActionWarn, false},
		{"auto/fetch-failed", fetchFailed, ModeAuto, true, ActionWarn, false},

		// up to date → skip
		{"prompt/up-to-date", clean, ModePrompt, true, ActionSkip, true},
		{"auto/up-to-date", clean, ModeAuto, true, ActionSkip, true},

		// behind, mode=auto → pull
		{"auto/behind", behind, ModeAuto, true, ActionPullAuto, true},
		{"auto/behind-ci", behind, ModeAuto, false, ActionPullAuto, true},

		// behind, mode=prompt+TTY → prompt
		{"prompt/behind-tty", behind, ModePrompt, true, ActionPullPrompt, false},
		// behind, mode=prompt+CI → warn
		{"prompt/behind-ci", behind, ModePrompt, false, ActionWarn, false},

		// behind, mode=check → warn
		{"check/behind", behind, ModeCheck, true, ActionWarn, false},

		// diverged → warn
		{"prompt/diverged", diverged, ModePrompt, true, ActionWarn, false},
		{"auto/diverged", diverged, ModeAuto, true, ActionWarn, false},
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
