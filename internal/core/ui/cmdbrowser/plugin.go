package cmdbrowser

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// Panel IDs for the two-panel command browser. The tree (groups) sits on the
// left; the list (commands within the focused group) on the right. They match
// the Frame focus order: index-0 (tree) is focused at launch.
const (
	panelTree tui.PanelID = "tree"
	panelList tui.PanelID = "list"
)

// listBadgesMinWidth is the minimum inner LIST panel width (cells) at which the
// list shows type badges and the param-count "[N]" indicator. Keyed on the
// framework INNER width (outer − border − padding), NOT raw terminal width:
// under the Frame the list takes weight 7 of {2,7}, so at terminal width 100 the
// list inner width is 74 and at 99 it is 73. The legacy model showed badges only
// at terminal width ≥ 100 (showBadges); 74 is the inner width at terminal 100,
// so this reproduces that boundary against the inner width the Frame now
// supplies — mirrors treeCountsMinWidth on the tree side.
const listBadgesMinWidth = 74

// browser is the cmdbrowser surface migrated onto the tui framework. It is a
// [tui.Plugin]: the Frame owns chrome (borders, focus highlight, Tab cycling,
// geometry, the status line) and the browser owns body content and behaviour.
//
// It replaced the hand-rolled *Model (formerly model.go, now deleted); Run drives
// the plugin directly (see run.go). The plugin holds the cmdbrowser-local tree
// (left) and a bubbles/v2 list (right); per-panel rendering happens in ViewPanel
// against the inner regions the Frame computes.
type browser struct {
	title string
	items []Item
	opts  Options

	tree     *treeModel
	list     list.Model
	delegate *cmdDelegate

	// active is the currently focused panel. It tracks the Frame's focus
	// manager (initial index-0 panel == tree) via FocusChangedMsg so nav and
	// scroll route to the right widget. Tree/list are distinct widgets with
	// distinct movement, so navigation cannot be panel-agnostic.
	active tui.PanelID

	// filter is the inline capture sub-state (CapturingInput() is true while it
	// is non-nil). inspect is the overlay sub-state (non-nil while the inspect
	// viewport is logically open); it captures via Overlay.CapturesInput routed
	// by the Frame, so the two stay mutually exclusive — the filter takes raw
	// input through the no-overlay capture branch, inspect through the modal-open
	// branch (routeWhileCapturing). inspectPending gates PendingOverlay so each
	// republish yields exactly one overlay value: openInspect sets it for the
	// first paint and updateInspect re-sets it after a scroll, with the Frame
	// pushing the first and replacing the top in place thereafter (the stack never
	// grows). Esc is handled Frame-side (it pops the overlay), which forwards a
	// [tui.OverlayClosedMsg] back to Update — that clears b.inspect/inspectPending
	// so a later unmatched raw key (forwarded raw to Update in normal mode) cannot
	// re-mark it pending and resurrect the closed modal.
	filter         *filterState
	inspect        *inspectState
	inspectPending bool

	skipConfirm bool

	result Result

	// body is the overall inner body region cached on Resize; treeInner /
	// listInner are the per-panel inner regions cached on ViewPanel so mouse
	// translation and re-renders can reuse them.
	body      tui.Region
	treeInner tui.Region
	listInner tui.Region

	tr     i18n.Translator
	locale string
}

// Compile-time guarantee that *browser satisfies the tui.Plugin contract.
var _ tui.Plugin = (*browser)(nil)

// newBrowser builds the plugin from the same inputs as the legacy newModel:
// the tree, the bubbles list, and the item delegate. Sizes are deferred — the
// Frame supplies geometry through Resize/ViewPanel, so the list and delegate
// start at zero width and are sized on the first render pass. Translator and
// locale are read from opts (nil-safe).
func newBrowser(title string, items []Item, opts Options) *browser {
	tm := newTreeModel(items, opts.IncludePrivate, opts.DefaultExpandedDepth)
	dlg := newCmdDelegate(0, opts.ShowTypeBadges)
	l := list.New(nil, dlg, 0, 0)
	l.SetShowTitle(false)
	l.SetShowFilter(false)
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	applyListStyles(&l)

	b := &browser{
		title:    title,
		items:    items,
		opts:     opts,
		tree:     tm,
		list:     l,
		delegate: dlg,
		active:   panelTree,
		tr:       opts.translatorOrNop(),
		locale:   opts.Locale,
	}
	b.refreshList()
	return b
}

