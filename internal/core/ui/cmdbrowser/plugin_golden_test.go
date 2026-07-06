package cmdbrowser

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/ask"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// goldenFrameHeight is the terminal height the full-frame goldens render at.
// Width varies across the buckets (80 / 99 / 100); height is fixed so the
// goldens differ only by width and panel content, not vertical geometry.
const goldenFrameHeight = 24

// goldenRunItems is a fixed command set (dotted IDs → nested groups) that
// exercises the tree (groups + counts), the list (badges / param counts at wide
// widths), and the breadcrumb. Kept stable so the goldens are byte-deterministic.
func goldenRunItems() []Item {
	return []Item{
		{ID: "db.migrate", Description: "apply pending migrations", Type: "shell", ParamCount: 1},
		{ID: "db.seed", Description: "load fixtures", Type: "shell"},
		{ID: "services.api.test", Description: "run api tests", Type: "shell"},
		{ID: "services.api.build", Description: "build api image", Type: "compose"},
		{ID: "services.web.deploy", Description: "deploy web", Type: "compose", ParamCount: 2},
	}
}

// goldenEditItems is the vars-browser equivalent: ModeEdit relabels the noun
// ("var") and the select verb ("Edit"), so a separate golden pins that surface.
func goldenEditItems() []Item {
	return []Item{
		{ID: "db.host", Description: "database host", Type: "string"},
		{ID: "db.port", Description: "database port", Type: "int"},
		{ID: "cache.ttl", Description: "cache lifetime", Type: "int"},
	}
}

