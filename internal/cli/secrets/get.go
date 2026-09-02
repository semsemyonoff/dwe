package secrets

import (
	"errors"
	"fmt"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"

	"github.com/spf13/cobra"
)

// secretGetJSON is the `dwe secrets get` payload: the requested path and the
// decrypted plaintext.
type secretGetJSON struct {
	Path  string `json:"path"`
	Value string `json:"value"`
}

func newGetCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "get <vars.path>",
		Short: "Decrypt one committed secret and print it",
		Long: `Decrypt the ENC[age:…] marker stored at a config path and print the plaintext.

The layers are read as written, so this reports the secret itself rather than
the merged value: when a marker in workspace/defaults.yml is shadowed by a
plaintext override in workspace/local.yml, 'dwe secrets get' still prints the
secret and 'dwe vars get' prints the override that actually wins at runtime.

A path that holds no marker is an error — use 'dwe vars get' for a plaintext
value. Reading needs the private identity; adding a secret does not.`,
		Example:      `  dwe secrets get vars.telegram.token`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGet(cmd, flags, args[0])
		},
	}
}

func runGet(cmd *cobra.Command, flags *cmdctx.RootFlags, path string) error {
	layers, err := loadRawLayers(flags)
	if err != nil {
		return err
	}

	marker, layerPath, ok := markerAt(layers, path)
	if !ok {
		return cmdctx.Err("secrets_not_encrypted",
			fmt.Sprintf("no encrypted value is stored at %q", path)).
			WithDetail("path", path).
			WithHint("run 'dwe secrets status' to list every marker, or 'dwe vars get' to read a plaintext value")
	}

	recipient, err := recipientOrErr(layers)
	if err != nil {
		return err
	}
	ids := loadIdentitySet(recipient)
	plain, err := ids.decrypt(marker)
	if err != nil {
		return decryptError(recipient, layerPath, path, err)
	}

	data := secretGetJSON{Path: path, Value: plain}
	return cmdctx.WriteData(flags, cmd, data, func(d secretGetJSON) string {
		return d.Value
	})
}

// markerAt returns the marker stored at path in the HIGHEST-precedence layer
// that holds one, plus that layer's file. Layers arrive lowest-first, so the
// last match wins — the same precedence the merged config applies.
func markerAt(layers []config.Layer, path string) (marker, layer string, ok bool) {
	for _, m := range config.CollectMarkers(layers) {
		if m.Path == path {
			marker, layer, ok = m.Value, m.Layer, true
		}
	}
	return marker, layer, ok
}

// decryptError names the value that failed and, for a missing identity, every
// place the lookup looked. A damaged payload keeps its own wording: no key
// would have helped, so pointing at `key import` would send the user hunting
// for the wrong thing.
func decryptError(recipient, layerPath, path string, err error) error {
	if errors.Is(err, secrets.ErrNoIdentity) {
		return identityError(recipient, err).WithDetail("path", path)
	}
	return cmdctx.ErrWrap("secrets_decrypt_failed", err).
		WithDetail("path", path).
		WithDetail("layer", layerPath).
		WithHint("run 'dwe secrets status' to see the state of every encrypted value")
}
