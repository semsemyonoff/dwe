package render

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/semsemyonoff/dwe/internal/core/ui/statusview"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
)

// GitWorkspace renders a styled Lipgloss table of per-service git
// workspace metadata.
//
// Columns: SERVICE, DIR, BRANCH, SHA, DIRTY, AHEAD/BEHIND.
//
// Rows with no own .git (Err == nil, Branch == "") render every git column as
// "—". Rows with Err != nil render the same way; the caller is expected to
// emit a single aggregate warning to stderr counting Err != nil rows.
func GitWorkspace(rows []statusview.GitWorkspaceRow) string {
	stringRows := make([][]string, len(rows))
	dirtyStyles := make([]bool, len(rows))

	for i, r := range rows {
		branch, sha, ab, dirty := "—", "—", "—", "—"
		blank := r.Branch == ""
		if !blank {
			branch = r.Branch
			if r.SHA != "" {
				sha = r.SHA
			}
			if r.AheadBehind != "" {
				ab = r.AheadBehind
			}
			if r.Dirty {
				dirty = "dirty"
			} else {
				dirty = "clean"
			}
		}
		stringRows[i] = []string{r.Service, r.Dir, branch, sha, dirty, ab}
		dirtyStyles[i] = r.Dirty
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(styles.BorderStyle()).
		Headers("SERVICE", "DIR", "BRANCH", "SHA", "DIRTY", "AHEAD/BEHIND").
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return styles.AccentStyle().Bold(true)
			}
			if row < 0 || row >= len(dirtyStyles) {
				return lipgloss.NewStyle()
			}
			if col == 4 { // DIRTY column
				if dirtyStyles[row] {
					return styles.WarningStyle()
				}
				return styles.SuccessStyle()
			}
			return lipgloss.NewStyle()
		})

	for _, r := range stringRows {
		t.Row(r...)
	}

	return t.String()
}
