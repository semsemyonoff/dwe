package cmdbrowser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// helpText builds the browser's ?-modal help overlay through the exported
// tui.BuildHelp harness and returns the ANSI-stripped rendered content.
func helpText(t *testing.T, b *browser, tr i18n.Translator, locale string) string {
	t.Helper()
	ov, err := tui.BuildHelp(b, tr, locale, 100, 40)
	if err != nil {
		t.Fatalf("BuildHelp error: %v", err)
	}
	return stripANSI(ov.Content)
}

// TestHelpModal_EnLocalizedStrings asserts the built-in en bundle resolves the
// tui.help.* namespace: the title, section labels, and per-action descriptions
// all come from translations/en.yml (added in this task), not just the in-code
// fallbacks. It also locks the per-mode action visibility: ModeRun shows the
// skip-confirm (`y`) and force-form (`e`) verbs; ModeEdit omits both.
func TestHelpModal_EnLocalizedStrings(t *testing.T) {
	store, err := i18n.Load("")
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}

	runOpts := DefaultOptions()
	run := helpText(t, newBrowser("pick", pluginTestItems(), runOpts), store, "en")

	// Title + section labels resolve from en.yml.
	for _, want := range []string{"Help", "Navigation", "Actions", "General"} {
		if !strings.Contains(run, want) {
			t.Errorf("ModeRun help missing %q\n%s", want, run)
		}
	}
	// Action descriptions resolve from en.yml.
	for _, want := range []string{"Filter", "Inspect", "Skip confirmation", "Edit parameters", "Toggle help", "Quit"} {
		if !strings.Contains(run, want) {
			t.Errorf("ModeRun help missing action %q\n%s", want, run)
		}
	}

	editOpts := DefaultOptions()
	editOpts.Mode = ModeEdit
	edit := helpText(t, newBrowser("pick", pluginTestItems(), editOpts), store, "en")

	// ModeRun-only verbs are not even registered in ModeEdit, so the help modal
	// must not list them.
	for _, absent := range []string{"Skip confirmation", "Edit parameters"} {
		if strings.Contains(edit, absent) {
			t.Errorf("ModeEdit help must omit %q\n%s", absent, edit)
		}
	}
	// The select binding keeps its mode-dependent fallback ("Edit") — it is
	// deliberately not keyed in en.yml, so the vars-browser relabel survives.
	if !strings.Contains(edit, "Edit") {
		t.Errorf("ModeEdit help missing the relabeled select verb (\"Edit\")\n%s", edit)
	}
}

// TestHelpModal_RuOverlay injects a ru bundle through a project i18n overlay (a
// temp workspace/i18n/ru.yml — NOT a committed repo file; the built-in bundle
// ships en only) and asserts the help modal resolves the ru strings for the ru
// locale. ModeEdit still omits the ModeRun-only verbs.
func TestHelpModal_RuOverlay(t *testing.T) {
	const ruYAML = `ui:
  tui.help.title: "Справка"
  tui.help.section.navigation: "Навигация"
  tui.help.section.actions: "Действия"
  tui.help.section.general: "Общее"
  tui.help.action.filter: "Фильтр"
  tui.help.action.inspect: "Просмотр"
  tui.help.action.cmd.skip-confirm: "Пропустить подтверждение"
  tui.help.action.cmd.force-form: "Изменить параметры"
`
	root := t.TempDir()
	dir := filepath.Join(root, "workspace", "i18n")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ru.yml"), []byte(ruYAML), 0o644); err != nil {
		t.Fatalf("write ru.yml: %v", err)
	}
	store, err := i18n.Load(root)
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}

	run := helpText(t, newBrowser("pick", pluginTestItems(), DefaultOptions()), store, "ru")
	for _, want := range []string{"Справка", "Навигация", "Действия", "Фильтр", "Пропустить подтверждение", "Изменить параметры"} {
		if !strings.Contains(run, want) {
			t.Errorf("ModeRun ru help missing %q\n%s", want, run)
		}
	}

	editOpts := DefaultOptions()
	editOpts.Mode = ModeEdit
	edit := helpText(t, newBrowser("pick", pluginTestItems(), editOpts), store, "ru")
	if strings.Contains(edit, "Пропустить подтверждение") || strings.Contains(edit, "Изменить параметры") {
		t.Errorf("ModeEdit ru help must omit the ModeRun-only verbs\n%s", edit)
	}
}
