// Package secrets implements the `dwe secrets` command tree: the project's age
// key pair, the ENC[age:…] markers committed in the config layers, and the
// native *.age sources of config packs.
//
// The tree is deliberately host-only: `secrets` is absent from
// bridgeAllowedTopLevel, so no container can mint, rekey or export an identity.
// Reads of already-decrypted values stay reachable through `dwe vars get`,
// which is the exposure a container already has through its rendered .env.
//
// Writers take the project locks (no preflight — this is not a stack
// mutation); status / get / key export are read-only. Every command routes its
// payload through cmdctx.WriteData and returns typed cmdctx errors, so
// --output json stays parseable.
package secrets

import (
	"errors"
	"os"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	localpkg "github.com/semsemyonoff/dwe/internal/core/project/local"
	"github.com/semsemyonoff/dwe/internal/core/workflow/keygate"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

// stdoutIsTerminal is the production tty probe behind the `key export` warning;
// tests swap it. Package-level state: tests overriding it MUST NOT run in
// parallel.
var stdoutIsTerminal = func() bool { return term.IsTerminal(os.Stdout.Fd()) }

// The inventory itself lives in core/workflow/keygate, so the onboarding gate,
// `key import` and `status` share one scanner and one classification. These
// aliases keep the local vocabulary (and the JSON row types) unchanged.
type (
	markerRow = keygate.MarkerRow
	fileRow   = keygate.FileRow
	inventory = keygate.Result
)

const (
	stateDecrypted      = keygate.StateDecrypted
	stateUnresolved     = keygate.StateUnresolved
	stateDecryptable    = keygate.StateDecryptable
	stateNotDecryptable = keygate.StateNotDecryptable
	reasonStaleKey      = keygate.ReasonStaleKey
)

// relToRoot renders a path relative to the project root for display. One
// implementation, shared with the inventory rows it has to agree with.
var relToRoot = keygate.RelToRoot

// NewCmd builds the `dwe secrets` command tree. Registered under
// groupConfiguration in cli/root.go next to `vars`: both edit the same three
// layer files, and a secret is just a var that happens to be encrypted.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "secrets",
		Aliases: []string{"secret"},
		GroupID: groupID,
		Short:   "Manage the project's encrypted secrets",
		Long: `Work with encrypted-at-rest secrets committed to the repository.

One X25519 age key pair per project: the public recipient is committed as
secrets.recipient in workspace.yml, so anyone with the repository can ADD a
secret; the private identity lives in ` + "`~/" + secrets.KeysDirRel + "/<recipient>.key`" + ` (or in
` + secrets.EnvKey + ` / ` + secrets.EnvKeyFile + ` for CI), so only identity holders can read one.

Secrets take two shapes: an ENC[age:…] scalar in any config layer (decrypted in
memory at load time, so ${vars.*} and exports.env see plaintext), and a whole
*.age file used as a config-pack source.

Without a usable identity the project still loads — markers stay literal,
` + "`dwe vars`" + ` shows <encrypted>, and the lifecycle commands stop with a named
fix instead of writing ciphertext into a config file.`,
		Example: `  dwe secrets status
  dwe secrets init
  dwe secrets set vars.telegram.token
  dwe secrets get vars.telegram.token
  dwe secrets key export
  dwe secrets key import --file identity.txt`,
		SilenceUsage: true,
	}

	cmd.AddCommand(newInitCmd(flags))
	cmd.AddCommand(newStatusCmd(flags))
	cmd.AddCommand(newSetCmd(flags))
	cmd.AddCommand(newGetCmd(flags))
	cmd.AddCommand(newKeyCmd(flags))
	cmd.AddCommand(newEncryptCmd(flags))
	cmd.AddCommand(newDecryptCmd(flags))
	cmd.AddCommand(newRekeyCmd(flags))
	return cmd
}

