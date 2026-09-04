package render

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// goldenSecretsStatus is the representative report: one readable marker, one
// encrypted to a recipient this machine has no key for, one damaged payload,
// plus a readable and an unreadable *.age pack source.
func goldenSecretsStatus() SecretsStatusView {
	return SecretsStatusView{
		Recipient: "age1qyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqs3fgh2p",
		Identity:  "keyfile (/home/dev/.config/dwe/keys/age1qyqs….key)",
		Markers: []SecretsMarkerRow{
			{Layer: "workspace/defaults.yml", Path: "vars.telegram.token", State: "decrypted", OK: true},
			{Layer: "workspace/defaults.yml", Path: "vars.google.service_account", State: "unresolved", Reason: "wrong_identity"},
			{Layer: "workspace/local.yml", Path: "vars.db.password", State: "unresolved", Reason: "corrupt"},
		},
		Files: []SecretsFileRow{
			{File: "workspace/templates/config/app/google-credentials.json.age", State: "decryptable", OK: true},
			{File: "workspace/templates/config/app/legacy.env.age", State: "not decryptable", Reason: "wrong_identity"},
		},
	}
}

// goldenSecretsStatusKeyless is the new-developer state: the recipient is
// committed, no identity is installed, and every marker fails for the one
// actionable reason — so the report closes with the fix.
func goldenSecretsStatusKeyless() SecretsStatusView {
	return SecretsStatusView{
		Recipient:    "age1qyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqs3fgh2p",
		Identity:     "none (looked at /home/dev/.config/dwe/keys/age1qyqs….key, $DWE_AGE_KEY, $DWE_AGE_KEY_FILE)",
		IdentityHint: "run 'dwe secrets key import' to store the identity at /home/dev/.config/dwe/keys/age1qyqs….key, or set DWE_AGE_KEY / DWE_AGE_KEY_FILE",
		Markers: []SecretsMarkerRow{
			{Layer: "workspace/defaults.yml", Path: "vars.telegram.token", State: "unresolved", Reason: "no_identity"},
		},
		Files: []SecretsFileRow{
			{File: "workspace/templates/config/app/creds.json.age", State: "not decryptable", Reason: "no_identity"},
		},
	}
}

// goldenSecretsStatusInvalid is the set-but-broken source: the header names the
// variable to repair rather than a key to obtain.
func goldenSecretsStatusInvalid() SecretsStatusView {
	v := goldenSecretsStatusKeyless()
	v.Identity = "invalid ($DWE_AGE_KEY is set but holds no age identity)"
	v.Markers[0].Reason = "invalid_identity"
	v.Files[0].Reason = "invalid_identity"
	return v
}

// goldenSecretsStatusWrong is the foreign-key state: a readable keyfile that
// opens nothing here, with both recipients on the header line.
func goldenSecretsStatusWrong() SecretsStatusView {
	v := goldenSecretsStatusKeyless()
	v.Identity = "wrong recipient (keyfile /home/dev/.config/dwe/keys/age1qyqs….key holds the identity for age1other, but the project uses " + v.Recipient + ")"
	v.Markers[0].Reason = "wrong_identity"
	v.Files[0].Reason = "wrong_identity"
	return v
}

// goldenSecretsStatusEmpty is a project that uses no secrets at all — the
// overwhelmingly common case, which must still read as a finished report.
func goldenSecretsStatusEmpty() SecretsStatusView {
	return SecretsStatusView{Identity: "none"}
}

func TestGolden_SecretsStatus(t *testing.T) {
	pinGoldenPalette(t)
	assertGolden(t, "secrets_status.golden", SecretsStatus(goldenSecretsStatus()))
}

func TestGolden_SecretsStatusKeyless(t *testing.T) {
	pinGoldenPalette(t)
	assertGolden(t, "secrets_status_keyless.golden", SecretsStatus(goldenSecretsStatusKeyless()))
}

func TestGolden_SecretsStatusEmpty(t *testing.T) {
	pinGoldenPalette(t)
	assertGolden(t, "secrets_status_empty.golden", SecretsStatus(goldenSecretsStatusEmpty()))
}

func TestGolden_SecretsStatusInvalid(t *testing.T) {
	pinGoldenPalette(t)
	assertGolden(t, "secrets_status_invalid.golden", SecretsStatus(goldenSecretsStatusInvalid()))
}

func TestGolden_SecretsStatusWrong(t *testing.T) {
	pinGoldenPalette(t)
	assertGolden(t, "secrets_status_wrong.golden", SecretsStatus(goldenSecretsStatusWrong()))
}

