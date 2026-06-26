package tui

import "fmt"

// Action is the stable identifier for one keyboard-triggered behaviour (help
// toggle, quit, focus cycling, or a plugin-defined verb). It is decoupled from
// the physical key(s) so Stage 1 can add rebinding without touching dispatch
// sites — callers switch on the Action, never on the raw key.
type Action string

// The framework's built-in actions. Their default [Binding]s are registered by
// [NewRegistry]. The IDs are stable; the default keys are locked in Stage 1 —
// the rebinding mechanism gated by [Binding.Rebindable] will let projects
// override them later.
const (
	// ActionHelp toggles the ?-modal help overlay.
	ActionHelp Action = "help"
	// ActionQuit exits the TUI.
	ActionQuit Action = "quit"
	// ActionFocusNext moves focus to the next panel (left→right, wrapping).
	ActionFocusNext Action = "focus.next"
	// ActionFocusPrev moves focus to the previous panel (wrapping).
	ActionFocusPrev Action = "focus.prev"
)

// Binding describes how an [Action] is triggered and how it presents in the help
// modal. The registry/keymap surface is locked in Stage 1; see [Package tui].
type Binding struct {
	// Keys are the physical key strings (bubbletea key syntax, e.g. "?", "tab",
	// "ctrl+c") that trigger the action. At least one is required. Keys are
	// shown in the help modal and participate in [Registry.Match] dispatch.
	Keys []string
	// Desc is the English help description for the binding.
	Desc string
	// Section is the help-modal section label this binding groups under. Sections
	// appear in first-registration order; see [Registry.Sections].
	Section string

	// Aliases are additional physical key strings that dispatch to the action
	// (wired into [Registry.Match]) but are hidden from the help modal. They
	// exist for muscle-memory compatibility — e.g. "esc" as a quit alias —
	// without cluttering the help display. Locked in Stage 1.
	//
	// Precedence when an overlay is open: the frame's modal-input policy
	// consumes "esc" to close the overlay before the registry is consulted;
	// "esc" only reaches [ActionQuit] in normal mode (no overlay). See the
	// [CapturesInput] contract for the capturing-overlay variant.
	Aliases []string
	// Rebindable marks whether a project may override Keys via a future
	// rebinding config. This is documented metadata only — no config loader is
	// built yet (YAGNI; no consumer until Stage 3). Locked in Stage 1.
	Rebindable bool
	// Mouse is the mouse-event string that triggers this action, resolved via
	// [Registry.MatchMouse]. Wired in Stage 2. Locked vocabulary: "wheel-up",
	// "wheel-down", "double-click". "click" is intentionally frame-owned and is
	// never registered as a mouse binding.
	Mouse string
}

// Entry pairs an [Action] with its [Binding] for ordered help generation.
type Entry struct {
	Action  Action
	Binding Binding
}

// Section is a named, ordered group of bindings, as rendered in the help modal.
type Section struct {
	Name    string
	Entries []Entry
}

// Registry maps actions to bindings and physical keys to actions. The
// registry/keymap/overlay-input surface is locked in Stage 1. Plugins extend
// the registry through their Actions hook; framework built-ins are
// pre-registered by [NewRegistry].
//
// The zero value is not usable; construct via [NewRegistry].
type Registry struct {
	order        []Action // action registration order
	bindings     map[Action]Binding
	keys         map[string]Action // physical key → action (dispatch)
	sectionOrder []string          // section first-seen order
	sections     map[string][]Action
}

// NewRegistry returns a registry pre-populated with the framework's built-in
// actions ([ActionHelp], [ActionQuit], [ActionFocusNext], [ActionFocusPrev]).
// Plugins extend it through their Actions hook.
//
// [ActionQuit] carries "esc" as a hidden alias: it dispatches (Match("esc")
// resolves to ActionQuit) but is absent from the help modal. The frame's
// modal-input policy takes precedence — "esc" closes an open overlay before
// the registry is consulted; it only reaches ActionQuit in normal mode (no
// overlay). Locked in Stage 1.
func NewRegistry() *Registry {
	r := &Registry{
		bindings: make(map[Action]Binding),
		keys:     make(map[string]Action),
		sections: make(map[string][]Action),
	}
	// Built-in defaults — locked in Stage 1.
	mustRegister(r, ActionFocusNext, Binding{Keys: []string{"tab"}, Desc: "Focus next panel", Section: sectionNavigation})
	mustRegister(r, ActionFocusPrev, Binding{Keys: []string{"shift+tab"}, Desc: "Focus previous panel", Section: sectionNavigation})
	mustRegister(r, ActionHelp, Binding{Keys: []string{"?"}, Desc: "Toggle help", Section: sectionGeneral})
	mustRegister(r, ActionQuit, Binding{Keys: []string{"q", "ctrl+c"}, Desc: "Quit", Section: sectionGeneral, Aliases: []string{"esc"}})
	return r
}

