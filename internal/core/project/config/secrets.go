package config

import "strings"

// SecretsConfig is the formalized top-level secrets: block. It carries the
// project's age recipient (the public half of the key pair), which is
// committed: anyone with the repository can ADD a secret, only identity
// holders can read one.
//
// The block is legal in workspace.yml only (validateSecretsBlock); a per-layer
// override would silently point half the tree at a different key pair.
type SecretsConfig struct {
	Recipient string `yaml:"recipient"`
}

// Unresolved reasons reported by SecretsState, `dwe secrets status` and the
// secrets.unresolved validator. They mirror the secrets package's sentinel
// errors and are part of the JSON contract, so they are stable strings.
const (
	// ReasonNoIdentity means no private identity was available at all.
	ReasonNoIdentity = "no_identity"
	// ReasonWrongIdentity means an identity was found but does not match the
	// recipient the value was encrypted to.
	ReasonWrongIdentity = "wrong_identity"
	// ReasonInvalidIdentity means an identity source was PRESENT but its
	// content is not an age identity — a truncated DWE_AGE_KEY, a keyfile that
	// lost its key line. It is deliberately distinct from ReasonNoIdentity: the
	// fix is repairing the source, not obtaining a key.
	ReasonInvalidIdentity = "invalid_identity"
	// ReasonCorrupt means the marker payload is malformed.
	ReasonCorrupt = "corrupt"
)

// SecretRef locates one encrypted scalar: the layer file that carries it and
// its dot-path inside that layer (sequence elements carry their index, e.g.
// vars.tokens.0).
type SecretRef struct {
	Layer string `json:"layer"`
	Path  string `json:"path"`
}

// UnresolvedSecret is a marker that could not be decrypted, with the reason.
type UnresolvedSecret struct {
	SecretRef
	Reason string `json:"reason"`
}

// SecretsState is the result of the load-time decrypt pass.
//
// It deliberately does NOT carry the identity: renderers and CLI commands that
// need to decrypt something later call secrets.LoadIdentity(Recipient) again
// (one file read), so a decrypted config can never hand a private key to a
// consumer that merely accepted a *DweConfig.
//
// Decrypted and Unresolved are ordered by layer (lowest precedence first) and
// then by path, so every list and table built from them is deterministic.
type SecretsState struct {
	Recipient string `json:"recipient,omitempty"`
	// IdentitySource is the source LoadIdentity CONSULTED, filled on a failed
	// lookup as well — it is what tells a reader which source to repair. It is
	// empty only when no lookup ran (a project with no markers).
	IdentitySource string             `json:"identity_source,omitempty"`
	Decrypted      []SecretRef        `json:"decrypted,omitempty"`
	Unresolved     []UnresolvedSecret `json:"unresolved,omitempty"`
}

// HasSecrets reports whether the load saw any marker at all.
func (s SecretsState) HasSecrets() bool {
	return len(s.Decrypted) > 0 || len(s.Unresolved) > 0
}

// UnresolvedAt reports whether the given layer file carries an unresolved
// marker at path, or anywhere beneath it. `dwe vars` uses it to decide whether
// the ORIGIN layer of a leaf is encrypted — a marker shadowed by a plaintext
// override must still render as plaintext.
//
// The "beneath it" half is what keeps the flag in step with the rendered value
// for a SEQUENCE: `varsusage.EnumerateVars` stops at a sequence and reports the
// leaf as `vars.tokens`, while the decrypt walk records each element with its
// index (`vars.tokens.0`), so an exact match would report a masked value as not
// encrypted.
func (s SecretsState) UnresolvedAt(layer, path string) bool {
	for _, u := range s.Unresolved {
		if u.Layer == layer && PathCovers(path, u.Path) {
			return true
		}
	}
	return false
}

// PathCovers reports whether the dot-path leaf is ref itself or one of its
// ancestors, matching only at a real dot boundary so "vars.db" covers
// "vars.db.pass" but not "vars.dbx".
func PathCovers(leaf, ref string) bool {
	if leaf == ref {
		return true
	}
	return len(ref) > len(leaf) && strings.HasPrefix(ref, leaf) && ref[len(leaf)] == '.'
}

// SecretsRecipient returns the configured recipient, or "" when no secrets:
// block is present. Safe when cfg is nil.
func SecretsRecipient(cfg *DweConfig) string {
	if cfg == nil {
		return ""
	}
	if cfg.SecretsState.Recipient != "" {
		return cfg.SecretsState.Recipient
	}
	if cfg.Secrets == nil {
		return ""
	}
	return cfg.Secrets.Recipient
}