// refreshList rebuilds the list contents from the currently focused tree group.
// Ported from *Model.refreshList; called after any tree mutation that changes
// the focused group.
func (b *browser) refreshList() {
	idxs := b.tree.itemsForFocus()
	out := make([]list.Item, 0, len(idxs))
	for _, idx := range idxs {
		it := b.items[idx]
		out = append(out, listItem{origIdx: idx, id: it.ID, desc: it.Description, typ: it.Type, paramCount: it.ParamCount})
	}
	b.list.SetItems(out)
}

// selectedOrigIdx returns the original items index of the currently selected
// list row, mapping through listItem.origIdx so Result.Idx stays stable across
// filtering and reordering inside the list. ok is false when no selectable row
// is focused (an empty list or a header row).
func (b *browser) selectedOrigIdx() (int, bool) {
	it, ok := b.list.SelectedItem().(listItem)
	if !ok || it.header {
		return 0, false
	}
	return it.origIdx, true
}

// Init implements tui.Plugin. The browser has no startup command.
func (b *browser) Init() tea.Cmd { return nil }

// Close implements tui.Plugin. The browser holds no async resources.
func (b *browser) Close() error { return nil }

// Panels implements tui.Plugin. Two static panels: tree (left) and list
// (right). Weights {2, 7} approximate the legacy split (leftWidth ≈ 2·w/9).
func (b *browser) Panels() []tui.Panel {
	return []tui.Panel{
		{ID: panelTree, Title: "Groups", Weight: 2},
		{ID: panelList, Title: "Commands", Weight: 7},
	}
}

// Resize implements tui.Plugin. The Frame owns geometry; the browser caches
// the overall inner body region. Per-panel inner regions arrive separately
// through ViewPanel.
func (b *browser) Resize(body tui.Region) { b.body = body }

// CapturingInput implements tui.Plugin. The browser takes raw input without an
// overlay only while the inline filter is active. The inspect overlay captures
// via Overlay.CapturesInput instead, so the two stay mutually exclusive.
func (b *browser) CapturingInput() bool { return b.filter != nil }

// Result implements tui.Plugin. Returned UNCHANGED by tui.Run; cmdbrowser.Run
// type-asserts it back to Result.
func (b *browser) Result() any { return b.result }

// StatusContext implements tui.Plugin. The middle status-line zone shows the
// focused group's breadcrumb and a `[--yes ON]` indicator when skip-confirm is
// on. It is called every render so the indicator is reactive.
func (b *browser) StatusContext() string {
	out := b.breadcrumb()
	if b.skipConfirm && b.opts.Mode == ModeRun {
		out += "  " + paletteSuccess().Bold(true).Render("[--yes ON]")
	}
	return out
}

// breadcrumb formats the focused group's full path and item count for the
// status line. Root group shows as "(root)"; nested groups use " › "
// separators. Mirrors *Model.breadcrumb.
func (b *browser) breadcrumb() string {
	n := b.tree.focusedNode()
	path := "(root)"
	if n != nil && n.id != "" {
		path = strings.ReplaceAll(n.id, ".", " › ")
	}
	count := len(b.list.Items())
	header := paletteKey().Bold(true).Render(path)
	tail := paletteDescription().Render(" · " + strconv.Itoa(count) + " " + b.itemNoun(count))
	return header + tail
}

// itemNoun returns the singular/plural noun for the breadcrumb count. ModeEdit
// (the vars browser) names rows "var"; every other mode keeps "command". This
// stays HARDCODED English for the pilot — it is not localized today. Mirrors
// *Model.itemNoun.
func (b *browser) itemNoun(count int) string {
	singular := "command"
	if b.opts.Mode == ModeEdit {
		singular = "var"
	}
	if count == 1 {
		return singular
	}
	return singular + "s"
}

