package secrets

import (
	"fmt"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"

	"github.com/spf13/cobra"
)

// identityJSON says where the identity came from, or why none was loaded.
// Source is the stable secrets.Source vocabulary ("env" / "env-file" /
// "keyfile" / "" for none), so a script can branch on it without parsing the
// human sentence in Error.
type identityJSON struct {
	Source  string `json:"source"`
	Keyfile string `json:"keyfile,omitempty"`
	Error   string `json:"error,omitempty"`
}

// statusJSON is the `dwe secrets status` payload. Markers and Files are always
// non-nil so a consumer can iterate without a null check.
type statusJSON struct {
	Recipient string       `json:"recipient"`
	Identity  identityJSON `json:"identity"`
	Markers   []markerRow  `json:"markers"`
	Files     []fileRow    `json:"files"`
}

func newStatusCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report every encrypted value and whether it can be read here",
		Long: `Show the project's age recipient, the identity this machine holds, and the
state of every encrypted value.

Two inventories are reported: every ENC[age:…] marker committed across
workspace.yml, workspace/defaults.yml and workspace/local.yml, and every *.age
config-pack source under workspace/templates/config. Each one is actually
decrypted, so the report distinguishes "no key on this machine" from "encrypted
to somebody else" from "the payload is damaged".

Read-only, and never fails over an encrypted value: a missing key, a value
encrypted to somebody else and a damaged payload are all reported as rows and
still exit 0. This is the report you run to find out why something is blocked,
not another thing that blocks. A config that does not load at all is still an
error — there would be no inventory to report.`,
		Example:      `  dwe secrets status`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd, flags)
		},
	}
}

func runStatus(cmd *cobra.Command, flags *cmdctx.RootFlags) error {
	inv, err := collectInventory(flags)
	if err != nil {
		return err
	}

	data := statusJSON{
		Recipient: inv.Recipient,
		Identity:  identityPayload(inv),
		Markers:   nonNilMarkers(inv.Markers),
		Files:     nonNilFiles(inv.Files),
	}
	return cmdctx.WriteData(flags, cmd, data, func(d statusJSON) string {
		return render.SecretsStatus(statusView(d))
	})
}

// identityPayload describes the identity lookup's outcome. A failure is data
// here, not an error: reporting "no identity, and here is where it looked" is
// the whole job of the command.
func identityPayload(inv inventory) identityJSON {
	out := identityJSON{Source: string(inv.IdentitySource)}
	if inv.IdentitySource == secrets.SourceKeyfile && inv.Recipient != "" {
		if path, err := secrets.KeyfilePath(inv.Recipient); err == nil {
			out.Keyfile = path
		}
	}
	if inv.IdentityErr != nil {
		out.Error = inv.IdentityErr.Error()
	}
	return out
}

// statusView maps the JSON payload onto the renderer's view, so the two
// surfaces can never report different rows: text is rendered FROM the same
// struct the JSON mode encodes.
func statusView(d statusJSON) render.SecretsStatusView {
	v := render.SecretsStatusView{
		Recipient: d.Recipient,
		Identity:  identityDisplay(d),
		Markers:   make([]render.SecretsMarkerRow, len(d.Markers)),
		Files:     make([]render.SecretsFileRow, len(d.Files)),
	}
	// A reason on a readable row is the stale-key qualifier: this machine can
	// open the value but the CONFIGURED identity cannot, so the loader still
	// reports it unresolved. Amber, not green — the row is a to-do.
	for i, m := range d.Markers {
		v.Markers[i] = render.SecretsMarkerRow{
			Layer: m.Layer, Path: m.Path, State: m.State, Reason: m.Reason,
			OK: m.State == stateDecrypted && m.Reason == "",
		}
	}
	for i, f := range d.Files {
		v.Files[i] = render.SecretsFileRow{
			File: f.File, State: f.State, Reason: f.Reason,
			OK: f.State == stateDecryptable && f.Reason == "",
		}
	}
	return v
}

// identityDisplay words the identity line for the text report: which source
// supplied the key, or — when none did — where the lookup looked, so the fix
// does not depend on knowing the precedence rules.
func identityDisplay(d statusJSON) string {
	switch secrets.Source(d.Identity.Source) {
	case secrets.SourceEnv:
		return "$" + secrets.EnvKey
	case secrets.SourceEnvFile:
		return "$" + secrets.EnvKeyFile
	case secrets.SourceKeyfile:
		if d.Identity.Keyfile != "" {
			return "keyfile (" + d.Identity.Keyfile + ")"
		}
		return "keyfile"
	default:
		if d.Recipient == "" {
			return "none"
		}
		return fmt.Sprintf("none (looked at %s, $%s, $%s)",
			secrets.DisplayKeyfilePath(d.Recipient), secrets.EnvKey, secrets.EnvKeyFile)
	}
}

// nonNilMarkers / nonNilFiles keep the JSON arrays as `[]` rather than `null`
// for a project with nothing encrypted.
func nonNilMarkers(rows []markerRow) []markerRow {
	if rows == nil {
		return []markerRow{}
	}
	return rows
}

func nonNilFiles(rows []fileRow) []fileRow {
	if rows == nil {
		return []fileRow{}
	}
	return rows
}
