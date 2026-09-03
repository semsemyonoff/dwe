package secrets

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/shared/render"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"

	"github.com/spf13/cobra"
)

// keyExportJSON carries the private identity text. It is the one payload in the
// tree that is a secret itself: `key export` exists so the identity can be
// moved to a password manager or a second machine.
type keyExportJSON struct {
	Recipient string `json:"recipient"`
	Identity  string `json:"identity"`
}

// keyImportJSON reports where the imported identity landed.
type keyImportJSON struct {
	Recipient string `json:"recipient"`
	Keyfile   string `json:"keyfile"`
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

The identity comes from --file or from stdin. It must match the project's
committed secrets.recipient — importing a key for another project would create
a keyfile that never opens anything.

The keyfile is written 0600 and is never overwritten: remove the existing one
first if you really mean to replace it.`,
		Example: `  dwe secrets key import --file identity.txt
  pbpaste | dwe secrets key import`,
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
	recipient, err := requireRecipient(flags)
	if err != nil {
		return err
	}

	text, err := readIdentityText(cmd, file)
	if err != nil {
		return err
	}
	id, err := secrets.ParseIdentity(text)
	if err != nil {
		return cmdctx.ErrWrap("secrets_identity_invalid", err).
			WithHint("the identity is the AGE-SECRET-KEY-… line printed by 'dwe secrets key export'")
	}
	// Verify BEFORE writing: a mismatching key would be stored under its own
	// recipient's name, look installed, and open nothing.
	if id.Recipient() != recipient {
		return cmdctx.Err("secrets_identity_mismatch",
			fmt.Sprintf("this identity is for %s, but the project uses %s", id.Recipient(), recipient)).
			WithDetail("recipient", recipient).
			WithDetail("identity_recipient", id.Recipient()).
			WithHint("export the identity from a machine that can already read this project's secrets")
	}

	// The project locks are held for symmetry with the other writers: an import
	// racing a `rekey` would otherwise install a key the rekey is retiring.
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
	return cmdctx.WriteData(flags, cmd, data, func(d keyImportJSON) string {
		return fmt.Sprintf("identity for %s stored at %s", d.Recipient, d.Keyfile)
	})
}

// readIdentityText reads the identity from --file or stdin. A terminal stdin
// with no --file is refused rather than left blocking on a read the user cannot
// see they are expected to satisfy.
func readIdentityText(cmd *cobra.Command, file string) (string, error) {
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", cmdctx.ErrWrap("secrets_identity_read_failed", err)
		}
		return string(data), nil
	}
	if widgets.IsInteractiveFn(cmd.InOrStdin()) {
		return "", cmdctx.Err("secrets_identity_source_required",
			"no identity on stdin").
			WithHint("pass --file PATH, or pipe the identity in: `pbpaste | dwe secrets key import`")
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
