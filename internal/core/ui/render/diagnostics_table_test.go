package render

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/semsemyonoff/dwe/internal/core/validate"
)

func TestRenderDiagnosticsTable(t *testing.T) {
	rows := []DiagnosticRow{
		{
			Severity: validate.SeverityOK,
			Domain:   "config",
			Target:   "config.dwe",
			File:     "workspace.yml",
			Message:  "",
			Hint:     "",
		},
		{
			Severity: validate.SeverityError,
			Domain:   "config",
			Target:   "config.deploy",
			File:     "workspace/deploy.yml",
			Message:  "unknown field \"invalid\"",
			Hint:     "check field name spelling",
		},
		{
			Severity: validate.SeverityWarning,
			Domain:   "templates",
			Target:   "templates.ide:app",
			File:     "",
			Message:  "missing template pack",
			Hint:     "create workspace/templates/ide/app/",
		},
	}

	output := DiagnosticsTable(rows)

	// Verify the output contains expected elements.
	checks := []string{
		"STATUS",
		"DOMAIN",
		"TARGET",
		"FILE",
		"MESSAGE",
		"HINT",
		"config",
		"deploy",
		"templates",
		"unknown field",
		"missing template pack",
	}

	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output missing expected string: %q", check)
		}
	}

	// Verify glyphs appear.
	if !strings.Contains(output, "✓") {
		t.Error("output missing OK glyph ✓")
	}
	if !strings.Contains(output, "✗") {
		t.Error("output missing error glyph ✗")
	}
	if !strings.Contains(output, "⚠") {
		t.Error("output missing warning glyph ⚠")
	}
}

func TestRenderDiagnosticsTable_WrapsLongMessageAndHint(t *testing.T) {
	rows := []DiagnosticRow{
		{
			Severity: validate.SeverityError,
			Domain:   "config",
			Target:   "target",
			File:     "file.yml",
			Message:  "workflow step services.main.database.restore declares a files_gate require entry that references command files but the command registry could not resolve the referenced file id",
			Hint:     "make sure the command file declares the referenced files entry or remove the files_gate require block from this step",
		},
	}

	output := DiagnosticsTable(rows)
	if !strings.Contains(output, "file id") {
		t.Fatalf("rendered output missing wrapped message tail: %q", output)
	}
	for line := range strings.SplitSeq(output, "\n") {
		if got := lipgloss.Width(line); got > 130 {
			t.Fatalf("rendered diagnostics line width = %d, want <= 130: %q", got, line)
		}
	}
}

func TestDiagnosticsByDomain(t *testing.T) {
	rows := []DiagnosticRow{
		{Severity: validate.SeverityError, Domain: "linters", Target: "hadolint", File: "Dockerfile", Message: "pin versions"},
		{Severity: validate.SeverityWarning, Domain: "config", Target: "config.workspace", File: "workspace.yml", Message: "deprecated field"},
		{Severity: validate.SeverityOK, Domain: "config", Target: "config.docker", File: "workspace/docker.yml"},
	}

	output := DiagnosticsByDomain(rows)

	// Per-domain titles are rendered (human labels), not the raw DOMAIN column.
	for _, want := range []string{"Configuration", "Linters", "hadolint", "deprecated field"} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
	// The DOMAIN column header must be gone in the per-domain layout.
	if strings.Contains(output, "DOMAIN") {
		t.Errorf("per-domain output should not contain a DOMAIN column:\n%s", output)
	}
	// config sorts before linters in domainDisplayOrder.
	if i, j := strings.Index(output, "Configuration"), strings.Index(output, "Linters"); i < 0 || j < 0 || i > j {
		t.Errorf("expected Configuration before Linters, got config@%d linters@%d", i, j)
	}
}

func TestDiagnosticsByDomain_Empty(t *testing.T) {
	if got := DiagnosticsByDomain(nil); got != "" {
		t.Errorf("expected empty string for no rows, got %q", got)
	}
}

// TestDiagnosticsByDomain_SharedMode_RecordsWhenAnyDomainDoesNotFit proves the
// mode decision is made once across every domain, not per domain: "config"
// alone would comfortably fit a table at the chosen budget, but "linters"
// carries a HINT URL wide enough to blow its floor past the budget (URLs are
// never split, per isURLToken), so both domains must fall back to records
// rather than pairing a table with a record block.
func TestDiagnosticsByDomain_SharedMode_RecordsWhenAnyDomainDoesNotFit(t *testing.T) {
	resetStyles()
	longURL := "https://example.com/" + strings.Repeat("a", 90)
	rows := []DiagnosticRow{
		{Severity: validate.SeverityOK, Domain: "config", Target: "config.workspace", File: "workspace.yml", Message: "ok"},
		{
			Severity: validate.SeverityError,
			Domain:   "linters",
			Target:   "hadolint",
			File:     "Dockerfile",
			Message:  "pin versions",
			Hint:     longURL,
		},
	}

	withTermWidth(t, 60)
	output := DiagnosticsByDomain(rows)

	if strings.Contains(output, "┌") || strings.Contains(output, "│") {
		t.Errorf("expected both domains to render as records (no table borders), got:\n%s", stripANSI(output))
	}
	for _, want := range []string{"Configuration", "Linters", "config.workspace", "hadolint"} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, stripANSI(output))
		}
	}
	if !strings.Contains(output, longURL) {
		t.Errorf("expected the long hint URL to stay intact, got:\n%s", stripANSI(output))
	}
}

