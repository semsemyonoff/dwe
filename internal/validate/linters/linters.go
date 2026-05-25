// Package linters provides external-linter integration for `devbox validate`.
//
// Each linter is exposed as an Adapter (the contract for what binary to run,
// what defaults to use, how to parse output, and which CLI flags are reserved
// from the user). The runtime wraps an Adapter into a validate.Validator,
// applies per-linter bounds (timeout, output cap, severity clamp), and emits
// diagnostics in the standard validate.Diagnostic shape.
//
// Linters run only inside `devbox validate` — never in preflight. Preflight
// answers "can we run?", not "is the code clean?".
package linters

import (
	"fmt"
	"strings"

	"devbox-cli/internal/validate"
)

// Domain is the diagnostic domain stamped on every linter-produced diagnostic.
const Domain = "linters"

// Adapter is the contract between the linters runtime and a specific linter
// integration (shellcheck, hadolint, generic, ...). Adapters are pure value
// types: no I/O, no goroutines, no state — the runtime owns the subprocess
// and concurrency.
type Adapter interface {
	// ID is the stable adapter identifier used as the map key in validate.yml
	// and as the diagnostic Target. Must be unique across registered adapters.
	ID() string

	// DefaultBin is the bare command name resolved via PATH when the user
	// does not override bin: in validate.yml.
	DefaultBin() string

	// DefaultPaths are the relative paths the walker scans when the user
	// does not override paths:. May contain "." to mean the project root
	// (used by hadolint for top-level Dockerfile).
	DefaultPaths() []string

	// DefaultExtensions are the file extensions (each starting with ".") the
	// walker matches when the user does not override extensions:.
	DefaultExtensions() []string

	// DefaultFilenames are literal basenames matched alongside extensions
	// (e.g. ["Dockerfile"]). Returning nil is fine for adapters that match
	// only by extension.
	DefaultFilenames() []string

	// ReservedFlags is the list of CLI flag tokens the user is forbidden from
	// passing in flags:. Built-in adapters reserve output-format flags because
	// the parser depends on a specific format. The generic adapter returns nil.
	//
	// The match policy in isReserved covers all four argv forms: exact match,
	// long --flag=value, short -fvalue (attached), and value-as-next-arg.
	ReservedFlags() []string

	// BuildArgs assembles the full argv (excluding bin) the runtime passes to
	// exec.CommandContext. Adapters should place forced flags first, then the
	// user's flags, then "--", then files — to keep filenames starting with
	// "-" from being treated as flags.
	BuildArgs(files []string, userFlags []string) []string

	// ParseOutput turns the subprocess's stdout/stderr/exit code into a slice
	// of diagnostics. A non-nil error is treated by the runtime as an
	// operational parse failure (Error), not a hard error — whatever
	// diagnostics were produced before the failure are still returned.
	ParseOutput(stdout, stderr []byte, exitCode int) ([]validate.Diagnostic, error)
}

// finding builds a diagnostic for an adapter's finding about user code. The
// runtime applies the severity clamp from LinterEntry.Severity to these (but
// not to operational diagnostics emitted by the runtime itself).
func finding(target string, sev validate.Severity, file string, line int, message, hint string) validate.Diagnostic {
	return validate.Diagnostic{
		Severity: sev,
		Domain:   Domain,
		Target:   target,
		File:     file,
		Line:     line,
		Message:  message,
		Hint:     hint,
	}
}

// fail builds an error-severity operational diagnostic (never clamped).
func fail(target, msg, hint string) validate.Diagnostic {
	return validate.Diagnostic{
		Severity: validate.SeverityError,
		Domain:   Domain,
		Target:   target,
		Message:  msg,
		Hint:     hint,
	}
}

// warn builds a warning-severity operational diagnostic (never clamped).
func warn(target, msg, hint string) validate.Diagnostic {
	return validate.Diagnostic{
		Severity: validate.SeverityWarning,
		Domain:   Domain,
		Target:   target,
		Message:  msg,
		Hint:     hint,
	}
}

// info builds an info-severity operational diagnostic (never clamped).
func info(target, msg, hint string) validate.Diagnostic {
	return validate.Diagnostic{
		Severity: validate.SeverityInfo,
		Domain:   Domain,
		Target:   target,
		Message:  msg,
		Hint:     hint,
	}
}

// ok builds an OK-severity diagnostic stamped for the linter target.
func ok(target string) validate.Diagnostic {
	return validate.Diagnostic{
		Severity: validate.SeverityOK,
		Domain:   Domain,
		Target:   target,
	}
}

// severityFromLevel maps the level string common to shellcheck and hadolint
// JSON output to a validate.Severity. Unknown levels fall back to Warning.
func severityFromLevel(level string) validate.Severity {
	switch strings.ToLower(level) {
	case "error":
		return validate.SeverityError
	case "warning":
		return validate.SeverityWarning
	case "info", "style":
		return validate.SeverityInfo
	default:
		return validate.SeverityWarning
	}
}

// nonzeroExitMessage formats the stderr payload from a failed linter run into
// a human-readable message. When stderr is empty it generates a placeholder
// using toolName so the diagnostic is never silently blank.
func nonzeroExitMessage(toolName string, stderr []byte) string {
	msg := strings.TrimSpace(string(stderr))
	if msg == "" {
		msg = toolName + " exited non-zero with no parsable output"
	}
	return msg
}

// validateUserFlags returns an error if any of userFlags matches one of the
// adapter's reserved flags. Called by the All(...) assembler at registration
// time so a bad config short-circuits before any subprocess runs.
func validateUserFlags(adapter Adapter, userFlags []string) error {
	reserved := adapter.ReservedFlags()
	if len(reserved) == 0 {
		return nil
	}
	for _, f := range userFlags {
		if isReserved(f, reserved) {
			return fmt.Errorf("flag %q is reserved (locked because the parser depends on the output format)", f)
		}
	}
	return nil
}

// isReserved reports whether flag matches one of reserved using a policy that
// covers all four argv forms a flag can take: exact, long --flag=value,
// short -fvalue (attached), and value-as-next-arg (handled by the exact branch
// matching the flag itself).
func isReserved(flag string, reserved []string) bool {
	// Strip the long-form "=value" suffix once for the equality comparison.
	token, _, _ := strings.Cut(flag, "=")
	for _, r := range reserved {
		if token == r {
			return true
		}
		// Short-flag attached value: reserved="-f", flag="-fgcc".
		if len(r) == 2 && strings.HasPrefix(r, "-") && !strings.HasPrefix(r, "--") &&
			strings.HasPrefix(flag, r) && len(flag) > 2 {
			return true
		}
	}
	return false
}