// assertGolden compares got against testdata/<name>, writing the file instead
// when UPDATE_GOLDEN is set. Mirrors the tui package helper so the cmdbrowser
// goldens regenerate with the same UPDATE_GOLDEN=1 switch.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("creating testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing golden %s: %v", path, err)
		}
		t.Logf("updated golden: %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s (run UPDATE_GOLDEN=1 to create): %v", path, err)
	}
	if got != string(want) {
		t.Errorf("golden %s mismatch:\ngot:\n%s\n\nwant:\n%s", name, got, want)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestBrowser_FullFrameGolden pins the full-frame plugin render at width buckets
// 80 / 99 / 100 (odd/even) for ModeRun and ModeEdit via the exported
// tui.RenderFrame harness. It asserts the frame fills the terminal exactly (no
// overflow), the status line carries the help hint, and the byte-stable layout
// matches the golden. Regenerate with
// make embedded-docs && UPDATE_GOLDEN=1 go test ./internal/core/ui/cmdbrowser/...
func TestBrowser_FullFrameGolden(t *testing.T) {
	modes := []struct {
		name  string
		mode  Mode
		items []Item
	}{
		{"run", ModeRun, goldenRunItems()},
		{"edit", ModeEdit, goldenEditItems()},
	}
	for _, mc := range modes {
		for _, w := range []int{80, 99, 100} {
			t.Run(mc.name+"_width_"+itoa(w), func(t *testing.T) {
				opts := DefaultOptions()
				opts.Mode = mc.mode
				b := newBrowser("dwe", mc.items, opts)
				content, err := tui.RenderFrame(b, tui.RunOptions{
					Brand:   "dwe",
					Project: "demo",
					Mouse:   true,
				}, w, goldenFrameHeight)
				if err != nil {
					t.Fatalf("RenderFrame: %v", err)
				}
				plain := stripANSI(content)

				rows := strings.Split(plain, "\n")
				if len(rows) != goldenFrameHeight {
					t.Errorf("row count = %d, want terminal height %d", len(rows), goldenFrameHeight)
				}
				for i, row := range rows {
					if got := lipgloss.Width(row); got != w {
						t.Errorf("row %d width = %d, want frame width %d: %q", i, got, w, row)
					}
				}
				if last := rows[len(rows)-1]; !strings.Contains(last, "? help") {
					t.Errorf("final row is not the status line: %q", last)
				}

				assertGolden(t, "frame_"+mc.name+"_"+itoa(w)+".golden", plain)
			})
		}
	}
}

// goldenEditFormItems are flat (no dotted IDs) so the root focus lists them
// directly, giving a selectable row to open the edit form over without expanding
// a group first.
func goldenEditFormItems() []Item {
	return []Item{
		{ID: "host", Description: "database host", Type: "string"},
		{ID: "port", Description: "database port", Type: "int"},
	}
}

// TestBrowser_EditFormOverlayGolden pins the full frame with the edit form
// overlay OPEN at the width buckets 80 / 99 / 100 × 24. The overlay is driven
// open through the real Frame (Tab focuses the list, Enter opens the form via
// onSelect → openEdit). No blink ticks are delivered (RenderFrameAfterSetup
// discards Init cmds), so the virtual cursor stays in its initial state and the
// render is byte-deterministic. Regenerate with
// make embedded-docs && UPDATE_GOLDEN=1 go test ./internal/core/ui/cmdbrowser/...
func TestBrowser_EditFormOverlayGolden(t *testing.T) {
	items := goldenEditFormItems()
	for _, w := range []int{80, 99, 100} {
		t.Run("width_"+itoa(w), func(t *testing.T) {
			opts := DefaultOptions()
			opts.Mode = ModeEdit
			opts.Edit = &EditSpec{
				BuildForm: func(idx int) (*ask.Form, error) {
					show := false
					return ask.Build("edit "+items[idx].ID, []ask.Field{{
						Key:         "value",
						Kind:        ask.FieldInput,
						Title:       items[idx].ID,
						Description: "current: " + items[idx].Description,
						Default:     items[idx].Description,
					}}, ask.RunOptions{ShowHelp: &show})
				},
				Commit: func(idx int, res ask.Result) (CommitOutcome, error) {
					return CommitOutcome{Item: items[idx]}, nil
				},
			}
			b := newBrowser("dwe", items, opts)
			content, err := tui.RenderFrameAfterSetup(b, tui.RunOptions{
				Brand:   "dwe",
				Project: "demo",
				Mouse:   true,
			}, w, goldenFrameHeight,
				tea.KeyPressMsg{Code: tea.KeyTab},
				tea.KeyPressMsg{Code: tea.KeyEnter},
			)
			if err != nil {
				t.Fatalf("RenderFrameAfterSetup: %v", err)
			}
			plain := stripANSI(content)

			rows := strings.Split(plain, "\n")
			if len(rows) != goldenFrameHeight {
				t.Errorf("row count = %d, want terminal height %d", len(rows), goldenFrameHeight)
			}
			for i, row := range rows {
				if got := lipgloss.Width(row); got != w {
					t.Errorf("row %d width = %d, want frame width %d: %q", i, got, w, row)
				}
			}
			if !strings.Contains(plain, "esc cancel") {
				t.Errorf("form overlay hint row missing from frame:\n%s", plain)
			}
			assertGolden(t, "frame_editform_"+itoa(w)+".golden", plain)
		})
	}
}

// goldenRunFormItems are flat (no dotted IDs) so the root focus lists them
// directly, giving a selectable row to open the param form over without expanding
// a group first.
func goldenRunFormItems() []Item {
	return []Item{
		{ID: "migrate", Description: "apply migrations", Type: "shell", ParamCount: 1},
		{ID: "deploy", Description: "deploy the app", Type: "compose", ParamCount: 3},
	}
}

// TestBrowser_RunFormOverlayGolden pins the full frame with the param-form overlay
// OPEN at the width buckets 80 / 99 / 100 × 24, for both a single-field form (row
// 0) and a multi-field form (row 1 — exercises the FormOverlay.MaxHeight bound).
// The overlay is driven open through the real Frame (Tab focuses the list, arrow
// keys move to the row, Enter opens the form via onSelect → openRunForm). No blink
// ticks are delivered (RenderFrameAfterSetup discards Init cmds), so the render is
// byte-deterministic. Regenerate with
// make embedded-docs && UPDATE_GOLDEN=1 go test ./internal/core/ui/cmdbrowser/...
func TestBrowser_RunFormOverlayGolden(t *testing.T) {
	items := goldenRunFormItems()
	// singleField builds a one-input form for row 0, a three-input form for row 1.
	buildForm := func(idx int, _ bool) (*ask.Form, error) {
		show := false
		fields := []ask.Field{{
			Key:         "name",
			Kind:        ask.FieldInput,
			Title:       "name",
			Description: "the target name",
		}}
		if idx == 1 {
			fields = append(fields,
				ask.Field{Key: "tag", Kind: ask.FieldInput, Title: "tag", Description: "image tag"},
				ask.Field{Key: "replicas", Kind: ask.FieldInput, Title: "replicas", Description: "replica count"},
			)
		}
		return ask.Build("dwe commands › "+items[idx].ID, fields, ask.RunOptions{ShowHelp: &show})
	}
	harvest := func(idx int, res ask.Result) map[string]string {
		return map[string]string{"name": res.String("name")}
	}

	forms := []struct {
		name string
		row  int // extra Down presses after Tab to reach the target row
	}{
		{"single", 0},
		{"multi", 1},
	}
	for _, f := range forms {
		for _, w := range []int{80, 99, 100} {
			t.Run(f.name+"_width_"+itoa(w), func(t *testing.T) {
				opts := DefaultOptions()
				opts.Mode = ModeRun
				opts.RunForm = &RunFormSpec{BuildForm: buildForm, Harvest: harvest}
				b := newBrowser("dwe", items, opts)

				setup := []tea.Msg{tea.KeyPressMsg{Code: tea.KeyTab}}
				for range f.row {
					setup = append(setup, tea.KeyPressMsg{Code: tea.KeyDown})
				}
				setup = append(setup, tea.KeyPressMsg{Code: tea.KeyEnter})

				content, err := tui.RenderFrameAfterSetup(b, tui.RunOptions{
					Brand:   "dwe",
					Project: "demo",
					Mouse:   true,
				}, w, goldenFrameHeight, setup...)
				if err != nil {
					t.Fatalf("RenderFrameAfterSetup: %v", err)
				}
				plain := stripANSI(content)

				rows := strings.Split(plain, "\n")
				if len(rows) != goldenFrameHeight {
					t.Errorf("row count = %d, want terminal height %d", len(rows), goldenFrameHeight)
				}
				for i, row := range rows {
					if got := lipgloss.Width(row); got != w {
						t.Errorf("row %d width = %d, want frame width %d: %q", i, got, w, row)
					}
				}
				if !strings.Contains(plain, "esc cancel") {
					t.Errorf("form overlay hint row missing from frame:\n%s", plain)
				}
				assertGolden(t, "frame_runform_"+f.name+"_"+itoa(w)+".golden", plain)
			})
		}
	}
}

// TestBrowser_HelpModalGolden pins the registry-generated ?-modal help per mode
// via the exported tui.BuildHelp harness, and locks the per-mode action
// visibility: ModeRun lists the skip-confirm (`y`) and force-form (`e`) verbs;
// ModeEdit omits both (they are not even registered there).
func TestBrowser_HelpModalGolden(t *testing.T) {
	store, err := i18n.Load("")
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}

	runOpts := DefaultOptions()
	runHelp, err := tui.BuildHelp(newBrowser("dwe", goldenRunItems(), runOpts), store, "en", 100, 40)
	if err != nil {
		t.Fatalf("BuildHelp run: %v", err)
	}
	runPlain := stripANSI(runHelp.Content)
	for _, want := range []string{"Skip confirmation", "Edit parameters"} {
		if !strings.Contains(runPlain, want) {
			t.Errorf("ModeRun help missing %q\n%s", want, runPlain)
		}
	}
	assertGolden(t, "help_run.golden", runPlain)

	editOpts := DefaultOptions()
	editOpts.Mode = ModeEdit
	editHelp, err := tui.BuildHelp(newBrowser("dwe", goldenEditItems(), editOpts), store, "en", 100, 40)
	if err != nil {
		t.Fatalf("BuildHelp edit: %v", err)
	}
	editPlain := stripANSI(editHelp.Content)
	for _, absent := range []string{"Skip confirmation", "Edit parameters"} {
		if strings.Contains(editPlain, absent) {
			t.Errorf("ModeEdit help must omit %q\n%s", absent, editPlain)
		}
	}
	assertGolden(t, "help_edit.golden", editPlain)
}

