package docstui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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
// It embeds *Model for the Tree/Viewport/Filter/DiagramState/heading-index/
// loaded-topic state.
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
	// derived from the context passed to newBrowser. Close() calls cancel() to
	// stop any future browser-scoped operations.
	ctx    context.Context //nolint:containedctx
	cancel context.CancelFunc

	// firstLoadDone guards the one-shot initial topic load that fires from
	// Update(tea.WindowSizeMsg). It prevents a second load on subsequent
	// resize events.
	firstLoadDone bool

	// filterSavedCursor is the cursor node saved when entering filter mode so
	// exitFilter (Esc) can restore the pre-filter selection exactly.
	filterSavedCursor *TreeNode

	// wheel coalesces tree-scroll topic loads. A mouse-wheel burst over the tree
	// panel moves the cursor immediately per notch but defers the expensive topic
	// load (glamour render) to a single debounced flush after the burst settles,
	// mirroring the revdiff wheel pattern. Without it, each notch spawned a full
	// async render that piled up and could not be interrupted.
	wheel wheelState

	// diagramRefreshPending / diagramRefreshTickInFlight debounce the viewport
	// placeholder refresh driven by background diagram prefetch. Re-inlining the
	// whole document on every ProgressMsg (one per rendered diagram) stormed the
	// viewport with full SetContent calls that hitched an in-progress scroll —
	// the "background touches the current render" cost on diagram-heavy docs. The
	// refresh is now coalesced onto a trailing tick AND deferred past an active
	// wheel burst, so background rendering updates the placeholders only once
	// things settle.
	diagramRefreshPending      bool
	diagramRefreshTickInFlight bool

	// errOverlay is the diagram render-error modal (nil = closed). errOverlayPending
	// gates PendingOverlay so the Frame pushes/refreshes it; cleared by
	// OverlayClosedMsg when the Frame pops it (esc / click-outside). Mirrors the
	// cmdbrowser inspect overlay lifecycle.
	errOverlay        *errorState
	errOverlayPending bool

	// flashGen tags each status-line flash so a stale clear tick (from an earlier
	// flash) never wipes a newer one — only the tick whose gen still matches the
	// current flash clears it.
	flashGen int
}

// statusFlashDuration is how long a transient status-line confirmation (e.g.
// "copied to clipboard") stays visible before the clear tick removes it.
const statusFlashDuration = 2 * time.Second

// statusFlashClearMsg clears the status flash whose generation matches the
// current one (see flashGen).
type statusFlashClearMsg struct{ gen int }

// setStatusFlash shows a transient confirmation in the status line and returns
// the Cmd that clears it after statusFlashDuration. No-op (nil Cmd) when there
// is no status bar.
func (b *browser) setStatusFlash(text string) tea.Cmd {
	if b.StatusBar == nil {
		return nil
	}
	b.StatusBar.SetFlash(text)
	b.flashGen++
	gen := b.flashGen
	return tea.Tick(statusFlashDuration, func(time.Time) tea.Msg {
		return statusFlashClearMsg{gen: gen}
	})
}

// diagramRefreshDelay is the debounce window for applying the background-diagram
// placeholder refresh to the viewport (see diagramRefreshPending).
const diagramRefreshDelay = 150 * time.Millisecond

// diagramRefreshMsg fires diagramRefreshDelay after a prefetch ProgressMsg to
// apply the coalesced placeholder refresh (deferred again if a scroll is still
// in flight).
type diagramRefreshMsg struct{}

// wheelLoadDelay is the idle window after the last tree-scroll notch before the
// debounced topic load fires. Short enough that a single notch still feels
// instant, long enough to coalesce a wheel/trackpad burst into one load.
const wheelLoadDelay = 60 * time.Millisecond

// wheelState drives the debounced wheel side-effects (revdiff pattern): each
// notch moves the cursor / scrolls the viewport immediately (both O(1)) and arms
// a single deferred flush for the EXPENSIVE follow-up work — the tree topic load
// (glamour render) and the viewport diagram re-sync (full-document SetContent).
// Without this, a buffered wheel flood ran that expensive work once per notch,
// stalling the bubbletea event loop so the backlog drained (and interrupts were
// reached) only long after the user stopped scrolling.
type wheelState struct {
	gen          int  // bumped on every wheel notch; captured by each scheduled tick
	loadPending  bool // a deferred tree topic load is owed (tree scroll)
	syncPending  bool // a deferred viewport diagram re-sync is owed (viewport scroll)
	pinPending   bool // a deferred viewport cursor re-pin is owed (viewport scroll)
	tickInFlight bool // exactly one debounce tick is scheduled at a time
}

