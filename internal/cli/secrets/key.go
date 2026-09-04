package secrets

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/ui/secretsprompt"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/workflow/keygate"
	sharedrender "github.com/semsemyonoff/dwe/internal/shared/render"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"

	"github.com/spf13/cobra"
)

// promptIdentityFn is the package-level seam over the hidden import form (same
// rule as runAsk in set.go). Tests stub it to drive the interactive branch
// deterministically, or to fail when a mode that must never prompt reaches it;
// they MUST NOT call t.Parallel() while overriding it.
var promptIdentityFn = secretsprompt.PromptIdentity

// keyExportJSON carries the private identity text. It is the one payload in the
// tree that is a secret itself: `key export` exists so the identity can be
// moved to a password manager or a second machine.
type keyExportJSON struct {
	Recipient string `json:"recipient"`
	Identity  string `json:"identity"`
}

// keyImportJSON reports where the imported identity landed and how much of the
// project it opened. The two counters carry no omitempty: zero readable values
// after a successful import is information (the surface is encrypted to
// somebody else), not an absent field.
//
// They are pointers so that a scan which never ran is distinguishable from one
// that found nothing — a hard zero there would report "your key opens nothing"
// for an unreadable templates tree, which is the opposite of what happened.
// ReportError carries that reason; it is never the import's own outcome.
type keyImportJSON struct {
	Recipient       string `json:"recipient"`
	Keyfile         string `json:"keyfile"`
	MarkersReadable *int   `json:"markers_readable,omitempty"`
	FilesReadable   *int   `json:"files_readable,omitempty"`
	ReportError     string `json:"report_error,omitempty"`
}

func newKeyCmd(flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "key",
		Short: "Move the project identity between machines",
		Long: `Export and import the private half of the project's age key pair.

The identity is never in git: it travels through a password manager or another
out-of-band channel. Export it on a machine that has it, import it on the one
that needs it.`,
		SilenceUsage: true,
	}
	cmd.AddCommand(newKeyExportCmd(flags))
	cmd.AddCommand(newKeyImportCmd(flags))
	cmd.AddCommand(newKeyListCmd(flags))
	cmd.AddCommand(newKeyRemoveCmd(flags))
	return cmd
}

func newKeyExportCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "export",
		Short: "Print the project's private identity",
		Long: `Print the private identity for this project's secrets.recipient.

This writes an AGE-SECRET-KEY-… line to stdout. Pipe it into a password manager
or a file rather than letting it sit in the terminal scrollback:

  dwe secrets key export | pbcopy
  dwe secrets key export > identity.txt`,
		Example:      `  dwe secrets key export`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKeyExport(cmd, flags)
		},
	}
}

func runKeyExport(cmd *cobra.Command, flags *cmdctx.RootFlags) error {
	recipient, err := requireRecipient(flags)
	if err != nil {
		return err
	}
	id, _, err := secrets.LoadIdentity(recipient)
	if err != nil {
		return identityError(recipient, err)
	}

	// Text mode only, and only when stdout is a real terminal: with the output
	// redirected or piped the warning is noise, and in JSON mode stderr must
	// stay free of anything a parser could trip over.
	if flags.Output != "json" && stdoutIsTerminal() {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"warning: printing a private key to the terminal — it stays in the scrollback; pipe it into a file or a password manager instead\n")
	}

	data := keyExportJSON{Recipient: recipient, Identity: id.Export()}
	return cmdctx.WriteData(flags, cmd, data, func(d keyExportJSON) string {
		return d.Identity
	})
}

func newKeyImportCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Store the project's private identity on this machine",
		Long: `Read an age identity and store it as this project's keyfile.

The identity comes from --file, from stdin when it is piped, or — on a terminal
with neither — from a hidden prompt. It must match the project's committed
secrets.recipient: importing a key for another project would create a keyfile
that never opens anything, so the check runs before anything is written.

Paste either the AGE-SECRET-KEY-… line or the whole keyfile; the comment header
an age keyfile carries is ignored.

The keyfile is written 0600 and is never overwritten: remove the existing one
first if you really mean to replace it.`,
		Example: `  pbpaste | dwe secrets key import
  dwe secrets key import --file identity.txt
  dwe secrets key import                     # hidden prompt`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKeyImport(cmd, flags, file)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "read the identity from PATH instead of stdin")
	return cmd
}

