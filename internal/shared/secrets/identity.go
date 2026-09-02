package secrets

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

// ParseIdentity reads an identity from key text. Blank lines and comment lines
// (as written by WriteKeyfile and by the age CLI) are skipped; the first key
// line wins.
func ParseIdentity(text string) (Identity, error) {
	line, ok := firstKeyLine(text)
	if !ok {
		return Identity{}, fmt.Errorf("%w: no AGE-SECRET-KEY line found", ErrCorrupt)
	}
	id, err := age.ParseX25519Identity(line)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
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

func firstKeyLine(text string) (string, bool) {
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line, true
	}
	return "", false
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
				return Identity{}, SourceNone, fmt.Errorf("%w: %s points at %s, which does not exist", ErrNoIdentity, EnvKeyFile, path)
			}
			return Identity{}, SourceNone, fmt.Errorf("read %s (%s): %w", path, EnvKeyFile, err)
		}
		return finishLoad(string(data), recipient, SourceEnvFile, EnvKeyFile+" "+path)
	}

	path, err := KeyfilePath(recipient)
	if err != nil {
		return Identity{}, SourceNone, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Identity{}, SourceNone, fmt.Errorf("%w: no keyfile at %s", ErrNoIdentity, path)
		}
		return Identity{}, SourceNone, fmt.Errorf("read keyfile %s: %w", path, err)
	}
	return finishLoad(string(data), recipient, SourceKeyfile, path)
}

func finishLoad(text, recipient string, src Source, label string) (Identity, Source, error) {
	id, err := ParseIdentity(text)
	if err != nil {
		return Identity{}, SourceNone, fmt.Errorf("parse identity from %s: %w", label, err)
	}
	if id.Recipient() != recipient {
		return Identity{}, SourceNone, fmt.Errorf("%w: %s holds the identity for %s, but the project uses %s",
			ErrWrongIdentity, label, id.Recipient(), recipient)
	}
	return id, src, nil
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