// loadRawLayers reads the config layers as written (ciphertext) and validates
// their roots. Every `secrets` command works on the raw tree: the whole point
// is to see the markers, which a decrypting load would have replaced.
func loadRawLayers(flags *cmdctx.RootFlags) ([]config.Layer, error) {
	layers, err := config.LoadRawLayers(flags.ConfigPath)
	if err != nil {
		return nil, cmdctx.ErrWrap("project_invalid_config", err)
	}
	// The same validation LoadConfig runs, so `secrets` refuses to write into a
	// tree that would not load afterwards (a secrets: block in the wrong layer,
	// a malformed recipient).
	if err := config.ValidateLayerRoots(layers); err != nil {
		return nil, cmdctx.ErrWrap("project_invalid_config", err)
	}
	return layers, nil
}

// requireRecipient returns the configured secrets.recipient, or a typed error
// naming `dwe secrets init` when the project has none.
func requireRecipient(flags *cmdctx.RootFlags) (string, error) {
	layers, err := loadRawLayers(flags)
	if err != nil {
		return "", err
	}
	return recipientOrErr(layers)
}

// collectInventory builds the encrypted-surface inventory for the current
// project. The scan, the classification and the row types all live in
// core/workflow/keygate, so `status`, `rekey` and the onboarding gate can never
// disagree about what a value's state is.
func collectInventory(flags *cmdctx.RootFlags) (inventory, error) {
	layers, err := loadRawLayers(flags)
	if err != nil {
		return inventory{}, err
	}
	recipient := config.RecipientFromLayers(layers)
	inv, err := keygate.Inventory(flags.ProjectRoot(), layers, keygate.LoadIdentitySet(recipient))
	if err != nil {
		return inventory{}, err
	}
	return inv, nil
}

// spliceUnsupportedError maps a splice-writer refusal onto the typed
// `secrets_write_unsupported` code. The three sentinels share one code — the
// file was left untouched and the document has to change before a retry can
// work — but not one hint: a shape refusal names the reshaping, while a
// verification failure is about the file NOT reading back as written.
//
// Every caller branches on this BEFORE its own generic ErrWrap: cmdctx.ErrWrap
// builds a NEW outer CodedError and the JSON serializer reports the outermost
// code, so wrapping first would bury the specific refusal under
// `secrets_recipient_write_failed` or `secrets_rekey_failed`.
func spliceUnsupportedError(err error, file string) (*cmdctx.CodedError, bool) {
	if !errors.Is(err, localpkg.ErrMultilineScalar) &&
		!errors.Is(err, localpkg.ErrUnsplicable) &&
		!errors.Is(err, localpkg.ErrVerify) {
		return nil, false
	}
	return cmdctx.ErrWrap("secrets_write_unsupported", err).
		WithDetail("file", file).
		WithHint(spliceUnsupportedHint(err, file)), true
}

// spliceUnsupportedHint is the fix for a refusal. ErrVerify is not a shape the
// author can reshape at the target — the write DID splice and then failed to
// read back — so pointing at the target's own formatting would send the
// developer to a line that is already fine; a duplicate or merge-inherited key
// elsewhere in the file is the shape that produces it.
func spliceUnsupportedHint(err error, file string) string {
	if errors.Is(err, localpkg.ErrVerify) {
		return "the edited " + file + " did not read back as written; check it for a duplicate key " +
			"or a '<<:' merge key that shadows the target, then retry"
	}
	return "dwe secrets writes a layer file by replacing single lines; make the target a single-line " +
		"value under a block mapping in " + file + " and retry"
}

// spliceWriteError is spliceUnsupportedError with the I/O fallback the plain
// write paths need: anything that is not a refusal keeps today's code.
func spliceWriteError(err error, file string) error {
	if unsupported, ok := spliceUnsupportedError(err, file); ok {
		return unsupported
	}
	return cmdctx.ErrWrap("secrets_write_failed", err).WithDetail("file", file)
}