// wheelDebounceMsg fires wheelLoadDelay after a tree-scroll notch. gen pins the
// burst generation captured when the tick was scheduled; a mismatch means more
// notches arrived since, so the flush re-arms rather than loading mid-burst.
type wheelDebounceMsg struct {
	gen int
}

// Compile-time guarantee that *browser satisfies the tui.Plugin contract.
var _ tui.Plugin = (*browser)(nil)

// newBrowser builds the plugin around an already-constructed *Model. Sizes are
// deferred — the Frame supplies geometry through Resize/ViewPanel, so the
// viewport starts at zero width and is sized on the first render pass. The
// context is used to scope the browser's own lifecycle; Close() cancels it.
// Translator and locale are read from the model (nil-safe).
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
		treeW  = 1
		vpW    = 5
		totalW = treeW + vpW
		chrome = 4 // 2*(borderSize+hPadding) = 2*(1+1), see tui/geometry.go
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
// loading here would use a zero content width.
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
// viewport (right, weight 5). The {1,5} split is pinned by the 60/79/80/99/100
// frame goldens.
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
// an overlay while the inline filter is active. When true the Frame bypasses
// the action registry and forwards raw keys to Update, reserving only ctrl+c.
func (b *browser) CapturingInput() bool {
	return b.Filter != nil && b.Filter.Active
}

// Result implements tui.Plugin. The docs browser is quit-only; docstui.Run
// returns error only.
func (b *browser) Result() any { return nil }

// StatusContext implements tui.Plugin. Returns the middle-zone status string:
// current path + 📊 focused-diagram indicator (+ ⏳ prefetch progress) + [lang].
// Called every render so the content is reactive: the path/prefetch counters are
// pushed by applyTopicLoaded and ProgressMsg, while the focused-diagram indicator
// is refreshed here from DiagramState so it tracks the cursor no matter which
// action moved it.
func (b *browser) StatusContext() string {
	if b.Model == nil || b.StatusBar == nil {
		return ""
	}
	b.refreshDiagramStatus()
	return b.StatusBar.View()
}

// refreshDiagramStatus syncs the status bar's focused-diagram indicator with the
// current DiagramState. focused is 1-based (0 when no diagram is selected or the
// topic has none).
func (b *browser) refreshDiagramStatus() {
	focused, total := 0, 0
	if b.DiagramState != nil {
		total = len(b.DiagramState.Diagrams)
		if total > 0 && b.DiagramState.Current >= 0 {
			focused = b.DiagramState.Current + 1
		}
	}
	b.StatusBar.SetDiagram(focused, total)
}

// PendingOverlay implements tui.Plugin. It hands the diagram render-error modal
// to the Frame when one is pending: errOverlayPending is set by openErrorOverlay
// (first paint) and updateErrorOverlay (after a scroll) and cleared here, so each
// republish yields exactly one overlay value. The Frame pushes the first and
// replaces it in place on subsequent scrolls, so the stack never grows.
func (b *browser) PendingOverlay() (tui.Overlay, bool) {
	if b.errOverlay == nil || !b.errOverlayPending {
		return tui.Overlay{}, false
	}
	b.errOverlayPending = false
	return b.errOverlay.overlay(), true
}

// errorOverlaySize returns the (width, height) for the error overlay's viewport.
// Height reserves the border + padding chrome against the body. Width grows to
// fit the widest content line (contentWidth) so a stack trace stays unwrapped
// when the terminal allows, clamped to [errorBoxMinWidth, availableBodyWidth].
func (b *browser) errorOverlaySize(contentWidth int) (int, int) {
	availW := max(b.body.Width-errorBoxHChrome, 10)
	w := min(max(contentWidth, errorBoxMinWidth), availW)
	h := max(b.body.Height-errorBoxVChrome-2, 3)
	return w, h
}

// applyErrorOverlayDims resizes the open error overlay's viewport for its current
// mode: the full body (minus the hint row) in selection mode so the opaque block
// covers everything behind it, or the content-fit box size otherwise. No-op when
// closed.
func (b *browser) applyErrorOverlayDims() {
	if b.errOverlay == nil {
		return
	}
	if b.errOverlay.selecting {
		// Selection mode takes over the FULL terminal (FullScreen overlay), so size
		// the block to the whole terminal minus the hint row — not the inner body —
		// so no frame chrome remains on screen to be swept into a native selection.
		b.errOverlay.resize(max(b.TermWidth, 10), max(b.TermHeight-1, 3))
		return
	}
	content := formatErrorContent(b.errOverlay.num, b.errOverlay.total, b.errOverlay.status, b.errOverlay.errText)
	w, h := b.errorOverlaySize(errorContentWidth(content))
	b.errOverlay.resize(w, h)
}

