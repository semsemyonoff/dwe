package tui

import "testing"

func TestRegistry_RegisterAndMatch(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Register("plugin.refresh", Binding{Keys: []string{"r"}, Desc: "Refresh", Section: "Plugin"}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	a, ok := r.Match("r")
	if !ok || a != "plugin.refresh" {
		t.Errorf("Match(r) = %q, %v; want plugin.refresh, true", a, ok)
	}
	if _, ok := r.Match("nope"); ok {
		t.Errorf("Match(nope) = true, want false for unbound key")
	}

	b, ok := r.Binding("plugin.refresh")
	if !ok || b.Desc != "Refresh" {
		t.Errorf("Binding(plugin.refresh) = %+v, %v", b, ok)
	}
}

func TestRegistry_DuplicateActionRejected(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Register("dup", Binding{Keys: []string{"x"}, Section: "S"}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := r.Register("dup", Binding{Keys: []string{"y"}, Section: "S"}); err == nil {
		t.Error("re-registering same action: want error, got nil")
	}
	// Re-registering a built-in action is also a duplicate.
	if err := r.Register(ActionHelp, Binding{Keys: []string{"h"}, Section: "S"}); err == nil {
		t.Error("re-registering built-in ActionHelp: want error, got nil")
	}
}

func TestRegistry_DuplicateKeyRejected(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Register("a1", Binding{Keys: []string{"k"}, Section: "S"}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := r.Register("a2", Binding{Keys: []string{"k"}, Section: "S"}); err == nil {
		t.Error("registering a key already claimed: want error, got nil")
	}
	// Colliding with a built-in key ("?") is rejected too.
	if err := r.Register("a3", Binding{Keys: []string{"?"}, Section: "S"}); err == nil {
		t.Error("colliding with built-in key: want error, got nil")
	}
	// The failed registrations must not have mutated state: a2/a3 absent, k still a1.
	if _, ok := r.Binding("a2"); ok {
		t.Error("a2 should not be registered after key collision")
	}
	if got, _ := r.Match("k"); got != "a1" {
		t.Errorf("Match(k) = %q after collision, want a1 (no partial mutation)", got)
	}
}

func TestRegistry_RejectsInvalidRegistration(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Register("", Binding{Keys: []string{"z"}, Section: "S"}); err == nil {
		t.Error("empty action: want error, got nil")
	}
	if err := r.Register("nokeys", Binding{Section: "S"}); err == nil {
		t.Error("binding with no keys: want error, got nil")
	}
}

func TestRegistry_SectionOrderingByFirstRegistration(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// Built-ins register Navigation then General. Add a new section last.
	if err := r.Register("p1", Binding{Keys: []string{"1"}, Desc: "One", Section: "Plugin"}); err != nil {
		t.Fatalf("Register p1: %v", err)
	}
	// A second binding into an existing section appends, does not reorder sections.
	if err := r.Register("p2", Binding{Keys: []string{"2"}, Desc: "Two", Section: sectionNavigation}); err != nil {
		t.Fatalf("Register p2: %v", err)
	}

	secs := r.Sections()
	gotOrder := make([]string, len(secs))
	for i, s := range secs {
		gotOrder[i] = s.Name
	}
	want := []string{sectionNavigation, sectionGeneral, "Plugin"}
	if len(gotOrder) != len(want) {
		t.Fatalf("section order = %v, want %v", gotOrder, want)
	}
	for i := range want {
		if gotOrder[i] != want[i] {
			t.Fatalf("section order = %v, want %v", gotOrder, want)
		}
	}

	// p2 must be appended to Navigation, after the two built-in focus actions.
	nav := secs[0]
	if nav.Name != sectionNavigation {
		t.Fatalf("first section = %q, want %q", nav.Name, sectionNavigation)
	}
	last := nav.Entries[len(nav.Entries)-1]
	if last.Action != "p2" {
		t.Errorf("last Navigation entry = %q, want p2 (registration order within section)", last.Action)
	}
}

func TestRegistry_BuiltinDefaultsPresent(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	builtins := []struct {
		action Action
		key    string
	}{
		{ActionHelp, "?"},
		{ActionQuit, "q"},
		{ActionQuit, "ctrl+c"},
		{ActionFocusNext, "tab"},
		{ActionFocusPrev, "shift+tab"},
	}
	for _, bi := range builtins {
		got, ok := r.Match(bi.key)
		if !ok {
			t.Errorf("built-in key %q not matched", bi.key)
			continue
		}
		if got != bi.action {
			t.Errorf("Match(%q) = %q, want %q", bi.key, got, bi.action)
		}
		if _, ok := r.Binding(bi.action); !ok {
			t.Errorf("built-in action %q has no binding", bi.action)
		}
	}
}

func TestRegistry_EscAliasDispatchesToQuit(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// "esc" is a hidden alias for ActionQuit — must dispatch.
	got, ok := r.Match("esc")
	if !ok {
		t.Fatal("Match(esc) = false; want true (esc is an alias for ActionQuit)")
	}
	if got != ActionQuit {
		t.Errorf("Match(esc) = %q; want %q", got, ActionQuit)
	}
}

func TestRegistry_AliasDispatchesAndNotInKeys(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// Register an action with an alias.
	err := r.Register("plugin.open", Binding{
		Keys:    []string{"o"},
		Aliases: []string{"enter"},
		Desc:    "Open",
		Section: "Plugin",
	})
	if err != nil {
		t.Fatalf("Register with alias: %v", err)
	}

	// Alias dispatches.
	got, ok := r.Match("enter")
	if !ok || got != "plugin.open" {
		t.Errorf("Match(enter) = %q, %v; want plugin.open, true", got, ok)
	}
	// Canonical key also dispatches.
	got, ok = r.Match("o")
	if !ok || got != "plugin.open" {
		t.Errorf("Match(o) = %q, %v; want plugin.open, true", got, ok)
	}
	// The Binding stored keeps canonical Keys (alias not moved to Keys).
	b, _ := r.Binding("plugin.open")
	if len(b.Keys) != 1 || b.Keys[0] != "o" {
		t.Errorf("Binding.Keys = %v; want [o]", b.Keys)
	}
}

func TestRegistry_AliasCollisionWithExistingKeyReturnsError(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// "?" is already bound to ActionHelp. Using it as an alias is rejected.
	err := r.Register("plugin.thing", Binding{
		Keys:    []string{"x"},
		Aliases: []string{"?"},
		Section: "S",
	})
	if err == nil {
		t.Error("alias colliding with existing key: want error, got nil")
	}
	// No partial mutation: "plugin.thing" must not be registered.
	if _, ok := r.Binding("plugin.thing"); ok {
		t.Error("plugin.thing should not be registered after alias collision")
	}
	// "x" must not be in the dispatch map.
	if got, ok := r.Match("x"); ok {
		t.Errorf("Match(x) = %q after failed registration; want no match", got)
	}
}

func TestRegistry_MatchMouse_StdlibDefaults(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := RegisterStandard(r, ActionNavUp, ActionNavDown, ActionSelect); err != nil {
		t.Fatalf("RegisterStandard: %v", err)
	}

	// Only ActionSelect has a mouse binding ("double-click"). Wheel events are
	// dispatched as WheelMsg; NavUp/NavDown have no mouse binding.
	got, ok := r.MatchMouse("double-click")
	if !ok {
		t.Fatalf("MatchMouse(double-click) = false; want true")
	}
	if got != ActionSelect {
		t.Errorf("MatchMouse(double-click) = %q; want %q", got, ActionSelect)
	}

	// wheel-up and wheel-down are no longer registered mouse events.
	if a, ok := r.MatchMouse("wheel-up"); ok {
		t.Errorf("MatchMouse(wheel-up) = (%q, true); want false (no longer registered)", a)
	}
	if a, ok := r.MatchMouse("wheel-down"); ok {
		t.Errorf("MatchMouse(wheel-down) = (%q, true); want false (no longer registered)", a)
	}
}

func TestRegistry_MatchMouse_ClickReturnsFalse(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := RegisterStandard(r, ActionNavUp, ActionNavDown, ActionSelect); err != nil {
		t.Fatalf("RegisterStandard: %v", err)
	}
	// "click" is frame-owned and must never be a registered mouse binding.
	got, ok := r.MatchMouse("click")
	if ok {
		t.Errorf("MatchMouse(click) = %q, true; want false (frame-owned, not registered)", got)
	}
}

func TestRegistry_MatchMouse_UnknownEventReturnsFalse(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := RegisterStandard(r, ActionNavUp, ActionNavDown, ActionSelect); err != nil {
		t.Fatalf("RegisterStandard: %v", err)
	}
	got, ok := r.MatchMouse("nonsense")
	if ok {
		t.Errorf("MatchMouse(nonsense) = %q, true; want false", got)
	}
}

func TestRegistry_MatchMouse_EmptyRegistryReturnsFalse(t *testing.T) {
	t.Parallel()
	// NewRegistry only registers built-ins; none have a Mouse field set.
	r := NewRegistry()
	for _, event := range []string{"wheel-up", "wheel-down", "double-click", "click"} {
		got, ok := r.MatchMouse(event)
		if ok {
			t.Errorf("MatchMouse(%q) on empty registry = %q, true; want false", event, got)
		}
	}
}

func TestRegistry_MatchMouse_EmptyEventReturnsFalse(t *testing.T) {
	t.Parallel()
	// Built-ins carry no Mouse field; an empty event must not spuriously match
	// the first such binding — it is never a real mouse vocabulary entry.
	r := NewRegistry()
	if got, ok := r.MatchMouse(""); ok {
		t.Errorf("MatchMouse(\"\") = %q, true; want false", got)
	}
	// Still false even after binding real mouse events.
	if err := RegisterStandard(r, ActionNavUp, ActionNavDown, ActionSelect); err != nil {
		t.Fatalf("RegisterStandard: %v", err)
	}
	if got, ok := r.MatchMouse(""); ok {
		t.Errorf("MatchMouse(\"\") after registration = %q, true; want false", got)
	}
}

func TestRegistry_MouseCollisionRejected(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Register("plugin.first", Binding{Keys: []string{"a"}, Mouse: "double-click"}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	// A second binding claiming the same mouse event must be rejected — without
	// this guard MatchMouse would silently return whichever registered first.
	err := r.Register("plugin.second", Binding{Keys: []string{"b"}, Mouse: "double-click"})
	if err == nil {
		t.Fatal("Register with colliding Mouse event: err = nil; want collision error")
	}
	// The colliding binding must not be committed.
	if _, ok := r.Binding("plugin.second"); ok {
		t.Error("colliding binding was committed despite the error")
	}
	if got, _ := r.MatchMouse("double-click"); got != "plugin.first" {
		t.Errorf("MatchMouse(double-click) = %q; want plugin.first (collision left first owner intact)", got)
	}
}

func TestRegistry_MouseVocabularyRejected(t *testing.T) {
	t.Parallel()
	// "click" is frame-owned and never registrable; anything outside the locked
	// vocabulary is dead state that would silently break the MatchMouse contract.
	// "wheel-up" and "wheel-down" are now also rejected: wheel events are
	// dispatched as WheelMsg by the frame, not through registry mouse bindings.
	for _, event := range []string{"click", "nonsense", "wheel-left", "Wheel-Up", "wheel-up", "wheel-down"} {
		r := NewRegistry()
		err := r.Register("plugin.bad", Binding{Keys: []string{"a"}, Mouse: event})
		if err == nil {
			t.Errorf("Register with Mouse=%q: err = nil; want vocabulary error", event)
		}
		if _, ok := r.Binding("plugin.bad"); ok {
			t.Errorf("Mouse=%q: binding committed despite the error", event)
		}
		if got, ok := r.MatchMouse(event); ok {
			t.Errorf("Mouse=%q: MatchMouse = %q, true; want false (not registered)", event, got)
		}
	}
}

func TestRegistry_AliasCollisionWithOwnCanonicalKeyReturnsError(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// Alias duplicating the binding's own canonical key is rejected.
	err := r.Register("plugin.thing", Binding{
		Keys:    []string{"x"},
		Aliases: []string{"x"},
		Section: "S",
	})
	if err == nil {
		t.Error("alias == canonical key: want error, got nil")
	}
	// No partial mutation.
	if _, ok := r.Binding("plugin.thing"); ok {
		t.Error("plugin.thing should not be registered after alias/key collision")
	}
	if got, ok := r.Match("x"); ok {
		t.Errorf("Match(x) = %q after failed registration; want no match", got)
	}
}
