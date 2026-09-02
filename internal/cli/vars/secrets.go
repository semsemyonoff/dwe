package vars

import (
	"fmt"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// secretsStateFor reads the load-time secrets state for the project. A load
// failure yields the zero state on purpose: every caller has already reported
// (or is about to report) the real load error, and a missing `secret:` note
// must never be how a broken layer set surfaces.
func secretsStateFor(flags *cmdctx.RootFlags) config.SecretsState {
	_, state, err := config.LoadLayersWithSecrets(flags.ConfigPath)
	if err != nil {
		return config.SecretsState{}
	}
	return state
}

// secretNote builds the one-line `secret:` annotation for `vars inspect`: how
// the value at (originLayer, path) was decrypted, or why it could not be. It
// returns "" when the var is not an encrypted secret at its origin layer —
// which is every var in a project that uses none.
//
// config.PathCovers, not equality: inspecting a sequence or a subtree resolves
// one node whose masked value carries markers recorded at descendant paths
// (`vars.tokens.0`), and those must still be annotated.
func secretNote(state config.SecretsState, originLayer, path string) string {
	if originLayer == "" {
		return ""
	}
	for _, u := range state.Unresolved {
		if u.Layer == originLayer && config.PathCovers(path, u.Path) {
			return unresolvedNote(u.Reason, state.Recipient)
		}
	}
	for _, d := range state.Decrypted {
		if d.Layer == originLayer && config.PathCovers(path, d.Path) {
			return "decrypted via " + identitySourceDisplay(state)
		}
	}
	return ""
}

// unresolvedNote words one unresolved reason for display. The reasons are the
// stable config.Reason* strings, so a new reason shows up as the generic form
// rather than as an empty note.
func unresolvedNote(reason, recipient string) string {
	switch reason {
	case config.ReasonNoIdentity:
		return fmt.Sprintf("unresolved — no identity for %s; run `dwe secrets key import`", recipient)
	case config.ReasonWrongIdentity:
		return fmt.Sprintf("unresolved — the available identity does not match %s", recipient)
	case config.ReasonCorrupt:
		return "unresolved — the marker is corrupt; see `dwe secrets status`"
	default:
		return "unresolved (" + reason + "); see `dwe secrets status`"
	}
}

// identitySourceDisplay names where the identity came from. The keyfile case
// resolves the concrete path so a developer with several projects can see
// which key opened this one; an unresolvable keys dir degrades to the bare
// source name rather than failing the note.
func identitySourceDisplay(state config.SecretsState) string {
	switch secrets.Source(state.IdentitySource) {
	case secrets.SourceEnv:
		return "$" + secrets.EnvKey
	case secrets.SourceEnvFile:
		return "$" + secrets.EnvKeyFile
	case secrets.SourceKeyfile:
		if path, err := secrets.KeyfilePath(state.Recipient); err == nil {
			return "keyfile (" + path + ")"
		}
		return "keyfile"
	default:
		return "the project identity"
	}
}
