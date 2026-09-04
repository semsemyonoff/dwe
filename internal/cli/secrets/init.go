package secrets

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	localpkg "github.com/semsemyonoff/dwe/internal/core/project/local"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	sharedrender "github.com/semsemyonoff/dwe/internal/shared/render"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"

	"github.com/spf13/cobra"
)

// initJSON is the JSON shape of `dwe secrets init`: the committed half and the
// private half's location, plus — for --replace-recipient only — the retired
// recipient and the values its loss orphaned. The three optional fields are
// absent on a first init, where there is nothing to retire and nothing to lose.
//
// The orphan rows reuse the `dwe secrets status` row types rather than plain
// strings: their state and reason describe WHY each value was already
// unreadable, which is the evidence behind the refusal this command is the
// escape from.
type initJSON struct {
	Recipient       string      `json:"recipient"`
	Keyfile         string      `json:"keyfile"`
	OldRecipient    string      `json:"old_recipient,omitempty"`
	OrphanedMarkers []markerRow `json:"orphaned_markers,omitempty"`
	OrphanedFiles   []fileRow   `json:"orphaned_files,omitempty"`
}

// The `identity` detail on a refused second `init`, in a fixed vocabulary so a
// script can pick the fix without parsing the sentence: the project's own
// identity loads on this machine (values are recoverable, `rekey` is the fix),
// or it does not (they are not, and --replace-recipient is).
const (
	identityAvailable = "available"
	identityMissing   = "missing"
)

func newInitCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var replace, yes bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Mint the project's age key pair",
		Long: `Generate an X25519 age key pair for this project.

The public recipient is written to secrets.recipient in workspace.yml — commit
it, so every developer can ADD a secret. The private identity is stored in
` + "`~/" + secrets.KeysDirRel + "/<recipient>.key`" + ` (0600, never in git); share it out of band
with 'dwe secrets key export' and 'dwe secrets key import'.

Refuses to run when secrets.recipient is already set. Replacing a LIVE key pair
is 'dwe secrets rekey', which re-encrypts the existing values instead of
orphaning them.

--replace-recipient covers the other case: the identity is lost, so 'rekey' —
which must read every value before it can rewrite it — cannot run at all. It
mints a new key pair and commits it, leaving every existing ENC[age:…] marker
and *.age source unreadable for good; those values only come back by being
re-entered from their own plaintexts, and the report lists each one. It refuses
while anything is still readable here, because that is 'rekey's case and the
values are not lost yet.`,
		Example: `  dwe secrets init
  dwe secrets init --replace-recipient`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd, flags, replace, yes)
		},
	}
	cmd.Flags().BoolVar(&replace, "replace-recipient", false,
		"mint a new key pair for a project whose identity is lost, orphaning every existing value")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func runInit(cmd *cobra.Command, flags *cmdctx.RootFlags, replace, yes bool) error {
	// Everything up to and including the confirmation runs UNLOCKED, the rule
	// `set` states and `key remove` follows: a form can sit open for as long as
	// the developer needs, and holding deploy.lock + snapshot.lock meanwhile
	// blocks every other dwe command in the project. The recipient is re-read
	// under the lock below, which is what keeps the decision and the write
	// talking about the same tree.
	layers, err := loadRawLayers(flags)
	if err != nil {
		return err
	}
	existing := config.RecipientFromLayers(layers)

	var orphans inventory
	switch {
	case existing == "" && replace:
		return cmdctx.Err("secrets_no_recipient",
			"this project has no secrets.recipient to replace").
			WithHint("run 'dwe secrets init' without --replace-recipient to mint the project's first key pair")
	case existing != "" && !replace:
		return alreadyInitializedError(existing)
	case existing != "":
		inv, done, oerr := planReplaceRecipient(cmd, flags, layers, existing, yes)
		if oerr != nil {
			return oerr
		}
		if !done {
			// A declined confirmation is a finished command, not a failure:
			// nothing was asked for that could still be done.
			return nil
		}
		orphans = inv
	}

	// Lock-held diagnostics go to stderr so JSON-mode stdout stays clean. No
	// preflight: minting a key pair touches no container and no stack state.
	w := sharedrender.NewWriter(cmd.ErrOrStderr())
	release, err := cmdctx.AcquireProjectLocksOrReport(flags.ProjectRoot(), w)
	if err != nil {
		return err
	}
	defer release()

	// The recipient decided the whole branch above, so it is re-read here rather
	// than trusted: between the unlocked read and this point another `init` may
	// have minted the project's first key pair, or a `rekey` retired the one this
	// run was about to orphan. Both make the decision stale, and neither is
	// something to resolve by guessing.
	locked, err := loadRawLayers(flags)
	if err != nil {
		return err
	}
	if now := config.RecipientFromLayers(locked); now != existing {
		return cmdctx.Err("secrets_recipient_changed",
			"secrets.recipient changed while this command was deciding what to do").
			WithDetail("recipient", now).
			WithDetail("expected", existing).
			WithHint("nothing was written; run 'dwe secrets status' to see the current state, then re-run")
	}

	id, err := secrets.Keygen()
	if err != nil {
		return cmdctx.ErrWrap("secrets_keygen_failed", err)
	}

	// The keyfile is the FIRST mutation: a recipient committed without a
	// readable identity would lock the project out of its own future secrets,
	// whereas an orphan keyfile is inert. If the workspace.yml write then fails
	// the keyfile is removed again so a re-run is not blocked by its own
	// no-clobber guard.
	keyfile, err := secrets.WriteKeyfile(id)
	if err != nil {
		return cmdctx.ErrWrap("secrets_keyfile_write_failed", err).
			WithHint("remove or back up the existing keyfile, then run 'dwe secrets init' again")
	}
	if err := writeRecipient(flags.ConfigPath, id.Recipient()); err != nil {
		_ = os.Remove(keyfile)
		if unsupported, ok := spliceUnsupportedError(err, relToRoot(flags.ProjectRoot(), flags.ConfigPath)); ok {
			return unsupported
		}
		return cmdctx.ErrWrap("secrets_recipient_write_failed", err)
	}

	data := initJSON{
		Recipient:       id.Recipient(),
		Keyfile:         keyfile,
		OldRecipient:    existing,
		OrphanedMarkers: orphans.Markers,
		OrphanedFiles:   orphans.Files,
	}
	return cmdctx.WriteData(flags, cmd, data, func(d initJSON) string {
		return render.SecretsInit(render.SecretsInitView{
			Recipient:    d.Recipient,
			Keyfile:      d.Keyfile,
			OldRecipient: d.OldRecipient,
			Orphans:      orphanRows(d),
		})
	})
}

