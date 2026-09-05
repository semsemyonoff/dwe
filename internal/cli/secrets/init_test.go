package secrets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/lock"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// TestInit_WritesRecipientAndKeyfile pins the whole happy path: the public half
// lands in workspace.yml, the private half in a 0600 keyfile, and the project
// still loads afterwards.
func TestInit_WritesRecipientAndKeyfile(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	out, _, err := runSecrets(t, flags, "init")
	if err != nil {
		t.Fatalf("secrets init: %v", err)
	}

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("loading config after init: %v", err)
	}
	recipient := config.SecretsRecipient(cfg)
	if recipient == "" {
		t.Fatal("secrets.recipient is empty after init")
	}
	if err := secrets.ParseRecipient(recipient); err != nil {
		t.Errorf("recipient %q does not parse: %v", recipient, err)
	}
	if !strings.Contains(out, recipient) {
		t.Errorf("init output does not name the recipient\ngot:\n%s", out)
	}

	keyfile, err := secrets.KeyfilePath(recipient)
	if err != nil {
		t.Fatalf("keyfile path: %v", err)
	}
	fi, err := os.Stat(keyfile)
	if err != nil {
		t.Fatalf("stat keyfile: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("keyfile mode = %v, want 0600", fi.Mode().Perm())
	}
	// The private half must never reach stdout: `key export` is the only way out.
	if strings.Contains(out, "AGE-SECRET-KEY-") {
		t.Errorf("init printed a private key\ngot:\n%s", out)
	}

	// The identity round-trips: the keyfile really opens values for the
	// committed recipient.
	id, source, err := secrets.LoadIdentity(recipient)
	if err != nil {
		t.Fatalf("loading the identity init just wrote: %v", err)
	}
	if source != secrets.SourceKeyfile {
		t.Errorf("identity source = %q, want %q", source, secrets.SourceKeyfile)
	}
	marker, err := secrets.Encrypt("hello", recipient)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if plain, derr := secrets.Decrypt(marker, id); derr != nil || plain != "hello" {
		t.Errorf("round-trip = %q, %v; want \"hello\", nil", plain, derr)
	}
}

