package statustui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// --- Actions registration ---

// statusReg builds a registry the way the Frame does for this single-panel
// plugin: the focus built-ins are stripped (freeing tab / shift+tab) before the
// plugin's Actions hook runs. Mirrors newFrame / BuildHelp.
func statusReg(t *testing.T, p *plugin) *tui.Registry {
	t.Helper()
	reg := tui.NewRegistry()
	reg.DisableFocusNav()
	if err := p.Actions(reg); err != nil {
		t.Fatalf("Actions() = %v", err)
	}
	return reg
}

func TestActions_RegistersWithoutCollision(t *testing.T) {
	p, _ := newTestPlugin(t)
	statusReg(t, p) // fatals on any registration error
}

func TestActions_RegistersAllExpectedActions(t *testing.T) {
	p, _ := newTestPlugin(t)
	reg := statusReg(t, p)

	want := []tui.Action{
		tui.ActionReload,
		actionSectionNext, actionSectionPrev,
		actionTabPrev, actionTabNext,
		actionTab1, actionTab2, actionTab3, actionTab4, actionTab5,
	}
	for _, a := range want {
		if _, ok := reg.Binding(a); !ok {
			t.Errorf("action %q not registered", a)
		}
	}
}

func TestActions_NoLegacyRBinding(t *testing.T) {
	// The legacy "r" reload key is replaced by stdlib ctrl+r. Registering "r"
	// here would silently resurrect the dropped binding.
	p, _ := newTestPlugin(t)
	reg := statusReg(t, p)
	if a, ok := reg.Match("r"); ok {
		t.Errorf("key %q unexpectedly matched action %q", "r", a)
	}
	if a, ok := reg.Match("ctrl+r"); !ok || a != tui.ActionReload {
		t.Errorf("key %q = (%q, %v), want (%q, true)", "ctrl+r", a, ok, tui.ActionReload)
	}
}

func TestActions_TabRebindAndBuiltins(t *testing.T) {
	// On this single-panel surface the Frame strips the focus built-ins, so the
	// plugin rebinds tab / shift+tab to switch tabs and ] / [ to jump between
	// sub-tables; ?/q/ctrl+c remain framework built-ins and esc stays
	// overlay-close only.
	p, _ := newTestPlugin(t)
	reg := statusReg(t, p)
	for _, k := range []string{"tab", "shift+tab", "]", "[", "?", "q", "ctrl+c", "esc"} {
		a, ok := reg.Match(k)
		switch k {
		case "tab":
			if a != actionTabNext {
				t.Errorf("key %q = %q, want %q", k, a, actionTabNext)
			}
		case "shift+tab":
			if a != actionTabPrev {
				t.Errorf("key %q = %q, want %q", k, a, actionTabPrev)
			}
		case "]":
			if a != actionSectionNext {
				t.Errorf("key %q = %q, want %q", k, a, actionSectionNext)
			}
		case "[":
			if a != actionSectionPrev {
				t.Errorf("key %q = %q, want %q", k, a, actionSectionPrev)
			}
		case "?":
			if a != tui.ActionHelp {
				t.Errorf("key %q = %q, want built-in %q", k, a, tui.ActionHelp)
			}
		case "q", "ctrl+c":
			if a != tui.ActionQuit {
				t.Errorf("key %q = %q, want built-in %q", k, a, tui.ActionQuit)
			}
		case "esc":
			if ok {
				t.Errorf("key %q matched action %q, want unmatched (esc is overlay-close only)", k, a)
			}
		}
	}
}

// --- HandleAction dispatch ---

func newActionTestPlugin(t *testing.T) *plugin {
	t.Helper()
	p, _ := newTestPlugin(t)
	p.m.tabs = goldenTabs()
	p.m.loading = false
	p.m.viewport.SetContent(p.m.tabs[0].content)
	return p
}

