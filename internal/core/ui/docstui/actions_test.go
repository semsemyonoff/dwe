package docstui

import (
	"context"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/docs"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// newTestBrowserWithHeadings builds a browser with a tree that has a file
// containing H2 headings. The file node has heading children so it is
// expandable — needed for Collapse/Expand directional tests.
func newTestBrowserWithHeadings(t *testing.T) *browser {
	t.Helper()
	// filterFixtureFS (defined in tree_widget_test.go) has guide.md with
	// one H2 heading — giving us a file node with a child heading node.
	roots := []docs.DocRoot{{Name: "test", FS: filterFixtureFS{}}}
	m, err := NewModel(
		context.Background(),
		roots,
		"en",
		nil,
		nil,
		80, 24,
		"",
		"Test",
		"auto",
	)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	return newBrowser(context.Background(), m)
}

// --- Actions registration ---

func TestActions_RegistersWithoutCollision(t *testing.T) {
	b := newTestBrowser(t)
	reg := tui.NewRegistry()
	if err := b.Actions(reg); err != nil {
		t.Errorf("Actions() returned error: %v", err)
	}
}

func TestActions_RegistersAllExpectedActions(t *testing.T) {
	b := newTestBrowser(t)
	reg := tui.NewRegistry()
	_ = b.Actions(reg)

	want := []tui.Action{
		tui.ActionNavUp, tui.ActionNavDown,
		tui.ActionTop, tui.ActionBottom,
		tui.ActionPageUp, tui.ActionPageDown,
		tui.ActionSelect, tui.ActionFilter, tui.ActionReload,
		actionTreeCollapse, actionTreeExpand,
		actionDiagramPrev, actionDiagramNext, actionDiagramOpen, actionDiagramCopy,
		actionLocaleCycle, actionLocaleEnglish,
	}
	for _, a := range want {
		if _, ok := reg.Binding(a); !ok {
			t.Errorf("action %q not registered", a)
		}
	}
}

func TestActions_BuiltinsNotRegistered(t *testing.T) {
	// Tab, shift+tab, ?, q, esc, ctrl+c are framework built-ins. Registering
	// them here would cause a collision and is explicitly forbidden.
	b := newTestBrowser(t)
	reg := tui.NewRegistry()
	_ = b.Actions(reg)

	// These built-in keys must NOT be claimed by any plugin action.
	builtinKeys := []string{"tab", "shift+tab", "?", "q", "ctrl+c", "esc"}
	for _, k := range builtinKeys {
		if a, ok := reg.Binding(tui.ActionQuit); ok && k == "q" {
			// q is a built-in ActionQuit key — already in the registry from
			// NewRegistry(); plugin should not add a second binding for it.
			_ = a // suppress unused warning
		}
		// Confirm none of the PLUGIN-registered actions use these keys.
		pluginActions := []tui.Action{
			actionTreeCollapse, actionTreeExpand,
			actionDiagramPrev, actionDiagramNext, actionDiagramOpen, actionDiagramCopy,
			actionLocaleCycle, actionLocaleEnglish,
		}
		for _, pa := range pluginActions {
			if b, ok := reg.Binding(pa); ok {
				for _, bk := range b.Keys {
					if bk == k {
						t.Errorf("plugin action %q claims built-in key %q", pa, k)
					}
				}
			}
		}
	}
}

func TestActions_ReloadBoundToCtrlR(t *testing.T) {
	b := newTestBrowser(t)
	reg := tui.NewRegistry()
	_ = b.Actions(reg)

	a, ok := reg.Match("ctrl+r")
	if !ok {
		t.Fatal("ctrl+r not bound in registry")
	}
	if a != tui.ActionReload {
		t.Errorf("ctrl+r bound to %q, want %q", a, tui.ActionReload)
	}
}

func TestActions_DiagramsInOwnSection(t *testing.T) {
	b := newTestBrowser(t)
	reg := tui.NewRegistry()
	_ = b.Actions(reg)

	sections := reg.Sections()
	var foundDiagrams bool
	for _, sec := range sections {
		if sec.Name == sectionDiagrams {
			foundDiagrams = true
			wantActions := []tui.Action{actionDiagramPrev, actionDiagramNext, actionDiagramOpen, actionDiagramCopy}
			got := make(map[tui.Action]bool)
			for _, e := range sec.Entries {
				got[e.Action] = true
			}
			for _, want := range wantActions {
				if !got[want] {
					t.Errorf("action %q missing from %q section", want, sectionDiagrams)
				}
			}
		}
	}
	if !foundDiagrams {
		t.Errorf("%q section not found in help", sectionDiagrams)
	}
}

func TestActions_LocalesInOwnSection(t *testing.T) {
	b := newTestBrowser(t)
	reg := tui.NewRegistry()
	_ = b.Actions(reg)

	sections := reg.Sections()
	var foundLocales bool
	for _, sec := range sections {
		if sec.Name == sectionLocales {
			foundLocales = true
			got := make(map[tui.Action]bool)
			for _, e := range sec.Entries {
				got[e.Action] = true
			}
			if !got[actionLocaleCycle] {
				t.Errorf("%q missing from Locales section", actionLocaleCycle)
			}
			if !got[actionLocaleEnglish] {
				t.Errorf("%q missing from Locales section", actionLocaleEnglish)
			}
		}
	}
	if !foundLocales {
		t.Errorf("%q section not found in help", sectionLocales)
	}
}

// --- HandleAction dispatch ---

func TestHandleAction_AllRegisteredActionsHandled(t *testing.T) {
	b := newTestBrowser(t)
	reg := tui.NewRegistry()
	_ = b.Actions(reg)

	sections := reg.Sections()
	for _, sec := range sections {
		for _, e := range sec.Entries {
			// Skip built-in actions which are handled by the frame, not the plugin.
			switch e.Action {
			case tui.ActionHelp, tui.ActionQuit, tui.ActionFocusNext, tui.ActionFocusPrev:
				continue
			}
			_, handled := b.HandleAction(e.Action)
			if !handled {
				t.Errorf("HandleAction(%q) handled=false; expected true for registered action", e.Action)
			}
		}
	}
}

func TestHandleAction_UnknownActionReturnsFalse(t *testing.T) {
	b := newTestBrowser(t)
	_, handled := b.HandleAction(tui.Action("unknown.action"))
	if handled {
		t.Error("HandleAction(unknown) handled=true; expected false")
	}
}

// --- Nav routing by active panel ---

func TestHandleAction_NavUp_RoutesToTreeWhenTreeActive(t *testing.T) {
	b := newTestBrowserWithHeadings(t)

	// Expand the file node so there are at least 2 visible rows to navigate.
	if b.Tree != nil && b.Tree.Cursor() != nil {
		b.Tree.Toggle() // expand to show headings
	}
	visibleBefore := len(b.Tree.VisibleNodes())
	if visibleBefore < 2 {
		t.Skip("tree has fewer than 2 visible nodes; cannot test nav up")
	}

	b.active = panelTree
	b.Tree.MoveEnd() // go to last visible node

	cursorBefore := b.Tree.Cursor()
	b.HandleAction(tui.ActionNavUp)
	cursorAfter := b.Tree.Cursor()

	if cursorAfter == cursorBefore {
		t.Error("NavUp with tree active: cursor did not move in tree")
	}
}

func TestHandleAction_NavDown_RoutesToViewportWhenViewportActive(t *testing.T) {
	b := newTestBrowser(t)
	b.Viewport.SetContent(tallContent(200))
	b.Viewport.SetDimensions(60, 10)

	b.active = panelViewport
	b.setViewportCursor(50)
	b.syncViewportToCursor()
	cursorBefore := b.viewportCursor

	b.HandleAction(tui.ActionNavDown)

	if b.viewportCursor <= cursorBefore {
		t.Errorf("NavDown with viewport active: cursor not advanced (before=%d, after=%d)", cursorBefore, b.viewportCursor)
	}
}

func TestHandleAction_NavUp_RoutesToViewportWhenViewportActive(t *testing.T) {
	b := newTestBrowser(t)
	b.Viewport.SetContent(tallContent(200))
	b.Viewport.SetDimensions(60, 10)

	b.active = panelViewport
	b.setViewportCursor(50)
	b.syncViewportToCursor()
	cursorBefore := b.viewportCursor

	b.HandleAction(tui.ActionNavUp)

	if b.viewportCursor >= cursorBefore {
		t.Errorf("NavUp with viewport active: cursor not retreated (before=%d, after=%d)", cursorBefore, b.viewportCursor)
	}
}

// --- Directional h/l semantics ---

func TestTreeWidget_Collapse_CollapsesExpandedNode(t *testing.T) {
	b := newTestBrowserWithHeadings(t)
	// The file node "guide.md" should have heading children and start collapsed.
	node := b.Tree.Cursor()
	if node == nil {
		t.Fatal("tree cursor is nil")
	}
	if len(node.Children) == 0 {
		t.Skip("cursor node has no children; cannot test collapse")
	}

	// Expand it first.
	b.Tree.SetExpanded(node, true)
	b.Tree.recomputeVisible()
	if !b.Tree.IsExpanded(node) {
		t.Fatal("pre-condition: node should be expanded")
	}

	// Press h → collapse.
	b.active = panelTree
	b.HandleAction(actionTreeCollapse)

	if b.Tree.IsExpanded(node) {
		t.Error("h (actionTreeCollapse) on expanded node: node is still expanded; expected collapsed")
	}
}

func TestTreeWidget_Collapse_StepsToParentWhenAlreadyCollapsed(t *testing.T) {
	b := newTestBrowserWithHeadings(t)
	// Expand the file node to reveal heading children.
	node := b.Tree.Cursor()
	if node == nil || len(node.Children) == 0 {
		t.Skip("no expandable node in test tree")
	}
	b.Tree.SetExpanded(node, true)
	b.Tree.recomputeVisible()

	// Move cursor to the first heading child (which has no children and is "collapsed").
	child := node.Children[0]
	b.Tree.SetCursor(child)
	if b.Tree.Cursor() != child {
		t.Fatal("cursor not set to child heading")
	}

	// Press h on the heading (already "collapsed" — heading is a leaf).
	b.active = panelTree
	b.HandleAction(actionTreeCollapse)

	// Cursor should have stepped to the parent file node.
	if b.Tree.Cursor() != node {
		t.Errorf("h on collapsed leaf: cursor = %v, want parent node %v", b.Tree.Cursor(), node)
	}
}

func TestTreeWidget_Expand_ExpandsCollapsedNode(t *testing.T) {
	b := newTestBrowserWithHeadings(t)
	node := b.Tree.Cursor()
	if node == nil || len(node.Children) == 0 {
		t.Skip("no expandable node in test tree")
	}
	// Ensure it's collapsed.
	b.Tree.SetExpanded(node, false)
	b.Tree.recomputeVisible()

	b.active = panelTree
	b.HandleAction(actionTreeExpand)

	if !b.Tree.IsExpanded(node) {
		t.Error("l (actionTreeExpand) on collapsed node: node is still collapsed; expected expanded")
	}
}

func TestTreeWidget_Expand_StepsIntoFirstChildWhenExpanded(t *testing.T) {
	b := newTestBrowserWithHeadings(t)
	node := b.Tree.Cursor()
	if node == nil || len(node.Children) == 0 {
		t.Skip("no expandable node in test tree")
	}
	// Expand the node first.
	b.Tree.SetExpanded(node, true)
	b.Tree.recomputeVisible()

	b.active = panelTree
	b.HandleAction(actionTreeExpand)

	if b.Tree.Cursor() != node.Children[0] {
		t.Errorf("l on expanded node: cursor = %v, want first child %v", b.Tree.Cursor(), node.Children[0])
	}
}

// TestHandleAction_DirectionalDoesNotToggle locks the intentional change from
// the old behaviour where both h and l called Toggle() (expanding if collapsed,
// collapsing if expanded). The new directional semantics are:
//   - h = collapse OR step to parent (never expands)
//   - l = expand OR step into first child (never collapses)
func TestHandleAction_DirectionalDoesNotToggle(t *testing.T) {
	b := newTestBrowserWithHeadings(t)
	node := b.Tree.Cursor()
	if node == nil || len(node.Children) == 0 {
		t.Skip("no expandable node in test tree")
	}

	b.active = panelTree

	// Start collapsed — h should NOT expand it (old Toggle would have).
	b.Tree.SetExpanded(node, false)
	b.Tree.recomputeVisible()
	b.HandleAction(actionTreeCollapse) // h on collapsed
	if b.Tree.IsExpanded(node) {
		t.Error("h on collapsed node expanded it (old Toggle behavior); expected step-to-parent only")
	}

	// Start expanded — l should NOT collapse it (old Toggle would have).
	b.Tree.SetExpanded(node, true)
	b.Tree.recomputeVisible()
	b.Tree.SetCursor(node)
	b.HandleAction(actionTreeExpand) // l on expanded
	if !b.Tree.IsExpanded(node) {
		t.Error("l on expanded node collapsed it (old Toggle behavior); expected step-into-child only")
	}
}

// --- TreeWidget.Collapse / TreeWidget.Expand unit tests ---

func TestTreeWidgetCollapse_HeadingRowStepsToParent(t *testing.T) {
	b := newTestBrowserWithHeadings(t)
	node := b.Tree.Cursor()
	if node == nil || len(node.Children) == 0 {
		t.Skip("no expandable node")
	}
	b.Tree.SetExpanded(node, true)
	b.Tree.recomputeVisible()
	child := node.Children[0] // heading child (leaf — no children, Expanded always false)
	b.Tree.SetCursor(child)

	// Collapse on a heading row steps to its parent, mirroring cmdbrowser onLeft
	// behaviour: headings are always "collapsed" so the step-to-parent branch runs.
	b.Tree.Collapse()
	if b.Tree.Cursor() != node {
		t.Errorf("Collapse on heading: cursor = %v, want parent %v", b.Tree.Cursor(), node)
	}
}

func TestTreeWidgetExpand_NoOpOnNodeWithNoChildren(t *testing.T) {
	// Use the basic test browser with a flat file — no children, no expansion.
	b := newTestBrowser(t)
	node := b.Tree.Cursor()
	if node == nil {
		t.Skip("empty tree")
	}
	if len(node.Children) > 0 {
		t.Skip("cursor node has children; need a leaf for this test")
	}
	cursorBefore := b.Tree.Cursor()
	b.Tree.Expand()
	if b.Tree.Cursor() != cursorBefore {
		t.Error("Expand on leaf node moved cursor; expected no-op")
	}
	if b.Tree.IsExpanded(node) {
		t.Error("Expand on leaf node set Expanded=true; expected no change")
	}
}
