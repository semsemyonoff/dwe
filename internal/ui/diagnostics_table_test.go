package ui

import (
	"strings"
	"testing"

	"devbox-cli/internal/validate"
)

func TestRenderDiagnosticsTable(t *testing.T) {
	rows := []DiagnosticRow{
		{
			Severity: validate.SeverityOK,
			Domain:   "config",
			Target:   "config.devbox",
			File:     "devbox.yml",
			Message:  "",
			Hint:     "",
		},
		{
			Severity: validate.SeverityError,
			Domain:   "config",
			Target:   "config.deploy",
			File:     "devbox/deploy.yml",
			Message:  "unknown field \"invalid\"",
			Hint:     "check field name spelling",
		},
		{
			Severity: validate.SeverityWarning,
			Domain:   "templates",
			Target:   "templates.ide:app",
			File:     "",
			Message:  "missing template pack",
			Hint:     "create devbox/templates/ide/app/",
		},
	}

	output := RenderDiagnosticsTable(rows)

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

func TestFormatDiagnostics_Quiet(t *testing.T) {
	diags := []validate.Diagnostic{
		{Severity: validate.SeverityOK, Domain: "config", Target: "config.devbox"},
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