// TestInit_RefusesSecondRun pins that a live key pair is never silently
// replaced: the fix is `rekey`, which re-encrypts the existing values.
func TestInit_RefusesSecondRun(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reading workspace.yml: %v", err)
	}

	_, _, err = runSecrets(t, flags, "init")
	if err == nil {
		t.Fatal("second init succeeded; want a refusal")
	}
	coded := codedError(t, err)
	if coded.Code != "secrets_already_initialized" {
		t.Fatalf("error code = %q, want secrets_already_initialized", coded.Code)
	}
	if coded.Details["identity"] != identityAvailable {
		t.Errorf("details[identity] = %v, want %q", coded.Details["identity"], identityAvailable)
	}
	if !strings.Contains(coded.Hint, "rekey") {
		t.Errorf("hint = %q, want it to point at rekey", coded.Hint)
	}
	if strings.Contains(coded.Hint, "--replace-recipient") {
		t.Errorf("hint = %q offers --replace-recipient while the values are still recoverable", coded.Hint)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("re-reading workspace.yml: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("workspace.yml changed on a refused init\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if _, err := secrets.KeyfilePath(recipient); err != nil {
		t.Fatalf("keyfile path: %v", err)
	}
}

// --- init --replace-recipient ------------------------------------------------

// lostIdentityProject builds the state R12 is about: a project whose secrets
// are committed — one marker and one *.age source — and whose identity exists
// nowhere on this machine. Returns the orphaned recipient.
func lostIdentityProject(t *testing.T, flags *cmdctx.RootFlags, root string) string {
	t.Helper()
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

	keyfile, err := secrets.KeyfilePath(recipient)
	if err != nil {
		t.Fatalf("keyfile path: %v", err)
	}
	if err := os.Remove(keyfile); err != nil {
		t.Fatalf("removing the keyfile: %v", err)
	}
	return recipient
}

// TestInit_RefusalNamesReplaceWhenIdentityLost pins R12's first half: with the
// identity gone, `rekey` cannot run at all — it has to read every value before
// it can rewrite one — so the refusal must name the recovery path instead of
// sending the developer into that dead end.
func TestInit_RefusalNamesReplaceWhenIdentityLost(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := lostIdentityProject(t, flags, root)

	_, _, err := runSecrets(t, flags, "init")
	if err == nil {
		t.Fatal("init succeeded on an already-initialized project")
	}
	coded := codedError(t, err)
	if coded.Code != "secrets_already_initialized" {
		t.Fatalf("error code = %q, want secrets_already_initialized", coded.Code)
	}
	if coded.Details["identity"] != identityMissing {
		t.Errorf("details[identity] = %v, want %q", coded.Details["identity"], identityMissing)
	}
	if !strings.Contains(coded.Hint, "--replace-recipient") {
		t.Errorf("hint = %q, want it to name init --replace-recipient", coded.Hint)
	}
	if !strings.Contains(coded.Hint, "key import") {
		t.Errorf("hint = %q, want it to offer key import first", coded.Hint)
	}
	if !strings.Contains(coded.Hint, recipient) {
		t.Errorf("hint = %q, want it to name the orphaned recipient", coded.Hint)
	}
}

// TestInit_RefusalIgnoresAForeignEnvKey pins that the `identity` verdict is
// about this machine, not about the first identity SOURCE: LoadIdentity is
// first-present-source-wins with no fall-through, so a DWE_AGE_KEY exported for
// another project used to make a healthy project report `missing` — and be
// offered --replace-recipient, i.e. "re-enter every value from its plaintext",
// with its own keyfile sitting right there.
func TestInit_RefusalIgnoresAForeignEnvKey(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	initProject(t, flags)

	foreign, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	t.Setenv(secrets.EnvKey, foreign.Export())

	_, _, err = runSecrets(t, flags, "init")
	if err == nil {
		t.Fatal("init succeeded on an already-initialized project")
	}
	coded := codedError(t, err)
	if coded.Details["identity"] != identityAvailable {
		t.Errorf("details[identity] = %v, want %q — the project's keyfile is installed",
			coded.Details["identity"], identityAvailable)
	}
	if strings.Contains(coded.Hint, "--replace-recipient") {
		t.Errorf("hint = %q offers the destructive recovery to a machine that holds the key", coded.Hint)
	}
}

// misnamedIdentityProject builds an initialized project with one committed
// marker whose ONLY copy of the identity sits in the keys directory under
// another recipient's filename — the state `key list` reports as `misnamed`.
// Returns the configured recipient.
func misnamedIdentityProject(t *testing.T, flags *cmdctx.RootFlags, root string) string {
	t.Helper()
	recipient := initProject(t, flags)
	marker, err := secrets.Encrypt("token", recipient)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "workspace", "defaults.yml"),
		[]byte("vars:\n  token: "+marker+"\n"), 0o644); err != nil {
		t.Fatalf("writing defaults.yml: %v", err)
	}

	keyfile, err := secrets.KeyfilePath(recipient)
	if err != nil {
		t.Fatalf("keyfile path: %v", err)
	}
	other, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	stray, err := secrets.KeyfilePath(other.Recipient())
	if err != nil {
		t.Fatalf("keyfile path: %v", err)
	}
	if err := os.Rename(keyfile, stray); err != nil {
		t.Fatalf("renaming the keyfile: %v", err)
	}
	return recipient
}

// TestInit_RefusalFindsAMisnamedKeyfile pins that a keyfile holding THIS
// project's identity counts as present even under a foreign filename.
// LoadIdentity only ever reads keys/<recipient>.key, and gating the directory
// pass on KeyfileOK dropped exactly the rows whose Recipient came from the
// PARSED identity — so the refusal advertised the destructive recovery to a
// machine that holds the key.
func TestInit_RefusalFindsAMisnamedKeyfile(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	misnamedIdentityProject(t, flags, root)

	_, _, err := runSecrets(t, flags, "init")
	if err == nil {
		t.Fatal("init succeeded on an already-initialized project")
	}
	coded := codedError(t, err)
	if coded.Details["identity"] != identityAvailable {
		t.Errorf("details[identity] = %v, want %q — the identity is in the keys dir under another name",
			coded.Details["identity"], identityAvailable)
	}
	if strings.Contains(coded.Hint, "--replace-recipient") {
		t.Errorf("hint = %q offers the destructive recovery to a machine that holds the key", coded.Hint)
	}
}

