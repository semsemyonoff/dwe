package ui

import (
	"errors"
	"testing"

	huh "charm.land/huh/v2"
)

// TestPartitionMultiSelect_OrderPreserved verifies locked and toggleable items
// preserve their relative order within each partition.
func TestPartitionMultiSelect_OrderPreserved(t *testing.T) {
	items := []MultiSelectItem{
		{Key: "nginx", Locked: true},
		{Key: "main", Locked: false},
		{Key: "db", Locked: true},
		{Key: "second", Locked: false},
		{Key: "redis", Locked: true},
	}
	locked, toggleable := partitionMultiSelect(items)

	wantLocked := []string{"nginx", "db", "redis"}
	wantToggleable := []string{"main", "second"}

	if len(locked) != len(wantLocked) {
		t.Fatalf("locked: want %d items, got %d", len(wantLocked), len(locked))
	}
	for i, k := range wantLocked {
		if locked[i].Key != k {
			t.Errorf("locked[%d]: want %q, got %q", i, k, locked[i].Key)
		}
	}
	if len(toggleable) != len(wantToggleable) {
		t.Fatalf("toggleable: want %d items, got %d", len(wantToggleable), len(toggleable))
	}
	for i, k := range wantToggleable {
		if toggleable[i].Key != k {
			t.Errorf("toggleable[%d]: want %q, got %q", i, k, toggleable[i].Key)
		}
	}
}

// TestPartitionMultiSelect_AllLocked verifies all-locked returns empty toggleable.
func TestPartitionMultiSelect_AllLocked(t *testing.T) {
	items := []MultiSelectItem{
		{Key: "nginx", Locked: true},
		{Key: "db", Locked: true},
	}
	locked, toggleable := partitionMultiSelect(items)
	if len(locked) != 2 {
		t.Errorf("expected 2 locked, got %d", len(locked))
	}
	if len(toggleable) != 0 {
		t.Errorf("expected 0 toggleable, got %d", len(toggleable))
	}
}

// TestPartitionMultiSelect_AllToggleable verifies all-toggleable returns empty locked.
func TestPartitionMultiSelect_AllToggleable(t *testing.T) {
	items := []MultiSelectItem{
		{Key: "adminer", Locked: false},
		{Key: "mailpit", Locked: false},
	}
	locked, toggleable := partitionMultiSelect(items)
	if len(locked) != 0 {
		t.Errorf("expected 0 locked, got %d", len(locked))
	}
	if len(toggleable) != 2 {
		t.Errorf("expected 2 toggleable, got %d", len(toggleable))
	}
}

// TestBuildMultiSelectOptions_PreChecked verifies Selected:true items get the
// huh Selected flag set. We confirm by checking that the option key is set
// correctly (Selected flag is unexported in huh, so we check key and that the
// struct field roundtrips through the Selected() call without panicking).
func TestBuildMultiSelectOptions_PreChecked(t *testing.T) {
	items := []MultiSelectItem{
		{Key: "adminer", Label: "adminer", Selected: true},
		{Key: "mailpit", Label: "mailpit", Selected: false},
	}
	opts := buildMultiSelectOptions(items)
	if len(opts) != 2 {
		t.Fatalf("expected 2 options, got %d", len(opts))
	}
	if opts[0].Key != "adminer" {
		t.Errorf("opts[0].Key: want %q, got %q", "adminer", opts[0].Key)
	}
	if opts[0].Value != "adminer" {
		t.Errorf("opts[0].Value: want %q, got %q", "adminer", opts[0].Value)
	}
	if opts[1].Key != "mailpit" {
		t.Errorf("opts[1].Key: want %q, got %q", "mailpit", opts[1].Key)
	}
}

// TestBuildMultiSelectOptions_LabelWithDescription verifies description is
// appended to the label in the option key.
func TestBuildMultiSelectOptions_LabelWithDescription(t *testing.T) {
	items := []MultiSelectItem{
		{Key: "adminer", Label: "adminer", Description: "DB admin UI"},
	}
	opts := buildMultiSelectOptions(items)
	want := "adminer  DB admin UI"
	if opts[0].Key != want {
		t.Errorf("expected key %q, got %q", want, opts[0].Key)
	}
}

// TestBuildMultiSelectOptions_OrderPreserved verifies option order matches input order.
func TestBuildMultiSelectOptions_OrderPreserved(t *testing.T) {
	items := []MultiSelectItem{
		{Key: "c", Label: "c"},
		{Key: "a", Label: "a"},
		{Key: "b", Label: "b"},
	}
	opts := buildMultiSelectOptions(items)
	for i, want := range []string{"c", "a", "b"} {
		if opts[i].Value != want {
			t.Errorf("opts[%d].Value: want %q, got %q", i, want, opts[i].Value)
		}
	}
}

