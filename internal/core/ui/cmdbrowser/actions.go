package cmdbrowser

import (
	tea "charm.land/bubbletea/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// Plugin-defined actions, registered only in ModeRun. They have no stdlib
// equivalent: skip-confirm toggles the `--yes` flag carried in Result, and
// force-form selects the current command but forces the orchestrator to open
// the param form even when every default is already satisfied.
const (
	actionSkipConfirm tui.Action = "cmd.skip-confirm"
	actionForceForm   tui.Action = "cmd.force-form"
)

// sectionActions is the help-modal section label for the browser's verbs
// (select / filter / inspect / skip-confirm / force-form). Navigation reuses the
// stdlib "Navigation" section (the RegisterStandard defaults already land there
// alongside the framework's Tab/Shift+Tab focus cycling), so it needs no
// constant here.
const sectionActions = "Actions"

// Actions implements tui.Plugin. It registers the browser's key bindings on the
// frame registry every render of the help modal is generated from these.
//
// Navigation (tree/list movement + wheel scroll) is always registered via the
// stdlib defaults — their canonical keys and the wheel-up/down mouse bindings
// are exactly what the browser wants. select / filter / inspect are registered
// explicitly so they regroup under "Actions" (the stdlib defaults split them
// across General/Filter/Inspect). The two ModeRun-only verbs — skip-confirm
// (`y`) and force-form (`e`) — are registered ONLY in ModeRun, so they are
// absent from the help modal and inert in ModeEdit/ModeInspect automatically.
//
// Tab/Shift+Tab (focus), ?/q/esc/ctrl+c are framework built-ins and are NOT
// registered here.
func (b *browser) Actions(reg *tui.Registry) error {
	if err := tui.RegisterStandard(reg,
		tui.ActionNavUp, tui.ActionNavDown, tui.ActionNavLeft, tui.ActionNavRight,
		tui.ActionTop, tui.ActionBottom, tui.ActionPageUp, tui.ActionPageDown,
	); err != nil {
		return err
	}

	// select — Enter / double-click. ModeEdit (vars browser) relabels it "Edit"
	// to match the legacy footer; every other mode keeps "Select".
	selectDesc := "Select"
	if b.opts.Mode == ModeEdit {
		selectDesc = "Edit"
	}
	if err := reg.Register(tui.ActionSelect, tui.Binding{
		Keys: []string{"enter"}, Desc: selectDesc, Section: sectionActions, Mouse: "double-click",
	}); err != nil {
		return err
	}
	if err := reg.Register(tui.ActionFilter, tui.Binding{
		Keys: []string{"/"}, Desc: "Filter", Section: sectionActions,
	}); err != nil {
		return err
	}
	if err := reg.Register(tui.ActionInspect, tui.Binding{
		Keys: []string{"i"}, Desc: "Inspect", Section: sectionActions,
	}); err != nil {
		return err
	}

	// ModeRun-only verbs. Absent from ModeEdit/ModeInspect registries → hidden
	// from help and never dispatched in those modes.
	if b.opts.Mode == ModeRun {
		if err := reg.Register(actionSkipConfirm, tui.Binding{
			Keys: []string{"y"}, Desc: "Skip confirmation", Section: sectionActions,
		}); err != nil {
			return err
		}
		if err := reg.Register(actionForceForm, tui.Binding{
			Keys: []string{"e"}, Desc: "Edit parameters", Section: sectionActions,
		}); err != nil {
			return err
		}
	}
	return nil
}

// actionForMode maps Mode → Action so Result carries the right intent for the
// caller. Mode is normalised by Options.applyDefaults before reaching here.
// Relocated from the deleted model.go (Task 11).
func actionForMode(mode Mode) Action {
	switch mode {
	case ModeInspect:
		return ActionInspect
	case ModeEdit:
		return ActionEdit
	default:
		return ActionRun
	}
}

// HandleAction implements tui.Plugin. The frame matches a key to an Action and
// dispatches here; built-ins (help/quit/focus) never reach this method.
// Navigation and select route to the active panel — tree and list are distinct
// widgets with distinct movement, so dispatch cannot be panel-agnostic. The
// active panel is tracked via FocusChangedMsg (wired in Task 9); it starts on
// the tree (focus index 0).
//
// A returned (cmd, true) is dispatched by the frame; (nil, false) lets the
// frame forward the raw key to Update. Every action the browser registers is
// handled here, so the default path is only reached for unregistered actions.
func (b *browser) HandleAction(a tui.Action) (tea.Cmd, bool) {
	switch a {
	case tui.ActionNavUp:
		b.navVertical(-1)
	case tui.ActionNavDown:
		b.navVertical(1)
	case tui.ActionNavLeft:
		b.navLeft()
	case tui.ActionNavRight:
		b.navRight()
	case tui.ActionTop:
		b.navHome()
	case tui.ActionBottom:
		b.navEnd()
	case tui.ActionPageUp:
		b.navPage(-1)
	case tui.ActionPageDown:
		b.navPage(1)
	case tui.ActionSelect:
		return b.onSelect()
	case tui.ActionFilter:
		b.enterFilter()
	case tui.ActionInspect:
		b.openInspect()
	case actionSkipConfirm:
		if b.opts.Mode == ModeRun {
			b.skipConfirm = !b.skipConfirm
		}
	case actionForceForm:
		return b.onForceForm()
	default:
		return nil, false
	}
	return nil, true
}

// navVertical moves the cursor up (delta < 0) or down (delta > 0) in the active
// panel. In the list this advances the bubbles cursor (paging as needed); in
// the tree it moves the focused node and re-syncs the list to the new group.
func (b *browser) navVertical(delta int) {
	if b.active == panelList {
		if delta < 0 {
			b.list.CursorUp()
		} else {
			b.list.CursorDown()
		}
		return
	}
	if delta < 0 {
		b.tree.moveUp()
	} else {
		b.tree.moveDown()
	}
	b.afterTreeMove()
}

// navLeft handles ←/h. In the tree it collapses the focused node or steps to its
// parent (treeModel.onLeft). In the list it is a no-op: focus is now Tab/click
// only, so the legacy left-arrow "return to tree" affordance (model.go updateRight)
// is intentionally gone — panel switching belongs to the frame.
func (b *browser) navLeft() {
	if b.active == panelList {
		return
	}
	b.tree.onLeft()
	b.afterTreeMove()
}

// navRight handles →/l. In the tree it expands the focused node or steps into
// its first child (treeModel.onRight). In the list it is a no-op (see navLeft).
func (b *browser) navRight() {
	if b.active == panelList {
		return
	}
	b.tree.onRight()
	b.afterTreeMove()
}

// navHome jumps to the first row of the active panel.
func (b *browser) navHome() {
	if b.active == panelList {
		b.list.GoToStart()
		return
	}
	b.tree.moveHome()
	b.afterTreeMove()
}

// navEnd jumps to the last row of the active panel.
func (b *browser) navEnd() {
	if b.active == panelList {
		b.list.GoToEnd()
		return
	}
	b.tree.moveEnd()
	b.afterTreeMove()
}

// navPage scrolls one viewport up (delta < 0) or down (delta > 0). The list uses
// the bubbles paginator. The legacy tree had no page binding; mapping the page
// keys to a cursor jump of the inner viewport height is an additive convenience.
func (b *browser) navPage(delta int) {
	if b.active == panelList {
		if delta < 0 {
			b.list.PrevPage()
		} else {
			b.list.NextPage()
		}
		return
	}
	h := max(b.treeInner.Height, 1)
	for range h {
		if delta < 0 {
			b.tree.moveUp()
		} else {
			b.tree.moveDown()
		}
	}
	b.afterTreeMove()
}

// afterTreeMove keeps the focused tree row on screen and re-syncs the list to
// the (possibly new) focused group. Called after every tree mutation.
func (b *browser) afterTreeMove() {
	b.tree.ensureFocusVisible(b.treeInner.Height)
	b.refreshList()
}

// onSelect implements Enter / double-click. On a tree group it toggles
// expansion (matching the single-click-moves / double-click-toggles mouse model,
// Decision 7). On a list item it commits the selection: Result carries the
// original item index, the mode-derived Action, and the current skip-confirm
// flag, then quits.
func (b *browser) onSelect() (tea.Cmd, bool) {
	if b.active == panelTree {
		b.tree.toggleFocused()
		b.afterTreeMove()
		return nil, true
	}
	idx, ok := b.selectedOrigIdx()
	if !ok {
		return nil, true
	}
	b.result = Result{Idx: idx, Action: actionForMode(b.opts.Mode), SkipConfirm: b.skipConfirm}
	return tea.Quit, true
}

// onForceForm implements `e` (ModeRun, list panel only — mirrors the legacy
// EditParams which was handled solely in the right/list panel). It commits the
// current list item like onSelect but sets ForceParamForm so the orchestrator
// opens the param form even when all defaults are satisfied.
func (b *browser) onForceForm() (tea.Cmd, bool) {
	if b.opts.Mode != ModeRun || b.active != panelList {
		return nil, true
	}
	idx, ok := b.selectedOrigIdx()
	if !ok {
		return nil, true
	}
	b.result = Result{
		Idx:            idx,
		Action:         actionForMode(b.opts.Mode),
		SkipConfirm:    b.skipConfirm,
		ForceParamForm: true,
	}
	return tea.Quit, true
}