// TestDiagnosticsByDomain_SharedMode_TablesWhenEveryDomainFits is the
// counterpart: when every domain fits the shared budget, every domain renders
// as a table, borders included.
func TestDiagnosticsByDomain_SharedMode_TablesWhenEveryDomainFits(t *testing.T) {
	resetStyles()
	rows := []DiagnosticRow{
		{Severity: validate.SeverityOK, Domain: "config", Target: "config.workspace", File: "workspace.yml", Message: "ok"},
		{Severity: validate.SeverityWarning, Domain: "linters", Target: "hadolint", File: "Dockerfile", Message: "pin versions"},
	}

	withTermWidth(t, 200)
	output := DiagnosticsByDomain(rows)

	if !strings.Contains(output, "│") {
		t.Errorf("expected both domains to render as tables (with borders), got:\n%s", stripANSI(output))
	}
	for _, want := range []string{"Configuration", "Linters", "config.workspace", "pin versions"} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, stripANSI(output))
		}
	}
}

// TestDiagnosticsByDomain_SingleDomain_MatchesDirectRender proves the shared-
// mode decision is a no-op for the single-domain case — it must render
// identically to the domain's own tableView rendered at the same budget,
// both when that budget fits as a table and when it forces records.
func TestDiagnosticsByDomain_SingleDomain_MatchesDirectRender(t *testing.T) {
	rows := []DiagnosticRow{
		{Severity: validate.SeverityError, Domain: "linters", Target: "hadolint", File: "Dockerfile", Message: "pin versions", Hint: "see docs"},
	}

	for _, budget := range []int{0, 200, 20} {
		t.Run(fmt.Sprintf("budget=%d", budget), func(t *testing.T) {
			resetStyles()
			withTermWidth(t, budget)

			got := DiagnosticsByDomain(rows)
			// stdoutBudget, not stderrBudget: it is the budget
			// DiagnosticsByDomain itself resolves. withTermWidth reports the
			// same width for every stream, so the two agree here — building
			// the oracle from the wrong one would silently stop catching a
			// regression to the sibling DiagnosticsTable's stderr sink.
			want := domainTitle("linters", rows) + "\n" + diagnosticsTableView(rows, false).Render(stdoutBudget())

			if got != want {
				t.Errorf("budget %d: DiagnosticsByDomain diverged from direct single-domain render\ngot:\n%s\nwant:\n%s",
					budget, stripANSI(got), stripANSI(want))
			}
		})
	}
}

func TestSortDomainsForDisplay(t *testing.T) {
	domains := []string{"zeta", "linters", "config", "alpha", "commands"}
	sortDomainsForDisplay(domains)
	want := []string{"config", "commands", "linters", "alpha", "zeta"}
	for i := range want {
		if domains[i] != want[i] {
			t.Fatalf("sortDomainsForDisplay = %v, want %v", domains, want)
		}
	}
}

func TestWrapPath(t *testing.T) {
	const width = 20
	got := wrapPath("services/catalog/src/docker/Dockerfile", width)
	for line := range strings.SplitSeq(got, "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Fatalf("wrapped path line width = %d, want <= %d: %q", w, width, line)
		}
	}
	if !strings.Contains(got, "\n") {
		t.Fatalf("expected long path to wrap, got %q", got)
	}
	// Short paths pass through untouched.
	if got := wrapPath("workspace.yml", width); got != "workspace.yml" {
		t.Fatalf("short path should not wrap, got %q", got)
	}
}

func TestFormatDiagnostics_Quiet(t *testing.T) {
	diags := []validate.Diagnostic{
		{Severity: validate.SeverityOK, Domain: "config", Target: "config.dwe"},
		{Severity: validate.SeverityInfo, Domain: "config", Target: "config.services"},
		{Severity: validate.SeverityWarning, Domain: "config", Target: "config.docker"},
		{Severity: validate.SeverityError, Domain: "config", Target: "config.deploy"},
	}

	// Without quiet, all rows returned.
	rows := FormatDiagnostics(diags, false)
	if len(rows) != 4 {
		t.Errorf("expected 4 rows without quiet, got %d", len(rows))
	}

	// With quiet, OK and Info filtered out.
	rows = FormatDiagnostics(diags, true)
	if len(rows) != 2 {
		t.Errorf("expected 2 rows with quiet, got %d", len(rows))
	}
	if rows[0].Severity != validate.SeverityWarning {
		t.Error("expected first row to be warning")
	}
	if rows[1].Severity != validate.SeverityError {
		t.Error("expected second row to be error")
	}
}