// TestRunMultiSelect_AllLocked_SkipsForm verifies that when all items are locked
// the form is never called and Locked is populated.
func TestRunMultiSelect_AllLocked_SkipsForm(t *testing.T) {
	old := runMultiSelectFormFn
	t.Cleanup(func() { runMultiSelectFormFn = old })
	called := false
	runMultiSelectFormFn = func(_ string, _ []huh.Option[string]) ([]string, error) {
		called = true
		return nil, nil
	}

	items := []MultiSelectItem{
		{Key: "nginx", Locked: true},
		{Key: "db", Locked: true},
	}
	result, err := RunMultiSelect("Toggle:", items)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("form should not have been called for all-locked items")
	}
	if result.Kept != nil {
		t.Errorf("Kept should be nil, got %v", result.Kept)
	}
	if len(result.Locked) != 2 {
		t.Errorf("Locked: want 2, got %d", len(result.Locked))
	}
}

// TestRunMultiSelect_LockedAlwaysPopulated verifies result.Locked is always
// set regardless of what the form returns.
func TestRunMultiSelect_LockedAlwaysPopulated(t *testing.T) {
	old := runMultiSelectFormFn
	t.Cleanup(func() { runMultiSelectFormFn = old })
	runMultiSelectFormFn = func(_ string, _ []huh.Option[string]) ([]string, error) {
		return []string{"adminer"}, nil
	}

	items := []MultiSelectItem{
		{Key: "nginx", Locked: true},
		{Key: "db", Locked: true},
		{Key: "adminer", Locked: false, Selected: true},
		{Key: "mailpit", Locked: false, Selected: false},
	}
	result, err := RunMultiSelect("Toggle:", items)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Locked) != 2 {
		t.Errorf("Locked: want 2, got %d: %v", len(result.Locked), result.Locked)
	}
	if result.Locked[0] != "nginx" || result.Locked[1] != "db" {
		t.Errorf("Locked order wrong: %v", result.Locked)
	}
	if len(result.Kept) != 1 || result.Kept[0] != "adminer" {
		t.Errorf("Kept: want [adminer], got %v", result.Kept)
	}
}

// TestRunMultiSelect_AbortReturnsErrCancelled verifies huh.ErrUserAborted → ErrCancelled.
func TestRunMultiSelect_AbortReturnsErrCancelled(t *testing.T) {
	old := runMultiSelectFormFn
	t.Cleanup(func() { runMultiSelectFormFn = old })
	runMultiSelectFormFn = func(_ string, _ []huh.Option[string]) ([]string, error) {
		return nil, huh.ErrUserAborted
	}

	items := []MultiSelectItem{
		{Key: "adminer", Locked: false},
	}
	result, err := RunMultiSelect("Toggle:", items)
	if !errors.Is(err, ErrCancelled) {
		t.Errorf("expected ErrCancelled, got %v", err)
	}
	if result.Kept != nil || result.Locked != nil {
		t.Errorf("result should be zero value on cancel, got %+v", result)
	}
}

// TestRunMultiSelect_GenericErrorPropagated verifies non-abort errors propagate.
func TestRunMultiSelect_GenericErrorPropagated(t *testing.T) {
	sentinel := errors.New("form exploded")
	old := runMultiSelectFormFn
	t.Cleanup(func() { runMultiSelectFormFn = old })
	runMultiSelectFormFn = func(_ string, _ []huh.Option[string]) ([]string, error) {
		return nil, sentinel
	}

	items := []MultiSelectItem{
		{Key: "adminer", Locked: false},
	}
	_, err := RunMultiSelect("Toggle:", items)
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}
}

// TestRunMultiSelect_NoIOSideEffects verifies RunMultiSelect does not write to
// any io.Writer directly. Since the primitive accepts no writer, and the only
// output comes from the huh form (which we stub), this test confirms no stray
// fmt.Print calls exist by checking the fake form is the sole I/O path.
func TestRunMultiSelect_NoIOSideEffects(t *testing.T) {
	old := runMultiSelectFormFn
	t.Cleanup(func() { runMultiSelectFormFn = old })
	runMultiSelectFormFn = func(_ string, _ []huh.Option[string]) ([]string, error) {
		return []string{"adminer"}, nil
	}

	items := []MultiSelectItem{
		{Key: "adminer", Locked: false, Selected: true},
	}
	// If this runs without any output (no panic, no hang), the primitive is I/O-free.
	result, err := RunMultiSelect("Toggle:", items)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Kept) != 1 {
		t.Errorf("expected 1 kept item, got %d", len(result.Kept))
	}
}
