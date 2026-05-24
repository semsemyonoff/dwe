package ui

import (
	"sync"

	huh "charm.land/huh/v2"
	lipgloss "charm.land/lipgloss/v2"
)

// huhHooksMu guards huhBeforeHook and huhAfterHook. The two hooks are written
// together under Lock so callers always observe a consistent pair via
// snapshotHuhHooks. Callers (RunConfirm / RunSelector / RunMultiSelect) snapshot
// the pair once at entry and use the snapshotted after-hook in a defer so that
// SetHuhHooks / ClearHuhHooks calls during a prompt cannot break pairing.
var (
	huhHooksMu    sync.RWMutex
	huhBeforeHook func()
	huhAfterHook  func()
)

// SetHuhHooks installs hooks invoked before/after every huh-based prompt
// (RunConfirm, RunSelector, RunMultiSelect). Both hooks are written together so
// snapshotHuhHooks always returns a consistent pair. Pass nil for either to
// disable that side; ClearHuhHooks is the canonical way to remove both at once.
//
// Only one PlainReporter is expected to be active per process; nested deploys
// are not supported by the global hook design.
func SetHuhHooks(before, after func()) {
	huhHooksMu.Lock()
	huhBeforeHook = before
	huhAfterHook = after
	huhHooksMu.Unlock()
}

// ClearHuhHooks removes the package-level prompt hooks. Safe to call when no
// hooks are installed.
func ClearHuhHooks() {
	huhHooksMu.Lock()
	huhBeforeHook = nil
	huhAfterHook = nil
	huhHooksMu.Unlock()
}

// snapshotHuhHooks returns the current (before, after) pair under a single
// RLock so callers do not re-read the globals between before() and after().
func snapshotHuhHooks() (before, after func()) {
	huhHooksMu.RLock()
	before = huhBeforeHook
	after = huhAfterHook
	huhHooksMu.RUnlock()
	return before, after
}

// SnapshotHuhHooks exposes the current hook pair. Used by cross-package tests
// (e.g. internal/pipeline) to assert hook installation; production callers
// should use snapshotHuhHooks via the prompt entry points instead.
func SnapshotHuhHooks() (before, after func()) {
	return snapshotHuhHooks()
}

// RunWithPromptHooks runs fn wrapped by the snapshotted before/after hook pair.
// It is the canonical entry point for full-screen prompt-like UI (e.g. the
// command browser TUI) outside of internal/ui's huh-backed primitives: it
// snapshots the current (before, after) pair once, calls before(), defers
// after(), then invokes fn(). The after hook still fires when fn returns an
// error so a paused LiveLine always resumes.
//
// Production callers should prefer this over SnapshotHuhHooks (which is
// scoped to cross-package tests per its docstring).
func RunWithPromptHooks(fn func() error) error {
	before, after := snapshotHuhHooks()
	if before != nil {
		before()
	}
	if after != nil {
		defer after()
	}
	return fn()
}

// huhTheme is the package-level huh.Theme built from devbox/styles.yml.
// It defaults to ThemeBase + devbox glyph overrides (no project palette
// applied) until ApplyStyles is called.
var huhTheme huh.Theme = huh.ThemeFunc(func(isDark bool) *huh.Styles {
	s := huh.ThemeBase(isDark)
	applyFormGlyphs(s)
	applyMultiSelectStateStyles(s, resolvedSuccess, resolvedMuted)
	return s
})

// applyFormGlyphs replaces the default huh prefix glyphs with the devbox look:
// "✓ " for selected items, "• " for unselected. Coloring is handled separately
// by buildPaletteApplier so the glyphs always render even without a palette.
func applyFormGlyphs(s *huh.Styles) {
	s.Focused.SelectedPrefix = s.Focused.SelectedPrefix.SetString("✓ ")
	s.Focused.UnselectedPrefix = s.Focused.UnselectedPrefix.SetString("• ")
	s.Blurred.SelectedPrefix = s.Blurred.SelectedPrefix.SetString("✓ ")
	s.Blurred.UnselectedPrefix = s.Blurred.UnselectedPrefix.SetString("• ")
}