// Update implements tui.Plugin. While the inline filter is active
// (CapturingInput() is true), the Frame forwards raw keys here so the browser
// drives its own search line. While the inspect overlay is open the Frame routes
// captured keys here too (via routeWhileCapturing — every key except ctrl+c and
// esc), which drive the inspect viewport.
//
// Mouse and focus messages (Task 9) arrive here regardless of capture state:
// FocusChangedMsg tracks the active panel for nav/scroll routing (Tab/Shift+Tab
// are framework built-ins that never otherwise reach the plugin); PanelClickMsg
// moves the cursor/selection to the clicked row (single click = move only, no
// run — Decision 7). Wheel scroll is delivered as nav.up/nav.down through
// HandleAction, and double-click as the Select action, so neither needs handling
// here.
//
// Filter and inspect are mutually exclusive (you cannot open inspect while
// filtering — `i` is typed into the query, not dispatched), so the filter branch
// takes precedence and inspect only runs when no filter is active.
func (b *browser) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case tui.FocusChangedMsg:
		b.active = m.Panel
		return nil
	case tui.PanelClickMsg:
		b.handlePanelClick(m)
		return nil
	case tui.OverlayClosedMsg:
		// The Frame popped our inspect overlay (esc). Clear the lingering state
		// so a later unmatched raw key cannot re-mark it pending and resurrect
		// the closed modal. Filter is not an overlay, so it never lands here.
		b.inspect = nil
		b.inspectPending = false
		return nil
	case tea.KeyPressMsg:
		switch {
		case b.filter != nil:
			return b.updateFilter(m)
		case b.inspect != nil:
			return b.updateInspect(m)
		}
	}
	return nil
}

// handlePanelClick moves the cursor/selection in response to a single click,
// without running anything (Decision 7 — single click moves, double click runs).
// The only guard is the inline filter: while it owns input the query line is not
// a row-addressable surface, so clicks are dropped. The inspect overlay needs no
// guard here — the Frame swallows panel clicks while a modal is open (a
// PanelClickMsg is never emitted), so this is only ever reached with no overlay
// (b.inspect is nil once closed — the Frame's OverlayClosedMsg clears it).
func (b *browser) handlePanelClick(msg tui.PanelClickMsg) {
	if b.filter != nil {
		return
	}
	switch msg.Panel {
	case panelTree:
		b.tree.focusRow(msg.Y)
		b.afterTreeMove()
	case panelList:
		b.selectListRow(msg.Y)
	}
}

// selectListRow moves the list selection to the item under the clicked
// panel-local row. The bubbles list stacks each item over delegate
// Height()+Spacing() rows, so the on-page item index is row/rowHeight; the
// global index adds the current page offset. Clicks on empty space past the last
// item (or before any page is sized) are no-ops.
func (b *browser) selectListRow(row int) {
	if row < 0 {
		return
	}
	rowHeight := b.delegate.Height() + b.delegate.Spacing()
	perPage := b.list.Paginator.PerPage
	if rowHeight <= 0 || perPage <= 0 {
		return
	}
	target := b.list.Paginator.Page*perPage + row/rowHeight
	if target < 0 || target >= len(b.list.Items()) {
		return
	}
	b.list.Select(target)
}

// ViewPanel implements tui.Plugin. It caches the per-panel inner region (for
// mouse translation and re-renders) and renders the panel body into it. The
// Frame owns the border/padding, so the inner region is already chrome-free.
// The list panel render lands in Task 5.
func (b *browser) ViewPanel(id tui.PanelID, inner tui.Region) string {
	switch id {
	case panelTree:
		b.treeInner = inner
		if b.filter != nil {
			return b.renderTreeFiltered(inner)
		}
		// Keep the focused row on screen across resizes before clipping.
		b.tree.ensureFocusVisible(inner.Height)
		return b.tree.renderRegion(inner, b.active == panelTree, nil)
	case panelList:
		b.listInner = inner
		return b.viewList(inner)
	}
	return ""
}

