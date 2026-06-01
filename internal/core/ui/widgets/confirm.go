// Package widgets hosts interactive huh-based form primitives (RunConfirm,
// RunSelector, RunMultiSelect) plus the SetHuhHooks / RunWithPromptHooks
// indirection used by the live pipeline reporter to pause/resume its frame.
//
// Widgets imports github.com/semsemyonoff/dwe/internal/core/ui/styles for the shared HuhTheme
// and palette; renderers in core/ui/render do not depend on widgets.
package widgets

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	huh "charm.land/huh/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
)

// runConfirmFormFn is the underlying form runner; swappable in tests.
var runConfirmFormFn = defaultRunConfirmForm

func defaultRunConfirmForm(title, affirmative, negative string) (bool, error) {
	var result bool
	field := huh.NewConfirm().
		Title(title).
		Affirmative(affirmative).
		Negative(negative).
		Value(&result)
	err := huh.NewForm(huh.NewGroup(field)).WithTheme(styles.Theme()).WithShowHelp(false).Run()
	return result, err
}

// RunConfirm displays an interactive yes/no confirmation form and returns the
// user's choice. ErrCancelled is returned when the user presses Esc or Ctrl-C.
func RunConfirm(title, affirmative, negative string) (bool, error) {
	before, after := snapshotHuhHooks()
	if before != nil {
		before()
	}
	if after != nil {
		defer after()
	}
	result, err := runConfirmFormFn(title, affirmative, negative)
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return false, ErrCancelled
		}
		return false, err
	}
	return result, nil
}

// runConfirmRunFormFn is the underlying form runner for ConfirmRun; swappable in tests.
var runConfirmRunFormFn = defaultRunConfirmRunForm

func defaultRunConfirmRunForm(title, summary string) (bool, error) {
	var result bool
	prompt := title
	if summary != "" {
		prompt = summary + "\n\n" + title
	}
	field := huh.NewConfirm().
		Title(prompt).
		Affirmative("Yes").
		Negative("No").
		Value(&result)
	err := huh.NewForm(huh.NewGroup(field)).WithTheme(styles.Theme()).WithShowHelp(false).Run()
	return result, err
}

// ConfirmRun displays a confirmation prompt with a rendered summary of parameter
// values above the title. The caller is responsible for any template expansion
// in title (this package does not import tpl/config/usercommands).
//
// Return contract:
//   - (true, nil) — user picked Yes
//   - (false, nil) — user picked No
//   - (false, ErrCancelled) — user pressed Esc or Ctrl-C
//
// When values is empty, falls back to RunConfirm without a summary.
func ConfirmRun(title string, values map[string]string) (bool, error) {
	if len(values) == 0 {
		return RunConfirm(title, "Yes", "No")
	}
	summary := renderParamSummary(values)

	before, after := snapshotHuhHooks()
	if before != nil {
		before()
	}
	if after != nil {
		defer after()
	}
	result, err := runConfirmRunFormFn(title, summary)
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return false, ErrCancelled
		}
		return false, err
	}
	return result, nil
}

// renderParamSummary formats a values map as aligned "key = value" lines,
// sorted by key for deterministic output.
func renderParamSummary(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	maxKey := 0
	for k := range values {
		keys = append(keys, k)
		if len(k) > maxKey {
			maxKey = len(k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%-*s = %s", maxKey, k, values[k])
	}
	return b.String()
}
