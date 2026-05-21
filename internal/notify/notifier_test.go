package notify

import (
	"context"
	"io"
	"os"
	"testing"

	"devbox-cli/internal/ui"
	"devbox-cli/internal/userconfig"
)

// recordingBackend captures every notify call for assertions.
type recordingBackend struct {
	events []Event
}

func (r *recordingBackend) notify(_ context.Context, ev Event) {
	r.events = append(r.events, ev)
}

// backendForTest exposes the internal backend for type assertions in
// tests living in this package.
func (n *Notifier) backendForTest() backend { return n.backend }

func (n *Notifier) enabledForTest() bool { return n.enabled }

// withInteractive forces isInteractiveForNotify to v for the duration of t.
func withInteractive(t *testing.T, v bool) {
	t.Helper()
	prev := isInteractiveForNotify
	isInteractiveForNotify = func() bool { return v }
	t.Cleanup(func() { isInteractiveForNotify = prev })
}

func enabledCfg() *userconfig.Config { return userconfig.Defaults() }

func TestNew_NilConfig_ReturnsNoop(t *testing.T) {
	withInteractive(t, true)
	n := New(nil)
	if n.enabled {
		t.Fatalf("expected disabled notifier for nil cfg")
	}
	if _, ok := n.backend.(noopBackend); !ok {
		t.Fatalf("expected noopBackend, got %T", n.backend)
	}
}

func TestNew_MasterSwitchOff_ReturnsNoop(t *testing.T) {
	withInteractive(t, true)
	cfg := enabledCfg()
	cfg.NotifyEnabled = false
	n := New(cfg)
	if n.enabled {
		t.Fatalf("expected disabled notifier when master off")
	}
	if _, ok := n.backend.(noopBackend); !ok {
		t.Fatalf("expected noopBackend, got %T", n.backend)
	}
}

func TestNew_EmptyChannels_ReturnsNoop(t *testing.T) {
	withInteractive(t, true)
	cfg := enabledCfg()
	cfg.NotifyChannels = nil
	n := New(cfg)
	if n.enabled {
		t.Fatalf("expected disabled when no channels")
	}
}

func TestNew_NonInteractive_ReturnsNoop(t *testing.T) {
	withInteractive(t, false)
	n := New(enabledCfg())
	if n.enabled {
		t.Fatalf("expected disabled in non-interactive env")
	}
}

func TestNew_OnlyUnknownChannels_ReturnsNoop(t *testing.T) {
	withInteractive(t, true)
	cfg := enabledCfg()
	cfg.NotifyChannels = []string{"telegram", "webhook"}
	n := New(cfg)
	if n.enabled {
		t.Fatalf("expected disabled when no recognised channels; backend=%T", n.backend)
	}
	if _, ok := n.backend.(noopBackend); !ok {
		t.Fatalf("expected noopBackend, got %T", n.backend)
	}
}

func TestNew_NativeChannel_ReturnsNative(t *testing.T) {
	withInteractive(t, true)
	n := New(enabledCfg())
	if !n.enabled {
		t.Fatalf("expected enabled notifier")
	}
	if _, ok := n.backend.(*nativeBackend); !ok {
		t.Fatalf("expected nativeBackend, got %T", n.backend)
	}
}

func TestNew_NativeAmongUnknown_PicksNative(t *testing.T) {
	withInteractive(t, true)
	cfg := enabledCfg()
	cfg.NotifyChannels = []string{"telegram", "native"}
	n := New(cfg)
	if !n.enabled {
		t.Fatalf("expected enabled when native is present")
	}
	if _, ok := n.backend.(*nativeBackend); !ok {
		t.Fatalf("expected nativeBackend, got %T", n.backend)
	}
}

func TestNotify_NilReceiver_NoPanic(t *testing.T) {
	var n *Notifier
	n.Notify(context.Background(), Event{Kind: OpDeploy})
}

