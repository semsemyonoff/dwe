package docstui

import (
	"context"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// Panel IDs for the two-panel docs browser. The tree (contents) sits on the
// left; the viewport (rendered markdown) on the right. They match the Frame
// focus order: index-0 (tree) is focused at launch.
const (
	panelTree     tui.PanelID = "tree"
	panelViewport tui.PanelID = "viewport"
)

// browser is the docs browser surface on the tui framework. It is a
// [tui.Plugin]: the Frame owns chrome (borders, focus highlight, Tab cycling,
// geometry, the status line) and the browser owns body content and behaviour.
//
// The existing *Model remains untouched and compilable through the Task 6–9
// coexistence window (its Init/Update/View/quit are not changed here). The
// browser embeds *Model to reuse its Tree/Viewport/Filter/DiagramState/
// heading-index/loaded-topic state, mirroring how cmdbrowser's browser held
// its data without deleting the old model first.
type browser struct {
	*Model

	// active is the currently focused panel. Tracks the Frame's focus manager
	// (initial index-0 panel == tree) via FocusChangedMsg so nav and scroll
	// route to the right widget.
	active tui.PanelID

	// body is the overall inner body region cached on Resize.
	body tui.Region

	// treeInner / viewportInner are the per-panel inner regions cached on
	// ViewPanel so mouse translation and re-renders can reuse them.
	treeInner     tui.Region
	viewportInner tui.Region

	tr     i18n.Translator
	locale string

	// ctx / cancel are the browser-owned context and its cancellation function,
	// derived from the context passed to newBrowser (and in Task 10, from
	// docstui.Run). Close() calls cancel() to stop any future browser-scoped
	// operations.
	ctx    context.Context    //nolint:containedctx
	cancel context.CancelFunc

	// firstLoadDone guards the one-shot initial topic load that fires from
	// Update(tea.WindowSizeMsg). It prevents a second load on subsequent
	// resize events.
	firstLoadDone bool
}

// Compile-time guarantee that *browser satisfies the tui.Plugin contract.
var _ tui.Plugin = (*browser)(nil)

// newBrowser builds the plugin from the same inputs as the legacy NewModel
// (passed through as a *Model). Sizes are deferred — the Frame supplies
// geometry through Resize/ViewPanel, so the viewport starts at zero width and
// is sized on the first render pass. The context is used to scope the
// browser's own lifecycle; Close() cancels it. Translator and locale are read
// from the model (nil-safe).
func newBrowser(ctx context.Context, m *Model) *browser {
	bctx, cancel := context.WithCancel(ctx)
	tr := m.Translator
	if tr == nil {
		tr = i18n.NopTranslator{}
	}
	return &browser{
		Model:  m,
		active: panelTree,
		ctx:    bctx,
		cancel: cancel,
		tr:     tr,
		locale: m.Locale,
	}
}

// viewportPanelInnerWidth computes the inner content width of the viewport
// panel at the given terminal width, replicating the Frame's layout math
// (layoutPanels + contentRegion, both unexported in tui/geometry.go). The
// tree panel takes the proportional floor; the viewport takes the remainder
// (last-panel rule so widths sum exactly to termW). contentRegion then
// subtracts 4 cells (2 × (borderSize:1 + hPadding:1)) per side.
func viewportPanelInnerWidth(termW int) int {
	const (
		treeW   = 1
		vpW     = 5
		totalW  = treeW + vpW
		chrome  = 4 // 2*(borderSize+hPadding) = 2*(1+1), see tui/geometry.go
	)
	treeOuter := termW * treeW / totalW // proportional floor
	vpOuter := termW - treeOuter        // remainder (last panel absorbs)
	if vpOuter < chrome {
		return 0
	}
	return vpOuter - chrome
}

// Init implements tui.Plugin. Subscribes to file-change events from the
// watcher so live-reload works. The initial topic load is deliberately NOT
// fired here — the Frame supplies geometry only later via WindowSizeMsg, so
// loading here would use a zero content width (Decision #10).
func (b *browser) Init() tea.Cmd {
	if b.Watcher == nil {
		return nil
	}
	return waitForFileChange(b.Watcher.Events())
}

// Close implements tui.Plugin. Tears down the watcher, the prefetch pool,
// and the browser-owned context. Safe to call with nil watcher/prefetch.
// Idempotent: the prefetch channel is set to nil after close so a second
// Close() does not panic.
func (b *browser) Close() error {
	if b.cancel != nil {
		b.cancel()
	}
	if b.Watcher != nil {
		_ = b.Watcher.Close()
	}
	if b.Prefetch != nil {
		b.Prefetch.Close()
		if b.prefetchChan != nil {
			close(b.prefetchChan)
			b.prefetchChan = nil
		}
	}
	return nil
}

// Panels implements tui.Plugin. Two static panels: tree (left, weight 1) and
// viewport (right, weight 5). The {1,5} split is the starting ratio validated
// against the 60/79/80/99/100 goldens in Task 11; adjust toward {2,7}/{2,5} if
// the tree inner width is too narrow at the 60-col bucket.
func (b *browser) Panels() []tui.Panel {
	return []tui.Panel{
		{ID: panelTree, Title: "Contents", Weight: 1},
		{ID: panelViewport, Title: "", Weight: 5},
	}
}

// Resize implements tui.Plugin. The Frame owns geometry; the browser caches
// the overall inner body region. Per-panel inner regions arrive separately
// through ViewPanel.
func (b *browser) Resize(body tui.Region) { b.body = body }

// CapturingInput implements tui.Plugin. The browser takes raw input without
// an overlay only while the inline filter is active (Task 7). Returns false
// for now; filter capture wires in Task 7.
func (b *browser) CapturingInput() bool { return false }

// Result implements tui.Plugin. The docs browser is quit-only; docstui.Run
// returns error only.
func (b *browser) Result() any { return nil }

// StatusContext implements tui.Plugin. Returns the middle-zone status string:
// current path + 📊 N/M diagram progress + [lang]. Stub delegates to the
// existing StatusBarWidget; full threading wires in Task 9.
func (b *browser) StatusContext() string {
	if b.Model == nil || b.StatusBar == nil {
		return ""
	}
	return b.StatusBar.View()
}


// PendingOverlay implements tui.Plugin. No overlay for the docs browser yet.
func (b *browser) PendingOverlay() (tui.Overlay, bool) { return tui.Overlay{}, false }

// Update implements tui.Plugin. The Frame forwards all non-key messages here
// (async preservation), including tea.WindowSizeMsg (forwarded after Resize).
// Key messages the registry did not handle also arrive here (raw forward).
func (b *browser) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		b.TermWidth = msg.Width
		b.TermHeight = msg.Height
		// Recompute ContentWidth so the NEXT load event (topic switch / reload /
		// FileChanged / locale change) uses the updated width. Resize only
		// changes the display window; existing glamour content is not re-rendered
		// until the next load (Decision #10, no load-storm / no YOffset reset).
		if msg.Width > 0 {
			b.ContentWidth = viewportPanelInnerWidth(msg.Width)
		}
		// Fire the first topic load exactly once, from the first non-zero-width
		// WindowSizeMsg. firstLoadDone prevents a second trigger on resize.
		// Resize(body) is void so this is the only Cmd-capable hook that has
		// the framework-supplied width (Decision #10).
		if !b.firstLoadDone && msg.Width > 0 {
			b.firstLoadDone = true
			if b.CurrentTopic != nil {
				cmd, _ := b.loadTopic(b.CurrentTopic)
				return cmd
			}
		}
		return nil

	case tui.FocusChangedMsg:
		b.active = msg.Panel
		return nil

	case topicLoadedMsg:
		return b.applyTopicLoaded(msg)

	case FileChangedMsg:
		// Reload the current topic if the changed file matches it. Mirrors
		// Model.Update(FileChangedMsg) exactly (generation filtering, path
		// comparison) so live-reload behavior is preserved.
		var topicCmd tea.Cmd
		if b.CurrentTopic != nil && b.CurrentTopic.Node != nil && b.ProjectRoot != "" {
			projectDocsPath := filepath.Join(b.ProjectRoot, "docs")
			absPath := filepath.Join(projectDocsPath, b.CurrentTopic.Node.Path)
			if filepath.Clean(absPath) == filepath.Clean(msg.Path) {
				topicCmd, _ = b.loadTopic(b.CurrentTopic)
			}
		}
		// Re-subscribe so the next event is delivered.
		if b.Watcher != nil {
			return tea.Batch(topicCmd, waitForFileChange(b.Watcher.Events()))
		}
		return topicCmd

	case ProgressMsg:
		// Drop stale ticks from a previous topic: workers that finished after
		// the user navigated away emit messages whose generation no longer
		// matches the current topic.
		if b.Prefetch != nil && msg.Generation != b.Prefetch.Generation() {
			if b.prefetchChan != nil {
				return waitForProgress(b.prefetchChan)
			}
			return nil
		}
		b.PrefetchProgress = msg
		b.StatusBar.SetProgress(msg.Rendered, msg.Total)
		if b.lastRenderedOutput != "" && len(b.lastRenderedDiagrams) > 0 {
			b.Viewport.SetContent(b.inlineDiagrams(b.lastRenderedOutput, b.lastRenderedDiagrams))
		}
		if b.prefetchChan != nil {
			return waitForProgress(b.prefetchChan)
		}
		return nil
	}
	return nil
}

