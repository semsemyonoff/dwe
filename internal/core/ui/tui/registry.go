package tui

import "fmt"

// Action is the stable identifier for one keyboard-triggered behaviour (help
// toggle, quit, focus cycling, or a plugin-defined verb). It is decoupled from
// the physical key(s) so Stage 1 can add rebinding without touching dispatch
// sites — callers switch on the Action, never on the raw key.
type Action string

// The framework's built-in actions. Their default [Binding]s are registered by
// [NewRegistry]. The IDs are stable; the default keys below are "defaults,
// finalised in Stage 1" — the rebinding mechanism prototyped via
// [Binding.Rebindable] will let projects override them later.
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
// modal.
//
// The first three fields (Keys, Desc, Section) are load-bearing in Stage 0.
// Aliases, Rebindable, and Mouse are DOCUMENTED PLACEHOLDERS reserved for later
// stages; they exist now so callers can be written against the final shape and
// Stage 1/2 can lock the semantics without restructuring every registration
// site. They are intentionally not consulted by Stage 0 dispatch or help
// generation.
type Binding struct {
	// Keys are the physical key strings (bubbletea key syntax, e.g. "?", "tab",
	// "ctrl+c") that trigger the action. At least one is required.
	Keys []string
	// Desc is the English help description for the binding.
	Desc string
	// Section is the help-modal section label this binding groups under. Sections
	// appear in first-registration order; see [Registry.Sections].
	Section string

	// Aliases is a Stage 1 placeholder: additional human-facing key names shown in
	// help without participating in [Registry.Match] dispatch. Unused in Stage 0.
	Aliases []string
	// Rebindable is a Stage 1 placeholder: whether a project may override Keys via
	// future rebinding config. Unused in Stage 0.
	Rebindable bool
	// Mouse is a Stage 2 seam: a placeholder spec for a mouse trigger bound to the
	// same action. The mouse layer (Stage 2) defines its grammar; unused in
	// Stage 0.
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

// Registry maps actions to bindings and physical keys to actions. It is the
// provisional action-keymap layer: minimal but shaped so Stage 1 can freeze the
// API (aliases, rebinding, mouse bindings) without changing call sites.
//
// The zero value is not usable; construct via [NewRegistry], which pre-registers
// the framework built-ins.
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
func NewRegistry() *Registry {
	r := &Registry{
		bindings: make(map[Action]Binding),
		keys:     make(map[string]Action),
		sections: make(map[string][]Action),
	}
	// Built-in defaults — finalised in Stage 1.
	mustRegister(r, ActionFocusNext, Binding{Keys: []string{"tab"}, Desc: "Focus next panel", Section: sectionNavigation})
	mustRegister(r, ActionFocusPrev, Binding{Keys: []string{"shift+tab"}, Desc: "Focus previous panel", Section: sectionNavigation})
	mustRegister(r, ActionHelp, Binding{Keys: []string{"?"}, Desc: "Toggle help", Section: sectionGeneral})
	mustRegister(r, ActionQuit, Binding{Keys: []string{"q", "ctrl+c"}, Desc: "Quit", Section: sectionGeneral})
	return r
}

// Built-in section labels. English here; the help renderer (Task 6) resolves
// display strings through the i18n Translator with these as fallbacks.
const (
	sectionGeneral    = "General"
	sectionNavigation = "Navigation"
)

// Register binds an action to a key binding. It is an error to register an
// action twice or to register a key already claimed by another action — both
// would make dispatch ambiguous. A binding with no keys is also rejected.
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
	for _, k := range b.Keys {
		if owner, taken := r.keys[k]; taken {
			return fmt.Errorf("tui: key %q already bound to action %q", k, owner)
		}
	}

	// All checks passed — commit. (No partial mutation: keys are validated above
	// before any map write.)
	r.bindings[a] = b
	r.order = append(r.order, a)
	for _, k := range b.Keys {
		r.keys[k] = a
	}
	if _, seen := r.sections[b.Section]; !seen {
		r.sectionOrder = append(r.sectionOrder, b.Section)
	}
	r.sections[b.Section] = append(r.sections[b.Section], a)
	return nil
}

// Match resolves a physical key to its action. The bool reports whether any
// binding claims the key. Aliases are NOT consulted in Stage 0 (placeholder).
func (r *Registry) Match(key string) (Action, bool) {
	a, ok := r.keys[key]
	return a, ok
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