// currentDiagramError returns the status verb and body text to show in the error
// overlay for the diagram under the cursor. It surfaces either the captured mmdc
// failure ("render failed") recorded by the prefetch pool, or — when rendering is
// disabled because mmdc is missing — the install guidance ("rendering disabled").
// The bool is false when there is nothing to show, so `E` stays a no-op (diagram
// rendered fine, or mermaid was explicitly turned off).
func (b *browser) currentDiagramError() (status, text string, ok bool) {
	if b.Prefetch != nil {
		if errText, has := b.Prefetch.RenderError(b.DiagramState.Current); has {
			return "render failed", errText, true
		}
	}
	if b.MmdcMissingNotice != "" {
		return "rendering disabled", b.MmdcMissingNotice, true
	}
	return "", "", false
}

// openErrorOverlay opens the render-error modal for the diagram under the cursor.
// A no-op when there is no current diagram or nothing to show (the diagram
// rendered fine, or mermaid was explicitly disabled) — see currentDiagramError.
func (b *browser) openErrorOverlay() {
	if b.DiagramState == nil || b.DiagramState.CurrentDiagram() == nil {
		return
	}
	status, errText, ok := b.currentDiagramError()
	if !ok {
		return
	}
	num := b.DiagramState.Current + 1
	total := len(b.DiagramState.Diagrams)
	w, h := b.errorOverlaySize(errorContentWidth(formatErrorContent(num, total, status, errText)))
	b.errOverlay = newErrorState(w, h, num, total, status, errText)
	b.errOverlayPending = true
}

// scrollErrorOverlay scrolls the open error overlay by delta wheel notches
// (delta<0 up, delta>0 down) and republishes the snapshot. No-op when closed.
func (b *browser) scrollErrorOverlay(delta int) {
	if b.errOverlay == nil || delta == 0 {
		return
	}
	tui.ScrollOverlayViewport(&b.errOverlay.vp, delta)
	b.errOverlayPending = true
}

// updateErrorOverlay drives the error overlay while it captures input (the Frame
// forwards every key except ctrl+c / esc here). `c` copies the whole error to the
// clipboard; `s` toggles selection mode (releases the mouse for native
// drag-select); every other key delegates to the viewport for scrolling. esc
// never reaches here (the Frame handles it as a close).
func (b *browser) updateErrorOverlay(msg tea.KeyPressMsg) tea.Cmd {
	if b.errOverlay == nil {
		return nil
	}
	switch msg.Text {
	case "c":
		return b.copyErrorToClipboard()
	case "s":
		// Flip mouse capture for native selection, resize the overlay for the new
		// mode (full body when selecting so nothing behind it is selectable), and
		// republish so the Frame re-reads ReleaseMouse and the footer hint updates.
		b.errOverlay.selecting = !b.errOverlay.selecting
		b.applyErrorOverlayDims()
		b.errOverlayPending = true
		return nil
	}
	var cmd tea.Cmd
	b.errOverlay.vp, cmd = b.errOverlay.vp.Update(msg)
	b.errOverlayPending = true
	return cmd
}

// copyErrorToClipboard copies the full render error to the system clipboard and
// flashes a confirmation. No-op when the overlay is closed or the error is blank.
func (b *browser) copyErrorToClipboard() tea.Cmd {
	if b.errOverlay == nil || strings.TrimSpace(b.errOverlay.errText) == "" {
		return nil
	}
	return tea.Batch(
		tea.SetClipboard(b.errOverlay.errText),
		b.setStatusFlash("✓ Error copied to clipboard"),
	)
}

