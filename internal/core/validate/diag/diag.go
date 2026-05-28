// Package diag holds the leaf types shared between the validate framework and
// loaders that need to produce diagnostics (e.g. internal/config). It carries
// no imports outside the standard library so any package can depend on it
// without creating a cycle with internal/validate proper.
package diag

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
