package ui

import (
	"errors"
	"image/color"
	"sync"
	"sync/atomic"
	"testing"

	huh "charm.land/huh/v2"
	lipgloss "charm.land/lipgloss/v2"

	"devbox-cli/internal/config"
)

func TestThemeNonNilByDefault(t *testing.T) {
	th := Theme()
	if th == nil {
		t.Fatal("Theme() returned nil before ApplyStyles")
	}
}

func TestThemeNonNilAfterApplyStylesNil(t *testing.T) {
	original := huhTheme
	t.Cleanup(func() { huhTheme = original })

	ApplyStyles(nil)
	th := Theme()
	if th == nil {
		t.Fatal("Theme() returned nil after ApplyStyles(nil)")
	}
}

func TestThemeReturnsNonNilStyles(t *testing.T) {
	original := huhTheme
	t.Cleanup(func() { huhTheme = original })

	ApplyStyles(nil)
	th := Theme()

	light := th.Theme(false)
	if light == nil {
		t.Fatal("Theme().Theme(false) returned nil")
	}

	dark := th.Theme(true)
	if dark == nil {
		t.Fatal("Theme().Theme(true) returned nil")
	}
}

func TestDefaultThemeMultiSelectStateStyles(t *testing.T) {
	original := huhTheme
	t.Cleanup(func() { huhTheme = original })
	resetStyles()
	huhTheme = huh.ThemeFunc(func(isDark bool) *huh.Styles {
		s := huh.ThemeBase(isDark)
		applyFormGlyphs(s)
		applyMultiSelectStateStyles(s, resolvedSuccess, resolvedMuted)
		return s
	})

	styles := Theme().Theme(false)
	if !styles.Focused.SelectedOption.GetBold() {
		t.Error("selected multi-select options should be bold")
	}
	if styles.Focused.SelectedOption.GetFaint() {
		t.Error("selected multi-select options should not be faint")
	}
	if styles.Focused.UnselectedOption.GetBold() {
		t.Error("unselected multi-select options should not be bold")
	}
	if !styles.Focused.UnselectedOption.GetFaint() {
		t.Error("unselected multi-select options should be faint")
	}
}

func TestApplyStylesPaletteReflected(t *testing.T) {
	original := huhTheme
	t.Cleanup(func() { huhTheme = original })

	cfg := &config.StylesConfig{
		Colors: config.StylesColors{
			Accent: "#123456",
		},
	}
	ApplyStyles(cfg)

	styles := Theme().Theme(false)
	got := styles.Focused.Title.GetForeground()
	want := lipgloss.Color("#123456")
	if got != want {
		t.Errorf("Focused.Title foreground = %v (%T), want %v (%T)", got, got, want, want)
	}
}

func TestApplyStylesGroupTitlePaletteReflected(t *testing.T) {
	original := huhTheme
	t.Cleanup(func() { huhTheme = original })

	cfg := &config.StylesConfig{
		Colors: config.StylesColors{
			Accent: "#123456",
		},
	}
	ApplyStyles(cfg)

	styles := Theme().Theme(false)
	got := styles.Group.Title.GetForeground()
	want := lipgloss.Color("#123456")
	if got != want {
		t.Errorf("Group.Title foreground = %v (%T), want %v (%T)", got, got, want, want)
	}
}

func TestBuildPaletteApplierNilNoOp(t *testing.T) {
	apply := buildPaletteApplier()
	s := huh.ThemeBase(false)
	// Should not panic.
	apply(s)
}

func TestBuildPaletteApplierEmptyColorsNoOp(t *testing.T) {
	apply := buildPaletteApplier()
	s := huh.ThemeBase(false)
	// Calling apply with an empty config should not change any foreground colors
	// on fields whose color comes from ThemeBase (they should stay as zero/default).
	apply(s)
	// The test verifies the applier runs without panicking.
}

// --- huh hook tests ---

// resetHooks clears the package-level hooks. Used as t.Cleanup so each test
// starts and ends with no hooks installed.
func resetHooks(t *testing.T) {
	t.Helper()
	ClearHuhHooks()
	t.Cleanup(ClearHuhHooks)
}

