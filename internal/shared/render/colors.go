package render

// ANSI color escape codes, matching legacy dwe color scheme.
const (
	Blue     = "\033[38;5;45m" // cyan-blue (inf color in legacy)
	Green    = "\033[0;32m"    // ok/success
	Yellow   = "\033[0;33m"    // warn
	Red      = "\033[0;31m"    // err
	Cyan     = "\033[0;36m"    // standard cyan
	BoldCyan = "\033[1;36m"    // bold cyan (used for tips/callouts)
	White    = "\033[1;37m"    // bold white
	Gray     = "\033[0;90m"    // dim/disabled
	Reset    = "\033[0m"
)