func TestWrapDiagnosticText(t *testing.T) {
	longMessage := "workflow step services.main.database.restore declares a files_gate require entry that references command files but the command registry could not resolve the referenced file id"

	wrapped := wrapText(longMessage, diagnosticTextWrapWidth)
	lines := strings.Split(wrapped, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected wrapped text to span multiple lines, got %q", wrapped)
	}
	for _, line := range lines {
		if got := lipgloss.Width(line); got > diagnosticTextWrapWidth {
			t.Fatalf("wrapped line width = %d, want <= %d: %q", got, diagnosticTextWrapWidth, line)
		}
	}
	if !strings.Contains(wrapped, "\n") {
		t.Fatalf("expected newline in wrapped text, got %q", wrapped)
	}
}

func TestWrapDiagnosticText_LongToken(t *testing.T) {
	longToken := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	wrapped := wrapText(longToken, diagnosticTextWrapWidth)
	for line := range strings.SplitSeq(wrapped, "\n") {
		if got := lipgloss.Width(line); got > diagnosticTextWrapWidth {
			t.Fatalf("wrapped line width = %d, want <= %d: %q", got, diagnosticTextWrapWidth, line)
		}
	}
}

func TestWrapDiagnosticText_KeepsURLWhole(t *testing.T) {
	url := "https://github.com/hadolint/hadolint/wiki/DL3008"
	if w := lipgloss.Width(url); w <= diagnosticTextWrapWidth {
		t.Fatalf("test precondition: URL width %d should exceed wrap width %d", w, diagnosticTextWrapWidth)
	}

	// Bare URL stays on a single line.
	if got := wrapText(url, diagnosticTextWrapWidth); strings.Contains(got, "\n") {
		t.Errorf("bare URL must not be split, got:\n%s", got)
	}

	// URL embedded in prose breaks onto its own line but stays intact.
	wrapped := wrapText("see "+url+" for details", diagnosticTextWrapWidth)
	if !strings.Contains(wrapped, url) {
		t.Errorf("URL must survive wrapping intact, got:\n%s", wrapped)
	}
}

func TestDiagnosticsTable_URLDoesNotTouchBorder(t *testing.T) {
	url := "https://github.com/hadolint/hadolint/wiki/DL3008"
	rows := []DiagnosticRow{
		{Severity: validate.SeverityWarning, Domain: "linters", Target: "hadolint", File: "Dockerfile", Message: "pin versions", Hint: url},
	}

	for _, output := range []string{DiagnosticsTable(rows), DiagnosticsByDomain(rows)} {
		if !strings.Contains(output, url) {
			t.Fatalf("URL missing from output:\n%s", output)
		}
		// The cell padding must keep the URL off the border, otherwise a
		// terminal link detector swallows the "│" into the link.
		if strings.Contains(output, url+"│") {
			t.Errorf("URL abuts the border (no padding):\n%s", output)
		}
	}
}

func TestFormatSummary(t *testing.T) {
	tests := []struct {
		name     string
		summary  validate.Summary
		expected string
	}{
		{
			name: "all zeros",
			summary: validate.Summary{
				Errors: 0, Warnings: 0, Infos: 0, OKs: 0,
			},
			expected: "validation skipped (no files found)",
		},
		{
			name: "one error",
			summary: validate.Summary{
				Errors: 1, Warnings: 0, Infos: 0, OKs: 0,
			},
			expected: "1 error",
		},
		{
			name: "multiple errors",
			summary: validate.Summary{
				Errors: 3, Warnings: 0, Infos: 0, OKs: 0,
			},
			expected: "3 errors",
		},
		{
			name: "one warning",
			summary: validate.Summary{
				Errors: 0, Warnings: 1, Infos: 0, OKs: 0,
			},
			expected: "1 warning",
		},
		{
			name: "mixed",
			summary: validate.Summary{
				Errors: 1, Warnings: 2, Infos: 0, OKs: 5,
			},
			expected: "1 error, 2 warnings, 5 checks",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatSummary(tt.summary)
			if !strings.Contains(result, tt.expected) {
				t.Errorf("expected %q in result, got: %q", tt.expected, result)
			}
		})
	}
}

func TestSeverityGlyph(t *testing.T) {
	tests := []struct {
		severity validate.Severity
		glyph    string
	}{
		{validate.SeverityOK, "✓"},
		{validate.SeverityInfo, "ⓘ"},
		{validate.SeverityWarning, "⚠"},
		{validate.SeverityError, "✗"},
	}

	for _, tt := range tests {
		t.Run(tt.glyph, func(t *testing.T) {
			g := severityGlyph(tt.severity)
			if g != tt.glyph {
				t.Errorf("expected %q, got %q", tt.glyph, g)
			}
		})
	}
}
