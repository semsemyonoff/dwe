package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// isolateHome points the keys directory at a temp dir and clears both env
// overrides, so no test can read or write the developer's ~/.config.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	t.Setenv(EnvKey, "")
	t.Setenv(EnvKeyFile, "")
	return home
}

func TestIdentityNeverPrintsThePrivateKey(t *testing.T) {
	id := testIdentity(t)
	secret := id.Export()
	if !strings.HasPrefix(secret, "AGE-SECRET-KEY-") {
		t.Fatalf("Export did not return a private key: %q", secret)
	}

	type wrapper struct {
		ID    Identity
		Label string
	}
	rendered := []string{
		id.String(),
		fmt.Sprintf("%v", id),
		fmt.Sprintf("%s|%v", id, id), // %s must go through String(), not the wrapped key
		fmt.Sprintf("%+v", id),
		fmt.Sprintf("%v", wrapper{ID: id, Label: "x"}),
		fmt.Sprintf("%+v", wrapper{ID: id, Label: "x"}),
		fmt.Sprintf("%v", &id),
		fmt.Sprintf("%v", []Identity{id}),
		fmt.Sprintf("%v", map[string]Identity{"k": id}),
	}
	for _, s := range rendered {
		if strings.Contains(s, "AGE-SECRET-KEY-") {
			t.Fatalf("formatted identity leaks the private key: %q", s)
		}
		if strings.Contains(s, secret) {
			t.Fatalf("formatted identity leaks the private key: %q", s)
		}
	}
	if !strings.Contains(id.String(), id.Recipient()) {
		t.Fatalf("String() should name the recipient, got %q", id.String())
	}
}

func TestZeroIdentity(t *testing.T) {
	var zero Identity
	if !zero.IsZero() {
		t.Fatalf("zero value: IsZero() = false")
	}
	if zero.Recipient() != "" || zero.Export() != "" {
		t.Fatalf("zero value leaks: recipient %q export %q", zero.Recipient(), zero.Export())
	}
	if got := zero.String(); got != "age identity (none)" {
		t.Fatalf("zero String() = %q", got)
	}
	if _, err := WriteKeyfile(zero); !errors.Is(err, ErrNoIdentity) {
		t.Fatalf("WriteKeyfile(zero) = %v, want ErrNoIdentity", err)
	}
}

func TestParseIdentity(t *testing.T) {
	id := testIdentity(t)
	withComments := fmt.Sprintf("# created: whenever\n# public key: %s\n\n%s\n", id.Recipient(), id.Export())

	parsed, err := ParseIdentity(withComments)
	if err != nil {
		t.Fatalf("ParseIdentity: %v", err)
	}
	if parsed.Recipient() != id.Recipient() {
		t.Fatalf("ParseIdentity recipient = %q, want %q", parsed.Recipient(), id.Recipient())
	}

	for _, bad := range []string{"", "# only a comment\n", "AGE-SECRET-KEY-1NONSENSE", "hello"} {
		if _, err := ParseIdentity(bad); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("ParseIdentity(%q) = %v, want ErrCorrupt", bad, err)
		}
	}
}

func TestParseRecipient(t *testing.T) {
	id := testIdentity(t)
	if err := ParseRecipient(id.Recipient()); err != nil {
		t.Fatalf("ParseRecipient(%q): %v", id.Recipient(), err)
	}
	for _, bad := range []string{"", "age1", "not-a-key", id.Export()} {
		if err := ParseRecipient(bad); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("ParseRecipient(%q) = %v, want ErrCorrupt", bad, err)
		}
	}
}

func TestKeyfilePath(t *testing.T) {
	home := isolateHome(t)
	id := testIdentity(t)

	path, err := KeyfilePath(id.Recipient())
	if err != nil {
		t.Fatalf("KeyfilePath: %v", err)
	}
	want := filepath.Join(home, ".config", "dwe", "keys", id.Recipient()+".key")
	if path != want {
		t.Fatalf("KeyfilePath = %q, want %q", path, want)
	}

	// A malformed recipient becomes a path element: it must be rejected before
	// it reaches the filesystem.
	for _, bad := range []string{"", "../../etc/passwd", "age1"} {
		if _, err := KeyfilePath(bad); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("KeyfilePath(%q) = %v, want ErrCorrupt", bad, err)
		}
	}
}