// alreadyInitializedError words the refusal a second `init` gets. Two very
// different situations reach it and they need opposite instructions, so the
// branch is on whether the project's own identity loads HERE:
//
//   - it does — the values are recoverable, and re-encrypting them to a new key
//     pair is exactly what `rekey` is;
//   - it does not — `rekey` must read every value before it can rewrite one, so
//     naming it sends the developer to a command that cannot run in this state.
//     That dead end is what --replace-recipient exists to close, and the only
//     honest thing to say alongside it is that the values are not coming back.
//
// One code for both, because the refusal is the same event; the `identity`
// detail is what a script switches on.
func alreadyInitializedError(existing string) error {
	err := cmdctx.Err("secrets_already_initialized",
		fmt.Sprintf("this project already uses the age recipient %s", existing)).
		WithDetail("recipient", existing)

	if identityPresent(existing) {
		return err.WithDetail("identity", identityAvailable).
			WithHint("run 'dwe secrets rekey' to replace it, re-encrypting every existing value")
	}
	return err.WithDetail("identity", identityMissing).
		WithHint("no identity for " + existing + " on this machine, so 'dwe secrets rekey' cannot run — " +
			"it has to read every value before it can rewrite one. Import the identity with " +
			"'dwe secrets key import' if it still exists somewhere; if it is lost for good, " +
			"'dwe secrets init --replace-recipient' mints a new key pair and every existing value " +
			"has to be re-entered from its own plaintext")
}

// identityPresent reports whether an identity for recipient is available on
// this machine.
//
// `secrets.LoadIdentity` alone is NOT that question: it is
// first-present-source-wins with NO fall-through, so a `DWE_AGE_KEY` exported
// for ANOTHER project answers `wrong_identity` while this project's keyfile
// sits right there — and the refusal would then advertise
// `--replace-recipient`, i.e. "re-enter every value from its plaintext", to a
// developer who holds the key. The keys directory is consulted after it, which
// also finds a MISNAMED file holding this project's identity (`key list`
// reports those under the recipient they hold). `KeyfileOK` is required
// because `KeyfileInfo.Recipient` falls back to the filename stem for a file
// that does not parse, and a corrupt `<recipient>.key` is not an identity.
func identityPresent(recipient string) bool {
	if _, _, err := secrets.LoadIdentity(recipient); err == nil {
		return true
	}
	infos, err := secrets.ListKeyfiles()
	if err != nil {
		return false
	}
	return slices.ContainsFunc(infos, func(i secrets.KeyfileInfo) bool {
		return i.State == secrets.KeyfileOK && i.Recipient == recipient
	})
}