// TestInit_ReplaceRecipientRefusesOnAMisnamedKeyfile pins the data-loss half of
// the same bug: the straggler scan excluded the configured recipient
// unconditionally, so a misnamed file holding it was dropped from BOTH lookups,
// every marker classified as unreadable, and --replace-recipient --yes orphaned
// a tree whose key sat on disk.
func TestInit_ReplaceRecipientRefusesOnAMisnamedKeyfile(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	misnamedIdentityProject(t, flags, root)

	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reading workspace.yml: %v", err)
	}

	_, _, err = runSecrets(t, flags, "init", "--replace-recipient", "--yes")
	if err == nil {
		t.Fatal("--replace-recipient orphaned a value a misnamed keyfile still opens")
	}
	coded := codedError(t, err)
	if coded.Code != "secrets_identity_available" {
		t.Fatalf("error code = %q, want secrets_identity_available", coded.Code)
	}
	if coded.Details["readable"] != 1 {
		t.Errorf("details[readable] = %v, want 1", coded.Details["readable"])
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("re-reading workspace.yml: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("workspace.yml changed on a refused replacement:\n%s", after)
	}
}

// TestInit_ReplaceRecipientRefusesWhileReadable pins the guard: with the
// identity present the values are not lost, and re-encrypting them is `rekey`'s
// job — so the destructive flag must refuse rather than orphan them. The
// refusal names a way out, because a guard that dead-ends is the bug R12 is
// about in the first place.
func TestInit_ReplaceRecipientRefusesWhileReadable(t *testing.T) {
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
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reading workspace.yml: %v", err)
	}

	_, _, err = runSecrets(t, flags, "init", "--replace-recipient", "--yes")
	if err == nil {
		t.Fatal("--replace-recipient orphaned a value this machine can still read")
	}
	coded := codedError(t, err)
	if coded.Code != "secrets_identity_available" {
		t.Fatalf("error code = %q, want secrets_identity_available", coded.Code)
	}
	if coded.Details["readable"] != 1 {
		t.Errorf("details[readable] = %v, want 1", coded.Details["readable"])
	}
	if !strings.Contains(coded.Hint, "rekey") || !strings.Contains(coded.Hint, "key remove") {
		t.Errorf("hint = %q, want both the rekey fix and the way out of the guard", coded.Hint)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("re-reading workspace.yml: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("workspace.yml changed on a refused replacement:\n%s", after)
	}
}

// TestInit_ReplaceRecipientNeedsConfirmation pins that a mode with no prompt
// refuses instead of silently orphaning the tree.
func TestInit_ReplaceRecipientNeedsConfirmation(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	lostIdentityProject(t, flags, root)
	forbidConfirm(t)
	stubStdinTTY(t, false)

	_, _, err := runSecrets(t, flags, "init", "--replace-recipient")
	if err == nil {
		t.Fatal("--replace-recipient ran unconfirmed in a non-interactive mode")
	}
	coded := codedError(t, err)
	if coded.Code != "secrets_confirmation_required" {
		t.Fatalf("error code = %q, want secrets_confirmation_required", coded.Code)
	}
	if coded.Details["orphaned"] != 2 {
		t.Errorf("details[orphaned] = %v, want 2 (one marker, one .age source)", coded.Details["orphaned"])
	}
	if names := keyfileNames(t); len(names) != 0 {
		t.Errorf("keys dir = %v, want nothing minted before the confirmation", names)
	}
}

