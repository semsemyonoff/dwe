package docstui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// Docs-custom action identifiers registered by the browser.
const (
	actionDiagramPrev   tui.Action = "diagram.prev"
	actionDiagramNext   tui.Action = "diagram.next"
	actionDiagramOpen   tui.Action = "diagram.open"
	actionDiagramCopy   tui.Action = "diagram.copy"
	actionDiagramError  tui.Action = "diagram.error"
	actionLocaleCycle   tui.Action = "locale.cycle"
	actionLocaleEnglish tui.Action = "locale.english"
	actionTreeCollapse  tui.Action = "tree.collapse"
	actionTreeExpand    tui.Action = "tree.expand"
	actionHalfPageUp    tui.Action = "nav.halfpage.up"
	actionHalfPageDown  tui.Action = "nav.halfpage.down"
)

// Help-modal section labels for the docs-custom action groups.
const (
	sectionDiagrams = "Diagrams"
	sectionLocales  = "Locales"
)

// Actions implements tui.Plugin. It registers all key bindings on the frame
// registry; the help modal is generated from these on every ? press.
//
// Standard navigation, select, filter, and reload use the stdlib defaults
// (ActionNavLeft/ActionNavRight are NOT registered — tree.collapse/tree.expand
// claim those keys with directional semantics instead of the old Toggle-on-both).
// Tab/Shift+Tab (focus), ?/q/esc/ctrl+c are framework built-ins — NOT registered.
func (b *browser) Actions(reg *tui.Registry) error {
	if err := tui.RegisterStandard(reg,
		tui.ActionNavUp,
		tui.ActionNavDown,
		tui.ActionTop,
		tui.ActionBottom,
		tui.ActionPageUp,
		tui.ActionPageDown,
		tui.ActionSelect,
		tui.ActionFilter,
		tui.ActionReload,
	); err != nil {
		return err
	}

	// Tree directional collapse/expand. Keys "left"/"h" and "right"/"l" map to
	// these instead of the stdlib NavLeft/NavRight. Intentional behavior change
	// from old Toggle-on-both: h collapses (or steps to parent), l expands (or
	// steps into first child). Documented in tui-keymap.md (Task 12).
	if err := reg.Register(actionTreeCollapse, tui.Binding{
		Keys:    []string{"left", "h"},
		Desc:    "Collapse / up to parent",
		Section: "Navigation",
	}); err != nil {
		return err
	}
	if err := reg.Register(actionTreeExpand, tui.Binding{
		Keys:    []string{"right", "l"},
		Desc:    "Expand / into first child",
		Section: "Navigation",
	}); err != nil {
		return err
	}

	// Half-page scroll (vim ctrl+d / ctrl+u) for reading the viewport without the
	// mouse — keyboard scroll is the primary path; the wheel is a bonus.
	if err := reg.Register(actionHalfPageDown, tui.Binding{
		Keys:    []string{"ctrl+d"},
		Desc:    "Half page down",
		Section: "Navigation",
	}); err != nil {
		return err
	}
	if err := reg.Register(actionHalfPageUp, tui.Binding{
		Keys:    []string{"ctrl+u"},
		Desc:    "Half page up",
		Section: "Navigation",
	}); err != nil {
		return err
	}

	// Diagram navigation and export.
	for _, spec := range []struct {
		a tui.Action
		b tui.Binding
	}{
		{actionDiagramPrev, tui.Binding{Keys: []string{"["}, Desc: "Previous diagram", Section: sectionDiagrams}},
		{actionDiagramNext, tui.Binding{Keys: []string{"]"}, Desc: "Next diagram", Section: sectionDiagrams}},
		{actionDiagramOpen, tui.Binding{Keys: []string{"o"}, Desc: "Open diagram", Section: sectionDiagrams}},
		{actionDiagramCopy, tui.Binding{Keys: []string{"y"}, Desc: "Copy diagram source", Section: sectionDiagrams}},
		{actionDiagramError, tui.Binding{Keys: []string{"E"}, Desc: "Show render error", Section: sectionDiagrams}},
	} {
		if err := reg.Register(spec.a, spec.b); err != nil {
			return err
		}
	}

	// Locale cycling and English fallback.
	if err := reg.Register(actionLocaleCycle, tui.Binding{
		Keys: []string{"L"}, Desc: "Cycle language", Section: sectionLocales,
	}); err != nil {
		return err
	}
	return reg.Register(actionLocaleEnglish, tui.Binding{
		Keys: []string{"e"}, Desc: "Show English", Section: sectionLocales,
	})
}