// planReplaceRecipient guards and confirms a --replace-recipient run, returning
// the surface it is about to orphan and whether it may proceed.
//
// The guard is on what this machine can still READ, not on whether an identity
// loads: a project with an identity and nothing encrypted loses nothing by
// rotating its key pair, while a value only a straggler keyfile opens is one
// `rekey` (or `dwe secrets get`) can still save — and this command would
// destroy it. That is a deliberately different test from
// `alreadyInitializedError`'s, which only picks WHICH fix to name.
//
// The inventory travels back to the caller rather than being re-collected for
// the report: a second scan is a second full decrypt attempt over every marker
// and every `*.age` file, and it would have to answer for its own failure at a
// point where the run is already committed to writing.
func planReplaceRecipient(cmd *cobra.Command, flags *cmdctx.RootFlags, layers []config.Layer, existing string, yes bool) (inventory, bool, error) {
	inv, err := inventoryFor(flags, layers, existing)
	if err != nil {
		return inventory{}, false, err
	}
	if n := readableRows(inv); n > 0 {
		return inventory{}, false, cmdctx.Err("secrets_identity_available",
			fmt.Sprintf("%d encrypted value(s) can still be read on this machine", n)).
			WithDetail("recipient", existing).
			WithDetail("readable", n).
			WithHint("--replace-recipient orphans every existing value, and these are not lost yet: " +
				"run 'dwe secrets rekey' to re-encrypt them to a new key pair. To discard them anyway, " +
				"save what you need with 'dwe secrets get', drop the identities that open them with " +
				"'dwe secrets key remove <recipient> --force', and run this again")
	}

	orphaned := len(inv.Markers) + len(inv.Files)
	if yes {
		return inv, true, nil
	}
	if flags.Output == "json" || cmdctx.NonInteractiveEnv() || !widgets.IsInteractiveFn(cmd.InOrStdin()) {
		return inventory{}, false, cmdctx.Err("secrets_confirmation_required",
			"replacing the recipient needs confirmation (no interactive prompt in this mode)").
			WithDetail("recipient", existing).
			WithDetail("orphaned", orphaned).
			WithHint("pass --yes to replace it non-interactively")
	}
	// The count, not the list: the prompt is one line, and the values are named
	// in the report the run ends with — where they are a to-do rather than a
	// wall of text between the developer and the decision.
	confirmed, cerr := runConfirm(replaceConfirmText(existing, orphaned), "Replace", "Cancel")
	if cerr != nil && !errors.Is(cerr, widgets.ErrCancelled) {
		return inventory{}, false, cmdctx.ErrWrap("secrets_confirmation_required", cerr)
	}
	if !confirmed || cerr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "kept %s\n", existing)
		return inventory{}, false, nil
	}
	return inv, true, nil
}

// replaceConfirmText words the confirmation by what is actually at stake: a
// project with nothing encrypted yet is a key rotation, and offering it the
// "0 encrypted value(s)" sentence would read as a threat about nothing.
//
// The count is every row in the report, and the sentence says "re-entered"
// rather than "become unreadable" so that it stays true of all of them: a
// corrupt marker or an unreadable `.age` source is already beyond this
// machine, and this command does not make it any less readable — but it does
// put it on the same to-do list, because it too has to come back from its
// plaintext under the new key pair.
func replaceConfirmText(recipient string, orphaned int) string {
	if orphaned == 0 {
		return fmt.Sprintf("Replace the key pair for %s? Nothing is encrypted yet.", recipient)
	}
	return fmt.Sprintf("Replace the key pair for %s? %d encrypted value(s) then have to be re-entered from their plaintexts.",
		recipient, orphaned)
}

// readableRows counts the rows this machine can open, INCLUDING the stale-key
// ones Result.Readable deliberately excludes: a value only a straggler keyfile
// opens is still readable here, and the guard is about what would be destroyed,
// not about what the loader currently resolves.
func readableRows(inv inventory) int {
	n := 0
	for _, m := range inv.Markers {
		if m.State == stateDecrypted {
			n++
		}
	}
	for _, f := range inv.Files {
		if f.State == stateDecryptable {
			n++
		}
	}
	return n
}

// orphanRows flattens the two inventories into the renderer's list. Markers
// carry their layer as the qualifier; a *.age path is already its own location,
// and repeating it would just print it twice.
func orphanRows(d initJSON) []render.SecretsOrphanRow {
	rows := make([]render.SecretsOrphanRow, 0, len(d.OrphanedMarkers)+len(d.OrphanedFiles))
	for _, m := range d.OrphanedMarkers {
		rows = append(rows, render.SecretsOrphanRow{Name: m.Path, Where: m.Layer})
	}
	for _, f := range d.OrphanedFiles {
		rows = append(rows, render.SecretsOrphanRow{Name: f.File})
	}
	return rows
}

// writeRecipient sets secrets.recipient in workspace.yml through the splice
// writer: an `init` on a hand-annotated workspace.yml appends the new block and
// changes nothing else, where the node writer would re-encode — and reformat —
// the whole document. workspace.yml is git-tracked, hence
// PreserveOrDefault(0644) rather than local.yml's forced 0600.
//
// The same call REPLACES an existing recipient for --replace-recipient: the
// splicer rewrites the value token in place, so the retired age1… line is the
// only thing the diff shows.
//
// Callers must branch on the splice sentinels (spliceUnsupportedError) before
// their own ErrWrap, so a refusal keeps its specific code.
func writeRecipient(workspacePath, recipient string) error {
	splicer, err := localpkg.NewSplicer(workspacePath, localpkg.LabelWorkspace)
	if err != nil {
		return err
	}
	if err := splicer.SetScalar([]string{"secrets", "recipient"}, recipient); err != nil {
		return err
	}
	return splicer.Write(workspacePath, localpkg.PreserveOrDefault(0o644))
}
