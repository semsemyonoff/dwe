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

// browser is the cmdbrowser surface migrated onto the tui framework. It is a
// [tui.Plugin]: the Frame owns chrome (borders, focus highlight, Tab cycling,
// geometry, the status line) and the browser owns body content and behaviour.
//
// It replaces the hand-rolled *Model (model.go), which still backs the legacy
// Run path until Task 11 deletes it. The plugin holds the cmdbrowser-local
// tree (left) and a bubbles/v2 list (right); per-panel rendering happens in
// ViewPanel against the inner regions the Frame computes.
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
	// is non-nil). The inspect overlay sub-state lands in Task 8.
	filter *filterState

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

// Update implements tui.Plugin. Message handling (filter capture, inspect
// scroll, mouse) lands in Tasks 7–9; the skeleton ignores messages.
func (b *browser) Update(_ tea.Msg) tea.Cmd { return nil }

// ViewPanel implements tui.Plugin. Per-panel rendering lands in Tasks 4–5; the
// skeleton caches the inner regions so mouse translation and re-renders can
// reuse them, and renders nothing yet.
func (b *browser) ViewPanel(id tui.PanelID, inner tui.Region) string {
	switch id {
	case panelTree:
		b.treeInner = inner
	case panelList:
		b.listInner = inner
	}
	return ""
}

// Actions implements tui.Plugin. Per-mode action registration lands in Task 6.
func (b *browser) Actions(_ *tui.Registry) error { return nil }

// HandleAction implements tui.Plugin. Action dispatch lands in Task 6.
func (b *browser) HandleAction(_ tui.Action) (tea.Cmd, bool) { return nil, false }

// PendingOverlay implements tui.Plugin. The inspect overlay lands in Task 8.
func (b *browser) PendingOverlay() (tui.Overlay, bool) { return tui.Overlay{}, false }
