package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestRegisterStandard_PageKeysUseCanonicalBubbleteaStrings locks the page
// bindings to the exact strings bubbletea emits. KeyPgUp.String() is "pgup" and
// KeyPgDown.String() is "pgdown" — NOT "pgdn". The registry matches by exact
// string, so a "pgdn" binding would never fire on a physical PageDown. The test
// derives the expected strings from bubbletea itself so it stays correct if the
// upstream vocabulary ever changes.
func TestRegisterStandard_PageKeysUseCanonicalBubbleteaStrings(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := RegisterStandard(r, ActionPageUp, ActionPageDown); err != nil {
		t.Fatalf("RegisterStandard: %v", err)
	}

	pgUp := tea.KeyPressMsg{Code: tea.KeyPgUp}.String()
	if got, ok := r.Match(pgUp); !ok || got != ActionPageUp {
		t.Errorf("Match(%q) = %q, %v; want %q, true", pgUp, got, ok, ActionPageUp)
	}
	pgDown := tea.KeyPressMsg{Code: tea.KeyPgDown}.String()
	if got, ok := r.Match(pgDown); !ok || got != ActionPageDown {
		t.Errorf("Match(%q) = %q, %v; want %q, true", pgDown, got, ok, ActionPageDown)
	}
}

func TestRegisterStandard_TopBottomDualBind(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := RegisterStandard(r, ActionTop, ActionBottom); err != nil {
		t.Fatalf("RegisterStandard: %v", err)
	}

	// ActionTop: both "g" and "home" must resolve.
	for _, key := range []string{"g", "home"} {
		got, ok := r.Match(key)
		if !ok || got != ActionTop {
			t.Errorf("Match(%q) = %q, %v; want %q, true", key, got, ok, ActionTop)
		}
	}

	// ActionBottom: both "G" and "end" must resolve.
	for _, key := range []string{"G", "end"} {
		got, ok := r.Match(key)
		if !ok || got != ActionBottom {
			t.Errorf("Match(%q) = %q, %v; want %q, true", key, got, ok, ActionBottom)
		}
	}
}

func TestRegisterStandard_AllStdlibActionsRegister(t *testing.T) {
	t.Parallel()
	allStdlib := []Action{
		ActionNavUp, ActionNavDown, ActionNavLeft, ActionNavRight,
		ActionSelect, ActionFilter, ActionInspect, ActionReload,
		ActionTop, ActionBottom, ActionPageUp, ActionPageDown,
	}

	r := NewRegistry()
	if err := RegisterStandard(r, allStdlib...); err != nil {
		t.Fatalf("RegisterStandard(all): %v", err)
	}

	for _, a := range allStdlib {
		b, ok := r.Binding(a)
		if !ok {
			t.Errorf("Binding(%q) = false; want registered", a)
			continue
		}
		if len(b.Keys) == 0 {
			t.Errorf("Binding(%q).Keys is empty", a)
		}
	}
}

func TestStandardBinding_UnknownActionReturnsFalse(t *testing.T) {
	t.Parallel()
	_, ok := standardBinding("not.a.stdlib.action")
	if ok {
		t.Error("standardBinding(unknown) = true; want false")
	}
}

func TestStandardBinding_KnownActionReturnsBinding(t *testing.T) {
	t.Parallel()
	b, ok := standardBinding(ActionNavUp)
	if !ok {
		t.Fatal("standardBinding(ActionNavUp) = false; want true")
	}
	if len(b.Keys) == 0 {
		t.Error("standardBinding(ActionNavUp).Keys is empty")
	}
}

func TestRegisterStandard_KeyCollisionReturnsError(t *testing.T) {
	t.Parallel()
	// RegisterStandard must surface a key collision regardless of whether the
	// occupied key belongs to a built-in or a previously-registered plugin
	// binding. We induce the collision by first pre-occupying a key the stdlib
	// uses, then asking RegisterStandard to register that action.
	r := NewRegistry()

	// Pre-occupy "enter" (ActionSelect's default key) with a custom binding.
	if err := r.Register("custom.enter", Binding{Keys: []string{"enter"}, Section: "Custom"}); err != nil {
		t.Fatalf("pre-occupying enter: %v", err)
	}

	// RegisterStandard for ActionSelect should now fail due to key collision.
	err := RegisterStandard(r, ActionSelect)
	if err == nil {
		t.Error("RegisterStandard(ActionSelect) with key collision: want error, got nil")
	}
}