// Update implements tui.Plugin. The Frame forwards all non-key messages here
// (async preservation), including tea.WindowSizeMsg (forwarded after Resize).
// Key messages the registry did not handle also arrive here (raw forward).
func (b *browser) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		// While the error overlay captures input, the Frame forwards raw keys here
		// (except ctrl+c / esc, which it handles as quit / close). Route them to the
		// overlay viewport so it scrolls.
		if b.errOverlay != nil {
			return b.updateErrorOverlay(msg)
		}
		// While the inline filter is active, the Frame forwards raw keys here
		// (CapturingInput() == true). Route them to updateFilter; the registry
		// is bypassed so printable characters extend the query rather than
		// firing bound actions.
		if b.Filter != nil && b.Filter.Active {
			return b.updateFilter(msg)
		}
		return nil

	case tea.MouseWheelMsg:
		// Raw wheel events reach Update only while a CapturesInput overlay is open
		// (the Frame forwards them so the overlay's viewport scrolls).
		if b.errOverlay == nil {
			return nil
		}
		var cmd tea.Cmd
		b.errOverlay.vp, cmd = b.errOverlay.vp.Update(msg)
		b.errOverlayPending = true
		return cmd

	case tui.OverlayClosedMsg:
		// The Frame popped our error overlay (esc / click-outside). Clear the
		// lingering state so a later raw key cannot resurrect the closed modal.
		b.errOverlay = nil
		b.errOverlayPending = false
		return nil

	case tea.WindowSizeMsg:
		b.TermWidth = msg.Width
		b.TermHeight = msg.Height
		// Recompute ContentWidth so the NEXT load event (topic switch / reload /
		// FileChanged / locale change) uses the updated width. Resize only
		// changes the display window; existing glamour content is not re-rendered
		// until the next load — no load-storm, no YOffset reset.
		if msg.Width > 0 {
			b.ContentWidth = viewportPanelInnerWidth(msg.Width)
		}
		// Re-size an open render-error overlay to the new geometry and re-publish it,
		// so a resize (especially in full-screen selection mode, whose block is sized
		// to the terminal) doesn't leave a stale snapshot that fullScreenOverlayView
		// then truncates. Resize(body) already ran before this, so b.body is current.
		if b.errOverlay != nil {
			b.applyErrorOverlayDims()
			b.errOverlayPending = true
		}
		// Fire the first topic load exactly once, from the first non-zero-width
		// WindowSizeMsg. firstLoadDone prevents a second trigger on resize.
		// Resize(body) is void so this is the only Cmd-capable hook that has
		// the framework-supplied width.
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

	case tui.WheelMsg:
		// Coalesced wheel for the open error overlay (sentinel panel) scrolls the
		// modal's embedded viewport by the net notch count; everything else is a
		// body-panel wheel.
		if msg.Panel == tui.OverlayWheelPanel {
			b.scrollErrorOverlay(msg.Delta)
			return nil
		}
		return b.handleWheel(msg)

	case wheelDebounceMsg:
		return b.handleWheelDebounce(msg)

	case tui.PanelClickMsg:
		return b.handlePanelClick(msg)

	case topicLoadedMsg:
		return b.applyTopicLoaded(msg)

	case FileChangedMsg:
		// Reload the current topic only when the changed file is the one on
		// screen (generation filtering plus path comparison).
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
		b.StatusBar.SetProgress(msg.Rendered)
		// Coalesce the placeholder refresh instead of a full SetContent per
		// completed diagram — see scheduleDiagramRefresh.
		refreshCmd := b.scheduleDiagramRefresh()
		if b.prefetchChan != nil {
			return tea.Batch(refreshCmd, waitForProgress(b.prefetchChan))
		}
		return refreshCmd

	case diagramRefreshMsg:
		return b.applyDiagramRefresh()

	case statusFlashClearMsg:
		// Clear only if no newer flash superseded this one.
		if b.StatusBar != nil && msg.gen == b.flashGen {
			b.StatusBar.ClearFlash()
		}
		return nil
	}
	return nil
}

// scheduleDiagramRefresh marks a viewport placeholder refresh pending and arms a
// single trailing tick. Background diagram prefetch emits one ProgressMsg per
// rendered diagram; without this each would re-inline the whole document via
// SetContent, hitching any in-progress scroll. Only the first ProgressMsg of a
// run schedules the tick; later ones ride it.
func (b *browser) scheduleDiagramRefresh() tea.Cmd {
	b.diagramRefreshPending = true
	if b.diagramRefreshTickInFlight {
		return nil
	}
	b.diagramRefreshTickInFlight = true
	return tea.Tick(diagramRefreshDelay, func(time.Time) tea.Msg { return diagramRefreshMsg{} })
}

// applyDiagramRefresh runs when a diagramRefreshMsg fires. If a wheel burst is
// still in flight it re-arms (deferring the refresh past the active scroll so
// background rendering never touches the foreground render mid-scroll);
// otherwise it re-inlines the document once to pick up newly rendered diagrams.
func (b *browser) applyDiagramRefresh() tea.Cmd {
	b.diagramRefreshTickInFlight = false
	if !b.diagramRefreshPending {
		return nil
	}
	if b.wheel.tickInFlight {
		return b.scheduleDiagramRefresh() // a scroll is active; defer
	}
	b.diagramRefreshPending = false
	if b.lastRenderedOutput != "" && len(b.lastRenderedDiagrams) > 0 {
		b.Viewport.SetContent(b.inlineDiagrams(b.lastRenderedOutput, b.lastRenderedDiagrams))
	}
	return nil
}

