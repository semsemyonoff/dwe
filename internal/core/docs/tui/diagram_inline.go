package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/semsemyonoff/dwe/internal/core/docs/mermaid"
	"github.com/semsemyonoff/dwe/internal/core/docs/render"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
)

// diagramTheme returns the mermaid.Theme corresponding to the model's current
// Theme string. "light" maps to ThemeLight; everything else maps to ThemeDark.
func (m *Model) diagramTheme() mermaid.Theme {
	if m.Theme == "light" {
		return mermaid.ThemeLight
	}
	return mermaid.ThemeDark
}

// resolveMermaidTheme maps the user-config value ("auto" | "dark" | "light")
// to the concrete theme name passed to mmdc. "auto" probes the terminal
// background via lipgloss; anything else is a hard override so the user
// can pin a theme regardless of terminal palette (e.g. a transparent
// terminal where background detection is unreliable). Empty input is
// treated as "auto" for forward-compat with future overrides.
func resolveMermaidTheme(pref string) string {
	switch strings.ToLower(strings.TrimSpace(pref)) {
	case "dark":
		return "dark"
	case "light":
		return "light"
	}
	if lipgloss.HasDarkBackground(os.Stdin, os.Stdout) {
		return "dark"
	}
	return "light"
}

// diagramRenderWidth returns the pixel width passed to mmdc. mmdc's
// `--width` takes pixels (despite our viewport being measured in cells),
// so a fixed 1200 produces diagrams that look good in the system viewer
// regardless of terminal size. It is also part of the cache key, so it
// must stay constant — varying with terminal width would re-render on
// every resize. 1200 px is a comfortable read size on retina displays and
// keeps the cache hit rate stable across users.
func diagramRenderWidth() int { return 1200 }

// diagramMarker returns the unique sentinel string substituted in for a
// mermaid block at preprocess time. Glamour leaves it intact (single token,
// no whitespace, no markdown syntax) so post-render substitution can find
// each block unambiguously by index.
func diagramMarker(index int) string {
	return fmt.Sprintf("⟦dwe-mermaid-%d⟧", index)
}

// inlineDiagrams replaces each diagramMarker in the glamour output with a
// status-aware text placeholder. The TUI shows text rather than inline
// images because bubbletea v2's diff renderer (cellbuf) can't safely
// passthrough kitty graphics escapes — they end up mis-cell-counted and
// the image either disappears or stomps the layout. The user opens the
// rendered PNG in their system viewer via `o`; `[`/`]` cycle the current
// selection so `o` knows which file to open.
//
// Safe to call repeatedly: each call re-resolves cache state so freshly
// prefetched diagrams light up on the next pass, and toggling the current
// selection updates the highlight without re-rendering glamour.
func (m *Model) inlineDiagrams(output string, diagrams []render.DiagramRef) string {
	if len(diagrams) == 0 {
		return output
	}

	theme := m.diagramTheme()
	lookuper, _ := m.MermaidRenderer.(mermaid.Lookuper)
	current := -1
	if m.DiagramState != nil {
		current = m.DiagramState.Current
	}

	for i := range diagrams {
		marker := diagramMarker(i)
		replacement := m.diagramPlaceholder(i, len(diagrams), i == current, diagrams[i].Source, theme, diagramRenderWidth(), lookuper)
		output = strings.ReplaceAll(output, marker, replacement)
	}
	return output
}

// diagramPlaceholder returns the inline text shown in place of a mermaid
// block. State is encoded as a single line so glamour's word-wrap leaves
// it intact and the user sees what's happening at a glance. The currently
// selected diagram — the one `o` will open — is rendered in the accent
// color and bold (with a `▸` marker); the rest are muted so it's obvious
// which one is active without reading the counter.
func (m *Model) diagramPlaceholder(index, total int, current bool, src string, theme mermaid.Theme, width int, lookuper mermaid.Lookuper) string {
	prefix := fmt.Sprintf("📊 Diagram %d/%d", index+1, total)
	if current {
		prefix = "▸ " + prefix
	} else {
		prefix = "  " + prefix
	}

	cached := false
	if lookuper != nil {
		_, cached = lookuper.Lookup(src, theme, width)
	}

	var text string
	switch {
	case lookuper == nil:
		text = fmt.Sprintf("<%s — rendering disabled · `y` source>", prefix)
	case cached:
		text = fmt.Sprintf("<%s — `o` open · `y` source · `[` prev · `]` next>", prefix)
	case m.prefetchFinished():
		text = fmt.Sprintf("<%s — render failed · `y` source>", prefix)
	default:
		text = fmt.Sprintf("<%s — rendering…>", prefix)
	}

	if current {
		return diagramActiveStyle().Render(text)
	}
	return diagramInactiveStyle().Render(text)
}

// diagramActiveStyle / diagramInactiveStyle style the inline diagram
// placeholders. The styles are rebuilt per call (cheap) so a runtime theme
// change is picked up without caching stale palette colors. They are applied
// after glamour has already rendered the surrounding markdown — the
// placeholder marker is substituted post-render — so the injected SGR codes
// don't interfere with glamour's own styling.
func diagramActiveStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorAccent())).Bold(true)
}

func diagramInactiveStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorMuted()))
}

