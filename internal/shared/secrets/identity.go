package secrets

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"filippo.io/age"
)

// Environment overrides for the private identity, in precedence order. They
// exist for CI, where no home directory is available. Both are stripped from
// the container environment at the bridge and re-supplied from the daemon's
// own environment, so a container can never point the host dwe at a file of
// its choosing.
const (
	EnvKey     = "DWE_AGE_KEY"
	EnvKeyFile = "DWE_AGE_KEY_FILE"
)

// KeysDirRel is the identity directory below the user's home directory. It
// sits next to the existing user-level config at ~/.config/dwe.
var KeysDirRel = filepath.Join(".config", "dwe", "keys")

// Source names where a loaded identity came from.
type Source string

const (
	// SourceNone means no identity was loaded.
	SourceNone Source = ""
	// SourceEnv means the identity text came from DWE_AGE_KEY.
	SourceEnv Source = "env"
	// SourceEnvFile means the identity was read from DWE_AGE_KEY_FILE.
	SourceEnvFile Source = "env-file"
	// SourceKeyfile means the identity was read from the keys directory.
	SourceKeyfile Source = "keyfile"
)

// Identity wraps an age X25519 private key.
//
// The wrapper owns String() on purpose: age.X25519Identity implements
// fmt.Stringer *with the secret key*, so an embedded value would leak the key
// through any %v / %+v format. Export() is the only accessor that yields the
// private key text.
type Identity struct {
	id *age.X25519Identity
}

// IsZero reports whether the identity holds no key.
func (i Identity) IsZero() bool { return i.id == nil }

// Recipient returns the public recipient ("age1…"), or "" for a zero value.
func (i Identity) Recipient() string {
	if i.id == nil {
		return ""
	}
	return i.id.Recipient().String()
}

// String is deliberately key-free: it names the recipient only.
func (i Identity) String() string {
	if i.id == nil {
		return "age identity (none)"
	}
	return "age identity for " + i.Recipient()
}

// Export returns the private key text ("AGE-SECRET-KEY-1…"). This is the only
// accessor that yields it; callers are responsible for where it goes.
func (i Identity) Export() string {
	if i.id == nil {
		return ""
	}
	return i.id.String()
}

// Keygen mints a fresh X25519 identity.
func Keygen() (Identity, error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return Identity{}, fmt.Errorf("generate age identity: %w", err)
	}
	return Identity{id: id}, nil
}

// secretKeyRe matches one age X25519 private key: the fixed HRP, the bech32
// separator and 58 characters of the upper-case bech32 charset (which excludes
// 1, B, I and O). Matching the token rather than a line is what lets a keyfile
// pasted into a single-line field parse: its comment and key arrive joined.
var secretKeyRe = regexp.MustCompile(`AGE-SECRET-KEY-1[AC-HJ-NP-Z02-9]{58}`)

// ParseIdentity reads an identity from key text.
//
// The FIRST AGE-SECRET-KEY-1… token anywhere in text wins, so a bare key line,
// a whole keyfile (comment plus key), age-keygen output, CRLF, surrounding
// whitespace and a keyfile whose lines a paste joined into one all parse. Later
// tokens are ignored: a multi-identity keyfile and a commented-out old key
// above the live one are both shapes age itself accepts.
//
// No token, or a token age refuses, is ErrInvalidIdentity — never ErrCorrupt,
// which means a damaged payload. The age error is not interpolated: its text
// echoes the input characters, which here are private-key bytes.
func ParseIdentity(text string) (Identity, error) {
	token := secretKeyRe.FindString(text)
	if token == "" {
		return Identity{}, fmt.Errorf("%w: no AGE-SECRET-KEY-1… key found", ErrInvalidIdentity)
	}
	id, err := age.ParseX25519Identity(token)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: not a valid age X25519 identity", ErrInvalidIdentity)
	}
	return Identity{id: id}, nil
}

// ParseRecipient checks that text is a well-formed age recipient ("age1…").
func ParseRecipient(text string) error {
	if text == "" {
		return fmt.Errorf("%w: empty recipient", ErrCorrupt)
	}
	if _, err := age.ParseX25519Recipient(text); err != nil {
		return fmt.Errorf("%w: invalid recipient %q: %v", ErrCorrupt, text, err)
	}
	return nil
}

