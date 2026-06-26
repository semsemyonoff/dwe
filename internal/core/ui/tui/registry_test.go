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
