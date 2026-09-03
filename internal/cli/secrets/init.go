package secrets

import (
	"fmt"
	"os"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	localpkg "github.com/semsemyonoff/dwe/internal/core/project/local"
	"github.com/semsemyonoff/dwe/internal/shared/render"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"

	"github.com/spf13/cobra"
)

// initJSON is the JSON shape of `dwe secrets init`: the committed half and the
// private half's location.
type initJSON struct {
	Recipient string `json:"recipient"`
	Keyfile   string `json:"keyfile"`
}

func newInitCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Mint the project's age key pair",
		Long: `Generate an X25519 age key pair for this project.

The public recipient is written to secrets.recipient in workspace.yml — commit
it, so every developer can ADD a secret. The private identity is stored in
` + "`~/" + secrets.KeysDirRel + "/<recipient>.key`" + ` (0600, never in git); share it out of band
with 'dwe secrets key export' and 'dwe secrets key import'.

Refuses to run when secrets.recipient is already set: replacing a live key pair
is 'dwe secrets rekey', which re-encrypts the existing values instead of
orphaning them.`,
		Example:      `  dwe secrets init`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd, flags)
		},
	}
}

func runInit(cmd *cobra.Command, flags *cmdctx.RootFlags) error {
	// Lock-held diagnostics go to stderr so JSON-mode stdout stays clean. No
	// preflight: minting a key pair touches no container and no stack state.
	w := render.NewWriter(cmd.ErrOrStderr())
	release, err := cmdctx.AcquireProjectLocksOrReport(flags.ProjectRoot(), w)
	if err != nil {
		return err
	}
	defer release()

	layers, err := loadRawLayers(flags)
	if err != nil {
		return err
	}
	if existing := config.RecipientFromLayers(layers); existing != "" {
		return cmdctx.Err("secrets_already_initialized",
			fmt.Sprintf("this project already uses the age recipient %s", existing)).
			WithDetail("recipient", existing).
			WithHint("run 'dwe secrets rekey' to replace it, re-encrypting every existing value")
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

	data := initJSON{Recipient: id.Recipient(), Keyfile: keyfile}
	return cmdctx.WriteData(flags, cmd, data, func(d initJSON) string {
		var b strings.Builder
		b.WriteString("age key pair created\n")
		fmt.Fprintf(&b, "  recipient: %s\n", d.Recipient)
		fmt.Fprintf(&b, "  keyfile:   %s\n\n", d.Keyfile)
		b.WriteString("secrets.recipient was written to workspace.yml — commit it.\n")
		b.WriteString("Back up the keyfile: it is the only way to read this project's secrets.")
		return b.String()
	})
}

// writeRecipient sets secrets.recipient in workspace.yml through the splice
// writer: an `init` on a hand-annotated workspace.yml appends the new block and
// changes nothing else, where the node writer would re-encode — and reformat —
// the whole document. workspace.yml is git-tracked, hence
// PreserveOrDefault(0644) rather than local.yml's forced 0600.
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