// Built-in section labels. English here; the help renderer (Task 6) resolves
// display strings through the i18n Translator with these as fallbacks.
const (
	sectionGeneral    = "General"
	sectionNavigation = "Navigation"
)

// Register binds an action to a key binding. It is an error to register an
// action twice, to register a key already claimed by another action, or to
// supply an alias that collides with any existing key/alias or with the
// binding's own canonical Keys — any of these would make dispatch ambiguous.
// A binding with no keys is also rejected.
//
// The validation pass is fully pre-commit: if any check fails, no map entry
// is written (no partial mutation).
func (r *Registry) Register(a Action, b Binding) error {
	if a == "" {
		return fmt.Errorf("tui: cannot register empty action")
	}
	if _, dup := r.bindings[a]; dup {
		return fmt.Errorf("tui: action %q already registered", a)
	}
	if len(b.Keys) == 0 {
		return fmt.Errorf("tui: action %q has no keys", a)
	}

	// Pre-commit: validate all canonical Keys.
	for _, k := range b.Keys {
		if owner, taken := r.keys[k]; taken {
			return fmt.Errorf("tui: key %q already bound to action %q", k, owner)
		}
	}

	// Pre-commit: validate all Aliases — must not collide with existing
	// keys/aliases and must not duplicate the binding's own canonical Keys.
	canonicalKeys := make(map[string]struct{}, len(b.Keys))
	for _, k := range b.Keys {
		canonicalKeys[k] = struct{}{}
	}
	for _, alias := range b.Aliases {
		if _, isCanonical := canonicalKeys[alias]; isCanonical {
			return fmt.Errorf("tui: alias %q for action %q duplicates a canonical key", alias, a)
		}
		if owner, taken := r.keys[alias]; taken {
			return fmt.Errorf("tui: alias %q for action %q already bound to action %q", alias, a, owner)
		}
	}

	// All checks passed — commit. Keys and Aliases both go into the dispatch map
	// so Match resolves them identically; only Keys are shown in help.
	r.bindings[a] = b
	r.order = append(r.order, a)
	for _, k := range b.Keys {
		r.keys[k] = a
	}
	for _, alias := range b.Aliases {
		r.keys[alias] = a
	}
	if _, seen := r.sections[b.Section]; !seen {
		r.sectionOrder = append(r.sectionOrder, b.Section)
	}
	r.sections[b.Section] = append(r.sections[b.Section], a)
	return nil
}

// Match resolves a physical key to its action. The bool reports whether any
// binding claims the key. Both canonical Keys and Aliases are consulted —
// locked in Stage 1.
func (r *Registry) Match(key string) (Action, bool) {
	a, ok := r.keys[key]
	return a, ok
}

// MatchMouse resolves a mouse-event string to its action. The bool reports
// whether any registered binding claims that event via [Binding.Mouse].
// The locked vocabulary is "wheel-up", "wheel-down", "double-click".
// "click" is frame-owned and is intentionally never registered as a
// mouse binding, so MatchMouse("click") always returns false.
func (r *Registry) MatchMouse(event string) (Action, bool) {
	for _, a := range r.order {
		if r.bindings[a].Mouse == event {
			return a, true
		}
	}
	return "", false
}

// Binding returns the binding registered for an action. The bool reports
// presence.
func (r *Registry) Binding(a Action) (Binding, bool) {
	b, ok := r.bindings[a]
	return b, ok
}

// Sections returns the registered bindings grouped into ordered sections for
// help generation. Sections appear in the order their first binding was
// registered; entries within a section preserve registration order.
func (r *Registry) Sections() []Section {
	out := make([]Section, 0, len(r.sectionOrder))
	for _, name := range r.sectionOrder {
		actions := r.sections[name]
		entries := make([]Entry, 0, len(actions))
		for _, a := range actions {
			entries = append(entries, Entry{Action: a, Binding: r.bindings[a]})
		}
		out = append(out, Section{Name: name, Entries: entries})
	}
	return out
}

// mustRegister panics on a registration error. It is used only for the built-in
// defaults, where a collision is a programmer error in this file, never runtime
// input.
func mustRegister(r *Registry, a Action, b Binding) {
	if err := r.Register(a, b); err != nil {
		panic(err)
	}
}
