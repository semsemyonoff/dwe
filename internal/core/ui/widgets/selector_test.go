package widgets

import (
	"errors"
	"testing"

	huh "charm.land/huh/v2"
)

// TestRunSelectorEmptyItems verifies the empty-items error.
func TestRunSelectorEmptyItems(t *testing.T) {
	_, err := RunSelector("title", nil)
	if err == nil {
		t.Fatal("expected error for empty items")
	}
	if err.Error() != "selector: no items to display" {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestBuildSelectorOptions_LabelOnly verifies key is just the label when no
// description or status is set.
func TestBuildSelectorOptions_LabelOnly(t *testing.T) {
	opts := buildSelectorOptions([]SelectorItem{{Label: "main"}})
	if len(opts) != 1 {
		t.Fatalf("expected 1 option, got %d", len(opts))
	}
	if opts[0].Key != "main" {
		t.Errorf("expected key %q, got %q", "main", opts[0].Key)
	}
	if opts[0].Value != 0 {
		t.Errorf("expected value 0 (index), got %d", opts[0].Value)
	}
}

// TestBuildSelectorOptions_WithDescription verifies description is appended.
func TestBuildSelectorOptions_WithDescription(t *testing.T) {
	opts := buildSelectorOptions([]SelectorItem{{Label: "main", Description: "app-main"}})
	if opts[0].Key != "main  app-main" {
		t.Errorf("unexpected key: %q", opts[0].Key)
	}
}

// TestBuildSelectorOptions_StatusEnabled verifies enabled status shows ✓.
func TestBuildSelectorOptions_StatusEnabled(t *testing.T) {
	opts := buildSelectorOptions([]SelectorItem{{Label: "svc", Status: "enabled"}})
	if opts[0].Key != "svc  ✓" {
		t.Errorf("unexpected key: %q", opts[0].Key)
	}
}

// TestBuildSelectorOptions_StatusDisabled verifies disabled status shows ○.
func TestBuildSelectorOptions_StatusDisabled(t *testing.T) {
	opts := buildSelectorOptions([]SelectorItem{{Label: "svc", Status: "disabled"}})
	if opts[0].Key != "svc  ○" {
		t.Errorf("unexpected key: %q", opts[0].Key)
	}
}

// TestBuildSelectorOptions_StatusFreeText verifies arbitrary status is appended literally.
func TestBuildSelectorOptions_StatusFreeText(t *testing.T) {
	opts := buildSelectorOptions([]SelectorItem{{Label: "cmd", Status: "running"}})
	if opts[0].Key != "cmd  running" {
		t.Errorf("unexpected key: %q", opts[0].Key)
	}
}

// TestBuildSelectorOptions_AllFields verifies all three fields combine correctly.
func TestBuildSelectorOptions_AllFields(t *testing.T) {
	opts := buildSelectorOptions([]SelectorItem{
		{Label: "main", Description: "app-main", Status: "enabled"},
	})
	want := "main  app-main  ✓"
	if opts[0].Key != want {
		t.Errorf("expected %q, got %q", want, opts[0].Key)
	}
}

// TestBuildSelectorOptions_IndexValues verifies each option carries its original index.
func TestBuildSelectorOptions_IndexValues(t *testing.T) {
	items := []SelectorItem{
		{Label: "a"},
		{Label: "b"},
		{Label: "c"},
	}
	opts := buildSelectorOptions(items)
	for i, opt := range opts {
		if opt.Value != i {
			t.Errorf("item %d: expected value %d, got %d", i, i, opt.Value)
		}
	}
}

// TestRunSelectorErrUserAborted verifies huh.ErrUserAborted → ErrCancelled.
func TestRunSelectorErrUserAborted(t *testing.T) {
	old := runSelectFormFn
	t.Cleanup(func() { runSelectFormFn = old })
	runSelectFormFn = func(_ string, _ []huh.Option[int]) (int, error) {
		return -1, huh.ErrUserAborted
	}

	_, err := RunSelector("title", []SelectorItem{{Label: "x"}})
	if !errors.Is(err, ErrCancelled) {
		t.Errorf("expected ErrCancelled, got %v", err)
	}
}

// TestRunSelectorGenericError verifies non-abort errors are propagated.
func TestRunSelectorGenericError(t *testing.T) {
	sentinel := errors.New("form exploded")
	old := runSelectFormFn
	t.Cleanup(func() { runSelectFormFn = old })
	runSelectFormFn = func(_ string, _ []huh.Option[int]) (int, error) {
		return -1, sentinel
	}

	_, err := RunSelector("title", []SelectorItem{{Label: "x"}})
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}
}

// TestRunSelectorReturnsIndex verifies the chosen index is returned.
func TestRunSelectorReturnsIndex(t *testing.T) {
	old := runSelectFormFn
	t.Cleanup(func() { runSelectFormFn = old })
	runSelectFormFn = func(_ string, _ []huh.Option[int]) (int, error) {
		return 2, nil // simulate user picking the third item
	}

	idx, err := RunSelector("title", []SelectorItem{
		{Label: "a"}, {Label: "b"}, {Label: "c"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idx != 2 {
		t.Errorf("expected index 2, got %d", idx)
	}
}
