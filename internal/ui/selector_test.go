package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// helpers to create test key messages

func keyPress(code rune, text string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Text: text}
}

func specialKey(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code}
}

// TestSelectorInit verifies that Init returns nil (no initial command).
func TestSelectorInit(t *testing.T) {
	m := selectorModel{
		items:    []SelectorItem{{Label: "a"}},
		cursor:   0,
		selected: -1,
	}
	cmd := m.Init()
	if cmd != nil {
		t.Error("expected Init to return nil")
	}
}

// TestSelectorView verifies that View renders labels and the hint line.
func TestSelectorView(t *testing.T) {
	m := selectorModel{
		title:    "Pick one",
		items:    []SelectorItem{{Label: "alpha"}, {Label: "beta"}},
		cursor:   0,
		selected: -1,
	}
	view := m.View()
	// view.Content holds the rendered string
	if !strings.Contains(view.Content, "Pick one") {
		t.Error("expected title in view")
	}
	if !strings.Contains(view.Content, "alpha") {
		t.Error("expected 'alpha' in view")
	}
	if !strings.Contains(view.Content, "beta") {
		t.Error("expected 'beta' in view")
	}
	if !strings.Contains(view.Content, "navigate") {
		t.Error("expected hint line in view")
	}
}

// TestSelectorViewStatusEnabled verifies enabled status shows checkmark.
func TestSelectorViewStatusEnabled(t *testing.T) {
	m := selectorModel{
		items: []SelectorItem{
			{Label: "svc", Status: "enabled"},
		},
		cursor:   0,
		selected: -1,
	}
	view := m.View()
	if !strings.Contains(view.Content, "✓") {
		t.Error("expected checkmark for enabled status")
	}
}

// TestSelectorViewDescription verifies description is rendered.
func TestSelectorViewDescription(t *testing.T) {
	m := selectorModel{
		items: []SelectorItem{
			{Label: "main", Description: "app-main container"},
		},
		cursor:   0,
		selected: -1,
	}
	view := m.View()
	if !strings.Contains(view.Content, "app-main container") {
		t.Error("expected description in view")
	}
}

// TestSelectorNavigateDown verifies down key moves cursor to next selectable.
func TestSelectorNavigateDown(t *testing.T) {
	m := selectorModel{
		items:    []SelectorItem{{Label: "a"}, {Label: "b"}, {Label: "c"}},
		cursor:   0,
		selected: -1,
	}
	updated, _ := m.Update(specialKey(tea.KeyDown))
	result := updated.(selectorModel)
	if result.cursor != 1 {
		t.Errorf("expected cursor 1 after down, got %d", result.cursor)
	}
}

// TestSelectorNavigateUp verifies up key moves cursor to previous selectable.
func TestSelectorNavigateUp(t *testing.T) {
	m := selectorModel{
		items:    []SelectorItem{{Label: "a"}, {Label: "b"}, {Label: "c"}},
		cursor:   2,
		selected: -1,
	}
	updated, _ := m.Update(specialKey(tea.KeyUp))
	result := updated.(selectorModel)
	if result.cursor != 1 {
		t.Errorf("expected cursor 1 after up, got %d", result.cursor)
	}
}

// TestSelectorNavigateWrapsDown verifies down wraps from last to first.
func TestSelectorNavigateWrapsDown(t *testing.T) {
	m := selectorModel{
		items:    []SelectorItem{{Label: "a"}, {Label: "b"}},
		cursor:   1,
		selected: -1,
	}
	updated, _ := m.Update(specialKey(tea.KeyDown))
	result := updated.(selectorModel)
	if result.cursor != 0 {
		t.Errorf("expected cursor 0 (wrap), got %d", result.cursor)
	}
}

// TestSelectorNavigateWrapsUp verifies up wraps from first to last.
func TestSelectorNavigateWrapsUp(t *testing.T) {
	m := selectorModel{
		items:    []SelectorItem{{Label: "a"}, {Label: "b"}},
		cursor:   0,
		selected: -1,
	}
	updated, _ := m.Update(specialKey(tea.KeyUp))
	result := updated.(selectorModel)
	if result.cursor != 1 {
		t.Errorf("expected cursor 1 (wrap), got %d", result.cursor)
	}
}

