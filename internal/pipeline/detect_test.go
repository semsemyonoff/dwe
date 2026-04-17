package pipeline

import (
	"bytes"
	"strings"
	"testing"

	"devbox-cli/internal/render"
)

// --- isCapableTTYWith ---

// fakeIsTTY returns a function that always returns the given value.
func fakeIsTTY(v bool) func(uintptr) bool {
	return func(_ uintptr) bool { return v }
}

// mapGetenv returns a getenv function backed by m.
func mapGetenv(m map[string]string) func(string) string {
	return func(key string) string {
		return m[key]
	}
}

func TestIsCapableTTYWith_NotTTY(t *testing.T) {
	// stdin/stdout/stderr not TTY → always false regardless of env
	got := isCapableTTYWith(fakeIsTTY(false), mapGetenv(nil))
	if got {
		t.Error("expected false when file descriptors are not TTYs")
	}
}

func TestIsCapableTTYWith_DumbTerm(t *testing.T) {
	got := isCapableTTYWith(fakeIsTTY(true), mapGetenv(map[string]string{
		"TERM": "dumb",
	}))
	if got {
		t.Error("expected false when TERM=dumb")
	}
}

func TestIsCapableTTYWith_CI(t *testing.T) {
	ciVars := []struct {
		name  string
		value string
	}{
		{"CI", "true"},
		{"GITHUB_ACTIONS", "true"},
		{"JENKINS_URL", "http://jenkins.example.com"},
		{"BUILDKITE", "true"},
		{"GITLAB_CI", "true"},
	}
	for _, cv := range ciVars {
		t.Run(cv.name, func(t *testing.T) {
			got := isCapableTTYWith(fakeIsTTY(true), mapGetenv(map[string]string{
				cv.name: cv.value,
			}))
			if got {
				t.Errorf("expected false when %s=%s", cv.name, cv.value)
			}
		})
	}
}

func TestIsCapableTTYWith_CapableTTY(t *testing.T) {
	// All conditions met: TTY + no dumb + no CI vars
	got := isCapableTTYWith(fakeIsTTY(true), mapGetenv(nil))
	if !got {
		t.Error("expected true when TTY and no CI/dumb-term vars set")
	}
}

func TestIsCapableTTYWith_CIValueEmpty(t *testing.T) {
	// CI var present but empty string → should not block
	got := isCapableTTYWith(fakeIsTTY(true), mapGetenv(map[string]string{
		"CI": "",
	}))
	if !got {
		t.Error("expected true when CI is set to empty string")
	}
}

func TestIsCapableTTYWith_TermNotDumb(t *testing.T) {
	// TERM set to something other than "dumb"
	got := isCapableTTYWith(fakeIsTTY(true), mapGetenv(map[string]string{
		"TERM": "xterm-256color",
	}))
	if !got {
		t.Error("expected true when TERM=xterm-256color")
	}
}

// --- ParseUIMode ---

func TestParseUIMode_Valid(t *testing.T) {
	cases := []struct {
		input string
		want  UIMode
	}{
		{"auto", UIModeAuto},
		{"plain", UIModePlain},
		{"tui", UIModeTUI},
	}
	for _, tc := range cases {
		got, err := ParseUIMode(tc.input)
		if err != nil {
			t.Errorf("ParseUIMode(%q): unexpected error: %v", tc.input, err)
		}
		if got != tc.want {
			t.Errorf("ParseUIMode(%q): got %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestParseUIMode_Invalid(t *testing.T) {
	invalids := []string{"", "TUI", "PLAIN", "interactive", "fancy", "0"}
	for _, s := range invalids {
		_, err := ParseUIMode(s)
		if err == nil {
			t.Errorf("ParseUIMode(%q): expected error, got nil", s)
		}
		if !strings.Contains(err.Error(), "--ui mode") {
			t.Errorf("ParseUIMode(%q): error should mention --ui mode, got: %v", s, err)
		}
	}
}

// --- NewReporter ---

func TestNewReporter_Plain(t *testing.T) {
	buf := &bytes.Buffer{}
	w := render.NewWriter(buf)
	rep := NewReporter(UIModePlain, w, nil)
	if _, ok := rep.(*PlainReporter); !ok {
		t.Errorf("NewReporter(plain): expected *PlainReporter, got %T", rep)
	}
	if buf.Len() != 0 {
		t.Errorf("NewReporter(plain): should not emit warnings, got: %q", buf.String())
	}
}

func TestNewReporter_Auto(t *testing.T) {
	// In the test environment stdout is not a TTY, so auto falls back to plain.
	buf := &bytes.Buffer{}
	w := render.NewWriter(buf)
	rep := NewReporter(UIModeAuto, w, nil)
	if _, ok := rep.(*PlainReporter); !ok {
		t.Errorf("NewReporter(auto): expected *PlainReporter in non-TTY, got %T", rep)
	}
	if buf.Len() != 0 {
		t.Errorf("NewReporter(auto): should not emit warnings in non-capable TTY, got: %q", buf.String())
	}
}

func TestNewReporter_TUI_FallbackWithWarning(t *testing.T) {
	// In test environment, stdout is not a TTY, so tui mode should warn and fall back.
	buf := &bytes.Buffer{}
	w := render.NewWriter(buf)
	rep := NewReporter(UIModeTUI, w, nil)
	if _, ok := rep.(*PlainReporter); !ok {
		t.Errorf("NewReporter(tui) fallback: expected *PlainReporter, got %T", rep)
	}
	// Should have emitted a warning about fallback.
	got := buf.String()
	if !strings.Contains(got, "not capable") {
		t.Errorf("NewReporter(tui) fallback: expected warning about terminal not capable, got: %q", got)
	}
}

// --- Interface compliance ---

// Verify UIMode constants have the correct string values.
func TestUIMode_Constants(t *testing.T) {
	if string(UIModeAuto) != "auto" {
		t.Errorf("UIModeAuto should be %q, got %q", "auto", UIModeAuto)
	}
	if string(UIModePlain) != "plain" {
		t.Errorf("UIModePlain should be %q, got %q", "plain", UIModePlain)
	}
	if string(UIModeTUI) != "tui" {
		t.Errorf("UIModeTUI should be %q, got %q", "tui", UIModeTUI)
	}
}