// TestSecretsStatus_HintClosesTheReport pins where the fix instruction lands:
// last, after the inventory it applies to — and only when the view carries one,
// so a healthy project's report is byte-identical to before.
func TestSecretsStatus_HintClosesTheReport(t *testing.T) {
	pinGoldenPalette(t)
	v := goldenSecretsStatusKeyless()
	got := stripANSI(SecretsStatusAt(v, 0))
	if !strings.HasSuffix(got, v.IdentityHint) {
		t.Errorf("report does not end with the hint:\n%s", got)
	}

	v.IdentityHint = ""
	if bare := stripANSI(SecretsStatusAt(v, 0)); strings.Contains(bare, "dwe secrets key import") {
		t.Errorf("a hintless view still renders a hint:\n%s", bare)
	}
}

// TestSecretsStatus_StableAcrossRuns pins that two consecutive renders of the
// same view are byte-identical: every row comes from an ordered slice, so a map
// walk sneaking into the renderer would show up here rather than as an
// intermittent golden failure.
func TestSecretsStatus_StableAcrossRuns(t *testing.T) {
	pinGoldenPalette(t)
	v := goldenSecretsStatus()
	first := SecretsStatus(v)
	if second := SecretsStatus(v); first != second {
		t.Error("SecretsStatus output not stable across two consecutive runs")
	}
	if !strings.ContainsRune(first, 0x1b) {
		t.Errorf("expected ANSI escapes under a pinned TrueColor profile, got none:\n%s", first)
	}
}

// TestSecretsStatus_RowOrderPreserved pins that the renderer emits rows in the
// order the inventory supplied them (which collectInventory sorts), rather than
// re-ordering by state or grouping.
func TestSecretsStatus_RowOrderPreserved(t *testing.T) {
	resetStyles()
	got := stripANSI(SecretsStatusAt(goldenSecretsStatus(), 0))

	wantOrder := []string{
		"vars.telegram.token",
		"vars.google.service_account",
		"vars.db.password",
		"google-credentials.json.age",
		"legacy.env.age",
	}
	pos := -1
	for _, want := range wantOrder {
		at := strings.Index(got, want)
		if at < 0 {
			t.Fatalf("row %q missing from output:\n%s", want, got)
		}
		if at < pos {
			t.Errorf("row %q rendered out of order:\n%s", want, got)
		}
		pos = at
	}
}

// TestSecretsStatus_StateCellCarriesReason pins the "state: reason" join — the
// reason is the only actionable half of an unresolved row.
func TestSecretsStatus_StateCellCarriesReason(t *testing.T) {
	resetStyles()
	got := stripANSI(SecretsStatusAt(goldenSecretsStatus(), 0))
	for _, want := range []string{"unresolved: wrong_identity", "unresolved: corrupt", "not decryptable: wrong_identity"} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q:\n%s", want, got)
		}
	}
	// A readable row has no reason, so nothing may be appended to it.
	if strings.Contains(got, "decrypted:") {
		t.Errorf("a decrypted row must render its state bare, got:\n%s", got)
	}
}

// TestSecretsStatus_EmptyProjectSaysSo pins that a project with nothing
// encrypted still gets a closing sentence instead of two header lines and
// silence.
func TestSecretsStatus_EmptyProjectSaysSo(t *testing.T) {
	resetStyles()
	got := stripANSI(SecretsStatusAt(goldenSecretsStatusEmpty(), 0))
	if !strings.Contains(got, secretsNoneNote) {
		t.Errorf("output is missing the no-secrets note:\n%s", got)
	}
	if !strings.Contains(got, secretsNoRecipient) {
		t.Errorf("an unset recipient must be spelled out, got:\n%s", got)
	}
	if strings.Contains(got, "Markers") || strings.Contains(got, "Encrypted files") {
		t.Errorf("empty project rendered a table heading:\n%s", got)
	}
}

// TestSecretsStatus_NoTrailingNewline pins the WriteData contract: the CLI
// appends the final newline, so the renderer must not.
func TestSecretsStatus_NoTrailingNewline(t *testing.T) {
	resetStyles()
	for name, out := range map[string]string{
		"full":  SecretsStatusAt(goldenSecretsStatus(), 0),
		"empty": SecretsStatusAt(goldenSecretsStatusEmpty(), 0),
	} {
		if strings.HasSuffix(out, "\n") {
			t.Errorf("%s: output ends with a newline: %q", name, out)
		}
	}
}