// LoadIdentity resolves the private identity for recipient.
//
// Order: DWE_AGE_KEY (identity text) → DWE_AGE_KEY_FILE (path) → the keyfile
// under ~/.config/dwe/keys. The first *present* source is used and must match
// recipient — a mismatch is ErrWrongIdentity naming the source, never a
// fall-through to the next source, so a stale env var can never be masked by a
// working keyfile. An empty recipient is ErrNoIdentity without touching the
// filesystem; a missing keyfile is ErrNoIdentity; any other read error is
// returned as-is so permission problems are never hidden.
//
// The returned Source is the CONSULTED source, on failure as well: a caller
// that reports "no identity" must be able to say which source it looked at and
// whether that source was set-but-broken. SourceNone means nothing was
// consulted at all, which happens only for an empty recipient.
func LoadIdentity(recipient string) (Identity, Source, error) {
	if recipient == "" {
		return Identity{}, SourceNone, fmt.Errorf("%w: no secrets.recipient configured", ErrNoIdentity)
	}

	if text := os.Getenv(EnvKey); text != "" {
		return finishLoad(text, recipient, SourceEnv, EnvKey)
	}

	if path := os.Getenv(EnvKeyFile); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return Identity{}, SourceEnvFile, fmt.Errorf("%w: %s points at %s, which does not exist", ErrNoIdentity, EnvKeyFile, path)
			}
			return Identity{}, SourceEnvFile, fmt.Errorf("read %s (%s): %w", path, EnvKeyFile, err)
		}
		return finishLoad(string(data), recipient, SourceEnvFile, EnvKeyFile+" "+path)
	}

	path, err := KeyfilePath(recipient)
	if err != nil {
		return Identity{}, SourceKeyfile, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Identity{}, SourceKeyfile, fmt.Errorf("%w: no keyfile at %s", ErrNoIdentity, path)
		}
		return Identity{}, SourceKeyfile, fmt.Errorf("read keyfile %s: %w", path, err)
	}
	return finishLoad(string(data), recipient, SourceKeyfile, path)
}

func finishLoad(text, recipient string, src Source, label string) (Identity, Source, error) {
	id, err := ParseIdentity(text)
	if err != nil {
		return Identity{}, src, fmt.Errorf("parse identity from %s: %w", label, err)
	}
	if id.Recipient() != recipient {
		return Identity{}, src, &WrongIdentityError{Source: src, label: label, Have: id.Recipient(), Want: recipient}
	}
	return id, src, nil
}

// WrongIdentityError reports that the consulted source holds a usable age
// identity for a DIFFERENT recipient than the project's.
//
// Both recipients are public values, so they are carried structurally rather
// than only inside the sentence: `dwe secrets status` words its own header from
// them, instead of re-parsing an error string.
type WrongIdentityError struct {
	Source Source // the consulted source
	Have   string // the recipient the source actually holds
	Want   string // the recipient the project uses
	label  string // how LoadIdentity names the source in prose
}

func (e *WrongIdentityError) Error() string {
	return fmt.Sprintf("%v: %s holds the identity for %s, but the project uses %s",
		ErrWrongIdentity, e.label, e.Have, e.Want)
}

// Unwrap keeps errors.Is(err, ErrWrongIdentity) working for every caller that
// only classifies the failure.
func (e *WrongIdentityError) Unwrap() error { return ErrWrongIdentity }

// SourceLabel names a consulted identity source for display: "$DWE_AGE_KEY",
// "$DWE_AGE_KEY_FILE <path>", "keyfile <path>". The paths are locations, never
// content — no key material passes through here. An unknown source yields "".
func SourceLabel(src Source, recipient string) string {
	switch src {
	case SourceEnv:
		return "$" + EnvKey
	case SourceEnvFile:
		if path := os.Getenv(EnvKeyFile); path != "" {
			return "$" + EnvKeyFile + " " + path
		}
		return "$" + EnvKeyFile
	case SourceKeyfile:
		return "keyfile " + DisplayKeyfilePath(recipient)
	default:
		return ""
	}
}

// KeysDir returns the absolute identity directory (~/.config/dwe/keys).
func KeysDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, KeysDirRel), nil
}

// KeyfilePath returns the keyfile path for recipient. The recipient is
// validated first: it becomes a path element, so a malformed value must never
// reach the filesystem.
func KeyfilePath(recipient string) (string, error) {
	if err := ParseRecipient(recipient); err != nil {
		return "", err
	}
	dir, err := KeysDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, recipient+".key"), nil
}

// DisplayKeyfilePath renders the keyfile location for a recipient, degrading to
// the generic form when the home directory or the recipient is unusable: this
// is help text, never a hard failure.
func DisplayKeyfilePath(recipient string) string {
	if path, err := KeyfilePath(recipient); err == nil {
		return path
	}
	return "~/" + KeysDirRel + string(os.PathSeparator) + "<recipient>.key"
}

