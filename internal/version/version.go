package version

import "fmt"

// These variables are set at build time via -ldflags -X.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
	BuiltBy = "unknown"
)

// Info returns a formatted version string.
func Info() string {
	return fmt.Sprintf("version %s (commit %s, built %s by %s)", Version, Commit, Date, BuiltBy)
}
