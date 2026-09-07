package render

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
)

// SecretsMarkerRow is one committed ENC[age:…] scalar as `dwe secrets status`
// reports it: where it lives and whether this machine can open it. State and
// Reason carry the CLI's stable vocabulary verbatim; OK is the only thing the
// renderer interprets, so the state strings can grow without touching styling.
type SecretsMarkerRow struct {
	Layer  string // layer file, relative to the project root
	Path   string // dot-path inside that layer
	State  string
	Reason string
	// ShadowedBy is the layer file whose plaintext value at the same path wins
	// the merge, appended to the STATE cell; "" when the marker is what the
	// project uses. ShadowIdentical marks the leftover-copy case — a flag rather
	// than the CLI's verdict token, for the same reason OK is a flag: the
	// vocabulary stays the CLI's and can grow without touching the renderer.
	ShadowedBy      string
	ShadowIdentical bool
	OK              bool
}

// SecretsFileRow is one native *.age config-pack source.
type SecretsFileRow struct {
	File   string // relative to the project root
	State  string
	Reason string
	Detail string // free-form cause behind a token reason; appended to the cell
	OK     bool
}

// SecretsStatusView is the whole encrypted surface of a project. Identity is
// already display-ready ("keyfile (/home/u/.config/dwe/keys/age1….key)",
// "$DWE_AGE_KEY", "none — …"): resolving it needs the keys-directory lookup,
// which belongs to the CLI, not to a pure formatter.
type SecretsStatusView struct {
	Recipient string
	Identity  string
	// IdentityHint is the fix instruction for an identity that did not load —
	// the same sentence the validator prints. Empty when the identity is
	// usable, and then no trailing line is rendered at all.
	IdentityHint string
	Markers      []SecretsMarkerRow
	Files        []SecretsFileRow
}

// SecretsKeyRow is one *.key file in the keys directory as `dwe secrets key
// list` reports it. State carries the CLI's fixed vocabulary verbatim ("ok",
// "unreadable", "unparsable", "misnamed"); OK is the only thing the renderer
// interprets, so the states can grow without touching styling.
type SecretsKeyRow struct {
	Recipient string
	File      string // file name inside the keys directory
	State     string
	Current   bool // the recipient this project uses
	OK        bool
}

// SecretsKeyListView is the keys directory as a whole. Dir is display-ready:
// resolving the home directory belongs to the CLI, not to a pure formatter.
type SecretsKeyListView struct {
	Dir  string
	Keys []SecretsKeyRow
}

// secretsNoRecipient is shown in place of an unset secrets.recipient — the
// project has no key pair yet, which is a normal state, not a failure.
const secretsNoRecipient = "not set (run `dwe secrets init`)"

// secretsNoneNote closes the report for a project that carries nothing
// encrypted at all, so the command never renders as two lines and a silence.
const secretsNoneNote = "No encrypted secrets in this project."

// secretsLabelWidth pads the two header labels so their separators line up.
const secretsLabelWidth = 10

// SecretsStatus renders the secrets report at the stdout width budget.
func SecretsStatus(v SecretsStatusView) string {
	return SecretsStatusAt(v, stdoutBudget())
}

// SecretsStatusAt is SecretsStatus at an explicit width budget (0 =
// unbounded). Both tables degrade through the shared tableView path
// (shrink → wrap → records), so a narrow terminal never overflows. The result
// carries no trailing newline — the CLI's WriteData adds it.
func SecretsStatusAt(v SecretsStatusView, width int) string {
	recipient := v.Recipient
	if recipient == "" {
		recipient = secretsNoRecipient
	}
	blocks := []string{
		secretsField("Recipient", recipient) + "\n" + secretsField("Identity", v.Identity),
	}

	switch {
	case len(v.Markers) == 0 && len(v.Files) == 0:
		blocks = append(blocks, styles.MutedStyle().Render(secretsNoneNote))
	default:
		if len(v.Markers) > 0 {
			blocks = append(blocks, styles.StyleSubheader(fmt.Sprintf("Markers (%d):", len(v.Markers)))+
				"\n"+secretsMarkerTable(v.Markers).Render(width))
		}
		if len(v.Files) > 0 {
			blocks = append(blocks, styles.StyleSubheader(fmt.Sprintf("Encrypted files (%d):", len(v.Files)))+
				"\n"+secretsFileTable(v.Files).Render(width))
		}
	}
	// The shadow note sits above the identity hint: it explains a qualifier the
	// table above it carries, while the hint answers a header line.
	if note := secretsShadowNote(v.Markers); note != "" {
		blocks = append(blocks, note)
	}
	// The fix instruction closes the report rather than sitting next to the
	// Identity line: it applies to every unresolved row below it, and a header
	// that grows a second line pushes the inventory off a short screen.
	if v.IdentityHint != "" {
		blocks = append(blocks, styles.MutedStyle().Render(v.IdentityHint))
	}
	return strings.Join(blocks, "\n\n")
}