// ViewPanel implements tui.Plugin. Caches the per-panel inner region and
// renders the panel body (tree or viewport).
func (b *browser) ViewPanel(id tui.PanelID, inner tui.Region) string {
	switch id {
	case panelTree:
		b.treeInner = inner
		if b.Model == nil || b.Tree == nil {
			return ""
		}
		// While the inline filter is active render the query header + filtered
		// tree; otherwise render the normal tree with the focus-visibility clip.
		if b.Filter != nil && b.Filter.Active {
			return b.renderTreeFiltered(inner)
		}
		// Keep the focused row on screen across resizes before clipping.
		b.Tree.eng.EnsureFocusVisible(inner.Height)
		return b.Tree.renderRegion(inner, b.active == panelTree)
	case panelViewport:
		b.viewportInner = inner
		if b.Model == nil || b.Viewport == nil {
			return ""
		}
		// Size the display window to the panel inner region every render. This
		// does NOT re-render the glamour content — content width is fixed at
		// load time, so resize only resizes the window. Mirrors cmdbrowser's
		// viewList sizing pattern.
		b.Viewport.SetDimensions(inner.Width, inner.Height)
		content := b.Viewport.View()
		content = b.applyCursorGlyph(content, inner.Height)
		return b.applyInnerScrollbar(content, inner.Height)
	}
	return ""
}

// applyCursorGlyph overdraws the cursor marker on the cursor row of the windowed
// viewport content. It runs only while the viewport panel is focused (the cursor
// is a reading aid, not shown when the tree drives navigation) and only when the
// cursor row is inside the visible window — during a wheel burst the cursor can
// scroll off-screen, in which case no glyph is drawn (it re-pins at settle). The
// overwrite is width-neutral (replaces glamour's margin space) so it does not
// disturb the right scrollbar column carved by applyInnerScrollbar.
func (b *browser) applyCursorGlyph(content string, h int) string {
	if b.active != panelViewport || b.Viewport == nil || h <= 0 {
		return content
	}
	row := b.viewportCursor - b.Viewport.YOffset()
	if row < 0 || row >= h {
		return content
	}
	lines := strings.Split(content, "\n")
	if row >= len(lines) {
		return content
	}
	lines[row] = overwriteFirstCell(lines[row], cursorGlyphStyled())
	return strings.Join(lines, "\n")
}

// applyInnerScrollbar overdraws the rightmost character column of the inner
// viewport panel string with a proportional scrollbar thumb/track. It operates
// on border-free inner content: the Frame owns the border, so the scrollbar
// column is carved out of the inner width instead of overwriting a border rune.
// Returns content unchanged when the whole document fits in the visible area.
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
	if w < 2 {
		return content // too narrow to carve a scrollbar column
	}
	lines := strings.Split(content, "\n")
	n := min(h, len(lines))
	for i := range n {
		glyph := track
		if i >= thumbStart && i < thumbStart+thumbSize {
			glyph = thumb
		}
		lw := lipgloss.Width(lines[i])
		if lw >= w {
			// Truncate to make room for the scrollbar glyph. ansi.Truncate is
			// O(n) and ANSI-aware; the previous hand-rolled clip was O(n²) (a
			// string alloc + width scan per rune) and dominated CPU on long
			// glamour lines — ~16ms per ViewPanel, the mouse-scroll "hang".
			lines[i] = ansi.Truncate(lines[i], w-1, "")
			lw = lipgloss.Width(lines[i]) // may land below w-1 if a wide rune straddled the cut
		}
		if lw < w-1 {
			// Pad so every row is exactly w-1 cells before the 1-cell glyph,
			// keeping the scrollbar column aligned even past a wide-rune cut.
			lines[i] += strings.Repeat(" ", (w-1)-lw)
		}
		lines[i] += glyph
	}
	return strings.Join(lines, "\n")
}

// --- Inline filter capture ---

// enterFilter opens the inline filter capture mode. It saves the current tree
// cursor, opens the TreeFilter with an empty query, and applies it (which causes
// the tree to recompute its visible set). After this call CapturingInput()
// returns true and the Frame forwards raw keys to Update → updateFilter.
func (b *browser) enterFilter() {
	if b.Filter == nil {
		b.Filter = NewTreeFilter()
	}
	b.filterSavedCursor = nil
	if b.Tree != nil {
		b.filterSavedCursor = b.Tree.Cursor()
	}
	b.Filter.Open()
	if b.Tree != nil {
		b.Tree.ApplyFilter(b.Filter)
	}
}

