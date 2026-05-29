package render

import (
	"devbox-cli/internal/core/ui/styles"

	"github.com/charmbracelet/lipgloss"
)

// LogoMark returns the Devbox logomark "{▪}" with the inner square colored
// using the accent token. The braces stay default text color. Use this in v1
// lipgloss contexts (internal/core/ui consumers, root + info headers, status section
// titles, docs generator progress lines, version output).
//
// Lipgloss handles NO_COLOR / non-TTY downgrade via its color profile, so this
// is safe to call unconditionally.
//
// Exclusion list — do NOT add the logomark to:
//   - desktop notification titles (spec §3.3)
//   - shell tab-completion descriptions
//   - pipeline phase headers
//   - template / PR / commit footers
//   - the bodies of any generated documentation artifact (Markdown / man / YAML)
//
// Use LogoMarkPlain inside any v2 lipgloss styled container (cmdbrowser title
// bar, future v2 surfaces) — v1's reset escape would cancel the outer v2 style.
func LogoMark() string {
	square := lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorAccent())).Render("▪")
	return "{" + square + "}"
}

// LogoMarkPlain returns the Devbox logomark "{▪}" with no styling. Use this in
// non-TTY paths or inside v2 lipgloss containers that color the whole string
// uniformly — embedding v1 escapes would cancel the outer v2 style.
func LogoMarkPlain() string {
	return "{▪}"
}