// SecretsKeyList renders the keyfile inventory at the stdout width budget.
func SecretsKeyList(v SecretsKeyListView) string {
	return SecretsKeyListAt(v, stdoutBudget())
}

// SecretsKeyListAt is SecretsKeyList at an explicit width budget (0 =
// unbounded). The table degrades through the shared tableView path, and the
// result carries no trailing newline — the CLI's WriteData adds it.
func SecretsKeyListAt(v SecretsKeyListView, width int) string {
	if len(v.Keys) == 0 {
		return styles.MutedStyle().Render("No identities in " + v.Dir + ".")
	}
	blocks := []string{
		secretsField("Directory", v.Dir),
		secretsKeyTable(v.Keys).Render(width),
	}
	return strings.Join(blocks, "\n\n")
}

// secretsKeyTable builds the keyfile table. RECIPIENT is the record-mode title:
// it is what `dwe secrets key remove` takes, and the file name is derived from
// it for every well-named file.
func secretsKeyTable(rows []SecretsKeyRow) tableView {
	stringRows := make([][]string, len(rows))
	ok := make([]bool, len(rows))
	for i, r := range rows {
		stringRows[i] = []string{r.Recipient, r.File, secretsKeyStateCell(r)}
		ok[i] = r.OK
	}
	return tableView{
		Headers: []string{"RECIPIENT", "FILE", "STATE"},
		Rows:    stringRows,
		Cols: []columnSpec{
			{Role: roleTitle, Flex: true, Wrap: wrapPath},
			{Flex: true, Wrap: wrapPath},
			secretsStateCol,
		},
		Style: secretsStateStyle(ok, 2),
	}
}

// secretsKeyStateCell marks the project's own identity inside the STATE cell.
// A separate column would be a mostly-empty one; the qualifier belongs to the
// state it modifies ("ok, and it is the one this project needs").
func secretsKeyStateCell(r SecretsKeyRow) string {
	if r.Current {
		return r.State + " (current project)"
	}
	return r.State
}

// secretsField renders one "  Label   — value" header line, matching the
// layout `dwe vars inspect` uses for its per-layer block.
//
// Deliberately unwrapped: both values (an age recipient, a keyfile path) are
// single unbreakable tokens the user copies, and hard-splitting one across
// lines to honour a narrow budget would only make it uncopyable. The tables
// below carry the responsive behaviour.
func secretsField(label, value string) string {
	pad := strings.Repeat(" ", max(secretsLabelWidth-len(label), 0))
	return "  " + styles.AccentStyle().Render(label) + pad +
		styles.MutedStyle().Render(styles.DefSep) + " " +
		styles.TextStyle().Render(value)
}

// secretsNotes renders the closing advice of a write report: muted, one line
// each, so the field block above stays the eye's first stop. Empty lines are
// dropped, which lets a caller pass a conditional note unguarded.
func secretsNotes(lines ...string) string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if l == "" {
			continue
		}
		out = append(out, styles.MutedStyle().Render(l))
	}
	return strings.Join(out, "\n")
}

// SecretsOrphanRow is one encrypted value a `--replace-recipient` run left
// unreadable. Name is what the developer feeds back to `dwe secrets set` (a
// dot-path) or `dwe secrets encrypt` (a file), and Where is the layer file
// holding it — empty for a `*.age` source, whose Name already IS its path.
type SecretsOrphanRow struct {
	Name  string
	Where string
}

// SecretsInitView is the outcome of `dwe secrets init`. OldRecipient is set
// only by `--replace-recipient`, and is what turns the report from "created"
// into "replaced"; Orphans is the to-do list that run handed back.
type SecretsInitView struct {
	Recipient    string
	Keyfile      string
	OldRecipient string
	Orphans      []SecretsOrphanRow
}

// SecretsInit renders the `dwe secrets init` report. The result carries no
// trailing newline — the CLI's WriteData adds it (same for every renderer
// below).
func SecretsInit(v SecretsInitView) string {
	head := "age key pair created"
	fields := []string{secretsField("Recipient", v.Recipient)}
	if v.OldRecipient != "" {
		head = "age key pair replaced"
		fields = append(fields, secretsField("Previous", v.OldRecipient))
	}
	fields = append(fields, secretsField("Keyfile", v.Keyfile))

	blocks := []string{styles.StyleSubheader(head) + "\n" + strings.Join(fields, "\n")}
	if len(v.Orphans) > 0 {
		blocks = append(blocks, secretsOrphanBlock(v.Orphans))
	}
	blocks = append(blocks, secretsNotes(
		"secrets.recipient was written to workspace.yml — commit it.",
		"Back up the keyfile: it is the only way to read this project's secrets.",
	))
	return strings.Join(blocks, "\n\n")
}

