package secrets

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/shared/lock"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// writePlainFile drops a plaintext file into the project and returns its path.
func writePlainFile(t *testing.T, root, rel, body string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating dir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s: %v", rel, err)
	}
	return path
}

// fileMode is the permission bits of path, failing the test when it is absent.
func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.Mode().Perm()
}

// TestEncryptDecrypt_RoundTripOnDisk pins the pair over the default output
// names: encrypt appends .age and leaves ciphertext, decrypt strips it again and
// writes the plaintext back at 0600.
func TestEncryptDecrypt_RoundTripOnDisk(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	initProject(t, flags)

	const plain = `{"client_email":"bot@example.com"}`
	src := writePlainFile(t, root, "workspace/templates/config/bot/creds.json", plain)

	if _, _, err := runSecrets(t, flags, "encrypt", src); err != nil {
		t.Fatalf("secrets encrypt: %v", err)
	}
	enc := src + ageExt
	data, err := os.ReadFile(enc)
	if err != nil {
		t.Fatalf("reading the encrypted file: %v", err)
	}
	if bytes.Contains(data, []byte("bot@example.com")) {
		t.Errorf("the encrypted file holds the plaintext:\n%s", data)
	}
	if !bytes.HasPrefix(data, []byte("age-encryption.org/v1")) {
		t.Errorf("the output is not a native age file: %q", data[:min(len(data), 40)])
	}
	if got := fileMode(t, enc); got != ciphertextMode {
		t.Errorf("ciphertext mode = %v, want %v (it is committed)", got, ciphertextMode)
	}

	// Decrypting back would collide with the original, so move it out of the way
	// first: the default target is exactly the name we started from.
	if err := os.Remove(src); err != nil {
		t.Fatalf("removing the source: %v", err)
	}
	if _, _, err := runSecrets(t, flags, "decrypt", enc); err != nil {
		t.Fatalf("secrets decrypt: %v", err)
	}
	back, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("reading the decrypted file: %v", err)
	}
	if string(back) != plain {
		t.Errorf("round trip = %q, want %q", back, plain)
	}
	if got := fileMode(t, src); got != plaintextMode {
		t.Errorf("plaintext mode = %v, want %v", got, plaintextMode)
	}
}

// TestEncryptDecrypt_ExplicitOut pins --out on both commands, including that the
// reported paths are project-relative.
func TestEncryptDecrypt_ExplicitOut(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	initProject(t, flags)

	src := writePlainFile(t, root, "creds.json", "hello")
	enc := filepath.Join(root, "workspace", "templates", "config", "bot", "creds.json.age")
	if err := os.MkdirAll(filepath.Dir(enc), 0o755); err != nil {
		t.Fatalf("creating pack dir: %v", err)
	}

	out, _, err := runSecrets(t, flags, "encrypt", src, "--out", enc)
	if err != nil {
		t.Fatalf("secrets encrypt --out: %v", err)
	}
	if !strings.Contains(out, filepath.Join("workspace", "templates", "config", "bot", "creds.json.age")) {
		t.Errorf("output does not name the target relative to the project root\ngot: %s", out)
	}

	dst := filepath.Join(root, "restored.json")
	if _, _, err := runSecrets(t, flags, "decrypt", enc, "--out", dst); err != nil {
		t.Fatalf("secrets decrypt --out: %v", err)
	}
	back, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("reading the decrypted file: %v", err)
	}
	if string(back) != "hello" {
		t.Errorf("decrypted %q, want %q", back, "hello")
	}
}