// viewList sizes the embedded bubbles list to the inner region and renders it.
// Badge and param-count visibility are keyed on the inner width (see
// listBadgesMinWidth), recomputed against the framework inner width rather than
// raw terminal width. The breadcrumb is NOT drawn here — it lives in the Frame
// status line via StatusContext (Decision 6), so the list fills the full inner
// height, mirroring the tree panel which has no in-panel header.
func (b *browser) viewList(inner tui.Region) string {
	b.delegate.width = inner.Width
	b.delegate.showBadges = b.opts.ShowTypeBadges && inner.Width >= listBadgesMinWidth
	b.list.SetSize(inner.Width, inner.Height)
	return b.list.View()
}

// Actions / HandleAction live in actions.go.

// enterFilter opens the inline filter capture mode: it snapshots the tree's
// expanded set and focused id, sets b.filter (so CapturingInput() reports true),
// and seeds the match list. The capture key handling (typing / esc / enter) and
// snapshot restore land in Task 7; Task 6 only opens the mode.
func (b *browser) enterFilter() {
	b.filter = newFilterState(b.tree.expanded, b.tree.focusedID)
	b.refreshFilterMatches()
}

// updateFilter handles keypresses while the inline filter is active. The search
// line behaves like a text input: every printable character — including letters
// bound elsewhere as actions (i / y / e / q / j / k / h / l) — extends the query
// rather than firing the action. Only non-printable keys (Enter, Backspace, Esc,
// arrows, page nav) keep their semantics. Reparented from *Model.updateFilter;
// the Frame's capture branch (Task 1) routes raw keys here while
// CapturingInput() reports true.
func (b *browser) updateFilter(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEnter:
		b.commitFilter()
		return requestFocus(panelList)
	case tea.KeyBackspace:
		if len(b.filter.query) > 0 {
			runes := []rune(b.filter.query)
			b.filter.query = string(runes[:len(runes)-1])
			b.refreshFilterMatches()
		}
		return nil
	}
	// Printable text goes into the query BEFORE consulting any letter-keyed
	// action (Inspect=i, SkipConfirm=y, ForceForm=e, Cancel=q, vi-nav j/k/h/l).
	// The cursor is "on the search line"; typed characters extend the search
	// string, not fire commands. Non-printable keys (esc, arrows, page nav) have
	// empty or non-printable msg.Text and fall through.
	if t := msg.Text; t != "" && isPrintable(t) {
		b.filter.query += t
		b.refreshFilterMatches()
		return nil
	}
	// Esc restores the snapshot and exits filter. "q" is consumed above as text.
	if msg.Code == tea.KeyEscape {
		b.exitFilter()
		return requestFocus(panelList)
	}
	// Forward arrow / page navigation to the list so the user can move through
	// the ranked matches while still typing (j/k are printable, handled above).
	switch msg.Code {
	case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd:
		var cmd tea.Cmd
		b.list, cmd = b.list.Update(msg)
		return cmd
	}
	return nil
}

// exitFilter (Esc) discards the filter session, restores the snapshotted
// expanded set + focused id, and moves the active panel to the list (the caller
// pairs this with a requestFocus(panelList) so the Frame border follows). Per §8
// cursor-restoration it keeps the tree cursor on the nearest ancestor of the
// highlighted match when one exists. Reparented from *Model.exitFilter; the
// single-panel populateList branch is dropped (Variant A has no single panel).
func (b *browser) exitFilter() {
	if b.filter == nil {
		return
	}
	// Track the highlighted match so the list cursor can be re-positioned after
	// refreshList rebuilds the items (SetItems preserves index, not identity).
	targetOrigIdx := -1
	if b.filter.query == "" {
		// Empty query: entered and immediately Esc'd. Restore the exact pre-filter
		// cursor rather than the (meaningless) first-row highlight.
		b.tree.focusedID = b.filter.savedFocusID
	} else if it, ok := b.list.SelectedItem().(listItem); ok && !it.header {
		targetOrigIdx = it.origIdx
		b.tree.focusedID = b.nearestRestoredAncestor(b.items[it.origIdx].ID)
	}
	b.filter.restoreExpansion(b.tree)
	// Restored expansion may leave focusedID on a now-hidden node — walk up.
	if b.tree.focusedID != "" && b.tree.indexOfFocused() < 0 {
		b.tree.focusedID = b.tree.nearestVisibleAncestor(b.tree.focusedID)
	}
	b.filter = nil
	b.active = panelList
	b.refreshList()
	b.reselectOrigIdx(targetOrigIdx)
}

