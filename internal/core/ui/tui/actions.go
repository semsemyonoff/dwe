package tui

import "fmt"

// Shared stdlib Action constants that plugins opt into. These are NOT
// auto-registered by [NewRegistry] — they are plugin-handled: the framework
// supplies the canonical keys and section; the plugin's HandleAction interprets
// them per its own context. Register any subset via [RegisterStandard].
//
// The IDs are stable and locked in Stage 1; default bindings are locked too.
// Plugins call RegisterStandard(reg, ActionNavUp, ActionNavDown, …) in their
// Actions hook and interpret these IDs in HandleAction.
const (
	// Navigation row — canonical list/table movement.
	ActionNavUp    Action = "nav.up"
	ActionNavDown  Action = "nav.down"
	ActionNavLeft  Action = "nav.left"
	ActionNavRight Action = "nav.right"

	// ActionSelect confirms / opens the focused item.
	ActionSelect Action = "select"

	// ActionFilter enters filter / search mode.
	ActionFilter Action = "filter"

	// ActionInspect opens the detail / inspect view.
	ActionInspect Action = "inspect"

	// ActionReload force-refreshes the displayed data.
	ActionReload Action = "reload"

	// ActionTop jumps to the first item. Dual-bound to "g" + "home" to unify
	// the cmdbrowser ("home") and docs-browser ("g") muscle memory.
	ActionTop Action = "nav.top"

	// ActionBottom jumps to the last item. Dual-bound to "G" + "end".
	ActionBottom Action = "nav.bottom"

	// ActionPageUp scrolls one page up.
	ActionPageUp Action = "nav.page-up"

	// ActionPageDown scrolls one page down.
	ActionPageDown Action = "nav.page-down"
)

// stdlib section labels used by the default bindings table. Added as new section
// constants here since the General/Navigation built-ins live in registry.go.
const (
	sectionFilter  = "Filter"
	sectionInspect = "Inspect"
)

// standardBindings is the default Binding table for each stdlib action. This is
// the single source of truth for default keys, sections, and descriptions.
// Plugins may read it via standardBinding for custom setups; most just call
// RegisterStandard.
var standardBindings = map[Action]Binding{
	ActionNavUp:    {Keys: []string{"up", "k"}, Desc: "Move up", Section: sectionNavigation},
	ActionNavDown:  {Keys: []string{"down", "j"}, Desc: "Move down", Section: sectionNavigation},
	ActionNavLeft:  {Keys: []string{"left", "h"}, Desc: "Move left", Section: sectionNavigation},
	ActionNavRight: {Keys: []string{"right", "l"}, Desc: "Move right", Section: sectionNavigation},
	ActionTop:      {Keys: []string{"g", "home"}, Desc: "Go to top", Section: sectionNavigation},
	ActionBottom:   {Keys: []string{"G", "end"}, Desc: "Go to bottom", Section: sectionNavigation},
	ActionPageUp:   {Keys: []string{"pgup", "b"}, Desc: "Page up", Section: sectionNavigation},
	ActionPageDown: {Keys: []string{"pgdn", "f"}, Desc: "Page down", Section: sectionNavigation},
	ActionSelect:   {Keys: []string{"enter"}, Desc: "Select", Section: sectionGeneral},
	ActionReload:   {Keys: []string{"ctrl+r"}, Desc: "Reload", Section: sectionGeneral},
	ActionFilter:   {Keys: []string{"/"}, Desc: "Filter", Section: sectionFilter},
	ActionInspect:  {Keys: []string{"i"}, Desc: "Inspect", Section: sectionInspect},
}

// RegisterStandard registers a subset of stdlib actions using their default
// bindings. It returns the first collision error and stops — earlier actions in
// the call may already be registered. Callers should pass all desired stdlib
// actions in a single call to get atomic-ish semantics (individual
// [Registry.Register] calls give the clearest per-action error if needed).
//
// Stdlib actions are opt-in: [NewRegistry] does not call this function. Plugins
// call it from their Actions hook:
//
//	RegisterStandard(reg, ActionNavUp, ActionNavDown, ActionSelect, ActionTop, ActionBottom)
func RegisterStandard(reg *Registry, actions ...Action) error {
	for _, a := range actions {
		b, ok := standardBindings[a]
		if !ok {
			return fmt.Errorf("tui: %q is not a stdlib action", a)
		}
		if err := reg.Register(a, b); err != nil {
			return err
		}
	}
	return nil
}

// standardBinding returns the default Binding for a stdlib action. The bool
// reports whether the action exists in the stdlib table. Useful for the
// fetch-then-customize pattern (fetch the default, tweak it, then call
// reg.Register manually).
//
// [RegisterStandard] is the primary entry point; this accessor is for the rare
// case where a plugin needs to inspect or modify a default before registering.
func standardBinding(a Action) (Binding, bool) {
	b, ok := standardBindings[a]
	return b, ok
}
