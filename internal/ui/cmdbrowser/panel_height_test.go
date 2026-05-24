package cmdbrowser

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestModel_PanelHeightsMatch_WithLongDescriptions guards against two
// regressions that surface together as the "torn right border" symptom:
//
//   - Multi-line descriptions (YAML literal blocks carry `\n`) used to render
//     beyond the delegate's Height()=2 contract, pushing the right panel taller
//     than the left under JoinHorizontal.
//   - listHeight previously reserved only one row for the breadcrumb instead of
//     also accounting for both panel borders, so even single-line descriptions
//     could exceed inner panel space by two rows.
//
// The two together stretched the right panel below where the left panel ended,
// leaving the right border without a closing corner in the visible area. This
// test asserts both panel frames close on the same row.
func TestModel_PanelHeightsMatch_WithLongDescriptions(t *testing.T) {
	t.Parallel()
	items := []Item{
		{ID: "db.change-admin-config", Type: "service_exec", Description: "Upsert one row in the `configs` table (INSERT … ON DUPLICATE KEY UPDATE). Generic primitive consumed by services.main.admin.* / services.main.config.*"},
		{ID: "db.cli", Type: "service_exec", Description: "Connect to the database in the db container"},
		{ID: "db.dump", Type: "shell", Description: "Dump `${param.database}` from the db container into `${param.out}`\n(gzipped). Generic primitive — snapshot workflows pass an explicit\n`out` path under `${snapshot.path}/db/`."},
		{ID: "db.sync-tables", Type: "shell", Description: "Sync tables from a remote dev MariaDB into the local DB. Pipeline:\nssh remote → mariadb-dump → xz → scp back → import via `docker exec db mariadb`.\n`tables` may be a comma-separated list."},
		{ID: "db.truncate", Type: "service_exec", Description: "Truncate a table (FKs temporarily disabled)."},
	}
	for _, dim := range []struct{ w, h int }{{100, 26}, {120, 26}, {150, 30}, {180, 40}} {
		t.Run("w_"+itoa(dim.w)+"_h_"+itoa(dim.h), func(t *testing.T) {
			t.Parallel()
			m := newModel("pick", items, DefaultOptions(), dim.w, dim.h)
			m.tree.focusedID = "db"
			m.refreshList()
			out := m.View().Content
			lines := strings.Split(out, "\n")
			lw := leftWidth(dim.w)
			leftClose, rightClose := -1, -1
			for i, line := range lines {
				plain := stripANSI(line)
				if lipgloss.Width(plain) < lw {
					continue
				}
				if r := []rune(plain)[lw-1]; r == '┘' {
					leftClose = i
				}
				runes := []rune(plain)
				if len(runes) >= dim.w && runes[dim.w-1] == '┘' {
					rightClose = i
				}
			}
			if leftClose < 0 {
				t.Fatalf("left panel never closed; output:\n%s", out)
			}
			if rightClose < 0 {
				t.Fatalf("right panel never closed; output:\n%s", out)
			}
			if leftClose != rightClose {
				t.Errorf("panel bottoms misaligned: left closes at row %d, right at row %d", leftClose, rightClose)
			}
		})
	}
}