// TestFiles_OverwriteRefusedWithoutForce pins the no-clobber default and that
// --force lifts it — and that only the plaintext side is chmod'ed, so an
// overwritten ciphertext file keeps the mode the repository gave it.
func TestFiles_OverwriteRefusedWithoutForce(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	initProject(t, flags)

	src := writePlainFile(t, root, "creds.json", "hello")
	enc := src + ageExt
	if err := os.WriteFile(enc, []byte("stale"), 0o640); err != nil {
		t.Fatalf("writing the pre-existing output: %v", err)
	}

	_, _, err := runSecrets(t, flags, "encrypt", src)
	if err == nil {
		t.Fatal("encrypt overwrote an existing file without --force")
	}
	coded := codedError(t, err)
	if coded.Code != "secrets_output_exists" {
		t.Errorf("error code = %q, want secrets_output_exists", coded.Code)
	}
	if !strings.Contains(coded.Hint, "--force") {
		t.Errorf("hint = %q, want it to name --force", coded.Hint)
	}
	if body, rerr := os.ReadFile(enc); rerr != nil || string(body) != "stale" {
		t.Errorf("the refused write touched the file: %q (%v)", body, rerr)
	}

	if _, _, err := runSecrets(t, flags, "encrypt", src, "--force"); err != nil {
		t.Fatalf("secrets encrypt --force: %v", err)
	}
	if got := fileMode(t, enc); got != 0o640 {
		t.Errorf("overwritten ciphertext mode = %v, want the file's own 0640 kept", got)
	}

	// The plaintext side is the opposite rule: a permissive existing target is
	// tightened rather than preserved.
	loose := writePlainFile(t, root, "restored.json", "placeholder")
	if _, _, err := runSecrets(t, flags, "decrypt", enc, "--out", loose, "--force"); err != nil {
		t.Fatalf("secrets decrypt --force: %v", err)
	}
	if got := fileMode(t, loose); got != plaintextMode {
		t.Errorf("overwritten plaintext mode = %v, want it tightened to %v", got, plaintextMode)
	}
}

