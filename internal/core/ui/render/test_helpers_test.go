package render

import (
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
)

// resetStyles re-initialises the styles package palette to the built-in
// defaults for the current dark/light mode. Provided here so the renderer
// tests still co-located in package ui can reset palette state via the
// styles package's public API.
func resetStyles() {
	styles.ApplyStyles(nil)
	styles.DefSep = "—"
}