// TestBrowser_ResultSemanticsRegression drives the plugin through every commit
// path (Enter-select, force-form, inspect-enter) across all modes and asserts the
// Result fields — Idx / Action / SkipConfirm / ForceParamForm — and the 0 ≤ Idx <
// len(items) invariant. This guards the external Run/Result contract the whole
// migration must preserve.
func TestBrowser_ResultSemanticsRegression(t *testing.T) {
	items := goldenRunItems() // db.migrate(0), db.seed(1), services.api.* , services.web.*

	cases := []struct {
		name           string
		mode           Mode
		skipConfirm    bool
		drive          func(b *browser) tea.Cmd
		wantAction     Action
		wantSkip       bool
		wantForceForm  bool
		wantItemSuffix string // ID suffix the selected Idx must resolve to
	}{
		{
			name: "run_select", mode: ModeRun, skipConfirm: true,
			drive: func(b *browser) tea.Cmd {
				b.tree.eng.SetCursorByKey("db")
				b.refreshList()
				b.active = panelList
				b.list.Select(1) // db.seed
				cmd, _ := b.onSelect()
				return cmd
			},
			wantAction: ActionRun, wantSkip: true, wantItemSuffix: "db.seed",
		},
		{
			name: "run_force_form", mode: ModeRun,
			drive: func(b *browser) tea.Cmd {
				b.tree.eng.SetCursorByKey("db")
				b.refreshList()
				b.active = panelList
				b.list.Select(0) // db.migrate
				cmd, _ := b.onForceForm()
				return cmd
			},
			wantAction: ActionRun, wantForceForm: true, wantItemSuffix: "db.migrate",
		},
		{
			name: "edit_select", mode: ModeEdit,
			drive: func(b *browser) tea.Cmd {
				b.tree.eng.SetCursorByKey("db")
				b.refreshList()
				b.active = panelList
				b.list.Select(0)
				cmd, _ := b.onSelect()
				return cmd
			},
			wantAction: ActionEdit, wantItemSuffix: "db.migrate",
		},
		{
			name: "inspect_enter", mode: ModeInspect,
			drive: func(b *browser) tea.Cmd {
				b.tree.eng.SetCursorByKey("db")
				b.refreshList()
				b.active = panelList
				b.list.Select(1) // db.seed
				b.openInspect()
				b.PendingOverlay()
				return b.updateInspect(tea.KeyPressMsg{Code: tea.KeyEnter})
			},
			wantAction: ActionInspect, wantItemSuffix: "db.seed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := DefaultOptions()
			opts.Mode = tc.mode
			b := newBrowser("dwe", items, opts)
			b.Resize(tui.Region{Width: 100, Height: 24})
			b.skipConfirm = tc.skipConfirm

			cmd := tc.drive(b)
			if cmd == nil {
				t.Fatalf("commit path returned nil cmd, want tea.Quit")
			}

			res := b.Result().(Result)
			if res.Idx < 0 || res.Idx >= len(items) {
				t.Fatalf("Result.Idx = %d out of range [0,%d)", res.Idx, len(items))
			}
			if items[res.Idx].ID != tc.wantItemSuffix {
				t.Errorf("Result.Idx resolves to %q, want %q", items[res.Idx].ID, tc.wantItemSuffix)
			}
			if res.Action != tc.wantAction {
				t.Errorf("Result.Action = %v, want %v", res.Action, tc.wantAction)
			}
			if res.SkipConfirm != tc.wantSkip {
				t.Errorf("Result.SkipConfirm = %v, want %v", res.SkipConfirm, tc.wantSkip)
			}
			if res.ForceParamForm != tc.wantForceForm {
				t.Errorf("Result.ForceParamForm = %v, want %v", res.ForceParamForm, tc.wantForceForm)
			}
		})
	}
}