func TestNotify_DisabledNotifier_DoesNotCallBackend(t *testing.T) {
	rec := &recordingBackend{}
	n := &Notifier{cfg: enabledCfg(), backend: rec, enabled: false}
	n.Notify(context.Background(), Event{Kind: OpDeploy, Outcome: OutcomeSuccess})
	if len(rec.events) != 0 {
		t.Fatalf("expected no events, got %d", len(rec.events))
	}
}

func TestNotify_PerOpGate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(c *userconfig.Config)
		kind    Op
		wantHit bool
	}{
		{"run-off blocks OpRun", func(c *userconfig.Config) { c.NotifyRunEnabled = false }, OpRun, false},
		{"run-on permits OpRun", func(_ *userconfig.Config) {}, OpRun, true},
		{"deploy-off blocks OpDeploy", func(c *userconfig.Config) { c.NotifyDeployEnabled = false }, OpDeploy, false},
		{"deploy-on permits OpDeploy", func(_ *userconfig.Config) {}, OpDeploy, true},
		{"command-off blocks OpCommand", func(c *userconfig.Config) { c.NotifyCommandsEnabled = false }, OpCommand, false},
		{"command-on permits OpCommand", func(_ *userconfig.Config) {}, OpCommand, true},
		{"OpUnknown is dropped", func(_ *userconfig.Config) {}, OpUnknown, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := enabledCfg()
			tc.mutate(cfg)
			rec := &recordingBackend{}
			n := &Notifier{cfg: cfg, backend: rec, enabled: true}
			n.Notify(context.Background(), Event{Kind: tc.kind})
			got := len(rec.events) == 1
			if got != tc.wantHit {
				t.Fatalf("hit=%v want=%v", got, tc.wantHit)
			}
		})
	}
}

func TestIsInteractiveForNotify_EnvVars(t *testing.T) {
	prevUI := ui.IsInteractiveFn
	ui.IsInteractiveFn = func(io.Reader) bool { return true }
	t.Cleanup(func() { ui.IsInteractiveFn = prevUI })

	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"clean env, TTY", nil, true},
		{"CI=1 disables", map[string]string{"CI": "1"}, false},
		{"DEVBOX_NONINTERACTIVE disables", map[string]string{"DEVBOX_NONINTERACTIVE": "1"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for k := range map[string]string{"CI": "", "DEVBOX_NONINTERACTIVE": ""} {
				t.Setenv(k, "")
				_ = os.Unsetenv(k)
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if got := isInteractiveForNotify(); got != tc.want {
				t.Fatalf("got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestIsInteractiveForNotify_NoTTY(t *testing.T) {
	prevUI := ui.IsInteractiveFn
	ui.IsInteractiveFn = func(io.Reader) bool { return false }
	t.Cleanup(func() { ui.IsInteractiveFn = prevUI })
	t.Setenv("CI", "")
	t.Setenv("DEVBOX_NONINTERACTIVE", "")
	if isInteractiveForNotify() {
		t.Fatalf("expected non-interactive when stdout is not a TTY")
	}
}

func TestOutcomeFromErr(t *testing.T) {
	if OutcomeFromErr(nil) != OutcomeSuccess {
		t.Fatalf("nil err should map to OutcomeSuccess")
	}
	if OutcomeFromErr(io.EOF) != OutcomeFailure {
		t.Fatalf("non-nil err should map to OutcomeFailure")
	}
}

func TestOp_configKey(t *testing.T) {
	cases := map[Op]string{
		OpUnknown: "",
		OpDeploy:  "deploy",
		OpRun:     "run",
		OpCommand: "command",
	}
	for k, want := range cases {
		if got := k.configKey(); got != want {
			t.Fatalf("Op(%d).configKey()=%q want %q", k, got, want)
		}
	}
}

// Sanity: backendForTest / enabledForTest accessors used by other tests
// must work on an enabled Notifier built via New.
func TestBackendForTest(t *testing.T) {
	withInteractive(t, true)
	n := New(enabledCfg())
	if !n.enabledForTest() {
		t.Fatalf("expected enabled=true")
	}
	if _, ok := n.backendForTest().(*nativeBackend); !ok {
		t.Fatalf("expected nativeBackend via backendForTest, got %T", n.backendForTest())
	}
}
