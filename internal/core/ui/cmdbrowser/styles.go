package cmdbrowser

import "github.com/semsemyonoff/dwe/internal/core/ui/styles"

// badgeRender returns a function that renders the type badge for a command
// type using existing styles.yml keys (via the internal/core/ui style accessors).
// Unknown / missing types fall back to the muted style. This keeps cmdbrowser
// from introducing new style keys in v1.
func badgeRender(typ string) func(string) string {
	switch typ {
	case "shell":
		return styles.StyleInfo
	case "script":
		return styles.StyleKey
	case "workflow":
		return styles.StyleWarning
	case "service_exec":
		return styles.RenderEnabled
	case "service_run":
		return styles.RenderPartial
	case "builtin":
		return styles.StyleMuted
	case "devbox":
		return styles.StyleSectionTitle
	default:
		return styles.StyleMuted
	}
}