func runKeyImport(cmd *cobra.Command, flags *cmdctx.RootFlags, file string) error {
	// The layers are read once and reused for the post-import inventory: the
	// report must describe the tree this command actually checked the recipient
	// against.
	layers, err := loadRawLayers(flags)
	if err != nil {
		return err
	}
	recipient, err := recipientOrErr(layers)
	if err != nil {
		return err
	}

	id, err := resolveIdentity(cmd, flags, recipient, file)
	if err != nil {
		return err
	}

	// The project locks are held for symmetry with the other writers: an import
	// racing a `rekey` would otherwise install a key the rekey is retiring. They
	// are taken AFTER the prompt on purpose — a form can sit open for as long as
	// the developer needs, and holding the locks meanwhile would block every
	// other dwe command in the project.
	w := sharedrender.NewWriter(cmd.ErrOrStderr())
	release, lerr := cmdctx.AcquireProjectLocksOrReport(flags.ProjectRoot(), w)
	if lerr != nil {
		return lerr
	}
	defer release()

	keyfile, err := secrets.WriteKeyfile(id)
	if err != nil {
		return cmdctx.ErrWrap("secrets_keyfile_write_failed", err).
			WithHint("an identity for this recipient is already installed; remove it first to replace it")
	}

	data := keyImportJSON{Recipient: recipient, Keyfile: keyfile}
	// A failed scan is not a failed import: the key is stored and usable, and a
	// non-zero exit here would describe a keyfile that IS on disk — one an
	// O_EXCL retry then refuses to write. So the command still succeeds, but the
	// report says it could not be built instead of counting zero.
	if inv, ierr := keygate.Inventory(flags.ProjectRoot(), layers, keygate.LoadIdentitySet(recipient)); ierr == nil {
		markers, files := inv.Readable()
		data.MarkersReadable, data.FilesReadable = &markers, &files
	} else {
		data.ReportError = ierr.Error()
	}

	return cmdctx.WriteData(flags, cmd, data, func(d keyImportJSON) string {
		stored := fmt.Sprintf("identity for %s stored at %s", d.Recipient, d.Keyfile)
		if d.MarkersReadable == nil || d.FilesReadable == nil {
			return stored + "\nthe readability report could not be built: " + d.ReportError
		}
		return fmt.Sprintf("%s\n%d encrypted value(s) and %d .age file(s) are now readable",
			stored, *d.MarkersReadable, *d.FilesReadable)
	})
}

// resolveIdentity produces the identity to store: from --file, from a piped
// stdin, or from the hidden prompt. Every branch ends up verified against the
// configured recipient before the caller writes anything — a mismatching key
// would be stored under its own recipient's name, look installed, and open
// nothing.
func resolveIdentity(cmd *cobra.Command, flags *cmdctx.RootFlags, recipient, file string) (secrets.Identity, error) {
	if file == "" && widgets.IsInteractiveFn(cmd.InOrStdin()) {
		return promptIdentity(cmd, flags, recipient)
	}
	text, err := readIdentityText(cmd, file)
	if err != nil {
		return secrets.Identity{}, err
	}
	id, err := secrets.ParseIdentity(text)
	if err != nil {
		return secrets.Identity{}, cmdctx.ErrWrap("secrets_identity_invalid", err).
			WithHint("the identity is the AGE-SECRET-KEY-… line printed by 'dwe secrets key export'")
	}
	if id.Recipient() != recipient {
		return secrets.Identity{}, identityMismatchError(recipient, id.Recipient())
	}
	return id, nil
}

// promptIdentity opens the hidden form. The keyfile is checked FIRST: the write
// is O_EXCL, so opening a form whose write is already doomed would make the
// developer hand over a private key for nothing.
func promptIdentity(cmd *cobra.Command, flags *cmdctx.RootFlags, recipient string) (secrets.Identity, error) {
	if flags.Output == "json" || cmdctx.NonInteractiveEnv() {
		return secrets.Identity{}, cmdctx.Err("secrets_identity_source_required",
			"no identity on stdin (no interactive prompt in this mode)").
			WithHint("pass --file PATH, or pipe the identity in: `pbpaste | dwe secrets key import`")
	}
	// Lstat, not Stat: O_EXCL fails on a dangling symlink too, so the path entry
	// itself is what blocks the write — following it would call the doomed import
	// viable and collect the key anyway.
	if path, perr := secrets.KeyfilePath(recipient); perr == nil {
		if _, serr := os.Lstat(path); serr == nil {
			return secrets.Identity{}, cmdctx.Err("secrets_keyfile_write_failed",
				fmt.Sprintf("an identity for %s is already installed at %s", recipient, path)).
				WithDetail("recipient", recipient).
				WithDetail("keyfile", path).
				WithHint("remove it first if you really mean to replace it")
		}
	}

	id, err := promptIdentityFn(cmd.Context(), recipient, cmd.InOrStdin(), cmd.OutOrStdout())
	if err != nil {
		if errors.Is(err, widgets.ErrCancelled) {
			return secrets.Identity{}, cmdctx.Err("secrets_import_cancelled",
				"no identity was entered").
				WithDetail("recipient", recipient).
				WithHint(secrets.IdentityHint(recipient))
		}
		// The form's own messages are DWE-authored; the age parser error, which
		// echoes input characters, never reaches this far.
		return secrets.Identity{}, cmdctx.ErrWrap("secrets_identity_invalid", err).
			WithDetail("recipient", recipient).
			WithHint("the identity is the AGE-SECRET-KEY-… line printed by 'dwe secrets key export'")
	}
	// Re-checked rather than trusted: the seam is stubbable, and a keyfile under
	// a foreign recipient's name opens nothing.
	if id.Recipient() != recipient {
		return secrets.Identity{}, identityMismatchError(recipient, id.Recipient())
	}
	return id, nil
}

