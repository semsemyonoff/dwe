package validate

import (
	"sort"

	"devbox-cli/internal/config"
)

// Severity represents the severity level of a validation diagnostic.
type Severity int

// Severity levels.
const (
	SeverityUnknown Severity = iota // Zero value guard.
	SeverityOK
	SeverityInfo
	SeverityWarning
	SeverityError
)

// Diagnostic represents a single validation finding.
type Diagnostic struct {
	Severity Severity
	Domain   string
	Target   string
	File     string
	Line     int
	Message  string
	Hint     string
}

// Context carries data for validator execution.
type Context struct {
	ProjectRoot string
	ConfigPath  string
	Cfg         *config.DevboxConfig
}

// Validator is the interface for domain-specific validators.
type Validator interface {
	ID() string
	Domain() string
	Run(ctx Context) []Diagnostic
}

// Registry manages and runs validators.
type Registry struct {
	validators []Validator
}

// NewRegistry creates a new validator registry.
func NewRegistry() *Registry {
	return &Registry{validators: []Validator{}}
}

// Register adds a validator to the registry.
func (r *Registry) Register(v Validator) {
	r.validators = append(r.validators, v)
}

// Run executes validators matching the given scope and returns collected diagnostics.
func (r *Registry) Run(ctx Context, scope ...string) []Diagnostic {
	var diags []Diagnostic
	for _, v := range r.validators {
		if MatchScope(v.Domain(), v.ID(), scope) {
			diags = append(diags, v.Run(ctx)...)
		}
	}
	sortDiagnostics(diags)
	return diags
}

// MatchScope returns true if the validator matches the requested scope.
// Empty scope matches all validators.
// Single-element scope matches validators in that domain.
// Two-element scope matches validators in that domain with that ID.
func MatchScope(domain, id string, scope []string) bool {
	if len(scope) == 0 {
		return true
	}
	if len(scope) == 1 {
		return scope[0] == domain
	}
	if len(scope) >= 2 {
		return scope[0] == domain && scope[1] == id
	}
	return false
}

func sortDiagnostics(diags []Diagnostic) {
	sort.Slice(diags, func(i, j int) bool {
		di, dj := diags[i], diags[j]
		// Sort by: Severity desc, Domain asc, Target asc, File asc, Line asc
		if di.Severity != dj.Severity {
			return di.Severity > dj.Severity
		}
		if di.Domain != dj.Domain {
			return di.Domain < dj.Domain
		}
		if di.Target != dj.Target {
			return di.Target < dj.Target
		}
		if di.File != dj.File {
			return di.File < dj.File
		}
		return di.Line < dj.Line
	})
}

// Summary aggregates counts of diagnostics by severity.
type Summary struct {
	Errors   int
	Warnings int
	Infos    int
	OKs      int
}

// Aggregate counts diagnostics by severity into a Summary.
func Aggregate(diags []Diagnostic) Summary {
	summary := Summary{}
	for _, d := range diags {
		switch d.Severity {
		case SeverityError:
			summary.Errors++
		case SeverityWarning:
			summary.Warnings++
		case SeverityInfo:
			summary.Infos++
		case SeverityOK:
			summary.OKs++
		}
	}
	return summary
}

// ExitCode returns the exit code based on the summary and strict flag.
// Errors always return 1; warnings return 1 only if strict is true.
func ExitCode(summary Summary, strict bool) int {
	if summary.Errors > 0 {
		return 1
	}
	if strict && summary.Warnings > 0 {
		return 1
	}
	return 0
}
