package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	other := testIdentity(t)

	// The shapes a developer can realistically hand to `key import`: an
	// age-keygen file, a hand-typed line, a CRLF file from a Windows editor,
	// and a whole keyfile pasted into a single-line field (which joins the
	// comment and the key onto one line — the case the old line scanner ate).
	cases := []struct {
		name string
		text string
		want string
	}{
		{"keyfile with comments", fmt.Sprintf("# created: whenever\n# public key: %s\n\n%s\n", id.Recipient(), id.Export()), id.Recipient()},
		{"bare key", id.Export(), id.Recipient()},
		{"joined paste", fmt.Sprintf("# public key: %s %s", id.Recipient(), id.Export()), id.Recipient()},
		{"crlf", fmt.Sprintf("# public key: %s\r\n%s\r\n", id.Recipient(), id.Export()), id.Recipient()},
		{"surrounding whitespace", "  \n\t" + id.Export() + " \n ", id.Recipient()},
		{"multi-identity keyfile: first wins", id.Export() + "\n" + other.Export() + "\n", id.Recipient()},
		{"commented-out old key above the live one: first token wins",
			fmt.Sprintf("# old: %s\n%s\n", id.Export(), other.Export()), id.Recipient()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := ParseIdentity(tc.text)
			if err != nil {
				t.Fatalf("ParseIdentity: %v", err)
			}
			if parsed.Recipient() != tc.want {
				t.Fatalf("ParseIdentity recipient = %q, want %q", parsed.Recipient(), tc.want)
			}
		})
	}

	bad := []string{"", "# only a comment\n", "AGE-SECRET-KEY-1NONSENSE", "hello",
		strings.Repeat("A", 20) + id.Export()[len(id.Export())-20:]}
	for _, text := range bad {
		_, err := ParseIdentity(text)
		if !errors.Is(err, ErrInvalidIdentity) {
			t.Fatalf("ParseIdentity(%q) = %v, want ErrInvalidIdentity", text, err)
		}
		if errors.Is(err, ErrCorrupt) {
			t.Fatalf("ParseIdentity(%q): a bad identity must not be reported as a corrupt payload: %v", text, err)
		}
		if tail := text[max(0, len(text)-20):]; tail != "" && strings.Contains(err.Error(), tail) {
			t.Fatalf("ParseIdentity error echoes its input: %v", err)
		}
	}

	// A token with a broken checksum reaches age and must still come back as
	// ErrInvalidIdentity, with no age text (it interpolates input characters).
	truncated := id.Export()[:len(id.Export())-1] + "Q"
	_, err := ParseIdentity(truncated)
	if !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("ParseIdentity(checksum-broken) = %v, want ErrInvalidIdentity", err)
	}
	if strings.Contains(err.Error(), truncated[len(truncated)-20:]) {
		t.Fatalf("ParseIdentity error echoes its input: %v", err)
	}
}

func TestListKeyfiles(t *testing.T) {
	t.Run("missing dir is empty", func(t *testing.T) {
		isolateHome(t)
		infos, err := ListKeyfiles()
		if err != nil {
			t.Fatalf("ListKeyfiles: %v", err)
		}
		if len(infos) != 0 {
			t.Fatalf("got %d keyfiles, want 0", len(infos))
		}
	})

	t.Run("classifies every file", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("running as root: permission bits are not enforced")
		}
		home := isolateHome(t)
		good := testIdentity(t)
		if _, err := WriteKeyfile(good); err != nil {
			t.Fatalf("WriteKeyfile: %v", err)
		}
		keysDir := filepath.Join(home, ".config", "dwe", "keys")

		junk := "definitely-not-a-key-0123456789"
		write := func(name, content string, mode os.FileMode) string {
			t.Helper()
			path := filepath.Join(keysDir, name)
			if err := os.WriteFile(path, []byte(content), mode); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
			return path
		}
		unreadable := write("age1aaa.key", "whatever", 0o000)
		t.Cleanup(func() { _ = os.Chmod(unreadable, 0o600) })
		write("age1bbb.key", junk, 0o600)
		misnamed := testIdentity(t)
		write("age1ccc.key", misnamed.Export()+"\n", 0o600)
		write("notes.txt", "ignored", 0o600)

		infos, err := ListKeyfiles()
		if err != nil {
			t.Fatalf("ListKeyfiles: %v", err)
		}
		want := map[string]KeyfileState{
			"age1aaa":            KeyfileUnreadable,
			"age1bbb":            KeyfileUnparsable,
			misnamed.Recipient(): KeyfileMisnamed,
			good.Recipient():     KeyfileOK,
		}
		if len(infos) != len(want) {
			t.Fatalf("got %d keyfiles, want %d: %+v", len(infos), len(want), infos)
		}
		var names []string
		for _, info := range infos {
			names = append(names, filepath.Base(info.Path))
			state, ok := want[info.Recipient]
			if !ok {
				t.Fatalf("unexpected recipient %q in %+v", info.Recipient, info)
			}
			if info.State != state {
				t.Fatalf("recipient %q: state = %q, want %q", info.Recipient, info.State, state)
			}
		}
		if !slices.IsSorted(names) {
			t.Fatalf("ListKeyfiles is not sorted by filename: %v", names)
		}
		// The unparsable file's content is never carried out of the scan.
		if strings.Contains(fmt.Sprintf("%+v", infos), junk) {
			t.Fatalf("ListKeyfiles leaks file content: %+v", infos)
		}
	})
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
		if _, _, err := LoadIdentity(id.Recipient()); !errors.Is(err, ErrInvalidIdentity) {
			t.Fatalf("LoadIdentity = %v, want ErrInvalidIdentity", err)
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