// TestFiles_SymlinkRefused pins the path discipline on both ends: a symlinked
// input is not read and a symlinked output is not written through — that is how
// a "decrypt to a scratch file" would otherwise clobber whatever it points at.
func TestFiles_SymlinkRefused(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	initProject(t, flags)

	real := writePlainFile(t, root, "creds.json", "hello")
	link := filepath.Join(root, "link.json")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	victim := writePlainFile(t, root, "victim.txt", "do not touch")
	outLink := filepath.Join(root, "out.age")
	if err := os.Symlink(victim, outLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	t.Run("input", func(t *testing.T) {
		_, _, err := runSecrets(t, flags, "encrypt", link)
		if err == nil {
			t.Fatal("encrypt followed a symlinked input")
		}
		if code := codedError(t, err).Code; code != "secrets_path_invalid" {
			t.Errorf("error code = %q, want secrets_path_invalid", code)
		}
	})

	t.Run("output", func(t *testing.T) {
		_, _, err := runSecrets(t, flags, "encrypt", real, "--out", outLink, "--force")
		if err == nil {
			t.Fatal("encrypt wrote through a symlinked output")
		}
		if code := codedError(t, err).Code; code != "secrets_path_invalid" {
			t.Errorf("error code = %q, want secrets_path_invalid", code)
		}
		if body, rerr := os.ReadFile(victim); rerr != nil || string(body) != "do not touch" {
			t.Errorf("the symlink target was written through: %q (%v)", body, rerr)
		}
	})
}

// TestDecrypt_NeedsOutForNonAgeInput pins that the default output name is only
// derived by stripping .age; anything else must say where it should go.
func TestDecrypt_NeedsOutForNonAgeInput(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	data, err := secrets.EncryptBytes([]byte("hello"), recipient)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	path := filepath.Join(root, "creds.enc")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	_, _, err = runSecrets(t, flags, "decrypt", path)
	if err == nil {
		t.Fatal("decrypt invented an output name for a non-.age input")
	}
	coded := codedError(t, err)
	if coded.Code != "secrets_output_required" {
		t.Errorf("error code = %q, want secrets_output_required", coded.Code)
	}
	if !strings.Contains(coded.Hint, "--out") {
		t.Errorf("hint = %q, want it to name --out", coded.Hint)
	}
}

// TestFiles_StdoutStream pins --out -: the raw bytes and nothing else in text
// mode, and a typed refusal in JSON mode where the two would share stdout.
func TestFiles_StdoutStream(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	data, err := secrets.EncryptBytes([]byte("payload-bytes"), recipient)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	enc := filepath.Join(root, "creds.json.age")
	if err := os.WriteFile(enc, data, 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	out, errOut, err := runSecrets(t, flags, "decrypt", enc, "--out", stdoutTarget)
	if err != nil {
		t.Fatalf("secrets decrypt --out -: %v", err)
	}
	if out != "payload-bytes" {
		t.Errorf("stdout = %q, want exactly the plaintext with no status line", out)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want it empty", errOut)
	}
	if _, err := os.Stat(filepath.Join(root, "creds.json")); err == nil {
		t.Error("--out - also wrote the default output file")
	}

	jsonFlags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}
	out, _, err = runSecrets(t, jsonFlags, "decrypt", enc, "--out", stdoutTarget)
	if err == nil {
		t.Fatal("--out - was accepted together with --output json")
	}
	if code := codedError(t, err).Code; code != "secrets_raw_stream" {
		t.Errorf("error code = %q, want secrets_raw_stream", code)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing written before the refusal", out)
	}
}

// TestDecrypt_NoIdentity pins the keyless machine on the file path: the failure
// names where the lookup looked and writes no output file.
func TestDecrypt_NoIdentity(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	enc := writeAgeFile(t, root, "bot/creds.json.age", recipient, "hello")
	keyfile, err := secrets.KeyfilePath(recipient)
	if err != nil {
		t.Fatalf("keyfile path: %v", err)
	}
	if err := os.Remove(keyfile); err != nil {
		t.Fatalf("removing keyfile: %v", err)
	}

	_, _, err = runSecrets(t, flags, "decrypt", enc)
	if err == nil {
		t.Fatal("decrypt succeeded with no identity on this machine")
	}
	coded := codedError(t, err)
	if coded.Code != "secrets_no_identity" {
		t.Fatalf("error code = %q, want secrets_no_identity", coded.Code)
	}
	if !strings.Contains(coded.Hint, "key import") {
		t.Errorf("hint = %q, want it to point at 'dwe secrets key import'", coded.Hint)
	}
	if _, serr := os.Stat(strings.TrimSuffix(enc, ageExt)); serr == nil {
		t.Error("a failed decrypt still created the output file")
	}
}

// TestEncrypt_NoRecipient pins that a project without a key pair is sent to
// `init` rather than failing inside the crypto layer.
func TestEncrypt_NoRecipient(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	src := writePlainFile(t, root, "creds.json", "hello")
	_, _, err := runSecrets(t, flags, "encrypt", src)
	if err == nil {
		t.Fatal("encrypt succeeded without a configured recipient")
	}
	coded := codedError(t, err)
	if coded.Code != "secrets_no_recipient" {
		t.Errorf("error code = %q, want secrets_no_recipient", coded.Code)
	}
	if !strings.Contains(coded.Hint, "secrets init") {
		t.Errorf("hint = %q, want it to point at 'dwe secrets init'", coded.Hint)
	}
}

// TestEncrypt_ReadsRecipientUnderTheLock pins the ordering `set` also keeps: the
// recipient is read AFTER the project locks, so a `rekey` finishing in between
// cannot leave the new file encrypted to the retired recipient. Held locks are
// therefore reported before the missing recipient is — reverse the two and this
// project (which has no recipient) answers secrets_no_recipient instead.
func TestEncrypt_ReadsRecipientUnderTheLock(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	release, err := lock.AcquireProjectLocks(root)
	if err != nil {
		t.Fatalf("holding the project locks: %v", err)
	}
	defer release()

	src := writePlainFile(t, root, "creds.json", "hello")
	if _, _, err := runSecrets(t, flags, "encrypt", src); !errors.As(err, new(*lock.ProjectLockHeldError)) {
		t.Fatalf("encrypt error = %v, want a held-lock refusal taken before the recipient read", err)
	}
}

// TestFiles_JSON pins the DTO of both commands.
func TestFiles_JSON(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}
	initProject(t, flags)

	src := writePlainFile(t, root, "creds.json", "hello")
	out, errOut, err := runSecrets(t, flags, "encrypt", src)
	if err != nil {
		t.Fatalf("secrets encrypt --output json: %v", err)
	}
	if errOut != "" {
		t.Errorf("stderr should be empty in JSON mode, got: %q", errOut)
	}
	var payload secretFileJSON
	if e := json.Unmarshal([]byte(out), &payload); e != nil {
		t.Fatalf("unmarshal encrypt json: %v\nraw: %s", e, out)
	}
	if payload.From != "creds.json" || payload.To != "creds.json.age" {
		t.Errorf("payload = %+v, want project-relative {creds.json creds.json.age}", payload)
	}

	if err := os.Remove(src); err != nil {
		t.Fatalf("removing the source: %v", err)
	}
	out, _, err = runSecrets(t, flags, "decrypt", src+ageExt)
	if err != nil {
		t.Fatalf("secrets decrypt --output json: %v", err)
	}
	if e := json.Unmarshal([]byte(out), &payload); e != nil {
		t.Fatalf("unmarshal decrypt json: %v\nraw: %s", e, out)
	}
	if payload.From != "creds.json.age" || payload.To != "creds.json" {
		t.Errorf("payload = %+v, want {creds.json.age creds.json}", payload)
	}
	if strings.Contains(out, "AGE-SECRET-KEY-") || strings.Contains(out, "hello") {
		t.Errorf("the DTO leaked key material or the plaintext: %s", out)
	}
}