func TestWriteKeyfileModesAndNoClobber(t *testing.T) {
	home := isolateHome(t)
	keysDir := filepath.Join(home, ".config", "dwe", "keys")
	// Pre-create the directory world-readable: WriteKeyfile must tighten it.
	if err := os.MkdirAll(keysDir, 0o755); err != nil {
		t.Fatalf("pre-create keys dir: %v", err)
	}

	id := testIdentity(t)
	path, err := WriteKeyfile(id)
	if err != nil {
		t.Fatalf("WriteKeyfile: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat keyfile: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("keyfile mode = %04o, want 0600", perm)
	}
	di, err := os.Stat(keysDir)
	if err != nil {
		t.Fatalf("stat keys dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Fatalf("keys dir mode = %04o, want 0700", perm)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read keyfile: %v", err)
	}
	back, err := ParseIdentity(string(data))
	if err != nil {
		t.Fatalf("ParseIdentity on the written keyfile: %v", err)
	}
	if back.Recipient() != id.Recipient() {
		t.Fatalf("keyfile round trip: %q != %q", back.Recipient(), id.Recipient())
	}

	if _, err := WriteKeyfile(id); err == nil {
		t.Fatalf("WriteKeyfile over an existing keyfile: expected refusal")
	}
}

func TestWriteKeyfileConcurrentWritersProduceOneFile(t *testing.T) {
	isolateHome(t)
	id := testIdentity(t)

	const n = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			if _, err := WriteKeyfile(id); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if ok != 1 {
		t.Fatalf("%d concurrent WriteKeyfile calls succeeded, want exactly 1", ok)
	}
}

func TestLoadIdentityPrecedenceAndMismatch(t *testing.T) {
	t.Run("env wins and must match", func(t *testing.T) {
		isolateHome(t)
		id := testIdentity(t)
		other := testIdentity(t)
		// A keyfile for `id` exists, but DWE_AGE_KEY holds another identity:
		// the env value must be used and must fail, not fall through.
		if _, err := WriteKeyfile(id); err != nil {
			t.Fatalf("WriteKeyfile: %v", err)
		}
		t.Setenv(EnvKey, other.Export())
		if _, _, err := LoadIdentity(id.Recipient()); !errors.Is(err, ErrWrongIdentity) {
			t.Fatalf("LoadIdentity = %v, want ErrWrongIdentity", err)
		}

		t.Setenv(EnvKey, id.Export())
		got, src, err := LoadIdentity(id.Recipient())
		if err != nil {
			t.Fatalf("LoadIdentity: %v", err)
		}
		if src != SourceEnv {
			t.Fatalf("source = %q, want %q", src, SourceEnv)
		}
		if got.Recipient() != id.Recipient() {
			t.Fatalf("recipient = %q, want %q", got.Recipient(), id.Recipient())
		}
	})

	t.Run("env file second", func(t *testing.T) {
		isolateHome(t)
		id := testIdentity(t)
		keyPath := filepath.Join(t.TempDir(), "ci.key")
		if err := os.WriteFile(keyPath, []byte(id.Export()+"\n"), 0o600); err != nil {
			t.Fatalf("write env keyfile: %v", err)
		}
		t.Setenv(EnvKeyFile, keyPath)

		got, src, err := LoadIdentity(id.Recipient())
		if err != nil {
			t.Fatalf("LoadIdentity: %v", err)
		}
		if src != SourceEnvFile {
			t.Fatalf("source = %q, want %q", src, SourceEnvFile)
		}
		if got.Recipient() != id.Recipient() {
			t.Fatalf("recipient = %q, want %q", got.Recipient(), id.Recipient())
		}

		// Mismatch is reported, not skipped, and the message names the source.
		other := testIdentity(t)
		_, _, err = LoadIdentity(other.Recipient())
		if !errors.Is(err, ErrWrongIdentity) {
			t.Fatalf("LoadIdentity mismatch = %v, want ErrWrongIdentity", err)
		}
		if !strings.Contains(err.Error(), EnvKeyFile) {
			t.Fatalf("error should name %s: %v", EnvKeyFile, err)
		}
	})

	t.Run("keyfile last", func(t *testing.T) {
		isolateHome(t)
		id := testIdentity(t)
		path, err := WriteKeyfile(id)
		if err != nil {
			t.Fatalf("WriteKeyfile: %v", err)
		}
		got, src, err := LoadIdentity(id.Recipient())
		if err != nil {
			t.Fatalf("LoadIdentity: %v", err)
		}
		if src != SourceKeyfile {
			t.Fatalf("source = %q, want %q", src, SourceKeyfile)
		}
		if got.Recipient() != id.Recipient() {
			t.Fatalf("recipient = %q, want %q", got.Recipient(), id.Recipient())
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("stat keyfile: %v", err)
		}
	})
}

func TestLoadIdentityMissingSources(t *testing.T) {
	t.Run("empty recipient", func(t *testing.T) {
		isolateHome(t)
		_, src, err := LoadIdentity("")
		if !errors.Is(err, ErrNoIdentity) {
			t.Fatalf("LoadIdentity(\"\") = %v, want ErrNoIdentity", err)
		}
		if src != SourceNone {
			t.Fatalf("source = %q, want none", src)
		}
	})

	t.Run("no keyfile", func(t *testing.T) {
		isolateHome(t)
		id := testIdentity(t)
		if _, _, err := LoadIdentity(id.Recipient()); !errors.Is(err, ErrNoIdentity) {
			t.Fatalf("LoadIdentity = %v, want ErrNoIdentity", err)
		}
	})

	t.Run("env file missing", func(t *testing.T) {
		isolateHome(t)
		id := testIdentity(t)
		t.Setenv(EnvKeyFile, filepath.Join(t.TempDir(), "nope.key"))
		_, _, err := LoadIdentity(id.Recipient())
		if !errors.Is(err, ErrNoIdentity) {
			t.Fatalf("LoadIdentity = %v, want ErrNoIdentity", err)
		}
		if !strings.Contains(err.Error(), EnvKeyFile) {
			t.Fatalf("error should name %s: %v", EnvKeyFile, err)
		}
	})

	t.Run("unreadable keyfile is not hidden", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("running as root: permission bits are not enforced")
		}
		isolateHome(t)
		id := testIdentity(t)
		path, err := WriteKeyfile(id)
		if err != nil {
			t.Fatalf("WriteKeyfile: %v", err)
		}
		if err := os.Chmod(path, 0o000); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

		_, _, err = LoadIdentity(id.Recipient())
		if err == nil {
			t.Fatalf("LoadIdentity on an unreadable keyfile: expected error")
		}
		if errors.Is(err, ErrNoIdentity) {
			t.Fatalf("a permission error must not be reported as ErrNoIdentity: %v", err)
		}
	})

	t.Run("corrupt keyfile", func(t *testing.T) {
		isolateHome(t)
		id := testIdentity(t)
		path, err := KeyfilePath(id.Recipient())
		if err != nil {
			t.Fatalf("KeyfilePath: %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte("garbage\n"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, _, err := LoadIdentity(id.Recipient()); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("LoadIdentity = %v, want ErrCorrupt", err)
		}
	})
}

