package validate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchScope(t *testing.T) {
	tests := []struct {
		name   string
		domain string
		id     string
		scope  []string
		want   bool
	}{
		{
			name:   "empty scope matches all",
			domain: "config",
			id:     "devbox",
			scope:  []string{},
			want:   true,
		},
		{
			name:   "single-element scope matches domain",
			domain: "config",
			id:     "devbox",
			scope:  []string{"config"},
			want:   true,
		},
		{
			name:   "single-element scope rejects different domain",
			domain: "templates",
			id:     "ide",
			scope:  []string{"config"},
			want:   false,
		},
		{
			name:   "two-element scope matches domain and id",
			domain: "config",
			id:     "deploy",
			scope:  []string{"config", "deploy"},
			want:   true,
		},
		{
			name:   "two-element scope rejects domain mismatch",
			domain: "templates",
			id:     "deploy",
			scope:  []string{"config", "deploy"},
			want:   false,
		},
		{
			name:   "two-element scope rejects id mismatch",
			domain: "config",
			id:     "reset",
			scope:  []string{"config", "deploy"},
			want:   false,
		},
		{
			name:   "three-element scope treated as two-element scope",
			domain: "config",
			id:     "deploy",
			scope:  []string{"config", "deploy", "extra"},
			want:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchScope(tt.domain, tt.id, tt.scope)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAggregate(t *testing.T) {
	tests := []struct {
		name     string
		diags    []Diagnostic
		expected Summary
	}{
		{
			name:  "empty diagnostics",
			diags: []Diagnostic{},
			expected: Summary{
				Errors:   0,
				Warnings: 0,
				Infos:    0,
				OKs:      0,
			},
		},
		{
			name: "mixed diagnostics",
			diags: []Diagnostic{
				{Severity: SeverityOK},
				{Severity: SeverityOK},
				{Severity: SeverityInfo},
				{Severity: SeverityWarning},
				{Severity: SeverityError},
				{Severity: SeverityError},
			},
			expected: Summary{
				Errors:   2,
				Warnings: 1,
				Infos:    1,
				OKs:      2,
			},
		},
		{
			name: "only errors",
			diags: []Diagnostic{
				{Severity: SeverityError},
				{Severity: SeverityError},
			},
			expected: Summary{
				Errors:   2,
				Warnings: 0,
				Infos:    0,
				OKs:      0,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Aggregate(tt.diags)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestExitCode(t *testing.T) {
	tests := []struct {
		name     string
		summary  Summary
		strict   bool
		expected int
	}{
		{
			name: "all OK no strict",
			summary: Summary{
				Errors:   0,
				Warnings: 0,
				Infos:    0,
				OKs:      5,
			},
			strict:   false,
			expected: 0,
		},
		{
			name: "one warning no strict",
			summary: Summary{
				Errors:   0,
				Warnings: 1,
				Infos:    0,
				OKs:      0,
			},
			strict:   false,
			expected: 0,
		},
		{
			name: "one warning with strict",
			summary: Summary{
				Errors:   0,
				Warnings: 1,
				Infos:    0,
				OKs:      0,
			},
			strict:   true,
			expected: 1,
		},
		{
			name: "one error no strict",
			summary: Summary{
				Errors:   1,
				Warnings: 0,
				Infos:    0,
				OKs:      0,
			},
			strict:   false,
			expected: 1,
		},
		{
			name: "one error with strict",
			summary: Summary{
				Errors:   1,
				Warnings: 0,
				Infos:    0,
				OKs:      0,
			},
			strict:   true,
			expected: 1,
		},
		{
			name: "warnings and errors no strict",
			summary: Summary{
				Errors:   1,
				Warnings: 5,
				Infos:    0,
				OKs:      0,
			},
			strict:   false,
			expected: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExitCode(tt.summary, tt.strict)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestSortDeterminism(t *testing.T) {
	// Create a set of diagnostics in non-deterministic order (by index)
	diags := []Diagnostic{
		// These are intentionally shuffled
		{Severity: SeverityWarning, Domain: "templates", Target: "z", File: "z.yml", Line: 1},
		{Severity: SeverityOK, Domain: "config", Target: "a", File: "a.yml", Line: 1},
		{Severity: SeverityError, Domain: "config", Target: "a", File: "a.yml", Line: 1},
		{Severity: SeverityInfo, Domain: "config", Target: "b", File: "b.yml", Line: 2},
		{Severity: SeverityWarning, Domain: "config", Target: "a", File: "a.yml", Line: 1},
	}

	// Run the sort twice and compare the outputs
	run1 := make([]Diagnostic, len(diags))
	copy(run1, diags)
	sortDiagnostics(run1)

	run2 := make([]Diagnostic, len(diags))
	copy(run2, diags)
	sortDiagnostics(run2)

	// Convert to a comparable form (the order should be identical)
	require.Equal(t, len(run1), len(run2))
	for i := range run1 {
		assert.Equal(t, run1[i], run2[i], "diagnostic at index %d differs between runs", i)
	}

	// Verify the sort order: Severity desc, Domain asc, Target asc, File asc, Line asc
	// SeverityError > SeverityWarning > SeverityInfo > SeverityOK
	require.Equal(t, SeverityError, run1[0].Severity)
	require.Equal(t, SeverityWarning, run1[1].Severity)
	require.Equal(t, SeverityWarning, run1[2].Severity)
	require.Equal(t, SeverityInfo, run1[3].Severity)
	require.Equal(t, SeverityOK, run1[4].Severity)

	// Within the same severity, check Domain ordering
	// run1[1] and run1[2] are both warnings; [1] should be "config", [2] should be "templates"
	assert.Equal(t, "config", run1[1].Domain)
	assert.Equal(t, "templates", run1[2].Domain)
}

type mockValidator struct {
	domain string
	id     string
	diags  []Diagnostic
}

func (m *mockValidator) ID() string {
	return m.id
}

func (m *mockValidator) Domain() string {
	return m.domain
}

func (m *mockValidator) Run(ctx Context) []Diagnostic {
	return m.diags
}

func TestRegistryRun(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&mockValidator{
		domain: "config",
		id:     "devbox",
		diags:  []Diagnostic{{Severity: SeverityOK, Domain: "config", Target: "config.devbox"}},
	})
	registry.Register(&mockValidator{
		domain: "config",
		id:     "deploy",
		diags:  []Diagnostic{{Severity: SeverityError, Domain: "config", Target: "config.deploy"}},
	})
	registry.Register(&mockValidator{
		domain: "templates",
		id:     "ide",
		diags:  []Diagnostic{{Severity: SeverityWarning, Domain: "templates", Target: "templates.ide"}},
	})

	tests := []struct {
		name     string
		scope    []string
		expected int
	}{
		{
			name:     "empty scope runs all",
			scope:    []string{},
			expected: 3,
		},
		{
			name:     "config scope runs config only",
			scope:    []string{"config"},
			expected: 2,
		},
		{
			name:     "config.deploy scope runs deploy only",
			scope:    []string{"config", "deploy"},
			expected: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diags := registry.Run(Context{}, tt.scope...)
			assert.Equal(t, tt.expected, len(diags), "expected %d diagnostics, got %d", tt.expected, len(diags))
		})
	}
}