// identityMismatchError is the one wording for "valid key, wrong project",
// shared by the file, stdin and prompt branches.
func identityMismatchError(recipient, got string) *cmdctx.CodedError {
	return cmdctx.Err("secrets_identity_mismatch",
		fmt.Sprintf("this identity is for %s, but the project uses %s", got, recipient)).
		WithDetail("recipient", recipient).
		WithDetail("identity_recipient", got).
		WithHint("export the identity from a machine that can already read this project's secrets")
}

// readIdentityText reads the identity from --file or a piped stdin. A terminal
// stdin with no --file never reaches here: it is the prompt's branch.
func readIdentityText(cmd *cobra.Command, file string) (string, error) {
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", cmdctx.ErrWrap("secrets_identity_read_failed", err)
		}
		return string(data), nil
	}
	data, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", cmdctx.ErrWrap("secrets_identity_read_failed", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", cmdctx.Err("secrets_identity_source_required", "stdin held no identity").
			WithHint("pass --file PATH, or pipe the identity in: `pbpaste | dwe secrets key import`")
	}
	return string(data), nil
}

// keyRowJSON is one keyfile in `dwe secrets key list`. State is the fixed
// secrets.KeyfileState vocabulary: neither an I/O nor a parse error reaches it,
// because the text of both echoes file content.
type keyRowJSON struct {
	Recipient string `json:"recipient"`
	File      string `json:"file"`
	State     string `json:"state"`
	Current   bool   `json:"current"`
}

// keyListJSON is the `dwe secrets key list` payload. Keys is always non-nil so
// a consumer can iterate without a null check.
type keyListJSON struct {
	Keys []keyRowJSON `json:"keys"`
}

// keyRemoveJSON reports the deletion. Removed is always true — a refusal is a
// typed error, not a `removed: false` payload.
type keyRemoveJSON struct {
	Recipient string `json:"recipient"`
	Keyfile   string `json:"keyfile"`
	Removed   bool   `json:"removed"`
}

// runConfirm is the package-level wrapper for the removal confirmation;
// swappable in tests. Package-level state: callers MUST NOT run in parallel.
var runConfirm = widgets.RunConfirm

func newKeyListCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the identities installed on this machine",
		Long: `List every identity stored in ` + "`~/" + secrets.KeysDirRel + "`" + `.

The directory is machine-wide, not per project: a key here may belong to any
project on this machine, which is why nothing is ever pruned automatically. The
identity this project needs is marked "current project".

A file that cannot be read or holds no age identity is listed with its state
and nothing else — its content is never echoed. A file whose identity belongs
to another recipient than its name claims is listed as "misnamed" under the
recipient it actually holds.

Runs outside a project too: with no project resolved, no row is marked.`,
		Example:      `  dwe secrets key list`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKeyList(cmd, flags)
		},
	}
}

func runKeyList(cmd *cobra.Command, flags *cmdctx.RootFlags) error {
	infos, err := secrets.ListKeyfiles()
	if err != nil {
		return cmdctx.ErrWrap("secrets_key_list_failed", err)
	}
	// Display only: ListKeyfiles already failed above if the home directory is
	// unresolvable, so the error here cannot be new information.
	dir, _ := secrets.KeysDir()
	current := currentRecipient(flags)

	data := keyListJSON{Keys: make([]keyRowJSON, 0, len(infos))}
	for _, info := range infos {
		data.Keys = append(data.Keys, keyRowJSON{
			Recipient: info.Recipient,
			File:      info.Path,
			State:     string(info.State),
			Current:   current != "" && info.Recipient == current,
		})
	}
	return cmdctx.WriteData(flags, cmd, data, func(d keyListJSON) string {
		return render.SecretsKeyList(keyListView(d, dir))
	})
}

