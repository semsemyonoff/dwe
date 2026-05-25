// Package env provides built-in environment probes for `devbox validate`
// and the preflight hook. Each probe is a validate.Validator that inspects
// the host environment (docker binary, daemon reachability, compose plugin,
// git/shell binaries, project filesystem permissions) and emits diagnostics
// with actionable hints.
//
// Probes are config-blind beyond the BinariesConfig accessors
// (config.DockerBin / config.GitBin / config.ShellBin). They never load
// project YAML, never touch the user-command registry, and never mutate.
package env

import (
	"devbox-cli/internal/validate"
)

// fail builds an error-severity env diagnostic.
func fail(id, msg, hint string) validate.Diagnostic {
	return validate.Diagnostic{
		Severity: validate.SeverityError,
		Domain:   "env",
		Target:   id,
		Message:  msg,
		Hint:     hint,
	}
}

// ok builds an OK env diagnostic.
func ok(id string) validate.Diagnostic {
	return validate.Diagnostic{
		Severity: validate.SeverityOK,
		Domain:   "env",
		Target:   id,
	}
}

// warn builds a warning-severity env diagnostic.
func warn(id, msg, hint string) validate.Diagnostic {
	return validate.Diagnostic{
		Severity: validate.SeverityWarning,
		Domain:   "env",
		Target:   id,
		Message:  msg,
		Hint:     hint,
	}
}
