package bridgeclient

import (
	"os"
	"slices"
	"testing"
)

func TestStripEnv_ForwardsBridgeService(t *testing.T) {
	in := []string{"DWE_BRIDGE_SERVICE=main", "DWE_BRIDGE_PROJECT=p", "TERM=xterm"}
	got := StripEnv(in)
	want := []string{"DWE_BRIDGE_SERVICE=main", "TERM=xterm"}
	if !slices.Equal(got, want) {
		t.Errorf("StripEnv(%v) = %v, want %v (DWE_BRIDGE_SERVICE is host-consumed, must pass)", in, got, want)
	}
}

func TestInContainer(t *testing.T) {
	t.Setenv(EnvInvokedFrom, InvokedFromContainer)
	if !InContainer() {
		t.Error("InContainer() = false with DWE_INVOKED_FROM=container")
	}
	t.Setenv(EnvInvokedFrom, "host")
	if InContainer() {
		t.Error("InContainer() = true with DWE_INVOKED_FROM=host")
	}
	_ = os.Unsetenv(EnvInvokedFrom)
	if InContainer() {
		t.Error("InContainer() = true with the variable unset")
	}
}

func TestCallingService(t *testing.T) {
	t.Setenv(EnvBridgeService, "admin")
	if got := CallingService(); got != "admin" {
		t.Errorf("CallingService() = %q, want %q", got, "admin")
	}
	_ = os.Unsetenv(EnvBridgeService)
	if got := CallingService(); got != "" {
		t.Errorf("CallingService() = %q, want empty when unset", got)
	}
}

func TestForceColorEnv(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "appends force and truecolor when undecided",
			in:   []string{"TERM=xterm", "HOME=/home/u"},
			want: []string{"TERM=xterm", "HOME=/home/u", "CLICOLOR_FORCE=1", "COLORTERM=truecolor"},
		},
		{
			name: "explicit container COLORTERM is respected",
			in:   []string{"TERM=xterm", "COLORTERM=ansi256"},
			want: []string{"TERM=xterm", "COLORTERM=ansi256", "CLICOLOR_FORCE=1"},
		},
		{
			name: "NO_COLOR wins unchanged",
			in:   []string{"NO_COLOR=1", "TERM=xterm"},
			want: []string{"NO_COLOR=1", "TERM=xterm"},
		},
		{
			name: "NO_COLOR presence counts even when empty",
			in:   []string{"NO_COLOR=", "TERM=xterm"},
			want: []string{"NO_COLOR=", "TERM=xterm"},
		},
		{
			name: "explicit CLICOLOR_FORCE wins unchanged",
			in:   []string{"CLICOLOR_FORCE=0"},
			want: []string{"CLICOLOR_FORCE=0"},
		},
		{
			name: "empty env",
			in:   []string{},
			want: []string{"CLICOLOR_FORCE=1", "COLORTERM=truecolor"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ForceColorEnv(slices.Clone(tt.in)); !slices.Equal(got, tt.want) {
				t.Errorf("ForceColorEnv(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// stubTTY pins the stdout tty probe for one test.
func stubTTY(t *testing.T, isTTY bool) {
	t.Helper()
	restore := stdoutIsTerminal
	stdoutIsTerminal = func() bool { return isTTY }
	t.Cleanup(func() { stdoutIsTerminal = restore })
}

// clearColorEnv guarantees NO_COLOR / CLICOLOR_FORCE are absent for the test
// (t.Setenv registers the restore; Unsetenv leaves them unset meanwhile).
func clearColorEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"NO_COLOR", "CLICOLOR_FORCE"} {
		t.Setenv(name, "x")
		_ = os.Unsetenv(name)
	}
}

func TestSetStdinTTYEnv(t *testing.T) {
	t.Run("appends the probed truth", func(t *testing.T) {
		got := SetStdinTTYEnv([]string{"TERM=xterm"}, true)
		want := []string{"TERM=xterm", EnvBridgeStdinTTY + "=1"}
		if !slices.Equal(got, want) {
			t.Errorf("SetStdinTTYEnv = %v, want %v", got, want)
		}
	})
	t.Run("drops a spoofed value on piped stdin", func(t *testing.T) {
		got := SetStdinTTYEnv([]string{EnvBridgeStdinTTY + "=1", "TERM=xterm"}, false)
		want := []string{"TERM=xterm"}
		if !slices.Equal(got, want) {
			t.Errorf("SetStdinTTYEnv = %v, want %v", got, want)
		}
	})
	t.Run("replaces a stale value with the probe", func(t *testing.T) {
		got := SetStdinTTYEnv([]string{EnvBridgeStdinTTY + "=0"}, true)
		want := []string{EnvBridgeStdinTTY + "=1"}
		if !slices.Equal(got, want) {
			t.Errorf("SetStdinTTYEnv = %v, want %v", got, want)
		}
	})
}

// stubStdinTTY pins the stdin tty probe for one test.
func stubStdinTTY(t *testing.T, isTTY bool) {
	t.Helper()
	restore := stdinIsTerminal
	stdinIsTerminal = func() bool { return isTTY }
	t.Cleanup(func() { stdinIsTerminal = restore })
}

func TestOptionsFromEnv_StdinTTYSignal(t *testing.T) {
	t.Run("tty stdin sets the signal", func(t *testing.T) {
		stubTTY(t, false)
		stubStdinTTY(t, true)
		opts := OptionsFromEnv(nil, nil)
		if !slices.Contains(opts.Env, EnvBridgeStdinTTY+"=1") {
			t.Error("tty stdin must add the stdin-tty signal")
		}
	})
	t.Run("piped stdin clears the signal", func(t *testing.T) {
		stubTTY(t, false)
		stubStdinTTY(t, false)
		t.Setenv(EnvBridgeStdinTTY, "1") // a spoofed inherited value
		opts := OptionsFromEnv(nil, nil)
		if slices.Contains(opts.Env, EnvBridgeStdinTTY+"=1") {
			t.Error("piped stdin must clear any inherited stdin-tty signal")
		}
	})
}

func TestOptionsFromEnv_ColorForcing(t *testing.T) {
	t.Run("tty stdout requests color", func(t *testing.T) {
		clearColorEnv(t)
		stubTTY(t, true)
		opts := OptionsFromEnv(nil, nil)
		if !slices.Contains(opts.Env, "CLICOLOR_FORCE=1") {
			t.Error("tty stdout must add CLICOLOR_FORCE=1 to the forwarded env")
		}
	})
	t.Run("piped stdout stays plain", func(t *testing.T) {
		clearColorEnv(t)
		stubTTY(t, false)
		opts := OptionsFromEnv(nil, nil)
		if slices.Contains(opts.Env, "CLICOLOR_FORCE=1") {
			t.Error("piped stdout must not force color")
		}
	})
	t.Run("container NO_COLOR wins over tty", func(t *testing.T) {
		clearColorEnv(t)
		t.Setenv("NO_COLOR", "1")
		stubTTY(t, true)
		opts := OptionsFromEnv(nil, nil)
		if slices.Contains(opts.Env, "CLICOLOR_FORCE=1") {
			t.Error("NO_COLOR in the container env must suppress color forcing")
		}
	})
}
