package command

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"

	"github.com/spf13/cobra"
)

// multiLineDesc is what a YAML `|` block with a leading blank line and CRLF
// line endings decodes to: the summary must be "Run migrations", never the
// usage lines below it.
const multiLineDesc = "\r\n  Run migrations\r\nUsage:\r\n  dwe cmd db.migrate --set step=1\r\n"

const (
	ruSummary      = "Выполнить миграции"
	ruGroupSummary = "Работа с базой"
)

// ruMultiLineStore loads an i18n store whose Russian overrides are themselves
// multi-line, so the summary is proven to be taken AFTER translation.
func ruMultiLineStore(t *testing.T) *i18n.Store {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "workspace", "i18n")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ru := "commands:\n" +
		"  db.migrate:\n" +
		"    description: |\n\n" +
		"      " + ruSummary + "\n" +
		"      Пример: dwe cmd db.migrate\n" +
		"groups:\n" +
		"  db:\n" +
		"    description: |\n" +
		"      " + ruGroupSummary + "\n" +
		"      Вторая строка\n"
	if err := os.WriteFile(filepath.Join(dir, "ru.yml"), []byte(ru), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := i18n.Load(root)
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	if got := store.CommandDescription("ru", "db.migrate", ""); !strings.Contains(got, "\n") {
		t.Fatalf("fixture: ru description should be multi-line, got %q", got)
	}
	return store
}

type summaryCase struct {
	name   string
	tr     i18n.Translator
	locale string
	want   string // command summary
	group  string // group summary
}

func summaryCases(t *testing.T) []summaryCase {
	return []summaryCase{
		{name: "authored", tr: i18n.NopTranslator{}, want: "Run migrations", group: "Database tasks"},
		{name: "translated", tr: ruMultiLineStore(t), locale: "ru", want: ruSummary, group: ruGroupSummary},
	}
}

func migrateDef() *usercommands.CommandDef {
	return &usercommands.CommandDef{
		ID:          "db.migrate",
		Group:       "db",
		LocalName:   "migrate",
		Type:        usercommands.CommandTypeShell,
		Description: multiLineDesc,
		Cmd:         "true",
	}
}

func assertOneLine(t *testing.T, surface, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("%s: missing summary %q in %q", surface, want, got)
	}
	for _, leak := range []string{"Usage:", "dwe cmd db.migrate", "Пример", "Вторая строка", "\r"} {
		if strings.Contains(got, leak) {
			t.Errorf("%s: leaks %q past the first line: %q", surface, leak, got)
		}
	}
}

func TestSummarySurfaces_ShowFirstLineOnly(t *testing.T) {
	for _, tc := range summaryCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			def := migrateDef()

			var banner bytes.Buffer
			printRunHeader(&banner, def, tc.tr, tc.locale)
			assertOneLine(t, "run banner", banner.String(), tc.want)
			if n := strings.Count(banner.String(), "\n"); n != 1 {
				t.Errorf("run banner: want exactly one line, got %d: %q", n, banner.String())
			}

			node := commandDefToTreeNode(def, tc.tr, tc.locale)
			if node.Desc != tc.want {
				t.Errorf("tree command: Desc = %q, want %q", node.Desc, tc.want)
			}

			gn := &usercommands.GroupNode{
				ID:       "db",
				Name:     "db",
				Meta:     usercommands.GroupMeta{Description: "Database tasks\nSecond line"},
				Commands: []*usercommands.CommandDef{def},
			}
			if g := groupNodeToSingleNode(gn, false, tc.tr, tc.locale); g == nil || g.Desc != tc.group {
				t.Errorf("tree group: got %+v, want Desc %q", g, tc.group)
			}

			entry := commandCompletionEntry(def, tc.tr, tc.locale)
			if want := cobra.CompletionWithDesc("db.migrate", tc.want); entry != want {
				t.Errorf("completion: got %q, want %q", entry, want)
			}

			reg := usercommands.NewEmptyRegistry()
			reg.AddCommandForTest(def)
			if got, want := inspectStepDescription(reg, tc.tr, tc.locale, "db.migrate"), " — "+tc.want; got != want {
				t.Errorf("inspect step reference: got %q, want %q", got, want)
			}

			item := commandBrowserItem(def, tc.tr, tc.locale, nil)
			if item.Summary != tc.want {
				t.Errorf("browser item: Summary = %q, want %q", item.Summary, tc.want)
			}
			if !strings.Contains(item.Description, "\n") {
				t.Errorf("browser item: Description must stay full (filter haystack), got %q", item.Description)
			}
		})
	}
}

// TestCompletionEntry_TabInSummary: a tab separates value from description in
// cobra's completion protocol, so one inside the summary must not survive.
func TestCompletionEntry_TabInSummary(t *testing.T) {
	def := &usercommands.CommandDef{ID: "x", Description: "Run\tthis\nsecond"}
	got := commandCompletionEntry(def, i18n.NopTranslator{}, "")
	if want := "x\tRun this"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSummaryJSON_FullDescriptionPlusSummary(t *testing.T) {
	for _, tc := range summaryCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			def := migrateDef()
			full := tc.tr.CommandDescription(tc.locale, def.ID, def.Description)

			for name, v := range map[string]any{
				"list":    commandDefToEntryJSON(def, tc.tr, tc.locale),
				"inspect": buildCommandInspectJSON(def, tc.tr, tc.locale),
			} {
				b, err := json.Marshal(v)
				if err != nil {
					t.Fatal(err)
				}
				var got struct {
					Description string `json:"description"`
					Summary     string `json:"summary"`
				}
				if err := json.Unmarshal(b, &got); err != nil {
					t.Fatal(err)
				}
				if got.Description != full {
					t.Errorf("%s: description = %q, want the full text %q", name, got.Description, full)
				}
				if got.Summary != tc.want {
					t.Errorf("%s: summary = %q, want %q", name, got.Summary, tc.want)
				}
			}
		})
	}
}

// TestInspect_KeepsFullDescription: inspect is the long form.
func TestInspect_KeepsFullDescription(t *testing.T) {
	var buf bytes.Buffer
	printInspectAt(&buf, migrateDef(), nil, nil, 200, i18n.NopTranslator{}, "", "")
	if !strings.Contains(buf.String(), "Usage:") {
		t.Errorf("inspect must show the full description, got:\n%s", buf.String())
	}
}