// TestSecretsStatus_DelegatesProbedWidth pins that SecretsStatus hands the
// width it probes to SecretsStatusAt. TestMain pins the probe to 0, so the
// golden tests alone cannot catch a dropped argument.
func TestSecretsStatus_DelegatesProbedWidth(t *testing.T) {
	resetStyles()
	v := goldenSecretsStatus()
	withTermWidth(t, 60)
	if got, want := SecretsStatus(v), SecretsStatusAt(v, 60); got != want {
		t.Errorf("SecretsStatus at a probed width of 60 diverged from SecretsStatusAt(v, 60):\ngot:  %q\nwant: %q", got, want)
	}
}

// tableBlockLines returns every rendered line except the two-line header
// block. The header is excluded on purpose: an age recipient and a keyfile path
// are single unbreakable tokens rendered whole (see secretsField), so they are
// the one part of the report a width budget cannot bound.
func tableBlockLines(out string) []string {
	blocks := strings.Split(out, "\n\n")
	if len(blocks) < 2 {
		return nil
	}
	return strings.Split(strings.Join(blocks[1:], "\n\n"), "\n")
}

// assertWithinBudget pins that every table line fits the budget.
func assertWithinBudget(t *testing.T, out string, budget int) {
	t.Helper()
	lines := tableBlockLines(out)
	if len(lines) == 0 {
		t.Fatalf("no table blocks rendered:\n%s", stripANSI(out))
	}
	for _, line := range lines {
		if w := lipgloss.Width(line); w > budget {
			t.Errorf("line overflows the %d-column budget at %d: %q", budget, w, stripANSI(line))
		}
	}
}

// TestSecretsStatusAt_ShrinksBeforeRecords pins the middle degradation step: at
// a width that is tight but still above the column floors the report shrinks
// and wraps rather than dropping to records.
func TestSecretsStatusAt_ShrinksBeforeRecords(t *testing.T) {
	resetStyles()
	for _, budget := range []int{110, 60} {
		out := SecretsStatusAt(goldenSecretsStatus(), budget)
		if !isTableMode(out) {
			t.Errorf("expected table mode at width %d, got records:\n%s", budget, stripANSI(out))
		}
		assertWithinBudget(t, out, budget)
	}
}

// TestSecretsStatusAt_NarrowWidthDegradesToRecords pins the last degradation
// step: below the column floors the tables fall back to the record layout, and
// still no line overflows.
func TestSecretsStatusAt_NarrowWidthDegradesToRecords(t *testing.T) {
	resetStyles()
	const budget = 30
	out := SecretsStatusAt(goldenSecretsStatus(), budget)

	if isTableMode(out) {
		t.Errorf("expected record mode at width %d, got a table:\n%s", budget, stripANSI(out))
	}
	assertWithinBudget(t, out, budget)

	// Record mode must not lose the identifying path: it is hard-split at the
	// budget, so it reassembles once the wrap newlines and indentation go.
	flat := strings.NewReplacer("\n", "", " ", "").Replace(stripANSI(out))
	if !strings.Contains(flat, "workspace/templates/config/app/legacy.env.age") {
		t.Errorf("narrow rendering dropped a file path:\n%s", stripANSI(out))
	}
}

// TestSecretsStatus_StateStyling pins that readability drives the state color,
// and that the styler never reaches outside its own column or row range (the
// table calls it for the header row with a negative index).
func TestSecretsStatus_StateStyling(t *testing.T) {
	resetStyles()
	style := secretsStateStyle([]bool{true, false}, 2)

	if got := style(0, 2).GetForeground(); got == (lipgloss.NoColor{}) {
		t.Error("a readable state cell got no color")
	}
	if style(0, 2).GetForeground() == style(1, 2).GetForeground() {
		t.Error("readable and unresolved state cells render in the same color")
	}
	for _, tc := range []struct{ row, col int }{{0, 0}, {0, 1}, {-1, 2}, {5, 2}} {
		if got := style(tc.row, tc.col).GetForeground(); got != (lipgloss.NoColor{}) {
			t.Errorf("style(%d, %d) = %v, want unstyled", tc.row, tc.col, got)
		}
	}
}

// goldenSecretsKeyList is the representative keys directory: the project's own
// identity, a foreign one, and the three broken shapes.
func goldenSecretsKeyList() SecretsKeyListView {
	return SecretsKeyListView{
		Dir: "/home/dev/.config/dwe/keys",
		Keys: []SecretsKeyRow{
			{Recipient: "age1broken", File: "age1broken.key", State: "unparsable"},
			{Recipient: "age1current", File: "age1current.key", State: "ok", Current: true, OK: true},
			{Recipient: "age1locked", File: "age1locked.key", State: "unreadable"},
			{Recipient: "age1other", File: "age1other.key", State: "ok", OK: true},
			{Recipient: "age1parsed", File: "age1stale.key", State: "misnamed"},
		},
	}
}

