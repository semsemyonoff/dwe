package setup

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"

	"devbox-cli/internal/core/ui/styles"
)

// TestWizardHelpKeepsEscAfterSuggestionsRefresh locks the AcceptSuggestion
// hijack against huh's runtime suggestion-refresh path. Returning an empty
// slice from SuggestionsFunc resets ShowSuggestions to false inside the
// updateSuggestionsMsg handler, which would drop our help slot. A non-empty
// single-space suggestion keeps the slot live without ever matching a typed
// prefix.
func TestWizardHelpKeepsEscAfterSuggestionsRefresh(t *testing.T) {
	val := "8080"
	field := huh.NewInput().
		Title("Port for web.http").
		Value(&val).
		SuggestionsFunc(func() []string { return []string{" "} }, nil)

	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"), key.WithHelp("esc", "cancel"))
	km.Input.AcceptSuggestion = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))

	form := huh.NewForm(huh.NewGroup(field).Title("Port Overrides")).
		WithTheme(styles.Theme()).
		WithKeyMap(km).
		WithShowHelp(true)

	form.Init()
	form.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	// Drive the form past several update cycles so updateSuggestionsMsg fires
	// at least once (which would have flipped ShowSuggestions to false if the
	// SuggestionsFunc returned an empty slice).
	for range 5 {
		form.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	}
	view := form.View()
	if !strings.Contains(view, "esc") || !strings.Contains(view, "cancel") {
		t.Errorf("expected help line to contain 'esc cancel' after suggestion refresh, got:\n%s", view)
	}
}
