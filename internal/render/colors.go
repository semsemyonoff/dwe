package render

// ANSI color escape codes, matching legacy devbox make color scheme.
const (
	Blue   = "\033[38;5;45m" // cyan-blue (inf color in legacy)
	Green  = "\033[0;32m"    // ok/success
	Yellow = "\033[0;33m"    // warn
	Red    = "\033[0;31m"    // err
	Cyan   = "\033[0;36m"    // standard cyan
	White  = "\033[1;37m"    // bold white
	Gray   = "\033[0;90m"    // dim/disabled
	Reset  = "\033[0m"
)

// ColorNone is the explicit no-color sentinel for ASCII art.
// It is the default when no color is specified in styles.yml.
const ColorNone = "none"

// ColorByName returns an ANSI escape code for a named color.
// Returns Reset for unknown names.
func ColorByName(name string) string {
	switch name {
	case "blue":
		return Blue
	case "green":
		return Green
	case "yellow":
		return Yellow
	case "red":
		return Red
	case "cyan":
		return Cyan
	default:
		return Reset
	}
}