func TestGolden_SecretsKeyList(t *testing.T) {
	pinGoldenPalette(t)
	assertGolden(t, "secrets_key_list.golden", SecretsKeyList(goldenSecretsKeyList()))
}

// TestSecretsKeyList_EmptyNamesTheDirectory pins that an empty keys directory
// still reads as a finished report, and names where identities would live.
func TestSecretsKeyList_EmptyNamesTheDirectory(t *testing.T) {
	resetStyles()
	got := stripANSI(SecretsKeyListAt(SecretsKeyListView{Dir: "/home/dev/.config/dwe/keys"}, 0))
	if want := "No identities in /home/dev/.config/dwe/keys."; got != want {
		t.Errorf("empty listing = %q, want %q", got, want)
	}
}

// TestSecretsKeyList_MarksTheCurrentProject pins the one qualifier the table
// carries, and that it marks exactly one row.
func TestSecretsKeyList_MarksTheCurrentProject(t *testing.T) {
	resetStyles()
	got := stripANSI(SecretsKeyListAt(goldenSecretsKeyList(), 0))
	if n := strings.Count(got, "current project"); n != 1 {
		t.Errorf("current-project marker appears %d times, want 1:\n%s", n, got)
	}
	if !strings.Contains(got, "ok (current project)") {
		t.Errorf("the marker is not attached to its state:\n%s", got)
	}
}

// TestSecretsKeyList_RowOrderPreserved pins that the renderer emits rows in the
// order ListKeyfiles supplied them (sorted by filename), rather than grouping
// by state or floating the current project to the top.
func TestSecretsKeyList_RowOrderPreserved(t *testing.T) {
	resetStyles()
	got := stripANSI(SecretsKeyListAt(goldenSecretsKeyList(), 0))
	pos := -1
	for _, want := range []string{"age1broken.key", "age1current.key", "age1locked.key", "age1other.key", "age1stale.key"} {
		at := strings.Index(got, want)
		if at < 0 {
			t.Fatalf("row %q missing from output:\n%s", want, got)
		}
		if at < pos {
			t.Errorf("row %q rendered out of order:\n%s", want, got)
		}
		pos = at
	}
}

// TestSecretsKeyListAt_DegradesWithinBudget pins the responsive contract: the
// table shrinks and finally drops to records, and no line overflows either way.
func TestSecretsKeyListAt_DegradesWithinBudget(t *testing.T) {
	resetStyles()
	for _, budget := range []int{80, 50, 24} {
		out := SecretsKeyListAt(goldenSecretsKeyList(), budget)
		assertWithinBudget(t, out, budget)
	}
}

// TestSecretsKeyList_DelegatesProbedWidth pins that SecretsKeyList hands the
// width it probes to SecretsKeyListAt (TestMain pins the probe to 0, so the
// golden alone cannot catch a dropped argument).
func TestSecretsKeyList_DelegatesProbedWidth(t *testing.T) {
	resetStyles()
	v := goldenSecretsKeyList()
	withTermWidth(t, 60)
	if got, want := SecretsKeyList(v), SecretsKeyListAt(v, 60); got != want {
		t.Errorf("SecretsKeyList at a probed width of 60 diverged from SecretsKeyListAt(v, 60)")
	}
}

// TestSecretsKeyList_NoTrailingNewline pins the WriteData contract for both
// shapes of the listing.
func TestSecretsKeyList_NoTrailingNewline(t *testing.T) {
	resetStyles()
	for name, out := range map[string]string{
		"rows":  SecretsKeyListAt(goldenSecretsKeyList(), 0),
		"empty": SecretsKeyListAt(SecretsKeyListView{Dir: "/keys"}, 0),
	} {
		if strings.HasSuffix(out, "\n") {
			t.Errorf("%s: output ends with a newline: %q", name, out)
		}
	}
}

// TestSecretsStatus_NeverPrintsAKey is the negative pin required by the plan's
// testing strategy: no surface of the report may carry key material.
func TestSecretsStatus_NeverPrintsAKey(t *testing.T) {
	resetStyles()
	v := goldenSecretsStatus()
	out := stripANSI(SecretsStatusAt(v, 0)) + stripANSI(SecretsStatusAt(v, 40))
	for _, forbidden := range []string{"AGE-SECRET-KEY-", "ENC[age:"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("output contains %q:\n%s", forbidden, out)
		}
	}
}