func TestLoadAnyIdentity(t *testing.T) {
	t.Run("missing dir is empty", func(t *testing.T) {
		isolateHome(t)
		ids, err := LoadAnyIdentity("")
		if err != nil {
			t.Fatalf("LoadAnyIdentity: %v", err)
		}
		if len(ids) != 0 {
			t.Fatalf("got %d identities, want 0", len(ids))
		}
	})

	t.Run("excludes the named recipient and skips junk", func(t *testing.T) {
		home := isolateHome(t)
		a := testIdentity(t)
		b := testIdentity(t)
		for _, id := range []Identity{a, b} {
			if _, err := WriteKeyfile(id); err != nil {
				t.Fatalf("WriteKeyfile: %v", err)
			}
		}
		keysDir := filepath.Join(home, ".config", "dwe", "keys")
		if err := os.WriteFile(filepath.Join(keysDir, "broken.key"), []byte("not a key"), 0o600); err != nil {
			t.Fatalf("write junk: %v", err)
		}
		if err := os.WriteFile(filepath.Join(keysDir, "notes.txt"), []byte("ignored"), 0o600); err != nil {
			t.Fatalf("write non-key: %v", err)
		}

		ids, err := LoadAnyIdentity(a.Recipient())
		if err != nil {
			t.Fatalf("LoadAnyIdentity: %v", err)
		}
		if len(ids) != 1 || ids[0].Recipient() != b.Recipient() {
			got := make([]string, len(ids))
			for i, id := range ids {
				got[i] = id.Recipient()
			}
			t.Fatalf("LoadAnyIdentity = %v, want [%s]", got, b.Recipient())
		}

		all, err := LoadAnyIdentity("")
		if err != nil {
			t.Fatalf("LoadAnyIdentity(\"\"): %v", err)
		}
		if len(all) != 2 {
			t.Fatalf("got %d identities, want 2", len(all))
		}
	})
}