// TestInit_ReplaceRecipientDeclined pins that a declined confirmation is a
// finished command, not a failure — and that it wrote nothing.
func TestInit_ReplaceRecipientDeclined(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := lostIdentityProject(t, flags, root)
	stubStdinTTY(t, true)
	stubRunConfirm(t, func(string, string, string) (bool, error) { return false, nil })

	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reading workspace.yml: %v", err)
	}
	if _, _, err := runSecrets(t, flags, "init", "--replace-recipient"); err != nil {
		t.Fatalf("a declined replacement returned an error: %v", err)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("re-reading workspace.yml: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("workspace.yml changed on a declined replacement:\n%s", after)
	}
	if got := currentRecipient(flags); got != recipient {
		t.Errorf("recipient = %q, want the original %q", got, recipient)
	}
	if names := keyfileNames(t); len(names) != 0 {
		t.Errorf("keys dir = %v, want nothing minted", names)
	}
}

// TestInit_ReplaceRecipientConfirmsWithoutHoldingTheLocks pins the ordering
// rule `set` states and `key remove` follows: a confirmation can sit open for as
// long as the developer needs, and holding deploy.lock + snapshot.lock meanwhile
// stalls every other dwe command in the project until someone answers.
func TestInit_ReplaceRecipientConfirmsWithoutHoldingTheLocks(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	lostIdentityProject(t, flags, root)
	stubStdinTTY(t, true)

	locksWereFree := false
	stubRunConfirm(t, func(string, string, string) (bool, error) {
		release, lerr := lock.AcquireProjectLocks(root)
		if lerr == nil {
			locksWereFree = true
			release()
		}
		return true, nil
	})

	if _, _, err := runSecrets(t, flags, "init", "--replace-recipient"); err != nil {
		t.Fatalf("secrets init --replace-recipient: %v", err)
	}
	if !locksWereFree {
		t.Error("the project locks were held while the confirmation was open")
	}
}

// TestInit_ReplaceRecipientRefusesAChangedRecipient pins the other half of
// moving the locks after the prompt: the recipient decided the whole branch, so
// it is re-read under the lock rather than trusted. The confirmation callback
// stands in for the concurrent `rekey` that would otherwise have its brand-new
// key pair overwritten by a decision taken before it ran.
func TestInit_ReplaceRecipientRefusesAChangedRecipient(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	lostIdentityProject(t, flags, root)
	stubStdinTTY(t, true)

	other, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	stubRunConfirm(t, func(string, string, string) (bool, error) {
		if werr := writeRecipient(cfgPath, other.Recipient()); werr != nil {
			t.Fatalf("swapping the recipient mid-confirmation: %v", werr)
		}
		return true, nil
	})

	_, _, err = runSecrets(t, flags, "init", "--replace-recipient")
	if err == nil {
		t.Fatal("the replacement went ahead on a recipient that changed under it")
	}
	if coded := codedError(t, err); coded.Code != "secrets_recipient_changed" {
		t.Errorf("error code = %q, want secrets_recipient_changed", coded.Code)
	}
	if got := currentRecipient(flags); got != other.Recipient() {
		t.Errorf("recipient = %q, want the concurrently written %q", got, other.Recipient())
	}
	if names := keyfileNames(t); len(names) != 0 {
		t.Errorf("keys dir = %v, want nothing minted", names)
	}
}

// TestInit_ReplaceRecipient pins the whole recovery: a new key pair is minted
// and committed, the orphaned values are left in place — they are the only
// record of WHICH secrets have to be re-entered — and the report names each one.
func TestInit_ReplaceRecipient(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	old := lostIdentityProject(t, flags, root)

	defaultsPath := filepath.Join(root, "workspace", "defaults.yml")
	beforeDefaults, err := os.ReadFile(defaultsPath)
	if err != nil {
		t.Fatalf("reading defaults.yml: %v", err)
	}

	out, _, err := runSecrets(t, flags, "init", "--replace-recipient", "--yes")
	if err != nil {
		t.Fatalf("secrets init --replace-recipient: %v", err)
	}

	recipient := currentRecipient(flags)
	if recipient == old || recipient == "" {
		t.Fatalf("recipient = %q, want a new one (was %q)", recipient, old)
	}
	if err := secrets.ParseRecipient(recipient); err != nil {
		t.Errorf("recipient %q does not parse: %v", recipient, err)
	}
	if _, _, lerr := secrets.LoadIdentity(recipient); lerr != nil {
		t.Errorf("the new identity does not load: %v", lerr)
	}

	// The orphans stay byte-identical: `dwe secrets set` overwrites each marker
	// in place as it is re-entered, and until then they ARE the to-do list.
	afterDefaults, err := os.ReadFile(defaultsPath)
	if err != nil {
		t.Fatalf("re-reading defaults.yml: %v", err)
	}
	if string(beforeDefaults) != string(afterDefaults) {
		t.Errorf("the orphaned marker was rewritten:\n%s", afterDefaults)
	}

	for _, want := range []string{old, recipient, "vars.token", "creds.age", "re-entered"} {
		if !strings.Contains(out, want) {
			t.Errorf("report does not mention %q\ngot:\n%s", want, out)
		}
	}
	assertNoSecretLeak(t, "", out)
}

