package secrets

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// --- fixtures ---------------------------------------------------------------

const fixtureWorkspace = `schema_version: "2"
project:
  name: sectest
  prefix: dwe
vars:
  app:
    name: myapp
`

// writeFixture writes a minimal project (workspace.yml only) and returns the
// workspace.yml path plus the project root.
func writeFixture(t *testing.T) (cfgPath, root string) {
	t.Helper()
	root = t.TempDir()
	cfgPath = filepath.Join(root, "workspace.yml")
	if err := os.WriteFile(cfgPath, []byte(fixtureWorkspace), 0o644); err != nil {
		t.Fatalf("writing workspace.yml: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "workspace"), 0o755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	return cfgPath, root
}

// readTestdata loads an annotated fixture from testdata/.
func readTestdata(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}
	return string(data)
}

// changedLines returns the 1-based numbers of the lines that differ between
// before and after. The whole point of the splice writer is that a `secrets`
// write replaces the bytes of ONE value token, so a changed line count is itself
// a failure: it means the document was re-encoded rather than spliced.
func changedLines(t *testing.T, before, after string) []int {
	t.Helper()
	b := strings.Split(before, "\n")
	a := strings.Split(after, "\n")
	if len(b) != len(a) {
		t.Fatalf("line count changed: %d -> %d\n--- before ---\n%s\n--- after ---\n%s", len(b), len(a), before, after)
	}
	var out []int
	for i := range b {
		if b[i] != a[i] {
			out = append(out, i+1)
		}
	}
	return out
}

// assertOnlyLinesChanged pins that exactly the given 1-based lines differ, every
// other byte of the file being identical.
func assertOnlyLinesChanged(t *testing.T, before, after string, want ...int) {
	t.Helper()
	got := changedLines(t, before, after)
	if !slices.Equal(got, want) {
		t.Fatalf("changed lines = %v, want %v\n--- after ---\n%s", got, want, after)
	}
}

// insertedLines pins that after is before with a run of new lines spliced in:
// the bytes before the insertion and the bytes after it are taken verbatim from
// before. An index-wise line compare cannot be used for an insertion — every
// following line shifts — so the prefix/suffix equality IS the assertion. It
// returns the inserted lines.
func insertedLines(t *testing.T, before, after string) []string {
	t.Helper()
	b := strings.Split(before, "\n")
	a := strings.Split(after, "\n")
	if len(a) <= len(b) {
		t.Fatalf("expected lines to be inserted, got %d -> %d\n--- after ---\n%s", len(b), len(a), after)
	}
	prefix := 0
	for prefix < len(b) && b[prefix] == a[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(b)-prefix && b[len(b)-1-suffix] == a[len(a)-1-suffix] {
		suffix++
	}
	if prefix+suffix != len(b) {
		t.Fatalf("the file changed outside the insertion (matched %d leading + %d trailing of %d lines)\n--- before ---\n%s\n--- after ---\n%s",
			prefix, suffix, len(b), before, after)
	}
	return a[prefix : len(a)-suffix]
}

// assertNoSecretLeak pins that neither a plaintext nor any private key material
// reached a user-facing surface.
func assertNoSecretLeak(t *testing.T, plaintext string, surfaces ...string) {
	t.Helper()
	for _, s := range surfaces {
		if plaintext != "" && strings.Contains(s, plaintext) {
			t.Errorf("output leaked the plaintext %q:\n%s", plaintext, s)
		}
		if strings.Contains(s, "AGE-SECRET-KEY-") {
			t.Errorf("output leaked private key material:\n%s", s)
		}
	}
}

// isolateHome points HOME at a temp dir so no test can ever read or write the
// developer's real ~/.config/dwe/keys, and clears the env overrides so a
// developer running the suite with DWE_AGE_KEY set does not change the outcome.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(secrets.EnvKey, "")
	t.Setenv(secrets.EnvKeyFile, "")
	return home
}

// runSecrets executes the secrets command tree against the fixture, returning
// stdout, stderr and the command error. The tree is its own root here (the real
// cli.NewRootCmd wiring is covered by the cli package), which avoids the
// cli→secrets import cycle while still exercising the full RunE.
func runSecrets(t *testing.T, flags *cmdctx.RootFlags, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := NewCmd("", flags)
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetIn(bytes.NewReader(nil))
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// stubStdoutTTY pins the stdout tty probe for one test and restores the
// production one afterwards. Package-level state: callers MUST NOT run in
// parallel.
func stubStdoutTTY(t *testing.T, isTTY bool) {
	t.Helper()
	prev := stdoutIsTerminal
	stdoutIsTerminal = func() bool { return isTTY }
	t.Cleanup(func() { stdoutIsTerminal = prev })
}

// codedError extracts the typed CLI error, failing the test when err is not
// one: every user-facing failure in this tree must carry a machine-readable
// code for --output json.
func codedError(t *testing.T, err error) *cmdctx.CodedError {
	t.Helper()
	var ce *cmdctx.CodedError
	if !errors.As(err, &ce) {
		t.Fatalf("error %v (%T) is not a *cmdctx.CodedError", err, err)
	}
	return ce
}

// initProject mints a key pair for the fixture and returns the recipient.
func initProject(t *testing.T, flags *cmdctx.RootFlags) string {
	t.Helper()
	if _, _, err := runSecrets(t, flags, "init"); err != nil {
		t.Fatalf("secrets init: %v", err)
	}
	recipient, err := requireRecipient(flags)
	if err != nil {
		t.Fatalf("reading recipient back: %v", err)
	}
	return recipient
}

// writeAgeFile encrypts data to recipient and stores it as a config-pack source.
func writeAgeFile(t *testing.T, root, rel, recipient, plain string) string {
	t.Helper()
	path := filepath.Join(root, "workspace", "templates", "config", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating pack dir: %v", err)
	}
	data, err := secrets.EncryptBytes([]byte(plain), recipient)
	if err != nil {
		t.Fatalf("encrypting %s: %v", rel, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing %s: %v", rel, err)
	}
	return path
}

// --- inventory --------------------------------------------------------------

// TestCollectInventory_MarkersAndFiles pins the inventory over a project that
// holds a decrypted marker, a foreign-recipient marker, a readable .age source
// and one encrypted to somebody else.
func TestCollectInventory_MarkersAndFiles(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	// A second key pair nobody has the identity for: its values must classify as
	// wrong_identity, not as "no key on this machine".
	foreign, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	mine, err := secrets.Encrypt("s3cret-token", recipient)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	theirs, err := secrets.Encrypt("not-for-me", foreign.Recipient())
	if err != nil {
		t.Fatalf("encrypt foreign: %v", err)
	}
	// Valid marker syntax and valid base64, but the payload is not an age file:
	// damaged, and visible as such without any key.
	corrupt := secrets.MarkerPrefix + base64.StdEncoding.EncodeToString([]byte("garbage")) + "]"
	defaults := "vars:\n  a_token: " + mine + "\n  b_token: " + theirs + "\n  c_token: " + corrupt + "\n"
	if err := os.WriteFile(filepath.Join(root, "workspace", "defaults.yml"), []byte(defaults), 0o644); err != nil {
		t.Fatalf("writing defaults.yml: %v", err)
	}

	writeAgeFile(t, root, "app/creds.json.age", recipient, `{"ok":true}`)
	writeAgeFile(t, root, "app/foreign.env.age", foreign.Recipient(), "X=1")

	inv, err := collectInventory(flags)
	if err != nil {
		t.Fatalf("collectInventory: %v", err)
	}
	if inv.Recipient != recipient {
		t.Errorf("recipient = %q, want %q", inv.Recipient, recipient)
	}
	if inv.IdentitySource != secrets.SourceKeyfile {
		t.Errorf("identity source = %q, want %q", inv.IdentitySource, secrets.SourceKeyfile)
	}
	if !inv.HasSecrets() {
		t.Error("HasSecrets() = false on a project with markers and .age files")
	}

	wantMarkers := []markerRow{
		{Layer: filepath.Join("workspace", "defaults.yml"), Path: "vars.a_token", State: stateDecrypted},
		{Layer: filepath.Join("workspace", "defaults.yml"), Path: "vars.b_token", State: stateUnresolved, Reason: "wrong_identity"},
		{Layer: filepath.Join("workspace", "defaults.yml"), Path: "vars.c_token", State: stateUnresolved, Reason: "corrupt"},
	}
	if len(inv.Markers) != len(wantMarkers) {
		t.Fatalf("markers = %+v, want %d rows", inv.Markers, len(wantMarkers))
	}
	for i, want := range wantMarkers {
		if inv.Markers[i] != want {
			t.Errorf("marker[%d] = %+v, want %+v", i, inv.Markers[i], want)
		}
	}

	wantFiles := []fileRow{
		{File: filepath.Join("workspace", "templates", "config", "app", "creds.json.age"), State: stateDecryptable},
		{File: filepath.Join("workspace", "templates", "config", "app", "foreign.env.age"), State: stateNotDecryptable, Reason: "wrong_identity"},
	}
	if len(inv.Files) != len(wantFiles) {
		t.Fatalf("files = %+v, want %d rows", inv.Files, len(wantFiles))
	}
	for i, want := range wantFiles {
		if inv.Files[i] != want {
			t.Errorf("file[%d] = %+v, want %+v", i, inv.Files[i], want)
		}
	}
}

// TestCollectInventory_NoIdentity pins the keyless machine: every marker is
// unresolved for the same, single actionable reason.
func TestCollectInventory_NoIdentity(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	marker, err := secrets.Encrypt("token", recipient)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "workspace", "defaults.yml"),
		[]byte("vars:\n  token: "+marker+"\n"), 0o644); err != nil {
		t.Fatalf("writing defaults.yml: %v", err)
	}
	writeAgeFile(t, root, "app/creds.age", recipient, "hello")

	// Hide the keyfile: the recipient stays committed, the identity is gone.
	keyfile, err := secrets.KeyfilePath(recipient)
	if err != nil {
		t.Fatalf("keyfile path: %v", err)
	}
	if err := os.Remove(keyfile); err != nil {
		t.Fatalf("removing keyfile: %v", err)
	}

	inv, err := collectInventory(flags)
	if err != nil {
		t.Fatalf("collectInventory: %v", err)
	}
	if inv.IdentityErr == nil {
		t.Error("IdentityErr = nil with no keyfile present")
	}
	if len(inv.Markers) != 1 || inv.Markers[0].Reason != "no_identity" {
		t.Errorf("markers = %+v, want one no_identity row", inv.Markers)
	}
	if len(inv.Files) != 1 || inv.Files[0].State != stateNotDecryptable || inv.Files[0].Reason != "no_identity" {
		t.Errorf("files = %+v, want one not-decryptable no_identity row", inv.Files)
	}
}

// TestCollectInventory_HalfRekeyed pins decision 11's recovery property: after
// an interrupted rekey the tree holds markers under two recipients, and every
// one of them is still reported as readable because a straggler keyfile opens it.
func TestCollectInventory_HalfRekeyed(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	oldRecipient := initProject(t, flags)

	oldMarker, err := secrets.Encrypt("old-value", oldRecipient)
	if err != nil {
		t.Fatalf("encrypt old: %v", err)
	}

	// Mint the "new" key pair and swap workspace.yml over to it, as a rekey
	// would just before it rewrote the last file.
	newID, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	if _, err := secrets.WriteKeyfile(newID); err != nil {
		t.Fatalf("writing new keyfile: %v", err)
	}
	if err := writeRecipient(cfgPath, newID.Recipient()); err != nil {
		t.Fatalf("swapping recipient: %v", err)
	}
	newMarker, err := secrets.Encrypt("new-value", newID.Recipient())
	if err != nil {
		t.Fatalf("encrypt new: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "workspace", "defaults.yml"),
		[]byte("vars:\n  a: "+newMarker+"\n  b: "+oldMarker+"\n"), 0o644); err != nil {
		t.Fatalf("writing defaults.yml: %v", err)
	}

	inv, err := collectInventory(flags)
	if err != nil {
		t.Fatalf("collectInventory: %v", err)
	}
	if len(inv.Markers) != 2 {
		t.Fatalf("markers = %+v, want 2", inv.Markers)
	}
	for _, m := range inv.Markers {
		if m.State != stateDecrypted {
			t.Errorf("%s: state = %q (%s), want %q — the straggler keyfile should open it",
				m.Path, m.State, m.Reason, stateDecrypted)
		}
	}
}

// TestCollectAgeFiles_RefusesSymlink pins that a symlinked .age source is
// reported rather than silently skipped or followed out of the project tree.
func TestCollectAgeFiles_RefusesSymlink(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	real := writeAgeFile(t, root, "app/creds.age", recipient, "hello")
	link := filepath.Join(root, "workspace", "templates", "config", "app", "link.age")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	inv, err := collectInventory(flags)
	if err != nil {
		t.Fatalf("collectInventory: %v", err)
	}
	if len(inv.Files) != 2 {
		t.Fatalf("files = %+v, want 2 (the real file and the refused symlink)", inv.Files)
	}
	var linkRow fileRow
	for _, f := range inv.Files {
		if filepath.Base(f.File) == "link.age" {
			linkRow = f
		}
	}
	if linkRow.State != stateNotDecryptable || linkRow.Reason == "" {
		t.Errorf("symlink row = %+v, want a not-decryptable row carrying a reason", linkRow)
	}
	if !bytes.Contains([]byte(linkRow.Reason), []byte("symlink")) {
		t.Errorf("symlink reason = %q, want it to name the symlink", linkRow.Reason)
	}
}

// TestCollectInventory_NoSecrets pins that a project without secrets reports an
// empty inventory (and, by loadIdentitySet's empty-recipient short-circuit,
// never touches the keys directory).
func TestCollectInventory_NoSecrets(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	inv, err := collectInventory(flags)
	if err != nil {
		t.Fatalf("collectInventory: %v", err)
	}
	if inv.HasSecrets() || inv.Recipient != "" {
		t.Errorf("inventory = %+v, want empty", inv)
	}
	if inv.IdentityErr == nil {
		t.Error("IdentityErr = nil with no recipient; want the no-identity sentinel")
	}
}
