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
	OK     bool
}

// SecretsFileRow is one native *.age config-pack source.
type SecretsFileRow struct {
	File   string // relative to the project root
	State  string
	Reason string
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
		stringRows[i] = []string{r.Layer, r.Path, secretsStateCell(r.State, r.Reason)}
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
		stringRows[i] = []string{r.File, secretsStateCell(r.State, r.Reason)}
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