// HandleAction implements tui.Plugin. The framework matches a key to an Action
// and dispatches here; built-ins (help/quit/focus) never reach this method.
// Navigation routes to the active panel (tree or viewport). Locale effects and
// reload complete fully in Task 6; their tree mutations and topic loads are
// already wired here.
func (b *browser) HandleAction(a tui.Action) (tea.Cmd, bool) {
	// A bound key interrupts an in-flight wheel-scroll burst: drop the deferred
	// tree load so this action takes over immediately instead of racing a stale
	// topic render queued by the wheel.
	b.cancelWheelLoad()
	switch a {
	case tui.ActionNavUp:
		return b.navVertical(-1), true
	case tui.ActionNavDown:
		return b.navVertical(1), true
	case tui.ActionTop:
		return b.navHome(), true
	case tui.ActionBottom:
		return b.navEnd(), true
	case tui.ActionPageUp:
		return b.navPage(-1), true
	case tui.ActionPageDown:
		return b.navPage(1), true
	case actionHalfPageUp:
		return b.navHalfPage(-1), true
	case actionHalfPageDown:
		return b.navHalfPage(1), true
	case actionTreeCollapse:
		b.navLeft()
		return nil, true
	case actionTreeExpand:
		b.navRight()
		return nil, true
	case tui.ActionSelect:
		return b.onEnter()
	case tui.ActionFilter:
		b.enterFilter()
		return nil, true
	case tui.ActionReload:
		return b.onReload()
	case actionDiagramPrev:
		b.jumpToDiagram(-1)
		return nil, true
	case actionDiagramNext:
		b.jumpToDiagram(1)
		return nil, true
	case actionDiagramOpen:
		_ = b.openCurrentDiagram()
		return nil, true
	case actionDiagramError:
		b.openErrorOverlay()
		return nil, true
	case actionDiagramCopy:
		return b.doCopyDiagram(), true
	case actionLocaleCycle:
		return b.onLocaleCycle()
	case actionLocaleEnglish:
		return b.onLocaleEnglish()
	default:
		return nil, false
	}
}

// navVertical moves the cursor up (delta < 0) or down (delta > 0) in the
// active panel. In the viewport it scrolls one line; in the tree it moves
// the focused node and triggers a topic load.
func (b *browser) navVertical(delta int) tea.Cmd {
	if b.active == panelViewport {
		if b.Viewport == nil {
			return nil
		}
		b.moveViewportCursor(delta)
		b.syncViewportToCursor()
		b.syncActiveDiagram()
		return nil
	}
	if b.Tree == nil {
		return nil
	}
	if delta < 0 {
		b.Tree.MoveUp()
	} else {
		b.Tree.MoveDown()
	}
	return b.afterTreeMove()
}

// navLeft implements ←/h in the tree: collapse the focused node, or step the
// cursor to its parent when already collapsed. No-op when the viewport is focused.
func (b *browser) navLeft() {
	if b.active != panelTree || b.Tree == nil {
		return
	}
	b.Tree.Collapse()
	b.Tree.eng.EnsureFocusVisible(b.treeInner.Height)
}

// navRight implements →/l in the tree: expand the focused node, or step the
// cursor into its first child when already expanded. No-op when the viewport
// is focused.
func (b *browser) navRight() {
	if b.active != panelTree || b.Tree == nil {
		return
	}
	b.Tree.Expand()
	b.Tree.eng.EnsureFocusVisible(b.treeInner.Height)
}

// navHome jumps to the top of the active panel.
func (b *browser) navHome() tea.Cmd {
	if b.active == panelViewport {
		if b.Viewport != nil {
			b.setViewportCursor(0)
			b.Viewport.ScrollStart()
			b.syncActiveDiagram()
		}
		return nil
	}
	if b.Tree == nil {
		return nil
	}
	b.Tree.MoveStart()
	return b.afterTreeMove()
}

// navEnd jumps to the bottom of the active panel.
func (b *browser) navEnd() tea.Cmd {
	if b.active == panelViewport {
		if b.Viewport != nil {
			b.setViewportCursor(b.Viewport.TotalLines() - 1)
			b.Viewport.ScrollEnd()
			b.syncActiveDiagram()
		}
		return nil
	}
	if b.Tree == nil {
		return nil
	}
	b.Tree.MoveEnd()
	return b.afterTreeMove()
}

// navPage scrolls one page up (delta < 0) or down (delta > 0) in the active
// panel. In the tree it jumps the cursor by the inner panel height (cmdbrowser
// pattern); in the viewport it uses the bubbles page methods.
func (b *browser) navPage(delta int) tea.Cmd {
	if b.active == panelViewport {
		if b.Viewport == nil {
			return nil
		}
		step := max(b.Viewport.VisibleHeight(), 1)
		if delta < 0 {
			step = -step
		}
		b.moveViewportCursor(step)
		b.syncViewportToCursor()
		b.syncActiveDiagram()
		return nil
	}
	if b.Tree == nil {
		return nil
	}
	h := max(b.treeInner.Height, 1)
	for range h {
		if delta < 0 {
			b.Tree.MoveUp()
		} else {
			b.Tree.MoveDown()
		}
	}
	return b.afterTreeMove()
}