func TestRegisterStandard_CollisionWithPreviousPluginBindingReturnsError(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// Register NavUp first.
	if err := RegisterStandard(r, ActionNavUp); err != nil {
		t.Fatalf("RegisterStandard(ActionNavUp): %v", err)
	}
	// Registering it again is a duplicate-action error.
	err := RegisterStandard(r, ActionNavUp)
	if err == nil {
		t.Error("re-registering ActionNavUp: want error, got nil")
	}
}

func TestRegisterStandard_UnknownActionReturnsError(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	err := RegisterStandard(r, "not.stdlib.action")
	if err == nil {
		t.Error("RegisterStandard(unknown action): want error, got nil")
	}
}

func TestRegisterStandard_SectionsFromStdlibAppearInRegistry(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := RegisterStandard(r, ActionFilter, ActionInspect); err != nil {
		t.Fatalf("RegisterStandard: %v", err)
	}

	secs := r.Sections()
	sectionNames := make(map[string]bool)
	for _, s := range secs {
		sectionNames[s.Name] = true
	}

	if !sectionNames[sectionFilter] {
		t.Errorf("section %q not present after registering ActionFilter", sectionFilter)
	}
	if !sectionNames[sectionInspect] {
		t.Errorf("section %q not present after registering ActionInspect", sectionInspect)
	}
}

func TestStandardBindings_MouseDefaults(t *testing.T) {
	t.Parallel()
	// Verify the mouse defaults locked in Stage 2 are present in standardBindings.
	cases := []struct {
		action Action
		mouse  string
	}{
		{ActionNavUp, "wheel-up"},
		{ActionNavDown, "wheel-down"},
		{ActionSelect, "double-click"},
	}
	for _, tc := range cases {
		b, ok := standardBinding(tc.action)
		if !ok {
			t.Errorf("standardBinding(%q) = false; want present", tc.action)
			continue
		}
		if b.Mouse != tc.mouse {
			t.Errorf("standardBinding(%q).Mouse = %q; want %q", tc.action, b.Mouse, tc.mouse)
		}
	}
}

func TestStandardBindings_OtherActionsHaveNoMouseDefault(t *testing.T) {
	t.Parallel()
	// Actions without a mouse default must have an empty Mouse field.
	noMouse := []Action{
		ActionNavLeft, ActionNavRight, ActionTop, ActionBottom,
		ActionPageUp, ActionPageDown, ActionReload, ActionFilter, ActionInspect,
	}
	for _, a := range noMouse {
		b, ok := standardBinding(a)
		if !ok {
			t.Errorf("standardBinding(%q) = false; want present", a)
			continue
		}
		if b.Mouse != "" {
			t.Errorf("standardBinding(%q).Mouse = %q; want empty", a, b.Mouse)
		}
	}
}

func TestRegisterStandard_NavSectionAppearsAfterBuiltins(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// The built-ins already own the Navigation section (FocusNext/FocusPrev).
	// Registering stdlib nav actions must append to the existing section, not
	// create a duplicate or reorder.
	if err := RegisterStandard(r, ActionNavUp, ActionNavDown); err != nil {
		t.Fatalf("RegisterStandard: %v", err)
	}

	secs := r.Sections()
	// Navigation must be the first section (built-ins registered it first).
	if len(secs) == 0 {
		t.Fatal("no sections returned")
	}
	if secs[0].Name != sectionNavigation {
		t.Errorf("first section = %q; want %q", secs[0].Name, sectionNavigation)
	}

	// Both built-in FocusNext/FocusPrev and the stdlib NavUp/NavDown must be in it.
	nav := secs[0]
	actionSet := make(map[Action]bool)
	for _, e := range nav.Entries {
		actionSet[e.Action] = true
	}
	for _, want := range []Action{ActionFocusNext, ActionFocusPrev, ActionNavUp, ActionNavDown} {
		if !actionSet[want] {
			t.Errorf("Navigation section missing %q", want)
		}
	}
}