// TestInit_ReplaceRecipientJSON pins the payload the recovery hands to a script:
// the retired recipient and both orphan inventories, which a plain `init` must
// not carry.
func TestInit_ReplaceRecipientJSON(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}
	old := lostIdentityProject(t, flags, root)

	out, errOut, err := runSecrets(t, flags, "init", "--replace-recipient", "--yes")
	if err != nil {
		t.Fatalf("secrets init --replace-recipient --output json: %v", err)
	}
	if errOut != "" {
		t.Errorf("stderr should be empty in JSON mode, got: %q", errOut)
	}
	var data initJSON
	if e := json.Unmarshal([]byte(out), &data); e != nil {
		t.Fatalf("unmarshal init json: %v\nraw: %s", e, out)
	}
	if data.OldRecipient != old {
		t.Errorf("old_recipient = %q, want %q", data.OldRecipient, old)
	}
	if len(data.OrphanedMarkers) != 1 || data.OrphanedMarkers[0].Path != "vars.token" {
		t.Errorf("orphaned_markers = %+v, want the one committed marker", data.OrphanedMarkers)
	}
	if len(data.OrphanedFiles) != 1 {
		t.Errorf("orphaned_files = %+v, want the one .age source", data.OrphanedFiles)
	}
	if strings.Contains(out, "AGE-SECRET-KEY-") {
		t.Errorf("init JSON leaked a private key: %s", out)
	}
}

// TestInit_ReplaceRecipientWithoutRecipient pins that the flag refuses on a
// project that has nothing to replace, rather than quietly behaving like a
// plain init.
func TestInit_ReplaceRecipientWithoutRecipient(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	_, _, err := runSecrets(t, flags, "init", "--replace-recipient", "--yes")
	if err == nil {
		t.Fatal("--replace-recipient succeeded with no recipient to replace")
	}
	if coded := codedError(t, err); coded.Code != "secrets_no_recipient" {
		t.Errorf("error code = %q, want secrets_no_recipient", coded.Code)
	}
	if names := keyfileNames(t); len(names) != 0 {
		t.Errorf("keys dir = %v, want nothing minted", names)
	}
}

// TestInit_PreservesCommentsAnchorsAndMode pins the splice-writer contract at
// the workspace.yml layer, on an annotated file: `init` APPENDS the secrets
// block and rewrites nothing — every existing line, blank line, comment, anchor,
// merge key and literal block comes back byte-identical. A tracked file also
// keeps its 0644 mode (local.yml's forced 0600 must not leak here).
func TestInit_PreservesCommentsAnchorsAndMode(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	cfgPath := filepath.Join(root, "workspace.yml")
	src := readTestdata(t, "annotated_workspace.yml")
	if err := os.WriteFile(cfgPath, []byte(src), 0o644); err != nil {
		t.Fatalf("writing workspace.yml: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "workspace"), 0o755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	if _, _, err := runSecrets(t, flags, "init"); err != nil {
		t.Fatalf("secrets init: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reading workspace.yml: %v", err)
	}
	// `writeRecipient` appends at len(src), so the result must still OPEN with
	// the fixture verbatim — insertedLines alone would stay green if a future
	// offset regression spliced the block into the middle of the document.
	if !strings.HasPrefix(string(data), src) {
		t.Errorf("workspace.yml no longer starts with the original fixture — the block was not appended")
	}
	added := insertedLines(t, src, string(data))
	joined := strings.Join(added, "\n")
	if !strings.Contains(joined, "secrets:") || !strings.Contains(joined, "recipient: age1") {
		t.Errorf("the appended block does not hold the recipient: %q", added)
	}
	fi, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat workspace.yml: %v", err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("workspace.yml mode = %v, want 0644 (tracked file, preserve policy)", fi.Mode().Perm())
	}
	if _, err := config.LoadConfig(cfgPath); err != nil {
		t.Errorf("config no longer loads after init: %v", err)
	}
}