func TestHandleAction_TabPrevNext_WrapsAround(t *testing.T) {
	tests := []struct {
		name    string
		start   int
		action  tui.Action
		wantIdx int
	}{
		{"next from first", 0, actionTabNext, 1},
		{"next from last wraps", 4, actionTabNext, 0},
		{"prev from first wraps", 0, actionTabPrev, 4},
		{"prev from middle", 2, actionTabPrev, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newActionTestPlugin(t)
			p.m.active = tt.start
			cmd, handled := p.HandleAction(tt.action)
			if !handled {
				t.Fatalf("HandleAction(%q) handled = false, want true", tt.action)
			}
			if cmd != nil {
				t.Errorf("HandleAction(%q) cmd = %v, want nil", tt.action, cmd)
			}
			if p.m.active != tt.wantIdx {
				t.Errorf("active = %d, want %d", p.m.active, tt.wantIdx)
			}
		})
	}
}

func TestHandleAction_TabJump(t *testing.T) {
	tests := []struct {
		action  tui.Action
		wantIdx int
	}{
		{actionTab1, 0},
		{actionTab2, 1},
		{actionTab3, 2},
		{actionTab4, 3},
		{actionTab5, 4},
	}
	for _, tt := range tests {
		t.Run(string(tt.action), func(t *testing.T) {
			p := newActionTestPlugin(t)
			p.m.active = -1 // sentinel; setActiveTab must overwrite on a valid jump
			_, handled := p.HandleAction(tt.action)
			if !handled {
				t.Fatalf("HandleAction(%q) handled = false, want true", tt.action)
			}
			if p.m.active != tt.wantIdx {
				t.Errorf("active = %d, want %d", p.m.active, tt.wantIdx)
			}
		})
	}
}

func TestHandleAction_TabJump_OutOfRangeIgnored(t *testing.T) {
	p := newActionTestPlugin(t)
	p.m.tabs = p.m.tabs[:2] // only Tab1/Tab2 valid
	p.m.active = 1

	_, handled := p.HandleAction(actionTab5)
	if !handled {
		t.Fatalf("HandleAction(actionTab5) handled = false, want true")
	}
	if p.m.active != 1 {
		t.Errorf("active = %d, want unchanged 1 (out-of-range jump ignored)", p.m.active)
	}
}

func TestHandleAction_TabPrevNext_NoopBeforeTabsLoaded(t *testing.T) {
	p, _ := newTestPlugin(t) // no tabs assigned
	for _, a := range []tui.Action{actionTabPrev, actionTabNext} {
		cmd, handled := p.HandleAction(a)
		if !handled {
			t.Errorf("HandleAction(%q) handled = false, want true", a)
		}
		if cmd != nil {
			t.Errorf("HandleAction(%q) cmd = %v, want nil", a, cmd)
		}
		if p.m.active != 0 {
			t.Errorf("active = %d, want unchanged 0", p.m.active)
		}
	}
}

func TestHandleAction_Reload_TriggersLoadAndPreservesOffset(t *testing.T) {
	p := newActionTestPlugin(t)
	p.m.active = 1
	p.m.loadGen = 3
	p.m.viewport.SetYOffset(2)

	cmd, handled := p.HandleAction(tui.ActionReload)
	if !handled {
		t.Fatalf("HandleAction(reload) handled = false, want true")
	}
	if cmd == nil {
		t.Fatalf("HandleAction(reload) cmd = nil, want a load command")
	}
	if p.m.loadGen != 4 {
		t.Errorf("loadGen = %d, want 4", p.m.loadGen)
	}
	if p.m.reloadGen != 4 {
		t.Errorf("reloadGen = %d, want 4", p.m.reloadGen)
	}
	if p.m.reloadActive != 1 {
		t.Errorf("reloadActive = %d, want 1", p.m.reloadActive)
	}
	if !p.m.reloading {
		t.Errorf("reloading = false, want true")
	}
}

func TestHandleAction_Reload_NoopBeforeTabsLoaded(t *testing.T) {
	p, _ := newTestPlugin(t)
	cmd, handled := p.HandleAction(tui.ActionReload)
	if !handled {
		t.Fatalf("HandleAction(reload) handled = false, want true")
	}
	if cmd != nil {
		t.Errorf("HandleAction(reload) cmd = %v, want nil", cmd)
	}
	if p.m.reloading {
		t.Errorf("reloading = true, want false (no tabs to reload)")
	}
}