// keyListView maps the JSON payload onto the renderer's view, so the two
// surfaces can never report different rows. The FILE column carries the file
// name only — the directory is a header line, and repeating it on every row
// would be the widest cell in the table.
func keyListView(d keyListJSON, dir string) render.SecretsKeyListView {
	v := render.SecretsKeyListView{Dir: dir, Keys: make([]render.SecretsKeyRow, len(d.Keys))}
	for i, k := range d.Keys {
		v.Keys[i] = render.SecretsKeyRow{
			Recipient: k.Recipient,
			File:      filepath.Base(k.File),
			State:     k.State,
			Current:   k.Current,
			OK:        k.State == string(secrets.KeyfileOK),
		}
	}
	return v
}

// currentRecipient reports the project's secrets.recipient, or "" when the
// command runs outside a project (both housekeeping subcommands are in
// allowedWithoutProject) or the config does not load. The raw layers are read
// directly rather than through loadRawLayers: a keyfile listing must not fail
// over a config it only uses to add a marker to one row.
// heldRecipient returns the recipient of the identity stored at path, or "" for
// a file that holds no age identity. The parse failure is not surfaced: its text
// echoes file content, which here is a private key — the same reason
// secrets.ListKeyfiles reduces those to a fixed enum.
//
// known is false only when the bytes could not be READ at all, which is a
// different answer from "no identity": the file may hold live key material this
// process simply cannot see. A path that resolves to nothing (a dangling
// symlink — Lstat finds it, the open does not) is not that case; there are no
// bytes to lose, and unlinking it is exactly what `key remove` is prescribed
// for by keygate's ErrKeyfileUnusable.
func heldRecipient(path string) (recipient string, known bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", errors.Is(err, fs.ErrNotExist)
	}
	id, err := secrets.ParseIdentity(string(data))
	if err != nil {
		return "", true
	}
	return id.Recipient(), true
}

func currentRecipient(flags *cmdctx.RootFlags) string {
	if flags.ConfigPath == "" {
		return ""
	}
	layers, err := config.LoadRawLayers(flags.ConfigPath)
	if err != nil {
		return ""
	}
	return config.RecipientFromLayers(layers)
}

func newKeyRemoveCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var force, yes bool
	cmd := &cobra.Command{
		Use:   "remove <recipient>",
		Short: "Delete an installed identity from this machine",
		Long: `Delete ` + "`~/" + secrets.KeysDirRel + "/<recipient>.key`" + `.

The argument names the FILE: a keyfile whose name does not match the identity it
holds (listed as "misnamed" by ` + "`dwe secrets key list`" + `) is removed under its own
name, never under the recipient the listing shows for it.

Removing the file that holds the identity this project uses is refused unless
--force is passed: the encrypted values in the repository become unreadable here
until the key is imported again, and it exists nowhere else unless it was
exported. A file that opens nothing — unreadable, holding no age identity, or
holding another project's key — is removed without --force, which is what makes
the "remove it and import the right one" advice work on a stale keyfile.`,
		Example: `  dwe secrets key list
  dwe secrets key remove age1qyqs…
  dwe secrets key remove age1qyqs… --force --yes`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKeyRemove(cmd, flags, args[0], force, yes)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "remove the identity this project itself uses")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func runKeyRemove(cmd *cobra.Command, flags *cmdctx.RootFlags, recipient string, force, yes bool) error {
	path, err := secrets.KeyfilePath(recipient)
	if err != nil {
		return cmdctx.ErrWrap("secrets_recipient_invalid", err).
			WithHint("the recipient is the age1… value committed as secrets.recipient; `dwe secrets key list` prints the installed ones")
	}
	// Existence is checked FIRST: on a machine that never imported the key,
	// "this project uses it, export it first" describes a file that is not
	// there and sends the reader to an export that cannot succeed.
	//
	// Lstat, not Stat: `key list` reports a dangling symlink as an unreadable
	// keyfile and O_EXCL refuses to write over it, so this command — the one that
	// clears the way — must see it too. os.Remove below unlinks the symlink
	// itself, never its target.
	if _, serr := os.Lstat(path); serr != nil {
		return cmdctx.Err("secrets_key_not_found",
			fmt.Sprintf("no identity for %s is installed on this machine", recipient)).
			WithDetail("recipient", recipient).
			WithDetail("keyfile", path).
			WithHint("run 'dwe secrets key list' to see the installed identities")
	}
	// The guard is on the identity the file HOLDS, not on its name. Two cases
	// turn on that distinction, and the filename answers neither:
	//
	//   - keygate's ErrKeyfileUnusable tells the developer to run exactly this
	//     command when ~/.config/dwe/keys/<recipient>.key exists but is
	//     unreadable, unparsable or holds somebody else's key. Inside the
	//     project the argument always equals secrets.recipient, so a name-based
	//     guard refused the one invocation the gate had just prescribed — and
	//     sent the reader to a `key export` that fails for the same reason.
	//     Nothing is lost by removing a file that already opens nothing.
	//   - the mirror image: <other>.key holding THIS project's key (`key list`
	//     reports it "misnamed" under the recipient it holds). A name-based
	//     guard waved that through and deleted live key material.
	//
	// An unreadable file is the one case the guard cannot answer, and it is
	// answered conservatively: os.Remove needs no read permission on the file
	// itself, so waving it through would unlink key material nobody has ruled
	// out as live — and, unlike every other refusal here, that one is final.
	held, known := heldRecipient(path)
	if !force && !known {
		return cmdctx.Err("secrets_key_unreadable",
			fmt.Sprintf("%s cannot be read, so dwe cannot tell whether it holds the identity this project uses", path)).
			WithDetail("recipient", recipient).
			WithDetail("keyfile", path).
			WithHint("check the file's permissions, or pass --force to remove it unread")
	}
	if !force && held != "" && held == currentRecipient(flags) {
		// `key export` reads keys/<current>.key, which is a DIFFERENT file when
		// the key material sits under a misnamed one — naming it there would
		// point the rescue at a file that does not hold this key.
		hint := "export it first with 'dwe secrets key export', or pass --force to remove it anyway"
		if held != recipient {
			hint = "copy " + path + " aside first, or pass --force to remove it anyway"
		}
		return cmdctx.Err("secrets_key_in_use",
			fmt.Sprintf("%s holds the identity this project uses (%s)", path, held)).
			WithDetail("recipient", held).
			WithDetail("keyfile", path).
			WithHint(hint)
	}

	if !yes {
		if flags.Output == "json" || cmdctx.NonInteractiveEnv() || !widgets.IsInteractiveFn(cmd.InOrStdin()) {
			return cmdctx.Err("secrets_confirmation_required",
				"removing an identity needs confirmation (no interactive prompt in this mode)").
				WithDetail("recipient", recipient).
				WithDetail("keyfile", path).
				WithHint("pass --yes to remove it non-interactively")
		}
		confirmed, cerr := runConfirm(fmt.Sprintf("Remove the identity for %s?", recipient), "Remove", "Cancel")
		if cerr != nil && !errors.Is(cerr, widgets.ErrCancelled) {
			return cmdctx.ErrWrap("secrets_confirmation_required", cerr)
		}
		// A decline is a finished command, not a failure: nothing was asked for
		// that could still be done.
		if !confirmed || cerr != nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "kept %s\n", path)
			return nil
		}
	}

	// The project locks are held for the same reason `key import` holds them: a
	// removal racing a `rekey` would retire the key the rekey is installing.
	// Outside a project there is nothing to lock — and nothing to race.
	if root := flags.ProjectRoot(); root != "" {
		release, lerr := cmdctx.AcquireProjectLocksOrReport(root, sharedrender.NewWriter(cmd.ErrOrStderr()))
		if lerr != nil {
			return lerr
		}
		defer release()
	}

	if rerr := os.Remove(path); rerr != nil {
		return cmdctx.ErrWrap("secrets_key_remove_failed", rerr).
			WithDetail("recipient", recipient).
			WithDetail("keyfile", path)
	}

	data := keyRemoveJSON{Recipient: recipient, Keyfile: path, Removed: true}
	return cmdctx.WriteData(flags, cmd, data, func(d keyRemoveJSON) string {
		return "removed " + d.Keyfile
	})
}

// identityError turns a LoadIdentity failure into the typed envelope, naming
// every place the lookup looked so the fix does not depend on knowing the
// precedence rules.
func identityError(recipient string, err error) *cmdctx.CodedError {
	return cmdctx.ErrWrap("secrets_no_identity", err).
		WithDetail("recipient", recipient).
		WithHint(secrets.IdentityHint(recipient))
}
