package meta

import (
	"fmt"
	"regexp"
)

// snapshotNamePattern enforces a filesystem- and CLI-safe identifier.
// First char restricted to [a-z0-9] to avoid leading dots/dashes that look
// like flags or hidden files; tail accepts the same plus `._-`. Max length
// 63 keeps full paths well below typical filename limits.
var snapshotNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// ValidateName reports whether s is a valid snapshot identifier.
//
// Allowed: [a-z0-9][a-z0-9._-]{0,62}. Empty string, uppercase, and any
// other characters are rejected with a clear error.
func ValidateName(s string) error {
	if s == "" {
		return fmt.Errorf("snapshot name is empty")
	}
	if !snapshotNamePattern.MatchString(s) {
		return fmt.Errorf("snapshot name %q is invalid (allowed: [a-z0-9][a-z0-9._-]{0,62})", s)
	}
	return nil
}