func TestHandleAction_Unknown(t *testing.T) {
	p := newActionTestPlugin(t)
	cmd, handled := p.HandleAction(tui.Action("bogus"))
	if handled || cmd != nil {
		t.Errorf("HandleAction(bogus) = (%v, %v), want (nil, false)", cmd, handled)
	}
}

// --- Help modal ---

// TestHelpModal_TabsSectionAndReloadBinding asserts the ?-modal (via
// tui.BuildHelp) shows the Tabs section and ctrl+r reload, and does not show a
// standalone "r" binding — locking the legacy r→ctrl+r removal.
func TestHelpModal_TabsSectionAndReloadBinding(t *testing.T) {
	p := newActionTestPlugin(t)
	ov, err := tui.BuildHelp(p, i18n.NopTranslator{}, "en", 100, 40)
	if err != nil {
		t.Fatalf("BuildHelp: %v", err)
	}
	plain := ansi.Strip(ov.Content)

	for _, want := range []string{sectionTabs, "ctrl+r", "Previous tab", "Next tab", "Next table", "Previous table"} {
		if !strings.Contains(plain, want) {
			t.Errorf("help modal missing %q:\n%s", want, plain)
		}
	}
	if !strings.Contains(plain, "Reload") {
		t.Errorf("help modal missing reload description:\n%s", plain)
	}
	// The dead focus built-in must be gone on this single-panel surface.
	if strings.Contains(plain, "Focus next panel") {
		t.Errorf("help modal still shows the stripped focus built-in:\n%s", plain)
	}
}

func TestHandleAction_SectionJump(t *testing.T) {
	// Anchors at lines 0/5/10 (e.g. Apps/Tools/Infra); jumps land on the next or
	// previous anchor and clamp at the ends (no wrap).
	p := newActionTestPlugin(t)
	p.m.active = 0
	p.m.sectionAnchors = [][]int{{0, 5, 10}}
	// Size the viewport with content taller than its height so offsets 5/10 are
	// reachable (SetYOffset clamps to the max scroll offset).
	p.m.viewport.SetHeight(3)
	p.m.viewport.SetWidth(40)
	p.m.viewport.SetContent(strings.Repeat("line\n", 15))

	tests := []struct {
		name       string
		start      int
		action     tui.Action
		wantOffset int
	}{
		{"next from top", 0, actionSectionNext, 5},
		{"next from middle", 5, actionSectionNext, 10},
		{"next at last clamps", 10, actionSectionNext, 10},
		{"prev from last", 10, actionSectionPrev, 5},
		{"prev at top clamps", 0, actionSectionPrev, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p.m.viewport.SetYOffset(tt.start)
			cmd, handled := p.HandleAction(tt.action)
			if !handled || cmd != nil {
				t.Fatalf("HandleAction(%q) = (%v, %v), want (nil, true)", tt.action, cmd, handled)
			}
			if got := p.m.viewport.YOffset(); got != tt.wantOffset {
				t.Errorf("YOffset = %d, want %d", got, tt.wantOffset)
			}
		})
	}
}

func TestHandleAction_SectionJump_NoopWithoutAnchors(t *testing.T) {
	// A tab with fewer than two anchors has nothing to jump between.
	p := newActionTestPlugin(t)
	p.m.active = 0
	p.m.sectionAnchors = [][]int{nil}
	p.m.viewport.SetYOffset(0)

	for _, a := range []tui.Action{actionSectionNext, actionSectionPrev} {
		_, handled := p.HandleAction(a)
		if !handled {
			t.Errorf("HandleAction(%q) handled = false, want true", a)
		}
		if got := p.m.viewport.YOffset(); got != 0 {
			t.Errorf("YOffset = %d, want unchanged 0", got)
		}
	}
}