func TestSetHuhHooks_FiresAroundConfirm(t *testing.T) {
	resetHooks(t)

	origConfirm := runConfirmFormFn
	t.Cleanup(func() { runConfirmFormFn = origConfirm })
	runConfirmFormFn = func(title, affirmative, negative string) (bool, error) {
		return true, nil
	}

	var order []string
	SetHuhHooks(
		func() { order = append(order, "before") },
		func() { order = append(order, "after") },
	)

	runConfirmFormFn = func(title, affirmative, negative string) (bool, error) {
		order = append(order, "form")
		return true, nil
	}
	if _, err := RunConfirm("?", "Y", "N"); err != nil {
		t.Fatal(err)
	}
	if len(order) != 3 || order[0] != "before" || order[1] != "form" || order[2] != "after" {
		t.Errorf("expected before/form/after, got %v", order)
	}
}

func TestSetHuhHooks_AfterFiresOnError(t *testing.T) {
	resetHooks(t)

	orig := runConfirmFormFn
	t.Cleanup(func() { runConfirmFormFn = orig })

	var afterCalled bool
	SetHuhHooks(nil, func() { afterCalled = true })

	sentinel := errors.New("form failed")
	runConfirmFormFn = func(title, affirmative, negative string) (bool, error) {
		return false, sentinel
	}
	_, err := RunConfirm("?", "Y", "N")
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if !afterCalled {
		t.Error("after hook must fire even when the form returns an error")
	}
}

func TestSetHuhHooks_AfterFiresOnCancel(t *testing.T) {
	resetHooks(t)

	orig := runConfirmFormFn
	t.Cleanup(func() { runConfirmFormFn = orig })

	var afterCalled bool
	SetHuhHooks(nil, func() { afterCalled = true })

	runConfirmFormFn = func(title, affirmative, negative string) (bool, error) {
		return false, huh.ErrUserAborted
	}
	_, err := RunConfirm("?", "Y", "N")
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("expected ErrCancelled, got %v", err)
	}
	if !afterCalled {
		t.Error("after hook must fire on user-cancel path")
	}
}