// navHalfPage scrolls half a page up (delta < 0) or down (delta > 0) in the
// active panel — the vim ctrl+u / ctrl+d reading motion. In the viewport it
// scrolls by half the visible height; in the tree it jumps the cursor by half
// the inner panel height.
func (b *browser) navHalfPage(delta int) tea.Cmd {
	if b.active == panelViewport {
		if b.Viewport == nil {
			return nil
		}
		step := max(b.Viewport.VisibleHeight()/2, 1)
		if delta < 0 {
			step = -step
		}
		b.moveViewportCursor(step)
		b.syncViewportToCursor()
		b.syncActiveDiagram()
		return nil
	}
	if b.Tree == nil {
		return nil
	}
	h := max(b.treeInner.Height/2, 1)
	for range h {
		if delta < 0 {
			b.Tree.MoveUp()
		} else {
			b.Tree.MoveDown()
		}
	}
	return b.afterTreeMove()
}

// afterTreeMove keeps the focused tree row on screen and re-syncs to the topic
// behind the cursor. The returned Cmd carries the async topic load (the
// topicLoadedMsg handler in Task 6 applies the rendered content).
func (b *browser) afterTreeMove() tea.Cmd {
	if b.Tree != nil {
		b.Tree.eng.EnsureFocusVisible(b.treeInner.Height)
	}
	return b.selectCursor()
}

// onEnter handles ActionSelect in the tree. For expandable nodes it expands
// the node and moves focus to the viewport so the user can read the content.
// For leaf nodes it loads the topic and also moves focus to the viewport.
func (b *browser) onEnter() (tea.Cmd, bool) {
	// In the viewport, Enter follows the first internal link on the cursor row
	// (external links are left to the terminal). Multi-link rows: first wins.
	if b.active == panelViewport {
		if href, ok := b.firstInternalLinkOnRow(b.viewportCursor); ok {
			return b.followLink(href), true
		}
		return nil, true
	}
	if b.active != panelTree || b.Tree == nil || b.Tree.Cursor() == nil {
		return nil, true
	}
	node := b.Tree.Cursor()
	if b.Tree.IsDir(node) || (node.Heading == nil && len(node.Children) > 0) {
		if !b.Tree.IsExpanded(node) {
			b.Tree.Toggle()
		}
		loadCmd := b.selectCursor()
		return tea.Batch(loadCmd, focusCmd(panelViewport)), true
	}
	loadCmd := b.selectCursor()
	return tea.Batch(loadCmd, focusCmd(panelViewport)), true
}

// onReload handles ActionReload: forces a fresh topic load from disk.
func (b *browser) onReload() (tea.Cmd, bool) {
	if b.Tree == nil || b.Tree.Cursor() == nil {
		return nil, true
	}
	cmd, _ := b.loadTopic(b.Tree.Cursor())
	return cmd, true
}

// doCopyDiagram copies the current diagram source to the system clipboard.
// It returns a tea.SetClipboard command rather than writing OSC-52 straight to
// os.Stdout: the bubbletea program owns the output stream, so a direct write
// races the renderer and is frequently dropped/garbled (the "y does nothing"
// symptom). Routing through the program emits the escape in sync with the frame.
func (b *browser) doCopyDiagram() tea.Cmd {
	if b.DiagramState == nil {
		return nil
	}
	d := b.DiagramState.CurrentDiagram()
	if d == nil {
		return nil
	}
	// Confirm the copy in the status line — the clipboard write is otherwise
	// invisible, so users could not tell `y` did anything.
	return tea.Batch(
		tea.SetClipboard(d.Source),
		b.setStatusFlash("✓ Diagram source copied to clipboard"),
	)
}

// onLocaleCycle advances to the next available locale for the current topic.
// Rebuilds the tree (so headings reflect the new locale) and re-loads the topic.
func (b *browser) onLocaleCycle() (tea.Cmd, bool) {
	if b.Tree == nil || b.Tree.Cursor() == nil || len(b.AvailableLocales) == 0 {
		return nil, true
	}
	idx := -1
	for i, l := range b.AvailableLocales {
		if l == b.Locale {
			idx = i
			break
		}
	}
	b.Locale = b.AvailableLocales[(idx+1)%len(b.AvailableLocales)]
	return b.applyLocaleChange(), true
}

// onLocaleEnglish switches to English when the current topic's source is not
// English. No-op when already reading the English source.
func (b *browser) onLocaleEnglish() (tea.Cmd, bool) {
	if b.Tree == nil || b.Tree.Cursor() == nil || b.CurrentSourceLang == "en" {
		return nil, true
	}
	b.Locale = "en"
	return b.applyLocaleChange(), true
}

// focusCmd returns a Cmd that asks the Frame to move focus to the given panel.
func focusCmd(panel tui.PanelID) tea.Cmd {
	return func() tea.Msg { return tui.FocusRequestMsg{Panel: panel} }
}
