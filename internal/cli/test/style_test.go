package test

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/semsemyonoff/dwe/internal/core/workflow/envtest"
)

// forceTrueColor forces the global lipgloss color profile to TrueColor for the
// duration of a test so styles.*.Render actually emits ANSI (the test process's
// stdout is not a TTY, so the default profile would render plain). Mirrors the
// pattern in internal/core/ui/styles/styles_test.go. Do not run these tests with
// t.Parallel — the profile is global.
func forceTrueColor(t *testing.T) {
	t.Helper()
	saved := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(saved) })
}

func TestStyledWarning_PlainIsByteIdentical(t *testing.T) {
	if got := styledWarning("smoke", "port in use", false); got != "[smoke] warning: port in use" {
		t.Errorf("tagged plain = %q", got)
	}
	if got := styledWarning("", "port in use", false); got != "warning: port in use" {
		t.Errorf("untagged plain = %q", got)
	}
}

func TestStyledWarning_ColoredCarriesANSIButSameText(t *testing.T) {
	forceTrueColor(t)
	got := styledWarning("smoke", "port in use", true)
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("colored warning must carry ANSI, got %q", got)
	}
	if stripANSI(got) != "[smoke] warning: port in use" {
		t.Errorf("visible text changed: %q", stripANSI(got))
	}
}

func TestRenderScenarioLine_PlainByteIdentity(t *testing.T) {
	// The historical plain form must be preserved exactly (no glyph, no color).
	cases := []struct {
		o    scenarioOutcome
		keep bool
		want string
	}{
		{
			o:    scenarioOutcome{Name: "smoke", Status: envtest.StatusPassed, Duration: 2 * time.Second},
			want: "smoke: passed [2s]",
		},
		{
			o:    scenarioOutcome{Name: "api", Status: envtest.StatusFailed, FailedStep: "app answers", Duration: 3 * time.Second},
			want: `api: failed — step "app answers" [3s]`,
		},
		{
			o:    scenarioOutcome{Name: "prep", Status: envtest.StatusError, Message: "flock held", Duration: 0},
			want: "prep: error (flock held) [0s]",
		},
	}
	for _, c := range cases {
		if got := renderScenarioLine(c.o, c.keep, false); got != c.want {
			t.Errorf("plain line = %q, want %q", got, c.want)
		}
	}
}

func TestRenderScenarioLine_ColoredGlyphAndContent(t *testing.T) {
	forceTrueColor(t)

	pass := renderScenarioLine(scenarioOutcome{Name: "smoke", Status: envtest.StatusPassed, Duration: time.Second}, false, true)
	if !strings.HasPrefix(stripANSI(pass), glyphPass+" ") {
		t.Errorf("passed line must start with %q glyph, got %q", glyphPass, stripANSI(pass))
	}
	if stripANSI(pass) != glyphPass+" smoke: passed [1s]" {
		t.Errorf("passed visible text = %q", stripANSI(pass))
	}

	fail := renderScenarioLine(scenarioOutcome{Name: "api", Status: envtest.StatusFailed, FailedStep: "app answers", Duration: time.Second}, false, true)
	if !strings.HasPrefix(stripANSI(fail), glyphFail+" ") {
		t.Errorf("failed line must start with %q glyph, got %q", glyphFail, stripANSI(fail))
	}
	if !strings.Contains(fail, "\x1b[") {
		t.Errorf("colored line must carry ANSI, got %q", fail)
	}
}

func TestBuildSummary_ColoredSameTextAsPlain(t *testing.T) {
	forceTrueColor(t)
	outcomes := []scenarioOutcome{
		{Name: "a", Status: envtest.StatusPassed},
		{Name: "b", Status: envtest.StatusFailed, FailedStep: "x"},
	}
	plain := buildSummary(outcomes, false)
	colored := buildSummary(outcomes, true)
	if plain != `1 passed, 1 failed (b: step "x")` {
		t.Fatalf("plain summary = %q", plain)
	}
	if !strings.Contains(colored, "\x1b[") {
		t.Errorf("colored summary must carry ANSI, got %q", colored)
	}
	if stripANSI(colored) != plain {
		t.Errorf("colored visible text %q != plain %q", stripANSI(colored), plain)
	}
}

func TestRenderTestListText_Colored(t *testing.T) {
	forceTrueColor(t)
	data := testListJSON{Scenarios: []testListEntryJSON{
		{Name: "smoke-deploy", Description: "full stack"},
		{Name: "bare"},
	}}
	plain := renderTestListText(data, false)
	if plain != "smoke-deploy             full stack\nbare" {
		t.Fatalf("plain list = %q", plain)
	}
	colored := renderTestListText(data, true)
	if !strings.Contains(colored, "\x1b[") {
		t.Errorf("colored list must carry ANSI, got %q", colored)
	}
	if stripANSI(colored) != plain {
		t.Errorf("colored visible text %q != plain %q", stripANSI(colored), plain)
	}
}

func TestRenderTestCleanText_PlainByteIdentityAndColored(t *testing.T) {
	data := testCleanJSON{
		Swept:   []cleanEntryJSON{{Scenario: "smoke", ComposeProject: "dwe-t-smoke-abc"}},
		Skipped: []cleanEntryJSON{{Scenario: "api", Reason: "flock held"}},
		Failed:  []cleanEntryJSON{{Scenario: "web", Error: "teardown failed"}},
	}
	wantPlain := strings.Join([]string{
		"smoke: swept (dwe-t-smoke-abc)",
		"api: skipped (flock held)",
		"web: failed (teardown failed)",
		"",
		"1 swept, 1 skipped, 1 failed, 0 orphan(s)",
	}, "\n")
	if got := renderTestCleanText(data, false); got != wantPlain {
		t.Fatalf("plain clean = %q, want %q", got, wantPlain)
	}

	forceTrueColor(t)
	colored := renderTestCleanText(data, true)
	if !strings.Contains(colored, glyphPass) || !strings.Contains(colored, glyphFail) {
		t.Errorf("colored clean must carry glyphs, got %q", stripANSI(colored))
	}
	if stripANSI(colored) != strings.Join([]string{
		glyphPass + " smoke: swept (dwe-t-smoke-abc)",
		glyphInfo + " api: skipped (flock held)",
		glyphFail + " web: failed (teardown failed)",
		"",
		"1 swept, 1 skipped, 1 failed, 0 orphan(s)",
	}, "\n") {
		t.Errorf("colored visible text = %q", stripANSI(colored))
	}
}

func TestRenderTestCleanText_DryRunPlain(t *testing.T) {
	data := testCleanJSON{DryRun: true, Swept: []cleanEntryJSON{{Scenario: "smoke", ComposeProject: "p"}}}
	want := "smoke: would sweep (p)\n\n1 would sweep, 0 skipped, 0 failed, 0 orphan(s)"
	if got := renderTestCleanText(data, false); got != want {
		t.Errorf("dry-run plain = %q, want %q", got, want)
	}
}
