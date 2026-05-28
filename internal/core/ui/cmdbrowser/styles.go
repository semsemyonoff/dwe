package cmdbrowser

import (
	"devbox-cli/internal/core/ui"
)

// badgeRender returns a function that renders the type badge for a command
// type using existing styles.yml keys (via the internal/core/ui style accessors).
// Unknown / missing types fall back to the muted style. This keeps cmdbrowser
// from introducing new style keys in v1.
func badgeRender(typ string) func(string) string {
	switch typ {
	case "shell":
		return ui.StyleInfo
	case "script":
		return ui.StyleKey
	case "workflow":
		return ui.StyleWarning
	case "service_exec":
		return ui.RenderEnabled
	case "service_run":
		return ui.RenderPartial
	case "builtin":
		return ui.StyleMuted
	case "devbox":
		return ui.StyleSectionTitle
	default:
		return ui.StyleMuted
	}
}