// TestSelectorNavigateSkipsDisabled verifies navigation skips Disabled items.
func TestSelectorNavigateSkipsDisabled(t *testing.T) {
	m := selectorModel{
		items: []SelectorItem{
			{Label: "a"},
			{Label: "b", Disabled: true},
			{Label: "c"},
		},
		cursor:   0,
		selected: -1,
	}
	updated, _ := m.Update(specialKey(tea.KeyDown))
	result := updated.(selectorModel)
	// should skip b (disabled) and land on c
	if result.cursor != 2 {
		t.Errorf("expected cursor 2 (skip disabled), got %d", result.cursor)
	}
}

// TestSelectorEnterSelects verifies Enter key selects current item.
func TestSelectorEnterSelects(t *testing.T) {
	m := selectorModel{
		items:    []SelectorItem{{Label: "a"}, {Label: "b"}},
		cursor:   1,
		selected: -1,
	}
	updated, cmd := m.Update(specialKey(tea.KeyEnter))
	result := updated.(selectorModel)
	if result.selected != 1 {
		t.Errorf("expected selected=1, got %d", result.selected)
	}
	if !result.done {
		t.Error("expected done=true after enter")
	}
	if cmd == nil {
		t.Error("expected Quit cmd after enter")
	}
}

// TestSelectorEnterOnDisabledDoesNotSelect verifies disabled items cannot be selected.
func TestSelectorEnterOnDisabledDoesNotSelect(t *testing.T) {
	m := selectorModel{
		items:    []SelectorItem{{Label: "a", Disabled: true}},
		cursor:   0,
		selected: -1,
	}
	updated, cmd := m.Update(specialKey(tea.KeyEnter))
	result := updated.(selectorModel)
	if result.selected != -1 {
		t.Error("expected disabled item cannot be selected")
	}
	if cmd != nil {
		t.Error("expected no cmd when trying to select disabled item")
	}
}

// TestSelectorEscCancels verifies Esc sets selected=-1 and done=true.
func TestSelectorEscCancels(t *testing.T) {
	m := selectorModel{
		items:    []SelectorItem{{Label: "a"}},
		cursor:   0,
		selected: -1,
	}
	updated, cmd := m.Update(specialKey(tea.KeyEsc))
	result := updated.(selectorModel)
	if result.selected != -1 {
		t.Errorf("expected selected=-1 after esc, got %d", result.selected)
	}
	if !result.done {
		t.Error("expected done=true after esc")
	}
	if cmd == nil {
		t.Error("expected Quit cmd after esc")
	}
}

// TestSelectorQKeyCancels verifies 'q' also cancels.
func TestSelectorQKeyCancels(t *testing.T) {
	m := selectorModel{
		items:    []SelectorItem{{Label: "a"}},
		cursor:   0,
		selected: -1,
	}
	updated, _ := m.Update(keyPress('q', "q"))
	result := updated.(selectorModel)
	if result.selected != -1 || !result.done {
		t.Error("expected cancellation on q key")
	}
}

// TestSelectorKJNavigation verifies vim-style k/j navigation.
func TestSelectorKJNavigation(t *testing.T) {
	m := selectorModel{
		items:    []SelectorItem{{Label: "a"}, {Label: "b"}, {Label: "c"}},
		cursor:   0,
		selected: -1,
	}
	// j = down
	updated, _ := m.Update(keyPress('j', "j"))
	m = updated.(selectorModel)
	if m.cursor != 1 {
		t.Errorf("j: expected cursor 1, got %d", m.cursor)
	}
	// k = up
	updated, _ = m.Update(keyPress('k', "k"))
	m = updated.(selectorModel)
	if m.cursor != 0 {
		t.Errorf("k: expected cursor 0, got %d", m.cursor)
	}
}

// TestInitialCursor verifies initialCursor picks first non-disabled item.
func TestInitialCursor(t *testing.T) {
	items := []SelectorItem{
		{Label: "a", Disabled: true},
		{Label: "b"},
		{Label: "c"},
	}
	got := initialCursor(items)
	if got != 1 {
		t.Errorf("expected initial cursor 1, got %d", got)
	}
}

// TestInitialCursorAllDisabled verifies initialCursor returns 0 when all disabled.
func TestInitialCursorAllDisabled(t *testing.T) {
	items := []SelectorItem{
		{Label: "a", Disabled: true},
		{Label: "b", Disabled: true},
	}
	got := initialCursor(items)
	if got != 0 {
		t.Errorf("expected 0 when all disabled, got %d", got)
	}
}