// commitFilter (Enter) ends the capture session but KEEPS the filter-induced
// expansion (the auto-collapsed view that surfaces the matches), unlike
// exitFilter which restores the pre-filter expansion. Tree focus moves to the
// nearest visible ancestor of the highlighted match so navigation resumes near
// the result the user picked.
func (b *browser) commitFilter() {
	if b.filter == nil {
		return
	}
	if it, ok := b.list.SelectedItem().(listItem); ok && !it.header {
		b.tree.focusedID = b.nearestRestoredAncestor(b.items[it.origIdx].ID)
	}
	if b.tree.focusedID != "" && b.tree.indexOfFocused() < 0 {
		b.tree.focusedID = b.tree.nearestVisibleAncestor(b.tree.focusedID)
	}
	b.filter = nil
	b.active = panelList
	b.refreshList()
}

// requestFocus returns a command that asks the Frame to focus panel p. The
// plugin tracks its own active panel (b.active) for nav routing, but the Frame
// owns focus truth (the panel border, the Tab cycle). When a filter session ends
// it moves b.active to the list itself AND requests the matching Frame focus, so
// the bordered panel and the nav target never diverge (the Frame echoes a
// FocusChangedMsg back, re-confirming b.active).
func requestFocus(p tui.PanelID) tea.Cmd {
	return func() tea.Msg { return tui.FocusRequestMsg{Panel: p} }
}

// nearestRestoredAncestor returns the closest ancestor group of id that exists
// in the tree's node set (the leaf's direct group, then its parent, …). The
// empty string denotes the root node. Shared by exit/commit so the tree cursor
// always lands on a real node.
func (b *browser) nearestRestoredAncestor(id string) string {
	g := groupOf(id)
	for g != "" {
		if _, exists := b.tree.nodesByID[g]; exists {
			return g
		}
		g = groupOf(g)
	}
	return ""
}

// reselectOrigIdx re-positions the list cursor onto the row whose origIdx
// matches target (a no-op when target < 0). SetItems preserves the previous
// cursor index, not item identity, so the cursor must be re-found by origIdx.
// The !header guard prevents a pseudo-header's zero origIdx from colliding with
// items[0] when target == 0.
func (b *browser) reselectOrigIdx(target int) {
	if target < 0 {
		return
	}
	for i, li := range b.list.Items() {
		if it, ok := li.(listItem); ok && !it.header && it.origIdx == target {
			b.list.Select(i)
			return
		}
	}
}

// renderTreeFiltered renders the tree panel during a filter session: a query
// prompt line on top (Decision 3 — the search line lives in the tree panel, not
// a dimming overlay) followed by the filter-aware tree (M/N counts, zero-match
// dimming) clipped to the remaining height.
func (b *browser) renderTreeFiltered(inner tui.Region) string {
	// Respect the Frame-provided height budget: a 0-row region renders nothing
	// and a 1-row region shows only the query line, so the header+body split
	// never overflows the panel during small resizes.
	if inner.Height <= 0 {
		return ""
	}
	header := paletteKey().Bold(true).Render(b.filter.renderQueryLine())
	if inner.Height == 1 {
		return header
	}
	treeRegion := inner
	treeRegion.Height = max(inner.Height-1, 0)
	b.tree.ensureFocusVisible(treeRegion.Height)
	body := b.tree.renderRegion(treeRegion, b.active == panelTree, b.filter)
	if body == "" {
		return header
	}
	return header + "\n" + body
}

// refreshFilterMatches re-ranks the items against the current query and rebuilds
// the list with the flat result. When AutoCollapseEmpty is set the tree is
// re-expanded to show only subtrees containing matches. Reparented from
// *Model.refreshFilterMatches.
func (b *browser) refreshFilterMatches() {
	if b.filter == nil {
		return
	}
	b.filter.recompute(b.items, b.opts.IncludePrivate)
	out := make([]list.Item, 0, len(b.filter.matched))
	for _, idx := range b.filter.matched {
		it := b.items[idx]
		out = append(out, listItem{origIdx: idx, id: it.ID, desc: it.Description, typ: it.Type, paramCount: it.ParamCount})
	}
	b.list.SetItems(out)
	if b.opts.AutoCollapseEmpty {
		b.filter.applyAutoCollapse(b.tree)
	}
}