// prefetchFinished reports whether the worker pool has reported a tick for
// every queued diagram. Used by diagramPlaceholder to distinguish "still
// rendering" (transient) from "failed" (terminal — mmdc errored, cache
// stayed empty).
func (m *Model) prefetchFinished() bool {
	return m.PrefetchProgress.Total > 0 && m.PrefetchProgress.Rendered >= m.PrefetchProgress.Total
}

// refreshDiagramView re-runs inline substitution (so the "current"
// highlight moves to the new selection) and scrolls the viewport so the
// active diagram is on screen. The line position is recovered by locating
// the unique "▸ 📊 Diagram N/M" marker in the rendered output — that's
// more reliable than DiagramRef.LineInRendered, which is the pre-glamour
// markdown line and doesn't account for word-wrap.
func (m *Model) refreshDiagramView() {
	if m.lastRenderedOutput == "" || len(m.lastRenderedDiagrams) == 0 {
		return
	}
	content := m.inlineDiagrams(m.lastRenderedOutput, m.lastRenderedDiagrams)
	m.Viewport.SetContent(content)
	if m.DiagramState == nil {
		return
	}
	diag := m.DiagramState.CurrentDiagram()
	if diag == nil {
		return
	}
	needle := fmt.Sprintf("▸ 📊 Diagram %d/%d", diag.Index+1, len(m.lastRenderedDiagrams))
	for i, line := range strings.Split(content, "\n") {
		if strings.Contains(stripANSI(line), needle) {
			m.Viewport.ScrollToLine(i)
			return
		}
	}
}

// diagramLineIndices returns, for each diagram index 0..n-1, the rendered
// viewport line where its placeholder marker sits — or -1 if the marker was
// not found. The markers survive glamour as single tokens (see
// diagramMarker); scanning the rendered output once and stripping ANSI per
// line locates them reliably. Markers appear in document order so the result
// is ascending (ignoring -1s).
func diagramLineIndices(output string, n int) []int {
	if n == 0 {
		return nil
	}
	res := make([]int, n)
	for i := range res {
		res[i] = -1
	}
	for li, line := range strings.Split(output, "\n") {
		plain := stripANSI(line)
		for i := range n {
			if res[i] < 0 && strings.Contains(plain, diagramMarker(i)) {
				res[i] = li
			}
		}
	}
	return res
}

// activeDiagramForOffset returns the index of the diagram that should be
// active for the current scroll position: the topmost diagram visible in the
// viewport, or — when none is visible — the last one scrolled past (so it
// stays selected until the next appears), falling back to the first diagram
// when the view sits above them all. Returns -1 when there are no diagrams.
func (m *Model) activeDiagramForOffset() int {
	lines := m.currentDiagramLines
	if len(lines) == 0 {
		return -1
	}
	top := m.Viewport.YOffset()
	bottom := top + m.Viewport.VisibleHeight()

	firstVisible, lastAbove := -1, -1
	for i, ln := range lines {
		if ln < 0 {
			continue
		}
		if firstVisible < 0 && ln >= top && ln < bottom {
			firstVisible = i
		}
		if ln < top {
			lastAbove = i
		}
	}
	switch {
	case firstVisible >= 0:
		return firstVisible
	case lastAbove >= 0:
		return lastAbove
	default:
		return 0
	}
}

// syncActiveDiagram recomputes the active diagram from the scroll position and,
// if it changed, re-inlines the rendered output so the highlight moves to the
// diagram now in view. It deliberately does NOT scroll — the user is driving
// the viewport — which is what separates it from refreshDiagramView (the
// `[`/`]` manual switch, which jumps the viewport to the chosen diagram).
func (m *Model) syncActiveDiagram() {
	if m.DiagramState == nil || len(m.DiagramState.Diagrams) == 0 {
		return
	}
	idx := m.activeDiagramForOffset()
	if idx < 0 || idx == m.DiagramState.Current {
		return
	}
	m.DiagramState.Current = idx
	if m.lastRenderedOutput != "" && len(m.lastRenderedDiagrams) > 0 {
		m.Viewport.SetContent(m.inlineDiagrams(m.lastRenderedOutput, m.lastRenderedDiagrams))
	}
}

// openCurrentDiagram writes the cached PNG of the currently-selected
// diagram to a stable temp path and opens it with the system viewer.
// Returns nil if there is no diagram selected or no PNG yet in the cache.
func (m *Model) openCurrentDiagram() error {
	if m.DiagramState == nil {
		return nil
	}
	diag := m.DiagramState.CurrentDiagram()
	if diag == nil {
		return nil
	}
	lookuper, ok := m.MermaidRenderer.(mermaid.Lookuper)
	if !ok {
		return nil
	}
	theme := m.diagramTheme()
	png, ok := lookuper.Lookup(diag.Source, theme, diagramRenderWidth())
	if !ok {
		return nil
	}
	// Write to a per-session temp dir so re-opens reuse the same file
	// instead of leaking a new temp on every `o` press, and so concurrent
	// `dwe docs` sessions don't fight over the same filename.
	if m.diagramExportDir == "" {
		dir, err := os.MkdirTemp("", "dwe-diagrams-*")
		if err != nil {
			return err
		}
		m.diagramExportDir = dir
	}
	path := filepath.Join(m.diagramExportDir, fmt.Sprintf("diagram-%d.png", diag.Index))
	if err := os.WriteFile(path, png, 0o600); err != nil {
		return err
	}
	return mermaid.OpenSystem(path)
}
