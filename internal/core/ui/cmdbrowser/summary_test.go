package cmdbrowser

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"

	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
)

// summaryItem mimics what cli/command builds for a command whose description
// is a YAML `|` block: Summary is its first line, Description the full text.
var summaryItem = Item{
	ID:          "db.migrate",
	Description: "\nRun migrations\nUsage: dwe cmd db.migrate --set step=1",
	Summary:     "Run migrations",
	Type:        "shell",
}

func TestRowText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, desc, summary, line string
		more                      bool
	}{
		{"summary of multi-line", "\nRun\nmore", "Run", "Run", true},
		{"summary equals single line", "Run it", "Run it", "Run it", false},
		{"summary only folded a tab", "Run\tit", "Run it", "Run it", false},
		{"no summary keeps raw first line", "a\nb", "", "a", true},
		{"no summary single line", "value", "", "value", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			line, more := rowText(tc.desc, tc.summary)
			if line != tc.line || more != tc.more {
				t.Errorf("rowText(%q, %q) = (%q, %v), want (%q, %v)", tc.desc, tc.summary, line, more, tc.line, tc.more)
			}
		})
	}
}

// TestCmdDelegate_RendersSummary: a leading blank line in the description must
// not blank the row — the row shows the summary, marked as having more.
func TestCmdDelegate_RendersSummary(t *testing.T) {
	t.Parallel()
	d := newCmdDelegate(80, true)
	items := []list.Item{newListItem(0, summaryItem)}
	out := stripANSI(renderDelegate(t, d, items, 1, 0))
	if !strings.Contains(out, "Run migrations…") {
		t.Errorf("row must show the summary with a more-marker; got %q", out)
	}
	if strings.Contains(out, "Usage:") {
		t.Errorf("row must not show the second line; got %q", out)
	}
}

// TestFallback_ShowsSummary: a narrow terminal drops to the flat selector,
// whose rows show the summary too.
func TestFallback_ShowsSummary(t *testing.T) {
	var got []widgets.SelectorItem
	withSeams(t, true, 50, 30, nil, func(_ string, items []widgets.SelectorItem) (int, error) {
		got = items
		return 0, nil
	})
	if _, err := Run("pick", []Item{summaryItem}, DefaultOptions()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Description != "Run migrations" {
		t.Errorf("fallback selector must show the summary, got %+v", got)
	}
}

// TestFilter_MatchesBeyondSummary: search keeps the full description, so text
// from the second line still finds the command.
func TestFilter_MatchesBeyondSummary(t *testing.T) {
	t.Parallel()
	f := newFilterState(map[string]bool{}, "")
	f.query = "step=1"
	f.recompute([]Item{summaryItem, {ID: "other", Description: "unrelated", Summary: "unrelated"}}, false)
	if len(f.matched) != 1 || f.matched[0] != 0 {
		t.Errorf("filter must match text past the summary; matched=%v", f.matched)
	}
}
