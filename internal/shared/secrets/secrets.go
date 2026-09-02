// Package secrets implements DWE's encrypted-at-rest secret format on top of
// age (age-encryption.org/v1).
//
// Two shapes are supported:
//
//   - scalar values, carried inside any config layer as the string marker
//     ENC[age:<base64 of a binary age file>]. A plain string survives every
//     YAML decoder in the tree unchanged, which a custom YAML tag would not
//     (yaml.v3 silently drops tags when decoding into map[string]any).
//   - whole files, as native binary age files (a config-pack source whose
//     from: ends in .age).
//
// A project has one X25519 key pair: the public recipient is committed in
// workspace.yml, the private identity lives outside the repository. Anyone
// with the repository can add a secret (encryption needs only the recipient);
// only identity holders can read one.
//
// filippo.io/age is the single cryptography dependency of the project and may
// be imported by this package only. It is chosen over a hand-rolled
// x/crypto envelope because the format is standard and audited, and because
// markers and .age files stay readable with the stock `age` CLI:
//
//	base64 -d <<<"<payload>" | age -d -i ~/.config/dwe/keys/<recipient>.key
//
// This package is a leaf: it MUST NOT import anything from internal/core.
package secrets

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"regexp"

	"filippo.io/age"
)

// MarkerPrefix opens an encrypted scalar marker.
const MarkerPrefix = "ENC[age:"

// Sentinel errors. Callers map them to the SecretsState unresolved reasons
// (no_identity / wrong_identity / corrupt), so every failure path in this
// package must wrap exactly one of them.
var (
	// ErrNoIdentity means no private identity was available at all.
	ErrNoIdentity = errors.New("no age identity available")
	// ErrWrongIdentity means an identity was found but it does not match the
	// recipient the data was encrypted to.
	ErrWrongIdentity = errors.New("identity does not match the recipient")
	// ErrCorrupt means the marker or the ciphertext is malformed.
	ErrCorrupt = errors.New("malformed encrypted value")
)

// markerRe matches a scalar that is *entirely* a marker. The whole scalar or
// nothing: a string that merely contains "ENC[" is data.
var markerRe = regexp.MustCompile(`^ENC\[age:[A-Za-z0-9+/=]+\]$`)

// embeddedMarkerRe matches a marker anywhere inside a larger text. Used by the
// output guards, which must never write a marker into a rendered file.
var embeddedMarkerRe = regexp.MustCompile(`ENC\[age:[A-Za-z0-9+/=]+\]`)

// IsMarker reports whether s is exactly an encrypted-value marker.
func IsMarker(s string) bool { return markerRe.MatchString(s) }

// ContainsMarker reports whether s contains a marker anywhere. Renderers use
// it to refuse materializing an undecrypted secret into an output file.
func ContainsMarker(s string) bool { return embeddedMarkerRe.MatchString(s) }

// Encrypt encrypts plain to recipient and returns the scalar marker.
func Encrypt(plain string, recipient string) (string, error) {
	ciphertext, err := EncryptBytes([]byte(plain), recipient)
	if err != nil {
		return "", err
	}
	return MarkerPrefix + base64.StdEncoding.EncodeToString(ciphertext) + "]", nil
}

// Decrypt opens a scalar marker with id. A value that is not a marker, whose
// payload is not valid base64, or whose ciphertext is malformed is ErrCorrupt;
// a marker encrypted to another recipient is ErrWrongIdentity.
func Decrypt(marker string, id Identity) (string, error) {
	if !IsMarker(marker) {
		return "", fmt.Errorf("%w: not an %s…] marker", ErrCorrupt, MarkerPrefix)
	}
	payload := marker[len(MarkerPrefix) : len(marker)-1]
	ciphertext, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Errorf("%w: payload is not valid base64: %v", ErrCorrupt, err)
	}
	plain, err := DecryptBytes(ciphertext, id)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// EncryptBytes encrypts plain to recipient and returns a binary age file. The
// recipient is the only input needed, so any developer with the repository can
// add a secret without holding the identity.
func EncryptBytes(plain []byte, recipient string) ([]byte, error) {
	if recipient == "" {
		return nil, fmt.Errorf("encrypt: no recipient configured")
	}
	rcpt, err := age.ParseX25519Recipient(recipient)
	if err != nil {
		return nil, fmt.Errorf("encrypt: invalid recipient %q: %w", recipient, err)
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, rcpt)
	if err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	if _, err := w.Write(plain); err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	return buf.Bytes(), nil
}

// DecryptBytes opens a binary age file with id.
func DecryptBytes(ciphertext []byte, id Identity) ([]byte, error) {
	if id.IsZero() {
		return nil, ErrNoIdentity
	}
	r, err := age.Decrypt(bytes.NewReader(ciphertext), id.id)
	if err != nil {
		var noMatch *age.NoIdentityMatchError
		if errors.As(err, &noMatch) || errors.Is(err, age.ErrIncorrectIdentity) {
			return nil, fmt.Errorf("%w: encrypted to another recipient", ErrWrongIdentity)
		}
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	plain, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return plain, nil
}