// TestBrowser_ZeroResultIsUnknownAction documents the contract Run relies on: a
// browser that never commits leaves a zero-value Result whose Action is
// ActionUnknown, which Run maps to widgets.ErrCancelled (exercised end-to-end by
// TestRun_WideActionUnknownIsCancelled).
func TestBrowser_ZeroResultIsUnknownAction(t *testing.T) {
	b := newBrowser("dwe", goldenRunItems(), DefaultOptions())
	res := b.Result().(Result)
	if res.Action != ActionUnknown {
		t.Errorf("uncommitted Result.Action = %v, want ActionUnknown", res.Action)
	}
}

// TestBrowser_FallbackRoutingNeverEntersFrame asserts the Variant A boundary: the
// 60–79 width bucket (the dropped in-TUI single-panel range) and the non-TTY case
// route to the flat huh fallback and NEVER drive the framework plugin. runTUI is
// stubbed to fail loudly if it is ever reached.
func TestBrowser_FallbackRoutingNeverEntersFrame(t *testing.T) {
	cases := []struct {
		name   string
		isTTY  bool
		width  int
		height int
		// wantSelector is false for non-TTY (the fallback also needs a TTY, so Run
		// short-circuits to ErrCancelled without invoking the selector).
		wantSelector bool
		wantErr      error
	}{
		{"width_60", true, 60, 30, true, nil},
		{"width_79", true, 79, 30, true, nil},
		{"non_tty", false, 120, 30, false, widgets.ErrCancelled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := 0
			withSeams(t, tc.isTTY, tc.width, tc.height, nil, func(string, []widgets.SelectorItem) (int, error) {
				called++
				return 0, nil
			})
			orig := runTUI
			t.Cleanup(func() { runTUI = orig })
			runTUI = func(_ tui.Plugin, _ tui.RunOptions) (any, error) {
				t.Fatalf("%s must route to the fallback, not the framework frame", tc.name)
				return nil, nil
			}

			_, err := Run("pick", []Item{{ID: "a"}, {ID: "b"}}, DefaultOptions())
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("Run err = %v, want %v", err, tc.wantErr)
			}
			wantCalls := 0
			if tc.wantSelector {
				wantCalls = 1
			}
			if called != wantCalls {
				t.Errorf("selector calls = %d, want %d", called, wantCalls)
			}
		})
	}
}