// updateFilter handles raw keypresses while the inline filter is active.
// Printable characters extend the query; Backspace removes the last rune;
// Enter commits; Esc cancels and restores the pre-filter selection. Up/Down
// arrows navigate the filtered tree without exiting the filter.
func (b *browser) updateFilter(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEnter:
		// Commit loads the picked topic; move focus to the viewport so the
		// user can immediately read it (mirrors the legacy filter Enter path).
		loadCmd := b.commitFilter()
		return tea.Batch(loadCmd, focusCmd(panelViewport))
	case tea.KeyBackspace:
		if b.Filter != nil {
			b.Filter.Backspace()
			if b.Tree != nil {
				b.Tree.ApplyFilter(b.Filter)
			}
		}
		return nil
	case tea.KeyEscape:
		b.exitFilter()
		// Filter nav live-previews the topic under the cursor (afterTreeMove →
		// selectCursor sets CurrentTopic + viewport). exitFilter restores the
		// pre-filter cursor but not the topic, so reload it here — otherwise the
		// viewport and CurrentTopic stay stuck on the last previewed topic and a
		// later CurrentTopic-based action (locale switch) operates on the wrong one.
		return tea.Batch(b.selectCursor(), focusCmd(panelTree))
	case tea.KeyUp:
		if b.Tree != nil {
			b.Tree.MoveUp()
			// Live-preview the match under the cursor (mirrors legacy filter
			// nav); afterTreeMove keeps the row visible and loads its topic.
			return b.afterTreeMove()
		}
		return nil
	case tea.KeyDown:
		if b.Tree != nil {
			b.Tree.MoveDown()
			return b.afterTreeMove()
		}
		return nil
	}
	// Printable characters extend the query (including keys bound elsewhere
	// as actions — while capturing, characters type into the search line).
	if t := msg.Text; t != "" && isPrintable(t) {
		if b.Filter != nil {
			for _, r := range t {
				b.Filter.Append(r)
			}
			if b.Tree != nil {
				b.Tree.ApplyFilter(b.Filter)
			}
		}
		return nil
	}
	return nil
}

// commitFilter ends the filter session keeping the current cursor selection.
// It expands the cursor's ancestors so the item remains visible in the
// unfiltered tree after the filter is cleared, closes the filter, re-pins the
// cursor on the picked node (ApplyFilter may otherwise reset it), and returns
// the Cmd that loads the picked topic into the viewport.
func (b *browser) commitFilter() tea.Cmd {
	if b.Filter == nil {
		return nil
	}
	var picked *TreeNode
	if b.Tree != nil {
		picked = b.Tree.Cursor()
		b.Tree.expandAncestors(picked)
	}
	b.Filter.Close()
	if b.Tree != nil {
		b.Tree.ApplyFilter(b.Filter)
		// ApplyFilter recomputes visibility and may move the cursor; re-pin it
		// on the captured node before loading so the right topic is shown.
		if picked != nil {
			b.Tree.SetCursor(picked)
		}
	}
	b.filterSavedCursor = nil
	return b.selectCursor()
}

// exitFilter ends the filter session and restores the cursor to the position
// it held when filter mode was entered (the pre-filter selection).
func (b *browser) exitFilter() {
	if b.Filter == nil {
		return
	}
	saved := b.filterSavedCursor
	if b.Tree != nil && saved != nil {
		// Set the cursor before ApplyFilter so recomputeVisible's
		// ParkCursorIfHidden keeps it (the engine uses pointer identity).
		b.Tree.SetCursor(saved)
	}
	b.Filter.Close()
	if b.Tree != nil {
		b.Tree.ApplyFilter(b.Filter)
	}
	b.filterSavedCursor = nil
}

// renderTreeFiltered renders the tree panel while the inline filter is active:
// a query-prompt header on the first row followed by the filtered tree rows
// clipped to the remaining height. The header shows the current query text
// with a block cursor and a match count ("/ query█ (N matches)").
func (b *browser) renderTreeFiltered(inner tui.Region) string {
	if inner.Height <= 0 {
		return ""
	}
	n := 0
	if b.Tree != nil {
		n = len(b.Tree.VisibleNodes())
	}
	query := ""
	if b.Filter != nil {
		query = b.Filter.Query
	}
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorAccent())).Bold(true)
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorMuted()))
	prompt := accent.Render("/")
	cursor := accent.Render("█")
	count := muted.Render(fmt.Sprintf("  %s", filterMatchLabel(n)))
	header := prompt + query + cursor + count
	if lipgloss.Width(header) > inner.Width {
		header = truncateLabel(header, inner.Width)
	}
	if inner.Height == 1 {
		return header
	}
	treeRegion := inner
	treeRegion.Height = max(inner.Height-1, 0)
	if b.Tree != nil {
		b.Tree.eng.EnsureFocusVisible(treeRegion.Height)
	}
	body := ""
	if b.Tree != nil {
		body = b.Tree.renderRegion(treeRegion, b.active == panelTree)
	}
	if body == "" {
		return header
	}
	return header + "\n" + body
}

