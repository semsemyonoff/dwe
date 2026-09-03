package secrets

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/ui/secretsprompt"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/workflow/keygate"
	"github.com/semsemyonoff/dwe/internal/shared/render"
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
type keyImportJSON struct {
	Recipient       string `json:"recipient"`
	Keyfile         string `json:"keyfile"`
	MarkersReadable int    `json:"markers_readable"`
	FilesReadable   int    `json:"files_readable"`
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
	w := render.NewWriter(cmd.ErrOrStderr())
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
	// A failed scan is not a failed import: the key is stored and usable, so the
	// report degrades to zeroes rather than turning a success into an error.
	if inv, ierr := keygate.Inventory(flags.ProjectRoot(), layers, keygate.LoadIdentitySet(recipient)); ierr == nil {
		data.MarkersReadable, data.FilesReadable = inv.Readable()
	}

	return cmdctx.WriteData(flags, cmd, data, func(d keyImportJSON) string {
		return fmt.Sprintf("identity for %s stored at %s\n%d encrypted value(s) and %d .age file(s) are now readable",
			d.Recipient, d.Keyfile, d.MarkersReadable, d.FilesReadable)
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
	if path, perr := secrets.KeyfilePath(recipient); perr == nil {
		if _, serr := os.Stat(path); serr == nil {
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

// identityError turns a LoadIdentity failure into the typed envelope, naming
// every place the lookup looked so the fix does not depend on knowing the
// precedence rules.
func identityError(recipient string, err error) *cmdctx.CodedError {
	return cmdctx.ErrWrap("secrets_no_identity", err).
		WithDetail("recipient", recipient).
		WithHint(secrets.IdentityHint(recipient))
}