// secretsOrphanBlock lists what a `--replace-recipient` run made unreadable.
// Amber and phrased as a to-do rather than red damage: the run was asked for,
// and every row is a value the developer re-enters from its own plaintext —
// until they do, `secrets.unresolved` keeps the lifecycle commands stopped.
//
// A plain list rather than a table: the rows have no second dimension worth
// aligning, and each Name is a token the developer copies into the next command.
func secretsOrphanBlock(rows []SecretsOrphanRow) string {
	var b strings.Builder
	b.WriteString(styles.WarningStyle().Render(
		fmt.Sprintf("%d encrypted value(s) can no longer be read and must be re-entered:", len(rows))))
	for _, r := range rows {
		b.WriteString("\n  " + styles.TextStyle().Render(r.Name))
		if r.Where != "" {
			b.WriteString(" " + styles.MutedStyle().Render("("+r.Where+")"))
		}
	}
	b.WriteString("\n\n" + secretsNotes(
		"Re-enter a marker with `dwe secrets set <path>`, a file with `dwe secrets encrypt <file>`."))
	return b.String()
}

// SecretsRekeyView is the outcome of `dwe secrets rekey`: the retired and the
// new recipient, where the new identity landed, and what was rewritten.
type SecretsRekeyView struct {
	Recipient    string
	OldRecipient string
	Keyfile      string
	Markers      int
	Layers       int
	Files        int
}

// SecretsRekey renders the `dwe secrets rekey` report.
func SecretsRekey(v SecretsRekeyView) string {
	fields := strings.Join([]string{
		secretsField("Recipient", v.Recipient),
		secretsField("Previous", v.OldRecipient),
		secretsField("Keyfile", v.Keyfile),
		secretsField("Rewritten", fmt.Sprintf("%d marker(s) in %d layer file(s), %d encrypted file(s)",
			v.Markers, v.Layers, v.Files)),
	}, "\n")
	return styles.StyleSubheader("re-encrypted to a new age key pair") + "\n" + fields + "\n\n" +
		secretsNotes(
			"Commit the rewritten files and the new secrets.recipient.",
			"Share the new identity with `dwe secrets key export`, then remove the old keyfile",
			"for "+v.OldRecipient+" once everyone has imported it.",
		)
}

// SecretsKeyImportView is the outcome of `dwe secrets key import`. Markers and
// Files count what the imported identity opened; ReportErr is set instead when
// the readability scan could not run, which is never the import's own outcome.
type SecretsKeyImportView struct {
	Recipient string
	Keyfile   string
	Markers   int
	Files     int
	ReportErr string
}

// SecretsKeyImport renders the `dwe secrets key import` report. A failed scan
// is reported amber rather than green: the key IS stored, but the line that
// would tell the developer whether it was the right one is missing.
func SecretsKeyImport(v SecretsKeyImportView) string {
	head := styles.StyleSubheader("identity stored") + "\n" +
		secretsField("Recipient", v.Recipient) + "\n" +
		secretsField("Keyfile", v.Keyfile)
	if v.ReportErr != "" {
		return head + "\n\n" + styles.WarningStyle().Render(
			"the readability report could not be built: "+v.ReportErr)
	}
	return head + "\n\n" + styles.SuccessStyle().Render(
		fmt.Sprintf("%d encrypted value(s) and %d .age file(s) are now readable.", v.Markers, v.Files))
}

// SecretsSetResult reports one value encrypted into a layer file.
func SecretsSetResult(path, file string) string {
	return styles.AccentStyle().Render(path) +
		styles.TextStyle().Render(" encrypted into ") +
		styles.AccentStyle().Render(file) +
		styles.MutedStyle().Render(" — commit it.")
}

// SecretsFileResult reports an encrypt/decrypt that landed on disk. verb is
// "encrypted" or "decrypted" and note is the mode qualifier the decrypt path
// adds; the two paths share one renderer so they cannot drift apart.
func SecretsFileResult(verb, from, to, note string) string {
	out := styles.AccentStyle().Render(from) +
		styles.TextStyle().Render(" "+verb+" → ") +
		styles.AccentStyle().Render(to)
	if note != "" {
		out += " " + styles.MutedStyle().Render(note)
	}
	return out
}

// SecretsKeyRemoved reports a deleted keyfile.
func SecretsKeyRemoved(keyfile string) string {
	return styles.TextStyle().Render("removed ") + styles.AccentStyle().Render(keyfile)
}