// filterMatchLabel returns a human-readable count string for the filter header.
func filterMatchLabel(n int) string {
	if n == 1 {
		return "1 match"
	}
	return fmt.Sprintf("%d matches", n)
}

// isPrintable reports whether s contains only visible, non-control characters.
// Used to distinguish user-typed characters from function/control key sequences.
func isPrintable(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return s != ""
}

// handlePanelClick moves the tree cursor in response to a single click and
// returns the async topic-load Cmd for the row under the cursor (mirroring the
// keyboard nav path through afterTreeMove → selectCursor; without this the
// click would reposition the highlight but leave the viewport on the previously
// loaded topic until the next key press). While the inline filter is active the
// query line is not row-addressable so clicks are dropped. In the viewport a
// click follows an internal link, jumps the scrollbar column, or moves the
// reading cursor to the clicked row; wheel scroll arrives separately via
// WheelMsg (pointer-routed by the framework).
func (b *browser) handlePanelClick(msg tui.PanelClickMsg) tea.Cmd {
	// A click interrupts an in-flight wheel-scroll burst: drop the deferred tree
	// load so the click's own selection takes over immediately.
	b.cancelWheelLoad()
	if b.CapturingInput() {
		// Filter owns raw input; don't reposition the tree under it.
		return nil
	}
	if msg.Panel == panelTree && b.Tree != nil {
		b.Tree.eng.FocusRow(msg.Y)
		return b.afterTreeMove()
	}
	if msg.Panel == panelViewport && b.Viewport != nil {
		// A click on an internal link navigates; external links are left to the
		// terminal's own OSC-8 handling. Checked before the scrollbar/cursor so a
		// link in the last column still wins.
		absLine := b.Viewport.YOffset() + msg.Y
		if href, ok := b.linkAt(absLine, msg.X); ok {
			if isExternalHref(href) {
				return nil
			}
			return b.followLink(href)
		}
		w, h := b.viewportInner.Width, b.viewportInner.Height
		// Scrollbar column (last cell, only when a scrollbar is drawn): keep the
		// proportional jump behavior.
		if w > 0 && h > 0 && msg.X >= w-1 && b.Viewport.TotalLines() > h {
			b.scrollViewportToClick(msg.X, msg.Y)
			return nil
		}
		// Otherwise move the reading cursor to the clicked row (the click is on a
		// visible row, so no scroll is needed) and re-pick the active diagram.
		b.setViewportCursor(b.Viewport.YOffset() + msg.Y)
		b.syncActiveDiagram()
		return nil
	}
	return nil
}

// scrollViewportToClick handles a click on the viewport scrollbar column: it
// jumps the viewport so the clicked track row maps proportionally to the
// document (GUI scrollbar behaviour — click near the top scrolls near the top,
// near the bottom scrolls near the bottom). Clicks off the scrollbar column, or
// when the whole document fits (no scrollbar drawn), are ignored. The scrollbar
// occupies the last inner column (see applyInnerScrollbar); PanelClickMsg coords
// are panel-inner-local, so the column index is viewportInner.Width-1.
func (b *browser) scrollViewportToClick(x, y int) {
	w, h := b.viewportInner.Width, b.viewportInner.Height
	if w <= 0 || h <= 0 || x < w-1 {
		return // not the scrollbar column
	}
	total := b.Viewport.TotalLines()
	if total <= h {
		return // whole document fits; no scrollbar to click
	}
	maxOffset := total - h
	target := min(max(y*maxOffset/max(h-1, 1), 0), maxOffset)
	b.Viewport.ScrollToLine(target)
	// Re-pin the reading cursor into the new window (like the wheel path's
	// flushWheel → pinCursorToWindow). Without this the cursor stays at its
	// pre-click row, off-screen, and the next j/k nav would immediately scroll the
	// viewport back to it — discarding the scrollbar jump.
	b.pinCursorToWindow()
	// Update the active-diagram highlight for the landed position (cheap; this is
	// a single click, not a wheel flood, so it need not be deferred).
	b.syncActiveDiagram()
}