func applyMultiSelectStateStyles(s *huh.Styles, selectedColor, unselectedColor string) {
	selected := lipgloss.Color(selectedColor)
	unselected := lipgloss.Color(unselectedColor)

	s.Focused.SelectedOption = s.Focused.SelectedOption.Foreground(selected).Bold(true).Faint(false)
	s.Focused.SelectedPrefix = s.Focused.SelectedPrefix.Foreground(selected).Bold(true).Faint(false)
	s.Blurred.SelectedOption = s.Blurred.SelectedOption.Foreground(selected).Bold(true).Faint(false)
	s.Blurred.SelectedPrefix = s.Blurred.SelectedPrefix.Foreground(selected).Bold(true).Faint(false)

	s.Focused.UnselectedOption = s.Focused.UnselectedOption.Foreground(unselected).Bold(false).Faint(true)
	s.Focused.UnselectedPrefix = s.Focused.UnselectedPrefix.Foreground(unselected).Bold(false).Faint(true)
	s.Blurred.UnselectedOption = s.Blurred.UnselectedOption.Foreground(unselected).Bold(false).Faint(true)
	s.Blurred.UnselectedPrefix = s.Blurred.UnselectedPrefix.Foreground(unselected).Bold(false).Faint(true)
}

// Theme returns the current package-level huh.Theme.
// All huh form/field call sites should use .WithTheme(ui.Theme()) so they
// automatically pick up palette changes from styles.yml.
func Theme() huh.Theme {
	return huhTheme
}

// buildPaletteApplier returns a function that applies project palette colors
// to a *huh.Styles in place. The returned function is safe to call multiple
// times on different *huh.Styles values (no shared state).
//
// Palette mapping (7-token → *huh.Styles):
//   - accent  → Focused.Title, Group.Title, SelectSelector, MultiSelectSelector,
//     Option, TextInput.Prompt, NextIndicator, PrevIndicator
//   - muted   → Focused.Description, Group.Description, Blurred.Title,
//     Blurred.Description, UnselectedOption, TextInput.Placeholder
//   - success → Focused.SelectedOption, Focused.SelectedPrefix (multi-select checked)
//   - danger  → Focused.ErrorIndicator, Focused.ErrorMessage
//
// The applier reads from the resolved token values (resolvedAccent, etc.) so
// passing nil or an empty StylesColors still produces a fully-themed *huh.Styles
// — empty user overrides have already been resolved to the built-in defaults
// by rebuildSemanticStyles.
func buildPaletteApplier() func(*huh.Styles) {
	return func(s *huh.Styles) {
		accent := lipgloss.Color(resolvedAccent)
		muted := lipgloss.Color(resolvedMuted)
		danger := lipgloss.Color(resolvedDanger)

		s.Focused.Title = s.Focused.Title.Foreground(accent).Bold(true)
		s.Group.Title = s.Group.Title.Foreground(accent).Bold(true)

		s.Focused.Description = s.Focused.Description.Foreground(muted)
		s.Group.Description = s.Group.Description.Foreground(muted)

		s.Focused.SelectSelector = s.Focused.SelectSelector.Foreground(accent)
		s.Focused.MultiSelectSelector = s.Focused.MultiSelectSelector.Foreground(accent)
		s.Focused.Option = s.Focused.Option.Foreground(accent)

		s.Blurred.Title = s.Blurred.Title.Foreground(muted)
		s.Blurred.Description = s.Blurred.Description.Foreground(muted)
		s.Focused.TextInput.Placeholder = s.Focused.TextInput.Placeholder.Foreground(muted)

		s.Focused.ErrorIndicator = s.Focused.ErrorIndicator.Foreground(danger)
		s.Focused.ErrorMessage = s.Focused.ErrorMessage.Foreground(danger)

		s.Focused.TextInput.Prompt = s.Focused.TextInput.Prompt.Foreground(accent)
		s.Focused.NextIndicator = s.Focused.NextIndicator.Foreground(accent)
		s.Focused.PrevIndicator = s.Focused.PrevIndicator.Foreground(accent)

		applyMultiSelectStateStyles(s, resolvedSuccess, resolvedMuted)
	}
}