// inspectMaxWidth caps the inspect viewport on wide terminals so the content
// lines up with the section divider rendered by render.SectionTitle (which uses
// the same min(width, 100) cap). Without this, the inspect area would stretch to
// the full body width and the section dividers would float in dead space at the
// right edge. Relocated from the deleted model.go (Task 11).
const inspectMaxWidth = 100

// inspectBoxHChrome / inspectBoxVChrome are the cells the rounded-border modal
// box (inspectState.overlay) adds around the viewport: 2 border + 2 horizontal
// padding wide, 2 border rows tall. inspectViewportSize subtracts them (plus a
// one-row top/bottom margin) so the composited box never overflows the body
// region and Composite's last-resort clamp never has to trim the border edge.
const (
	inspectBoxHChrome = 4
	inspectBoxVChrome = 2
)

// inspectViewportSize returns the (width, height) for the inspect overlay's
// viewport. Width is capped at inspectMaxWidth so the content lines up with the
// section dividers render.SectionTitle draws (same min(width, 100) cap), then the
// border + padding chrome is reserved so the bordered box fits the body the Frame
// centres it over. Defensive lower clamps protect against degenerate sizes from
// transient resizes (and the zero-body case before the first Resize).
func (b *browser) inspectViewportSize() (int, int) {
	w := max(min(b.body.Width, inspectMaxWidth)-inspectBoxHChrome, 10)
	h := max(b.body.Height-inspectBoxVChrome-2, 3)
	return w, h
}

// openInspect builds the inspect viewport for the currently selected list item,
// stashes it on b.inspect, and marks it pending so PendingOverlay pushes the
// CapturesInput overlay onto the Frame stack on the next drain. A no-op when no
// selectable item is focused (e.g. an empty list or a header row).
func (b *browser) openInspect() {
	idx, ok := b.selectedOrigIdx()
	if !ok {
		return
	}
	w, h := b.inspectViewportSize()
	b.inspect = newInspectState(w, h, b.items[idx].Inspect, idx)
	b.inspectPending = true
}

// updateInspect drives the inspect overlay while it captures input (the Frame
// forwards every key except ctrl+c and esc here). Enter commits the inspected
// item as the result and quits; everything else is delegated to the viewport for
// scrolling (arrows / page / half-page / home-end per the viewport keymap). Esc
// never reaches here — the Frame's routeWhileCapturing handles it as a close
// (pop) — so this method only ever opens or scrolls, never closes.
func (b *browser) updateInspect(msg tea.KeyPressMsg) tea.Cmd {
	if b.inspect == nil {
		return nil
	}
	if msg.Code == tea.KeyEnter {
		b.result = Result{
			Idx:         b.inspect.inspectIdx,
			Action:      actionForMode(b.opts.Mode),
			SkipConfirm: b.skipConfirm,
		}
		return tea.Quit
	}
	var cmd tea.Cmd
	b.inspect.vp, cmd = b.inspect.vp.Update(msg)
	// The viewport may have scrolled; re-mark pending so the Frame re-pulls a
	// fresh overlay snapshot (replacing the top in place) and the new scroll
	// position actually paints. Without this the overlay stays frozen at the
	// position it had when first opened.
	b.inspectPending = true
	return cmd
}

// PendingOverlay implements tui.Plugin. It hands the inspect modal to the Frame
// when one is pending: inspectPending is set by openInspect (first paint) and by
// updateInspect (after a scroll key) and cleared here, so each republish yields
// exactly one overlay value. The Frame pushes the first one and replaces it in
// place on subsequent scrolls (refreshCapturingOverlay), so the stack never
// grows. The overlay is built fresh from the current viewport so the live scroll
// position is always reflected.
func (b *browser) PendingOverlay() (tui.Overlay, bool) {
	if b.inspect == nil || !b.inspectPending {
		return tui.Overlay{}, false
	}
	b.inspectPending = false
	return b.inspect.overlay(), true
}