// handleWheel handles a WheelMsg from the framework. The wheel is pointer-routed
// (Panel is the panel under the pointer, not the focused panel) and does not
// change b.active. Belt-and-suspenders: the Frame already swallows wheel events
// while CapturingInput() is true, but we guard here too for safety.
func (b *browser) handleWheel(msg tui.WheelMsg) tea.Cmd {
	if b.CapturingInput() {
		return nil
	}
	switch msg.Panel {
	case panelViewport:
		if b.Viewport == nil {
			return nil
		}
		// ScrollBy is O(1) (only the offset moves), so it stays immediate for a
		// responsive scroll. The cursor re-pin and the diagram re-sync (both of
		// which re-inline the WHOLE document via SetContent on a boundary
		// crossing) are deferred so a fast scroll does them once at settle, not
		// once per notch.
		b.Viewport.ScrollBy(msg.Delta * wheelViewportStep)
		b.wheel.pinPending = true
		return b.scheduleDiagramSync()
	case panelTree:
		if b.Tree == nil {
			return nil
		}
		// Delta is the coalesced notch count (the framework batches a wheel flood);
		// MoveBy clamps the whole jump in one step (no O(|Delta|) loop). Move the
		// cursor + keep it on screen immediately, but defer the expensive topic
		// load: a fast wheel burst coalesces into a single render at settle
		// (revdiff pattern) instead of spawning one glamour render per notch.
		b.Tree.MoveBy(msg.Delta)
		b.Tree.eng.EnsureFocusVisible(b.treeInner.Height)
		return b.scheduleTreeLoad()
	}
	return nil
}

// scheduleTreeLoad marks a deferred tree topic load pending and arms the wheel
// tick. selectCursor (glamour render) then runs once at burst settle.
func (b *browser) scheduleTreeLoad() tea.Cmd {
	b.wheel.loadPending = true
	return b.armWheelTick()
}

// scheduleDiagramSync marks a deferred viewport diagram re-sync pending and arms
// the wheel tick. syncActiveDiagram (full-document SetContent on a boundary
// crossing) then runs once at burst settle instead of once per notch.
func (b *browser) scheduleDiagramSync() tea.Cmd {
	b.wheel.syncPending = true
	return b.armWheelTick()
}

// armWheelTick bumps the burst generation and ensures exactly one debounce tick
// is in flight: only the first notch of a burst schedules a tick, later notches
// ride it (coalescing). The tick fires wheelLoadDelay later and re-checks the
// generation.
func (b *browser) armWheelTick() tea.Cmd {
	b.wheel.gen++
	if b.wheel.tickInFlight {
		return nil
	}
	b.wheel.tickInFlight = true
	return wheelTick(b.wheel.gen)
}

// wheelTick returns the Cmd that posts a wheelDebounceMsg after wheelLoadDelay,
// pinning the burst generation gen captured at scheduling time.
func wheelTick(gen int) tea.Cmd {
	return tea.Tick(wheelLoadDelay, func(time.Time) tea.Msg {
		return wheelDebounceMsg{gen: gen}
	})
}

// handleWheelDebounce runs when a debounce tick fires. If newer notches arrived
// since this tick was scheduled (gen advanced), the burst is still in progress,
// so it re-arms for the latest generation rather than flushing mid-burst.
// Otherwise the burst has settled: flush the deferred work.
func (b *browser) handleWheelDebounce(msg wheelDebounceMsg) tea.Cmd {
	if msg.gen != b.wheel.gen {
		return wheelTick(b.wheel.gen)
	}
	return b.flushWheel()
}

// flushWheel runs the deferred wheel side-effects at burst settle: the viewport
// diagram re-sync and/or the tree topic load (whichever was armed). Clears the
// debounce state. A load interrupted by a key/click (cancelWheelLoad) is already
// cleared, so only the still-pending work runs.
func (b *browser) flushWheel() tea.Cmd {
	b.wheel.tickInFlight = false
	if b.wheel.pinPending {
		b.wheel.pinPending = false
		b.pinCursorToWindow()
	}
	if b.wheel.syncPending {
		b.wheel.syncPending = false
		b.syncActiveDiagram()
	}
	if b.wheel.loadPending {
		b.wheel.loadPending = false
		return b.selectCursor()
	}
	return nil
}

// cancelWheelLoad drops a pending debounced tree load so a key press or click
// takes over immediately ("interrupt the scroll cycle"). It clears only the load
// flag (not the diagram re-sync); an in-flight tick still fires but finds no load
// pending and resets itself in flushWheel.
func (b *browser) cancelWheelLoad() {
	b.wheel.loadPending = false
}