// IdentityHint names every place LoadIdentity looks, in its own precedence
// order, so the fix does not depend on the reader knowing the lookup rules. It
// is the single source of this wording: the validator, the CLI and the
// onboarding gate all print the same sentence.
func IdentityHint(recipient string) string {
	return fmt.Sprintf("run 'dwe secrets key import' to store the identity at %s, or set %s / %s",
		DisplayKeyfilePath(recipient), EnvKey, EnvKeyFile)
}

// WriteKeyfile stores id in the keys directory and returns the path. The
// directory is created and chmod'ed 0700 (an existing 0755 one is tightened);
// the file is a true no-clobber write (O_CREATE|O_EXCL) at 0600, so an
// existing key is never silently replaced.
func WriteKeyfile(id Identity) (string, error) {
	if id.IsZero() {
		return "", ErrNoIdentity
	}
	path, err := KeyfilePath(id.Recipient())
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create keys dir %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("tighten keys dir %s: %w", dir, err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("keyfile %s already exists", path)
		}
		return "", fmt.Errorf("create keyfile %s: %w", path, err)
	}
	content := fmt.Sprintf("# public key: %s\n%s\n", id.Recipient(), id.Export())
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("write keyfile %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("write keyfile %s: %w", path, err)
	}
	return path, nil
}

// LoadAnyIdentity returns every parsable identity in the keys directory except
// the one for the excluded recipient, sorted by filename. It exists for rekey
// recovery: after an interrupted rekey the tree holds markers under two
// recipients, and status/rekey must be able to open both. A missing directory
// is not an error; an unreadable or malformed keyfile is skipped.
func LoadAnyIdentity(exclude string) ([]Identity, error) {
	dir, err := KeysDir()
	if err != nil {
		return nil, err
	}
	names, err := keyfileNames(dir)
	if err != nil {
		return nil, err
	}

	out := make([]Identity, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		id, err := ParseIdentity(string(data))
		if err != nil {
			continue
		}
		if exclude != "" && id.Recipient() == exclude {
			continue
		}
		out = append(out, id)
	}
	return out, nil
}

// KeyfileState classifies one keyfile for `dwe secrets key list`. The values
// are a fixed enum on purpose: neither an I/O error nor a parse error may reach
// a caller, because the text of both echoes file content.
type KeyfileState string

const (
	// KeyfileOK is a readable keyfile whose identity matches its filename.
	KeyfileOK KeyfileState = "ok"
	// KeyfileUnreadable is a keyfile that could not be read at all.
	KeyfileUnreadable KeyfileState = "unreadable"
	// KeyfileUnparsable is a keyfile that holds no age identity.
	KeyfileUnparsable KeyfileState = "unparsable"
	// KeyfileMisnamed is a keyfile that parses, but whose identity belongs to
	// another recipient than the filename claims. `key remove` targets the
	// canonical <recipient>.key only, so such a file is reported and left.
	KeyfileMisnamed KeyfileState = "misnamed"
)

// KeyfileInfo describes one *.key file in the keys directory. Recipient is the
// PARSED identity's recipient when the file parses, and the filename stem
// otherwise — the filename is the recipient by construction, so the fallback
// reveals nothing the directory listing does not.
type KeyfileInfo struct {
	Path      string
	Recipient string
	State     KeyfileState
}

// ListKeyfiles returns every *.key file in the keys directory, sorted by
// filename. A missing directory is empty, not an error.
func ListKeyfiles() ([]KeyfileInfo, error) {
	dir, err := KeysDir()
	if err != nil {
		return nil, err
	}
	names, err := keyfileNames(dir)
	if err != nil {
		return nil, err
	}

	out := make([]KeyfileInfo, 0, len(names))
	for _, name := range names {
		path := filepath.Join(dir, name)
		info := KeyfileInfo{Path: path, Recipient: strings.TrimSuffix(name, ".key")}
		data, err := os.ReadFile(path)
		switch id, perr := ParseIdentity(string(data)); {
		case err != nil:
			info.State = KeyfileUnreadable
		case perr != nil:
			info.State = KeyfileUnparsable
		case id.Recipient() != info.Recipient:
			info.Recipient = id.Recipient()
			info.State = KeyfileMisnamed
		default:
			info.State = KeyfileOK
		}
		out = append(out, info)
	}
	return out, nil
}

// keyfileNames lists the *.key entries of dir, sorted. A missing directory
// yields no names and no error: the keys directory is created on first import.
func keyfileNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read keys dir %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".key") {
			continue
		}
		names = append(names, e.Name())
	}
	slices.Sort(names)
	return names, nil
}