// TestInit_RefusesUnsplicableSecretsBlock pins that a workspace.yml whose
// `secrets:` block is written in flow style is refused with the specific
// secrets_write_unsupported code rather than the generic
// secrets_recipient_write_failed wrapper — cmdctx.ErrWrap reports the OUTERMOST
// code, so the branch order is what makes the refusal reach JSON. The minted
// keyfile is rolled back, and no key material reaches the output.
func TestInit_RefusesUnsplicableSecretsBlock(t *testing.T) {
	for _, mode := range []string{"text", "json"} {
		t.Run(mode, func(t *testing.T) {
			isolateHome(t)
			root := t.TempDir()
			cfgPath := filepath.Join(root, "workspace.yml")
			const src = `schema_version: "2"
project:
  name: sectest
  prefix: dwe
secrets: {}
`
			if err := os.WriteFile(cfgPath, []byte(src), 0o644); err != nil {
				t.Fatalf("writing workspace.yml: %v", err)
			}
			if err := os.MkdirAll(filepath.Join(root, "workspace"), 0o755); err != nil {
				t.Fatalf("creating workspace dir: %v", err)
			}
			flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
			if mode == "json" {
				flags.Output = "json"
			}

			stdout, stderr, err := runSecrets(t, flags, "init")
			if err == nil {
				t.Fatal("init succeeded against a flow-style secrets block")
			}
			coded := codedError(t, err)
			if coded.Code != "secrets_write_unsupported" {
				t.Errorf("error code = %q, want secrets_write_unsupported (message: %s)", coded.Code, coded.Message)
			}
			payload, merr := json.Marshal(coded)
			if merr != nil {
				t.Fatalf("marshalling the coded error: %v", merr)
			}
			assertNoSecretLeak(t, "", stdout, stderr, coded.Message, coded.Hint, string(payload))

			after, rerr := os.ReadFile(cfgPath)
			if rerr != nil {
				t.Fatalf("reading workspace.yml: %v", rerr)
			}
			if string(after) != src {
				t.Errorf("workspace.yml changed after a refused init:\n%s", after)
			}
			if names := keyfileNames(t); len(names) != 0 {
				t.Errorf("keys dir = %v, want the minted keyfile rolled back", names)
			}
		})
	}
}

// TestInit_JSON pins the DTO and the clean-stdout contract.
func TestInit_JSON(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}

	out, errOut, err := runSecrets(t, flags, "init")
	if err != nil {
		t.Fatalf("secrets init --output json: %v", err)
	}
	if errOut != "" {
		t.Errorf("stderr should be empty in JSON mode, got: %q", errOut)
	}
	var data initJSON
	if e := json.Unmarshal([]byte(out), &data); e != nil {
		t.Fatalf("unmarshal init json: %v\nraw: %s", e, out)
	}
	if err := secrets.ParseRecipient(data.Recipient); err != nil {
		t.Errorf("recipient %q does not parse: %v", data.Recipient, err)
	}
	if !strings.HasSuffix(data.Keyfile, data.Recipient+".key") {
		t.Errorf("keyfile = %q, want it to end in <recipient>.key", data.Keyfile)
	}
	if strings.Contains(out, "AGE-SECRET-KEY-") {
		t.Errorf("init JSON leaked a private key: %s", out)
	}
}