// secretsStateCol is the STATE column of both tables: wrappable, but
// deliberately NOT Flex. A non-flex column holds its natural width in the fit
// decision, which is what makes the report drop to the record layout at a width
// where the path columns would otherwise be shredded down to their headers —
// while the Wrap still lets record mode fold a long "state: reason" cell
// instead of overflowing.
var secretsStateCol = columnSpec{Wrap: wrapText}

// secretsMarkerTable builds the marker table. PATH is the record-mode title
// (it is what identifies the row); LAYER is the compressible column, since a
// nested `workspace/services/x/…` path is the widest cell and the least
// surprising thing to wrap.
func secretsMarkerTable(rows []SecretsMarkerRow) tableView {
	stringRows := make([][]string, len(rows))
	ok := make([]bool, len(rows))
	for i, r := range rows {
		stringRows[i] = []string{r.Layer, r.Path, secretsMarkerStateCell(r)}
		ok[i] = r.OK
	}
	return tableView{
		Headers: []string{"LAYER", "PATH", "STATE"},
		Rows:    stringRows,
		Cols: []columnSpec{
			{Flex: true, Wrap: wrapPath},
			{Role: roleTitle, Flex: true, Wrap: wrapPath},
			secretsStateCol,
		},
		Style: secretsStateStyle(ok, 2),
	}
}

// secretsFileTable builds the *.age source table.
func secretsFileTable(rows []SecretsFileRow) tableView {
	stringRows := make([][]string, len(rows))
	ok := make([]bool, len(rows))
	for i, r := range rows {
		stringRows[i] = []string{r.File, secretsStateCell(r.State, secretsReasonText(r.Reason, r.Detail))}
		ok[i] = r.OK
	}
	return tableView{
		Headers: []string{"FILE", "STATE"},
		Rows:    stringRows,
		Cols: []columnSpec{
			{Role: roleTitle, Flex: true, Wrap: wrapPath},
			secretsStateCol,
		},
		Style: secretsStateStyle(ok, 1),
	}
}

// secretsStateCell composes the STATE cell: the bare state, or "state: reason"
// when the CLI supplied a cause. Joining here rather than at the call site
// keeps the two tables spelling it the same way.
func secretsStateCell(state, reason string) string {
	if reason == "" {
		return state
	}
	return state + ": " + reason
}

// secretsMarkerStateCell appends the shadow qualifier to the marker's own state.
// It hangs off the state rather than taking a column of its own because it
// MODIFIES what the state means: "decrypted" alone reads as "this value works",
// which is the reading a shadowed marker has to take away.
func secretsMarkerStateCell(r SecretsMarkerRow) string {
	cell := secretsStateCell(r.State, r.Reason)
	if r.ShadowedBy == "" {
		return cell
	}
	return cell + " (shadowed by " + r.ShadowedBy + ")"
}

// secretsShadowNote explains the qualifier the marker table just added. Without
// it the table states a fact whose consequence is not obvious — a row can be
// green-worded and still describe a value the project never reads.
//
// The second line is the migration leftover: a plaintext copy holding the SAME
// value as the marker, which is the case where the reader believes the report
// already answered "did my migration land?".
func secretsShadowNote(rows []SecretsMarkerRow) string {
	total, identical := 0, 0
	for _, r := range rows {
		if r.ShadowedBy == "" {
			continue
		}
		total++
		if r.ShadowIdentical {
			identical++
		}
	}
	if total == 0 {
		return ""
	}
	// Rendered line by line, like secretsNotes: lipgloss pads a multi-line block
	// out to its widest line, which would bake trailing spaces into the report.
	lines := []string{styles.WarningStyle().Render(fmt.Sprintf(
		"%d encrypted value(s) are shadowed: a plaintext value in a higher layer wins the merge, so the encrypted one is unused.",
		total))}
	if identical > 0 {
		lines = append(lines, styles.WarningStyle().Render(fmt.Sprintf(
			"Same-value shadows (%d) are most likely copies left behind when the value was encrypted.", identical)))
	}
	return strings.Join(lines, "\n")
}

// secretsReasonText joins a token reason with its free-form detail, so the text
// report keeps the cause the JSON contract moved out of `reason`.
func secretsReasonText(reason, detail string) string {
	if detail == "" {
		return reason
	}
	if reason == "" {
		return detail
	}
	return reason + " (" + detail + ")"
}

// secretsStateStyle colors the state column green when the value is readable
// and amber when it is not. Amber rather than red on purpose: an unresolved
// secret on a machine without the key is the expected state for a new
// developer, and `status` is a report, not a failure.
func secretsStateStyle(ok []bool, stateCol int) func(row, col int) lipgloss.Style {
	return func(row, col int) lipgloss.Style {
		if col != stateCol || row < 0 || row >= len(ok) {
			return lipgloss.NewStyle()
		}
		if ok[row] {
			return styles.SuccessStyle()
		}
		return styles.WarningStyle()
	}
}