func TestSnapshotHuhHooks_NilSafe(t *testing.T) {
	resetHooks(t)

	orig := runConfirmFormFn
	t.Cleanup(func() { runConfirmFormFn = orig })
	runConfirmFormFn = func(title, affirmative, negative string) (bool, error) {
		return true, nil
	}

	// No hooks installed — must not panic.
	if _, err := RunConfirm("?", "Y", "N"); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotHuhHooks_SurvivesMidPromptClear(t *testing.T) {
	resetHooks(t)

	orig := runConfirmFormFn
	t.Cleanup(func() { runConfirmFormFn = orig })

	var afterCalled bool
	SetHuhHooks(
		func() {},
		func() { afterCalled = true },
	)

	// Simulate a mid-prompt ClearHuhHooks. The snapshot taken at RunConfirm
	// entry guarantees the after hook still fires.
	runConfirmFormFn = func(title, affirmative, negative string) (bool, error) {
		ClearHuhHooks()
		return true, nil
	}
	if _, err := RunConfirm("?", "Y", "N"); err != nil {
		t.Fatal(err)
	}
	if !afterCalled {
		t.Error("after hook must fire even when hooks were cleared mid-prompt")
	}
}

func TestHuhHooks_ConcurrentSetAndSnapshot(t *testing.T) {
	resetHooks(t)

	const writers, readers, iters = 100, 100, 200

	var wg sync.WaitGroup
	var invokes atomic.Int64

	for range writers {
		wg.Go(func() {
			for j := range iters {
				if j%2 == 0 {
					SetHuhHooks(func() {}, func() {})
				} else {
					ClearHuhHooks()
				}
			}
		})
	}

	for range readers {
		wg.Go(func() {
			for range iters {
				before, after := snapshotHuhHooks()
				if before != nil {
					before()
				}
				if after != nil {
					after()
				}
				invokes.Add(1)
			}
		})
	}

	wg.Wait()
	if invokes.Load() == 0 {
		t.Fatal("expected at least one snapshot invocation")
	}
}

// --- RunWithPromptHooks tests ---

func TestRunWithPromptHooks_Order(t *testing.T) {
	resetHooks(t)
	var order []string
	SetHuhHooks(
		func() { order = append(order, "before") },
		func() { order = append(order, "after") },
	)
	err := RunWithPromptHooks(func() error {
		order = append(order, "fn")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 3 || order[0] != "before" || order[1] != "fn" || order[2] != "after" {
		t.Errorf("order=%v", order)
	}
}

func TestRunWithPromptHooks_AfterFiresOnError(t *testing.T) {
	resetHooks(t)
	var afterCalled bool
	SetHuhHooks(nil, func() { afterCalled = true })
	sentinel := errors.New("boom")
	err := RunWithPromptHooks(func() error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Errorf("want sentinel, got %v", err)
	}
	if !afterCalled {
		t.Error("after hook must fire on error")
	}
}

func TestRunWithPromptHooks_NilSafe(t *testing.T) {
	resetHooks(t)
	if err := RunWithPromptHooks(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestRunWithPromptHooks_SurvivesMidClear(t *testing.T) {
	resetHooks(t)
	var afterCalled bool
	SetHuhHooks(func() {}, func() { afterCalled = true })
	err := RunWithPromptHooks(func() error {
		ClearHuhHooks()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !afterCalled {
		t.Error("after hook must fire even when cleared mid-fn (snapshot semantics)")
	}
}

func TestBuildPaletteApplierAllFields(t *testing.T) {
	c := &config.StylesConfig{Colors: config.StylesColors{
		Accent:  "#012345",
		Success: "#011223",
		Danger:  "#993311",
		Muted:   "#445566",
	}}
	ApplyStyles(c)

	apply := buildPaletteApplier()
	s := huh.ThemeBase(false)
	apply(s)

	accent := lipgloss.Color("#012345")
	success := lipgloss.Color("#011223")
	danger := lipgloss.Color("#993311")
	muted := lipgloss.Color("#445566")

	tests := []struct {
		name string
		got  color.Color
		want color.Color
	}{
		{"Focused.Title", s.Focused.Title.GetForeground(), accent},
		{"Group.Title", s.Group.Title.GetForeground(), accent},
		{"Focused.Description", s.Focused.Description.GetForeground(), muted},
		{"Group.Description", s.Group.Description.GetForeground(), muted},
		{"Focused.SelectSelector", s.Focused.SelectSelector.GetForeground(), accent},
		{"Focused.MultiSelectSelector", s.Focused.MultiSelectSelector.GetForeground(), accent},
		{"Focused.Option", s.Focused.Option.GetForeground(), accent},
		{"Blurred.Title", s.Blurred.Title.GetForeground(), muted},
		{"Blurred.Description", s.Blurred.Description.GetForeground(), muted},
		{"Focused.UnselectedOption", s.Focused.UnselectedOption.GetForeground(), muted},
		{"Focused.SelectedOption", s.Focused.SelectedOption.GetForeground(), success},
		{"Focused.SelectedPrefix", s.Focused.SelectedPrefix.GetForeground(), success},
		{"Focused.ErrorIndicator", s.Focused.ErrorIndicator.GetForeground(), danger},
		{"Focused.ErrorMessage", s.Focused.ErrorMessage.GetForeground(), danger},
		{"Focused.NextIndicator", s.Focused.NextIndicator.GetForeground(), accent},
		{"Focused.PrevIndicator", s.Focused.PrevIndicator.GetForeground(), accent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %v (%T), want %v (%T)", tt.got, tt.got, tt.want, tt.want)
			}
		})
	}

	if !s.Focused.SelectedOption.GetBold() || s.Focused.SelectedOption.GetFaint() {
		t.Error("focused selected option should be bold and not faint")
	}
	if s.Focused.UnselectedOption.GetBold() || !s.Focused.UnselectedOption.GetFaint() {
		t.Error("focused unselected option should be faint and not bold")
	}
}