// ViewPanel implements tui.Plugin. Caches the per-panel inner region and
// renders the panel body. Tree render wired in Task 3; viewport render here.
func (b *browser) ViewPanel(id tui.PanelID, inner tui.Region) string {
	switch id {
	case panelTree:
		b.treeInner = inner
		if b.Model == nil || b.Tree == nil {
			return ""
		}
		// Keep the focused row on screen across resizes before clipping.
		b.Tree.ensureFocusVisible(inner.Height)
		return b.Tree.renderRegion(inner, b.active == panelTree)
	case panelViewport:
		b.viewportInner = inner
		if b.Model == nil || b.Viewport == nil {
			return ""
		}
		// Size the display window to the panel inner region every render. This
		// does NOT re-render the glamour content (content width is fixed at load
		// time per Decision #10 — resize only resizes the window). Mirrors
		// cmdbrowser's viewList sizing pattern.
		b.Viewport.SetDimensions(inner.Width, inner.Height)
		content := b.Viewport.View()
		return b.applyInnerScrollbar(content, inner.Height)
	}
	return ""
}

// applyInnerScrollbar overdraws the rightmost character column of the inner
// viewport panel string with a proportional scrollbar thumb/track. This mirrors
// the old applyViewportScrollbar (view.go) but operates on border-free inner
// content: the Frame owns the border, so the scrollbar column is carved out of
// the inner width instead of overwriting a border rune. Returns content
// unchanged when the whole document fits in the visible area.
func (b *browser) applyInnerScrollbar(content string, h int) string {
	if b.Viewport == nil {
		return content
	}
	total := b.Viewport.TotalLines()
	if h <= 0 || total <= h {
		return content
	}

	thumbSize := max(h*h/total, 1)
	thumbSize = min(thumbSize, h)
	maxStart := h - thumbSize
	thumbStart := 0
	if denom := total - h; denom > 0 {
		thumbStart = min(b.Viewport.YOffset()*maxStart/denom, maxStart)
	}

	thumb := lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorAccent())).Bold(true).Render(scrollbarThumbGlyph)
	track := lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorMuted())).Render(scrollbarTrackGlyph)

	w := b.viewportInner.Width
	lines := strings.Split(content, "\n")
	n := min(h, len(lines))
	for i := range n {
		glyph := track
		if i >= thumbStart && i < thumbStart+thumbSize {
			glyph = thumb
		}
		lw := lipgloss.Width(lines[i])
		switch {
		case lw < w-1:
			lines[i] += strings.Repeat(" ", (w-1)-lw)
		case lw >= w:
			// Truncate to make room for the scrollbar glyph.
			lines[i] = scrollbarClip(lines[i], w-1)
		}
		lines[i] += glyph
	}
	return strings.Join(lines, "\n")
}

// scrollbarClip truncates s to at most width display cells without appending
// an ellipsis. Used by applyInnerScrollbar to make room for the scrollbar glyph.
func scrollbarClip(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	runes := []rune(s)
	for i := len(runes) - 1; i >= 0; i-- {
		if lipgloss.Width(string(runes[:i])) <= width {
			return string(runes[:i])
		}
	}
	return ""
}
