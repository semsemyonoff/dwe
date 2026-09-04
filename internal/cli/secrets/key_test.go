package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"

	"github.com/spf13/cobra"
)

// runSecretsStdin is runSecrets with the given bytes on stdin (the `key import`
// pipe path).
func runSecretsStdin(t *testing.T, flags *cmdctx.RootFlags, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := NewCmd("", flags)
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// --- key export -------------------------------------------------------------

// TestKeyExport_PrintsIdentity pins that export yields exactly the identity the
// keyfile holds, and that the TTY warning stays off a redirected stdout.
func TestKeyExport_PrintsIdentity(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	stubStdoutTTY(t, false)

	out, errOut, err := runSecrets(t, flags, "key", "export")
	if err != nil {
		t.Fatalf("secrets key export: %v", err)
	}
	identity := strings.TrimSpace(out)
	if !strings.HasPrefix(identity, "AGE-SECRET-KEY-") {
		t.Fatalf("export output = %q, want an AGE-SECRET-KEY- line", identity)
	}
	parsed, perr := secrets.ParseIdentity(identity)
	if perr != nil {
		t.Fatalf("exported identity does not parse: %v", perr)
	}
	if parsed.Recipient() != recipient {
		t.Errorf("exported identity is for %s, want %s", parsed.Recipient(), recipient)
	}
	if errOut != "" {
		t.Errorf("stderr = %q on a non-TTY stdout, want no warning", errOut)
	}
}

// TestKeyExport_WarnsOnTerminal pins the scrollback warning: stderr only, text
// mode only, and only when stdout is a real terminal.
func TestKeyExport_WarnsOnTerminal(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	initProject(t, flags)

	stubStdoutTTY(t, true)

	_, errOut, err := runSecrets(t, flags, "key", "export")
	if err != nil {
		t.Fatalf("secrets key export: %v", err)
	}
	if !strings.Contains(errOut, "warning:") {
		t.Errorf("stderr = %q, want the scrollback warning", errOut)
	}
	if strings.Contains(errOut, "AGE-SECRET-KEY-") {
		t.Errorf("the warning leaked the key: %q", errOut)
	}

	// JSON mode keeps stderr clean even on a terminal: nothing a parser reads
	// may carry a human note.
	jsonFlags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}
	out, errOut, err := runSecrets(t, jsonFlags, "key", "export")
	if err != nil {
		t.Fatalf("secrets key export --output json: %v", err)
	}
	if errOut != "" {
		t.Errorf("stderr = %q in JSON mode, want empty", errOut)
	}
	var data keyExportJSON
	if e := json.Unmarshal([]byte(out), &data); e != nil {
		t.Fatalf("unmarshal export json: %v\nraw: %s", e, out)
	}
	if !strings.HasPrefix(data.Identity, "AGE-SECRET-KEY-") {
		t.Errorf("json identity = %q, want an AGE-SECRET-KEY- value", data.Identity)
	}
}

// TestKeyExport_NoIdentity pins the typed error and its hint when this machine
// holds no identity for the committed recipient.
func TestKeyExport_NoIdentity(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	keyfile, err := secrets.KeyfilePath(recipient)
	if err != nil {
		t.Fatalf("keyfile path: %v", err)
	}
	if err := os.Remove(keyfile); err != nil {
		t.Fatalf("removing keyfile: %v", err)
	}

	_, _, err = runSecrets(t, flags, "key", "export")
	if err == nil {
		t.Fatal("key export succeeded without an identity")
	}
	coded := codedError(t, err)
	if coded.Code != "secrets_no_identity" {
		t.Errorf("code = %q, want secrets_no_identity", coded.Code)
	}
	for _, want := range []string{"key import", secrets.EnvKey, secrets.EnvKeyFile} {
		if !strings.Contains(coded.Hint, want) {
			t.Errorf("hint %q does not name %q", coded.Hint, want)
		}
	}
}

// TestKeyExport_NoRecipient pins the pre-identity refusal on a project that
// never ran init.
func TestKeyExport_NoRecipient(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	_, _, err := runSecrets(t, flags, "key", "export")
	if err == nil {
		t.Fatal("key export succeeded without a recipient")
	}
	if coded := codedError(t, err); coded.Code != "secrets_no_recipient" {
		t.Errorf("code = %q, want secrets_no_recipient", coded.Code)
	}
}

// --- key import -------------------------------------------------------------

// TestKeyImport_FromFile pins the onboarding path end to end: a second machine
// imports the exported identity and can then read the project's secrets.
func TestKeyImport_FromFile(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	keyfile, err := secrets.KeyfilePath(recipient)
	if err != nil {
		t.Fatalf("keyfile path: %v", err)
	}
	exported, err := os.ReadFile(keyfile)
	if err != nil {
		t.Fatalf("reading keyfile: %v", err)
	}
	// Simulate the second machine: same project, no identity installed.
	if err := os.Remove(keyfile); err != nil {
		t.Fatalf("removing keyfile: %v", err)
	}
	identityFile := filepath.Join(t.TempDir(), "identity.txt")
	if err := os.WriteFile(identityFile, exported, 0o600); err != nil {
		t.Fatalf("writing identity file: %v", err)
	}

	out, _, err := runSecrets(t, flags, "key", "import", "--file", identityFile)
	if err != nil {
		t.Fatalf("secrets key import: %v", err)
	}
	if !strings.Contains(out, recipient) {
		t.Errorf("import output %q does not name the recipient", out)
	}
	if strings.Contains(out, "AGE-SECRET-KEY-") {
		t.Errorf("import echoed the private key: %q", out)
	}

	fi, err := os.Stat(keyfile)
	if err != nil {
		t.Fatalf("stat imported keyfile: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("imported keyfile mode = %v, want 0600", fi.Mode().Perm())
	}
	if _, source, lerr := secrets.LoadIdentity(recipient); lerr != nil || source != secrets.SourceKeyfile {
		t.Errorf("LoadIdentity after import = %q, %v; want keyfile, nil", source, lerr)
	}
}

// TestKeyImport_FromStdin pins the `pbpaste | dwe secrets key import` path and
// the JSON DTO.
func TestKeyImport_FromStdin(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}
	recipient := initProject(t, flags)

	keyfile, err := secrets.KeyfilePath(recipient)
	if err != nil {
		t.Fatalf("keyfile path: %v", err)
	}
	exported, err := os.ReadFile(keyfile)
	if err != nil {
		t.Fatalf("reading keyfile: %v", err)
	}
	if err := os.Remove(keyfile); err != nil {
		t.Fatalf("removing keyfile: %v", err)
	}

	out, errOut, err := runSecretsStdin(t, flags, string(exported), "key", "import")
	if err != nil {
		t.Fatalf("secrets key import (stdin): %v", err)
	}
	if errOut != "" {
		t.Errorf("stderr = %q in JSON mode, want empty", errOut)
	}
	var data keyImportJSON
	if e := json.Unmarshal([]byte(out), &data); e != nil {
		t.Fatalf("unmarshal import json: %v\nraw: %s", e, out)
	}
	if data.Recipient != recipient || data.Keyfile != keyfile {
		t.Errorf("json = %+v, want recipient %s and keyfile %s", data, recipient, keyfile)
	}
}

// TestKeyImport_RejectsMismatch pins that an identity for another project is
// refused BEFORE anything is written: a stored key that opens nothing is worse
// than no key, because it looks installed.
func TestKeyImport_RejectsMismatch(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	foreign, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	_, _, err = runSecretsStdin(t, flags, foreign.Export()+"\n", "key", "import")
	if err == nil {
		t.Fatal("import of a foreign identity succeeded")
	}
	coded := codedError(t, err)
	if coded.Code != "secrets_identity_mismatch" {
		t.Fatalf("code = %q, want secrets_identity_mismatch", coded.Code)
	}
	if !strings.Contains(coded.Message, foreign.Recipient()) || !strings.Contains(coded.Message, recipient) {
		t.Errorf("message %q should name both recipients", coded.Message)
	}

	keysDir, err := secrets.KeysDir()
	if err != nil {
		t.Fatalf("keys dir: %v", err)
	}
	entries, err := os.ReadDir(keysDir)
	if err != nil {
		t.Fatalf("reading keys dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != recipient+".key" {
		t.Errorf("keys dir = %v, want only the project's own keyfile", entries)
	}
}

// TestKeyImport_RefusesExistingKeyfile pins the no-clobber guard: an installed
// identity is never silently replaced.
func TestKeyImport_RefusesExistingKeyfile(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	keyfile, err := secrets.KeyfilePath(recipient)
	if err != nil {
		t.Fatalf("keyfile path: %v", err)
	}
	exported, err := os.ReadFile(keyfile)
	if err != nil {
		t.Fatalf("reading keyfile: %v", err)
	}

	_, _, err = runSecretsStdin(t, flags, string(exported), "key", "import")
	if err == nil {
		t.Fatal("import over an existing keyfile succeeded")
	}
	if coded := codedError(t, err); coded.Code != "secrets_keyfile_write_failed" {
		t.Errorf("code = %q, want secrets_keyfile_write_failed", coded.Code)
	}
	after, err := os.ReadFile(keyfile)
	if err != nil {
		t.Fatalf("re-reading keyfile: %v", err)
	}
	if !bytes.Equal(exported, after) {
		t.Error("the existing keyfile changed on a refused import")
	}
}

// TestKeyImport_EmptyStdin pins that an empty pipe is an explicit error rather
// than a keyfile holding nothing.
func TestKeyImport_EmptyStdin(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	initProject(t, flags)

	_, _, err := runSecretsStdin(t, flags, "   \n", "key", "import")
	if err == nil {
		t.Fatal("import from an empty stdin succeeded")
	}
	if coded := codedError(t, err); coded.Code != "secrets_identity_source_required" {
		t.Errorf("code = %q, want secrets_identity_source_required", coded.Code)
	}
}

// TestKeyImport_CorruptIdentity pins the parse failure path.
func TestKeyImport_CorruptIdentity(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	initProject(t, flags)

	_, _, err := runSecretsStdin(t, flags, "not-a-key\n", "key", "import")
	if err == nil {
		t.Fatal("import of garbage succeeded")
	}
	if coded := codedError(t, err); coded.Code != "secrets_identity_invalid" {
		t.Errorf("code = %q, want secrets_identity_invalid", coded.Code)
	}
}

// --- key import: the interactive branch --------------------------------------

// stubStdinTTY pins the interactivity probe for one test. Package-level state:
// callers MUST NOT run in parallel.
func stubStdinTTY(t *testing.T, isTTY bool) {
	t.Helper()
	prev := widgets.IsInteractiveFn
	widgets.IsInteractiveFn = func(io.Reader) bool { return isTTY }
	t.Cleanup(func() { widgets.IsInteractiveFn = prev })
}

// stubPromptIdentity swaps the hidden import form for fn.
func stubPromptIdentity(t *testing.T, fn func(context.Context, string, io.Reader, io.Writer) (secrets.Identity, error)) {
	t.Helper()
	prev := promptIdentityFn
	promptIdentityFn = fn
	t.Cleanup(func() { promptIdentityFn = prev })
}

// forbidPrompt installs a form that fails the test if it is ever opened. It is
// the only way to prove a negative here: without the stub a mode that must not
// prompt would simply block on a terminal read that never comes.
func forbidPrompt(t *testing.T) {
	t.Helper()
	stubPromptIdentity(t, func(context.Context, string, io.Reader, io.Writer) (secrets.Identity, error) {
		t.Error("the hidden prompt opened in a mode that must never prompt")
		return secrets.Identity{}, nil
	})
}

// secondMachine strips the freshly-minted keyfile and returns its contents plus
// the path it used to live at: the state a teammate's clone starts from.
func secondMachine(t *testing.T, recipient string) (exported, keyfile string) {
	t.Helper()
	path, err := secrets.KeyfilePath(recipient)
	if err != nil {
		t.Fatalf("keyfile path: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading keyfile: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("removing keyfile: %v", err)
	}
	return string(data), path
}

// TestKeyImport_Prompt pins the new-machine path with neither --file nor a pipe:
// the hidden form supplies the identity, the keyfile lands 0600, and the report
// says what became readable.
func TestKeyImport_Prompt(t *testing.T) {
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
	exported, keyfile := secondMachine(t, recipient)

	stubStdinTTY(t, true)
	var gotRecipient string
	stubPromptIdentity(t, func(_ context.Context, r string, _ io.Reader, _ io.Writer) (secrets.Identity, error) {
		gotRecipient = r
		return secrets.ParseIdentity(exported)
	})

	out, _, err := runSecrets(t, flags, "key", "import")
	if err != nil {
		t.Fatalf("secrets key import (prompt): %v", err)
	}
	if gotRecipient != recipient {
		t.Errorf("prompt asked for %q, want %q", gotRecipient, recipient)
	}
	if !strings.Contains(out, recipient) || !strings.Contains(out, keyfile) {
		t.Errorf("import output %q should name the recipient and the keyfile", out)
	}
	if !strings.Contains(out, "1 encrypted value(s) and 0 .age file(s) are now readable") {
		t.Errorf("import output %q should carry the readability report", out)
	}
	assertNoSecretLeak(t, "", out)

	fi, err := os.Stat(keyfile)
	if err != nil {
		t.Fatalf("stat imported keyfile: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("imported keyfile mode = %v, want 0600", fi.Mode().Perm())
	}
}

// TestKeyImport_ReportCounts pins the two counters over a mixed surface: only
// what the imported identity actually opens is counted.
func TestKeyImport_ReportCounts(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}
	recipient := initProject(t, flags)

	foreign, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	mine, err := secrets.Encrypt("one", recipient)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	also, err := secrets.Encrypt("two", recipient)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	theirs, err := secrets.Encrypt("not-mine", foreign.Recipient())
	if err != nil {
		t.Fatalf("encrypt foreign: %v", err)
	}
	defaults := "vars:\n  a: " + mine + "\n  b: " + also + "\n  c: " + theirs + "\n"
	if err := os.WriteFile(filepath.Join(root, "workspace", "defaults.yml"), []byte(defaults), 0o644); err != nil {
		t.Fatalf("writing defaults.yml: %v", err)
	}
	writeAgeFile(t, root, "app/creds.json.age", recipient, `{"ok":true}`)
	writeAgeFile(t, root, "app/foreign.env.age", foreign.Recipient(), "X=1")

	exported, keyfile := secondMachine(t, recipient)
	identityFile := filepath.Join(t.TempDir(), "identity.txt")
	if err := os.WriteFile(identityFile, []byte(exported), 0o600); err != nil {
		t.Fatalf("writing identity file: %v", err)
	}

	out, _, err := runSecrets(t, flags, "key", "import", "--file", identityFile)
	if err != nil {
		t.Fatalf("secrets key import: %v", err)
	}
	var data keyImportJSON
	if e := json.Unmarshal([]byte(out), &data); e != nil {
		t.Fatalf("unmarshal import json: %v\nraw: %s", e, out)
	}
	if data.Recipient != recipient || data.Keyfile != keyfile || data.ReportError != "" {
		t.Errorf("json = %+v, want recipient %q at %q with no report error", data, recipient, keyfile)
	}
	if data.MarkersReadable == nil || *data.MarkersReadable != 2 ||
		data.FilesReadable == nil || *data.FilesReadable != 1 {
		t.Errorf("readable counters = %v/%v, want 2/1", data.MarkersReadable, data.FilesReadable)
	}
	// A scan that ran reports its counters: a zero is a present field, and only
	// a scan that never ran omits them.
	for _, key := range []string{`"markers_readable"`, `"files_readable"`} {
		if !strings.Contains(out, key) {
			t.Errorf("json %s is missing %s", out, key)
		}
	}
}

// TestKeyImport_UnscannableSurface pins the honest degradation: the import
// still succeeds — the keyfile IS on disk, and an O_EXCL retry would refuse —
// but a scan that could not run says so instead of counting zero, which would
// read as "your key opens nothing".
func TestKeyImport_UnscannableSurface(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	packs := filepath.Join(root, "workspace", "templates", "config")
	if err := os.MkdirAll(packs, 0o755); err != nil {
		t.Fatalf("creating pack dir: %v", err)
	}
	if err := os.Chmod(packs, 0o000); err != nil {
		t.Fatalf("locking pack dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(packs, 0o755) })

	exported, keyfile := secondMachine(t, recipient)
	identityFile := filepath.Join(t.TempDir(), "identity.txt")
	if err := os.WriteFile(identityFile, []byte(exported), 0o600); err != nil {
		t.Fatalf("writing identity file: %v", err)
	}

	out, _, err := runSecrets(t, flags, "key", "import", "--file", identityFile)
	if err != nil {
		t.Fatalf("secrets key import over an unscannable pack tree: %v", err)
	}
	if _, serr := os.Stat(keyfile); serr != nil {
		t.Fatalf("the identity was not stored: %v", serr)
	}
	if !strings.Contains(out, "the readability report could not be built") {
		t.Errorf("output does not report the failed scan:\n%s", out)
	}
	if strings.Contains(out, "are now readable") {
		t.Errorf("a scan that never ran was reported as a count:\n%s", out)
	}
}

// TestKeyImport_PromptCancelled pins Esc: a typed refusal carrying the fix, and
// no keyfile.
func TestKeyImport_PromptCancelled(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)
	_, keyfile := secondMachine(t, recipient)

	stubStdinTTY(t, true)
	stubPromptIdentity(t, func(context.Context, string, io.Reader, io.Writer) (secrets.Identity, error) {
		return secrets.Identity{}, widgets.ErrCancelled
	})

	_, _, err := runSecrets(t, flags, "key", "import")
	if err == nil {
		t.Fatal("a cancelled import succeeded")
	}
	coded := codedError(t, err)
	if coded.Code != "secrets_import_cancelled" {
		t.Fatalf("code = %q, want secrets_import_cancelled", coded.Code)
	}
	if !strings.Contains(coded.Hint, "key import") {
		t.Errorf("hint %q does not carry the fix instruction", coded.Hint)
	}
	if _, serr := os.Stat(keyfile); serr == nil {
		t.Error("a cancelled import wrote a keyfile")
	}
}

// TestKeyImport_PromptRejectsForeignIdentity pins the mismatch check on the
// prompt branch too — the form validates in place, but the seam is stubbable
// and huh's accessible mode can hand back an unvalidated value.
func TestKeyImport_PromptRejectsForeignIdentity(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)
	_, keyfile := secondMachine(t, recipient)

	foreign, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	stubStdinTTY(t, true)
	stubPromptIdentity(t, func(context.Context, string, io.Reader, io.Writer) (secrets.Identity, error) {
		return foreign, nil
	})

	_, _, err = runSecrets(t, flags, "key", "import")
	if err == nil {
		t.Fatal("import of a foreign identity through the prompt succeeded")
	}
	coded := codedError(t, err)
	if coded.Code != "secrets_identity_mismatch" {
		t.Fatalf("code = %q, want secrets_identity_mismatch", coded.Code)
	}
	// The whole error envelope, not just the rendered table: the typed key must
	// not survive into message, hint, any detail, or the serialized JSON form a
	// script would capture.
	surfaces := []string{coded.Message, coded.Hint, jsonErrorEnvelope(t, err)}
	for _, v := range coded.Details {
		surfaces = append(surfaces, fmt.Sprint(v))
	}
	assertNoKeyTail(t, foreign.Export(), surfaces...)
	if _, serr := os.Stat(keyfile); serr == nil {
		t.Error("a refused import wrote a keyfile")
	}
}

// TestKeyImport_ExistingKeyfileRefusedBeforePrompt pins R1.6: the write is
// O_EXCL, so a doomed import must not first make the developer hand over a
// private key. The --file / stdin order (parse → recipient → write) is
// deliberately unchanged; see TestKeyImport_RejectsMismatch.
func TestKeyImport_ExistingKeyfileRefusedBeforePrompt(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	stubStdinTTY(t, true)
	forbidPrompt(t)

	_, _, err := runSecrets(t, flags, "key", "import")
	if err == nil {
		t.Fatal("import over an existing keyfile succeeded")
	}
	coded := codedError(t, err)
	if coded.Code != "secrets_keyfile_write_failed" {
		t.Errorf("code = %q, want secrets_keyfile_write_failed", coded.Code)
	}
	if !strings.Contains(coded.Message, recipient) {
		t.Errorf("message %q does not name the recipient", coded.Message)
	}
}

// TestKeyImport_DanglingSymlinkRefusedBeforePrompt pins the same R1.6 guard for
// the path shape Stat cannot see: O_EXCL fails on a dangling symlink too, so
// following the link would collect a private key for a write that cannot land.
func TestKeyImport_DanglingSymlinkRefusedBeforePrompt(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)
	path := danglingKeyfile(t, recipient)

	stubStdinTTY(t, true)
	forbidPrompt(t)

	_, _, err := runSecrets(t, flags, "key", "import")
	if err == nil {
		t.Fatal("import over a dangling keyfile symlink succeeded")
	}
	if coded := codedError(t, err); coded.Code != "secrets_keyfile_write_failed" {
		t.Errorf("code = %q, want secrets_keyfile_write_failed", coded.Code)
	}
	if _, serr := os.Lstat(path); serr != nil {
		t.Errorf("the refused import disturbed the existing path entry: %v", serr)
	}
}

// TestKeyImport_NoPromptWhenNonInteractive pins R3.2 for this command: a piped
// stdin, --output json at a terminal and DWE_NONINTERACTIVE all keep today's
// typed refusal, and none of them opens a form.
func TestKeyImport_NoPromptWhenNonInteractive(t *testing.T) {
	cases := []struct {
		name     string
		tty      bool
		output   string
		nonInter bool
		wantCode string
	}{
		{name: "piped stdin", tty: false, wantCode: "secrets_identity_source_required"},
		{name: "json at a terminal", tty: true, output: "json", wantCode: "secrets_identity_source_required"},
		{name: "DWE_NONINTERACTIVE", tty: true, nonInter: true, wantCode: "secrets_identity_source_required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			cfgPath, root := writeFixture(t)
			flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: tc.output}
			recipient := initProject(t, flags)
			secondMachine(t, recipient)

			if tc.nonInter {
				t.Setenv("DWE_NONINTERACTIVE", "1")
			} else {
				t.Setenv("DWE_NONINTERACTIVE", "")
			}
			stubStdinTTY(t, tc.tty)
			forbidPrompt(t)

			_, _, err := runSecrets(t, flags, "key", "import")
			if err == nil {
				t.Fatal("import without an identity source succeeded")
			}
			if coded := codedError(t, err); coded.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", coded.Code, tc.wantCode)
			}
		})
	}
}

// jsonErrorEnvelope renders err the way main.go does in --output json mode, so
// a leak assertion covers the bytes a script actually reads rather than only the
// typed fields they are built from.
func jsonErrorEnvelope(t *testing.T, err error) string {
	t.Helper()
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&buf)
	cmdctx.WriteError(&cmdctx.RootFlags{Output: "json"}, cmd, err)
	return buf.String()
}

// assertNoKeyTail pins that no surface carries the tail of a private key. The
// prefix alone is too weak an assertion: a message that truncates
// `AGE-SECRET-KEY-…` still leaks most of the key.
func assertNoKeyTail(t *testing.T, key string, surfaces ...string) {
	t.Helper()
	tail := key
	if len(tail) > 20 {
		tail = tail[len(tail)-20:]
	}
	for _, s := range surfaces {
		if s == "" {
			continue
		}
		if strings.Contains(s, tail) {
			t.Errorf("output leaked the private key tail:\n%s", s)
		}
		if strings.Contains(s, "AGE-SECRET-KEY-") {
			t.Errorf("output leaked private key material:\n%s", s)
		}
	}
}

// --- key list / key remove ---------------------------------------------------

// stubRunConfirm swaps the removal confirmation. Package-level state: callers
// MUST NOT run in parallel.
func stubRunConfirm(t *testing.T, fn func(title, affirmative, negative string) (bool, error)) {
	t.Helper()
	prev := runConfirm
	runConfirm = fn
	t.Cleanup(func() { runConfirm = prev })
}

// forbidConfirm installs a confirmation that fails the test if it is opened.
func forbidConfirm(t *testing.T) {
	t.Helper()
	stubRunConfirm(t, func(string, string, string) (bool, error) {
		t.Error("the removal confirmation opened in a mode that must never prompt")
		return false, nil
	})
}

// writeKeyfile drops a file straight into the isolated keys directory, which is
// how the broken shapes (`unreadable`, `unparsable`, `misnamed`) are produced —
// no dwe command can create one.
func writeKeyfile(t *testing.T, name, content string, mode os.FileMode) string {
	t.Helper()
	dir, err := secrets.KeysDir()
	if err != nil {
		t.Fatalf("keys dir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating keys dir: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return path
}

// danglingKeyfile replaces the canonical keyfile with a symlink to a path that
// does not exist. It is the one keyfile shape os.Stat reports as absent while
// O_EXCL still refuses to write over it, so both the import guard and the
// removal have to see it through os.Lstat.
func danglingKeyfile(t *testing.T, recipient string) string {
	t.Helper()
	path, err := secrets.KeyfilePath(recipient)
	if err != nil {
		t.Fatalf("keyfile path: %v", err)
	}
	if rerr := os.Remove(path); rerr != nil && !os.IsNotExist(rerr) {
		t.Fatalf("clearing %s: %v", path, rerr)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("creating keys dir: %v", err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone.key"), path); err != nil {
		t.Fatalf("creating dangling symlink: %v", err)
	}
	return path
}

// foreignIdentity mints a key pair that belongs to no project here.
func foreignIdentity(t *testing.T) secrets.Identity {
	t.Helper()
	id, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	return id
}

// TestKeyList_ReportsEveryKeyfile pins the listing over all five shapes: the
// project's own identity, a foreign one, and the three broken files. Neither
// output may carry the unparsable file's content.
func TestKeyList_ReportsEveryKeyfile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	current := initProject(t, flags)

	foreign := foreignIdentity(t)
	writeKeyfile(t, foreign.Recipient()+".key", foreign.Export()+"\n", 0o600)
	const junk = "definitely-not-a-key-0123456789"
	writeKeyfile(t, "age1junk.key", junk, 0o600)
	unreadable := writeKeyfile(t, "age1locked.key", "whatever", 0o000)
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o600) })
	misnamed := foreignIdentity(t)
	writeKeyfile(t, "age1stale.key", misnamed.Export()+"\n", 0o600)

	out, _, err := runSecrets(t, flags, "key", "list")
	if err != nil {
		t.Fatalf("secrets key list: %v", err)
	}
	for _, want := range []string{current, foreign.Recipient(), misnamed.Recipient(),
		"age1junk", "age1locked", "ok (current project)", "unparsable", "unreadable", "misnamed"} {
		if !strings.Contains(out, want) {
			t.Errorf("listing is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, junk) {
		t.Errorf("listing echoed an unparsable file's content:\n%s", out)
	}
	assertNoKeyTail(t, foreign.Export(), out)

	jsonFlags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}
	raw, _, err := runSecrets(t, jsonFlags, "key", "list")
	if err != nil {
		t.Fatalf("secrets key list --output json: %v", err)
	}
	var data keyListJSON
	if e := json.Unmarshal([]byte(raw), &data); e != nil {
		t.Fatalf("unmarshal key list json: %v\nraw: %s", e, raw)
	}
	if len(data.Keys) != 5 {
		t.Fatalf("got %d rows, want 5: %+v", len(data.Keys), data.Keys)
	}
	states := map[string]string{}
	currents := 0
	for _, k := range data.Keys {
		states[k.Recipient] = k.State
		if k.Current {
			currents++
			if k.Recipient != current {
				t.Errorf("current marker on %s, want %s", k.Recipient, current)
			}
		}
		if !strings.HasSuffix(k.File, ".key") {
			t.Errorf("file = %q, want an absolute *.key path", k.File)
		}
	}
	if currents != 1 {
		t.Errorf("%d rows marked current, want exactly 1", currents)
	}
	want := map[string]string{
		current:              "ok",
		foreign.Recipient():  "ok",
		misnamed.Recipient(): "misnamed",
		"age1junk":           "unparsable",
		"age1locked":         "unreadable",
	}
	for recipient, state := range want {
		if states[recipient] != state {
			t.Errorf("%s: state = %q, want %q", recipient, states[recipient], state)
		}
	}
	assertNoKeyTail(t, foreign.Export(), raw)
	if strings.Contains(raw, junk) {
		t.Errorf("json echoed an unparsable file's content:\n%s", raw)
	}
}

// TestKeyList_OutsideProject pins that the listing works with no project
// resolved (both housekeeping subcommands are in allowedWithoutProject) and
// marks nothing as current.
func TestKeyList_OutsideProject(t *testing.T) {
	isolateHome(t)
	foreign := foreignIdentity(t)
	writeKeyfile(t, foreign.Recipient()+".key", foreign.Export()+"\n", 0o600)

	flags := &cmdctx.RootFlags{Output: "json"}
	raw, _, err := runSecrets(t, flags, "key", "list")
	if err != nil {
		t.Fatalf("secrets key list outside a project: %v", err)
	}
	var data keyListJSON
	if e := json.Unmarshal([]byte(raw), &data); e != nil {
		t.Fatalf("unmarshal key list json: %v\nraw: %s", e, raw)
	}
	if len(data.Keys) != 1 || data.Keys[0].Recipient != foreign.Recipient() {
		t.Fatalf("rows = %+v, want the one installed identity", data.Keys)
	}
	if data.Keys[0].Current {
		t.Error("a row is marked current with no project resolved")
	}
}

// TestKeyList_EmptyDirectory pins the empty listing: a finished report naming
// where identities live, and `[]` rather than null in JSON.
func TestKeyList_EmptyDirectory(t *testing.T) {
	isolateHome(t)
	flags := &cmdctx.RootFlags{}

	out, _, err := runSecrets(t, flags, "key", "list")
	if err != nil {
		t.Fatalf("secrets key list: %v", err)
	}
	if !strings.Contains(out, "No identities in") {
		t.Errorf("empty listing = %q, want the no-identities note", out)
	}

	jsonFlags := &cmdctx.RootFlags{Output: "json"}
	raw, _, err := runSecrets(t, jsonFlags, "key", "list")
	if err != nil {
		t.Fatalf("secrets key list --output json: %v", err)
	}
	if !strings.Contains(raw, `"keys": []`) && !strings.Contains(raw, `"keys":[]`) {
		t.Errorf("json = %s, want an empty keys array", raw)
	}
}

// TestKeyRemove_RemovesForeignIdentity pins the happy path in both output
// modes: the file is gone and the payload says so.
func TestKeyRemove_RemovesForeignIdentity(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	initProject(t, flags)
	forbidConfirm(t)

	foreign := foreignIdentity(t)
	path := writeKeyfile(t, foreign.Recipient()+".key", foreign.Export()+"\n", 0o600)

	out, _, err := runSecrets(t, flags, "key", "remove", foreign.Recipient(), "--yes")
	if err != nil {
		t.Fatalf("secrets key remove: %v", err)
	}
	if !strings.Contains(out, "removed "+path) {
		t.Errorf("output = %q, want the removed path", out)
	}
	if _, serr := os.Stat(path); !os.IsNotExist(serr) {
		t.Errorf("keyfile still present after remove: %v", serr)
	}

	second := foreignIdentity(t)
	secondPath := writeKeyfile(t, second.Recipient()+".key", second.Export()+"\n", 0o600)
	jsonFlags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}
	raw, _, err := runSecrets(t, jsonFlags, "key", "remove", second.Recipient(), "--yes")
	if err != nil {
		t.Fatalf("secrets key remove --output json: %v", err)
	}
	var data keyRemoveJSON
	if e := json.Unmarshal([]byte(raw), &data); e != nil {
		t.Fatalf("unmarshal key remove json: %v\nraw: %s", e, raw)
	}
	if data.Recipient != second.Recipient() || data.Keyfile != secondPath || !data.Removed {
		t.Errorf("payload = %+v, want the removed keyfile", data)
	}
	if _, serr := os.Stat(secondPath); !os.IsNotExist(serr) {
		t.Errorf("keyfile still present after remove: %v", serr)
	}
	assertNoKeyTail(t, second.Export(), out, raw)
}

// TestKeyRemove_MissingFile pins the typed refusal when nothing is installed
// for the recipient.
func TestKeyRemove_MissingFile(t *testing.T) {
	isolateHome(t)
	flags := &cmdctx.RootFlags{}
	forbidConfirm(t)
	foreign := foreignIdentity(t)

	_, _, err := runSecrets(t, flags, "key", "remove", foreign.Recipient(), "--yes")
	if err == nil {
		t.Fatal("removing an absent identity succeeded")
	}
	if coded := codedError(t, err); coded.Code != "secrets_key_not_found" {
		t.Errorf("code = %q, want secrets_key_not_found", coded.Code)
	}
}

// TestKeyRemove_InvalidRecipient pins that a malformed argument never reaches
// the filesystem.
func TestKeyRemove_InvalidRecipient(t *testing.T) {
	isolateHome(t)
	flags := &cmdctx.RootFlags{}
	forbidConfirm(t)

	_, _, err := runSecrets(t, flags, "key", "remove", "../../etc/passwd", "--yes")
	if err == nil {
		t.Fatal("removing a malformed recipient succeeded")
	}
	if coded := codedError(t, err); coded.Code != "secrets_recipient_invalid" {
		t.Errorf("code = %q, want secrets_recipient_invalid", coded.Code)
	}
}

// TestKeyRemove_CurrentRecipientNeedsForce pins the guard on the one key whose
// loss is not recoverable from the repository, and that --force lifts it.
func TestKeyRemove_CurrentRecipientNeedsForce(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)
	forbidConfirm(t)
	path, err := secrets.KeyfilePath(recipient)
	if err != nil {
		t.Fatalf("keyfile path: %v", err)
	}

	_, _, err = runSecrets(t, flags, "key", "remove", recipient, "--yes")
	if err == nil {
		t.Fatal("removing the project's own identity succeeded without --force")
	}
	coded := codedError(t, err)
	if coded.Code != "secrets_key_in_use" {
		t.Errorf("code = %q, want secrets_key_in_use", coded.Code)
	}
	if !strings.Contains(coded.Hint, "--force") {
		t.Errorf("hint = %q, want it to name --force", coded.Hint)
	}
	if _, serr := os.Stat(path); serr != nil {
		t.Fatalf("a refused removal deleted the keyfile: %v", serr)
	}

	if _, _, ferr := runSecrets(t, flags, "key", "remove", recipient, "--force", "--yes"); ferr != nil {
		t.Fatalf("secrets key remove --force: %v", ferr)
	}
	if _, serr := os.Stat(path); !os.IsNotExist(serr) {
		t.Errorf("--force did not remove the keyfile: %v", serr)
	}
}

// TestKeyRemove_CurrentRecipientNotInstalled pins that the existence check runs
// BEFORE the in-use guard: on a machine that never imported the project's key
// there is nothing to protect, and "export it first" names a file that is not
// there.
func TestKeyRemove_CurrentRecipientNotInstalled(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)
	forbidConfirm(t)
	path, err := secrets.KeyfilePath(recipient)
	if err != nil {
		t.Fatalf("keyfile path: %v", err)
	}
	if rerr := os.Remove(path); rerr != nil {
		t.Fatalf("remove keyfile: %v", rerr)
	}

	_, _, err = runSecrets(t, flags, "key", "remove", recipient, "--yes")
	if err == nil {
		t.Fatal("removing an absent identity succeeded")
	}
	if coded := codedError(t, err); coded.Code != "secrets_key_not_found" {
		t.Errorf("code = %q, want secrets_key_not_found", coded.Code)
	}
}

// TestKeyRemove_DanglingSymlink pins that the command which clears the way can
// clear this shape too: `key list` reports the dangling symlink as an
// unreadable keyfile and O_EXCL refuses to write over it, so answering
// "nothing is installed" would leave the path unremovable through dwe. The
// symlink itself is unlinked; nothing is followed.
func TestKeyRemove_DanglingSymlink(t *testing.T) {
	isolateHome(t)
	flags := &cmdctx.RootFlags{}
	foreign := foreignIdentity(t)
	path := danglingKeyfile(t, foreign.Recipient())

	if _, _, err := runSecrets(t, flags, "key", "remove", foreign.Recipient(), "--yes"); err != nil {
		t.Fatalf("secrets key remove over a dangling symlink: %v", err)
	}
	if _, serr := os.Lstat(path); !os.IsNotExist(serr) {
		t.Errorf("the dangling symlink survived the removal: %v", serr)
	}
}

// TestKeyRemove_UnreadableNeedsForce pins the one shape the content guard
// cannot answer. os.Remove needs no read permission on the file, so a keyfile
// whose bytes are unreachable would otherwise be unlinked without anyone having
// ruled out that it holds live key material — the single irreversible outcome
// this command has. `--force` still gets through, which is what keeps keygate's
// "remove it and import the right one" prescription usable.
func TestKeyRemove_UnreadableNeedsForce(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}
	isolateHome(t)
	flags := &cmdctx.RootFlags{}
	foreign := foreignIdentity(t)
	path := writeKeyfile(t, foreign.Recipient()+".key", foreign.Export()+"\n", 0o000)
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	forbidConfirm(t)

	_, _, err := runSecrets(t, flags, "key", "remove", foreign.Recipient(), "--yes")
	if err == nil {
		t.Fatal("an unreadable keyfile was removed without --force")
	}
	if coded := codedError(t, err); coded.Code != "secrets_key_unreadable" {
		t.Errorf("code = %q, want secrets_key_unreadable", coded.Code)
	}
	if _, serr := os.Lstat(path); serr != nil {
		t.Fatalf("the refused removal deleted the file anyway: %v", serr)
	}

	if _, _, ferr := runSecrets(t, flags, "key", "remove", foreign.Recipient(), "--yes", "--force"); ferr != nil {
		t.Fatalf("secrets key remove --force over an unreadable keyfile: %v", ferr)
	}
	if _, serr := os.Lstat(path); !os.IsNotExist(serr) {
		t.Errorf("the forced removal left the file behind: %v", serr)
	}
}

// TestKeyRemove_NoConfirmationInNonInteractiveModes pins R3.2 for this command:
// every mode that cannot ask refuses instead of deleting, and the file stays.
func TestKeyRemove_NoConfirmationInNonInteractiveModes(t *testing.T) {
	cases := []struct {
		name     string
		tty      bool
		output   string
		nonInter bool
	}{
		{name: "piped stdin", tty: false},
		{name: "json at a terminal", tty: true, output: "json"},
		{name: "DWE_NONINTERACTIVE", tty: true, nonInter: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			flags := &cmdctx.RootFlags{Output: tc.output}
			foreign := foreignIdentity(t)
			path := writeKeyfile(t, foreign.Recipient()+".key", foreign.Export()+"\n", 0o600)

			if tc.nonInter {
				t.Setenv("DWE_NONINTERACTIVE", "1")
			} else {
				t.Setenv("DWE_NONINTERACTIVE", "")
			}
			stubStdinTTY(t, tc.tty)
			forbidConfirm(t)

			_, _, err := runSecrets(t, flags, "key", "remove", foreign.Recipient())
			if err == nil {
				t.Fatal("removal without --yes succeeded")
			}
			coded := codedError(t, err)
			if coded.Code != "secrets_confirmation_required" {
				t.Errorf("code = %q, want secrets_confirmation_required", coded.Code)
			}
			if _, serr := os.Stat(path); serr != nil {
				t.Errorf("a refused removal deleted the keyfile: %v", serr)
			}
			assertNoKeyTail(t, foreign.Export(), coded.Message, coded.Hint, jsonErrorEnvelope(t, err))
		})
	}
}

// TestKeyRemove_DeclineIsANoOp pins that answering "Cancel" leaves the file in
// place and still exits 0 — nothing was asked for that could still be done.
func TestKeyRemove_DeclineIsANoOp(t *testing.T) {
	isolateHome(t)
	flags := &cmdctx.RootFlags{}
	foreign := foreignIdentity(t)
	path := writeKeyfile(t, foreign.Recipient()+".key", foreign.Export()+"\n", 0o600)

	stubStdinTTY(t, true)
	t.Setenv("DWE_NONINTERACTIVE", "")
	asked := 0
	stubRunConfirm(t, func(title, _, _ string) (bool, error) {
		asked++
		if !strings.Contains(title, foreign.Recipient()) {
			t.Errorf("confirmation title = %q, want it to name the recipient", title)
		}
		return false, nil
	})

	_, errOut, err := runSecrets(t, flags, "key", "remove", foreign.Recipient())
	if err != nil {
		t.Fatalf("a declined removal returned an error: %v", err)
	}
	if asked != 1 {
		t.Errorf("confirmation asked %d times, want 1", asked)
	}
	if !strings.Contains(errOut, "kept "+path) {
		t.Errorf("stderr = %q, want the kept note", errOut)
	}
	if _, serr := os.Stat(path); serr != nil {
		t.Errorf("a declined removal deleted the keyfile: %v", serr)
	}
}

// TestKeyRemove_ConfirmedRemoves pins the interactive accept path.
func TestKeyRemove_ConfirmedRemoves(t *testing.T) {
	isolateHome(t)
	flags := &cmdctx.RootFlags{}
	foreign := foreignIdentity(t)
	path := writeKeyfile(t, foreign.Recipient()+".key", foreign.Export()+"\n", 0o600)

	stubStdinTTY(t, true)
	t.Setenv("DWE_NONINTERACTIVE", "")
	stubRunConfirm(t, func(string, string, string) (bool, error) { return true, nil })

	if _, _, err := runSecrets(t, flags, "key", "remove", foreign.Recipient()); err != nil {
		t.Fatalf("secrets key remove: %v", err)
	}
	if _, serr := os.Stat(path); !os.IsNotExist(serr) {
		t.Errorf("keyfile still present after a confirmed remove: %v", serr)
	}
}

// TestKeyRemove_UnusableCurrentKeyfileNeedsNoForce closes the loop keygate's
// ErrKeyfileUnusable opens: it tells the developer to run exactly this command
// on the canonical keyfile when that file exists but opens nothing. Inside the
// project the argument always equals secrets.recipient, so a name-based in-use
// guard refused the prescribed fix — and its "export it first" hint named a
// command that fails for the same reason. Nothing is at risk here: the file
// already decrypts nothing.
func TestKeyRemove_UnusableCurrentKeyfileNeedsNoForce(t *testing.T) {
	cases := map[string]func(t *testing.T, recipient string) string{
		"unparsable": func(t *testing.T, recipient string) string {
			return writeKeyfile(t, recipient+".key", "not an age key\n", 0o600)
		},
		"foreign key under the current name": func(t *testing.T, recipient string) string {
			return writeKeyfile(t, recipient+".key", foreignIdentity(t).Export()+"\n", 0o600)
		},
	}
	for name, seed := range cases {
		t.Run(name, func(t *testing.T) {
			isolateHome(t)
			cfgPath, root := writeFixture(t)
			flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
			recipient := initProject(t, flags)
			// initProject installed the real keyfile; replace it in place with
			// the unusable one the gate would have complained about.
			path, err := secrets.KeyfilePath(recipient)
			if err != nil {
				t.Fatalf("keyfile path: %v", err)
			}
			if rerr := os.Remove(path); rerr != nil {
				t.Fatalf("clearing the installed keyfile: %v", rerr)
			}
			path = seed(t, recipient)
			forbidConfirm(t)

			if _, _, rerr := runSecrets(t, flags, "key", "remove", recipient, "--yes"); rerr != nil {
				t.Fatalf("secrets key remove: %v", rerr)
			}
			if _, serr := os.Stat(path); !os.IsNotExist(serr) {
				t.Errorf("the unusable keyfile survived: %v", serr)
			}
		})
	}
}

// TestKeyRemove_MisnamedFileHoldingCurrentKeyNeedsForce is the mirror image:
// the file name is foreign but its CONTENT is the project's own identity, and
// deleting it destroys the only copy. A name-based guard waved this through.
func TestKeyRemove_MisnamedFileHoldingCurrentKeyNeedsForce(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)
	forbidConfirm(t)

	canonical, err := secrets.KeyfilePath(recipient)
	if err != nil {
		t.Fatalf("keyfile path: %v", err)
	}
	key, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatalf("reading the installed keyfile: %v", err)
	}
	stale := foreignIdentity(t).Recipient()
	path := writeKeyfile(t, stale+".key", string(key), 0o600)

	_, _, err = runSecrets(t, flags, "key", "remove", stale, "--yes")
	if err == nil {
		t.Fatal("removing a misnamed file holding the project's key succeeded without --force")
	}
	coded := codedError(t, err)
	if coded.Code != "secrets_key_in_use" {
		t.Errorf("code = %q, want secrets_key_in_use", coded.Code)
	}
	// `key export` reads keys/<current>.key, a different file — naming it here
	// would point the rescue at something that does not hold this key.
	if strings.Contains(coded.Hint, "key export") {
		t.Errorf("hint = %q, want it not to name `key export` for a misnamed file", coded.Hint)
	}
	if _, serr := os.Stat(path); serr != nil {
		t.Errorf("a refused removal deleted the keyfile: %v", serr)
	}

	if _, _, ferr := runSecrets(t, flags, "key", "remove", stale, "--force", "--yes"); ferr != nil {
		t.Fatalf("secrets key remove --force: %v", ferr)
	}
	if _, serr := os.Stat(path); !os.IsNotExist(serr) {
		t.Errorf("--force did not remove the keyfile: %v", serr)
	}
}

// TestKeyRemove_MisnamedFileNeverTargeted pins that only the canonical
// <recipient>.key is deleted: a file whose name does not match the identity it
// holds is reported by `key list` and left alone.
func TestKeyRemove_MisnamedFileNeverTargeted(t *testing.T) {
	isolateHome(t)
	flags := &cmdctx.RootFlags{}
	forbidConfirm(t)
	misnamed := foreignIdentity(t)
	path := writeKeyfile(t, "age1stale.key", misnamed.Export()+"\n", 0o600)

	_, _, err := runSecrets(t, flags, "key", "remove", misnamed.Recipient(), "--yes")
	if err == nil {
		t.Fatal("removing a misnamed file's recipient succeeded")
	}
	if coded := codedError(t, err); coded.Code != "secrets_key_not_found" {
		t.Errorf("code = %q, want secrets_key_not_found", coded.Code)
	}
	if _, serr := os.Stat(path); serr != nil {
		t.Errorf("the misnamed file was removed: %v", serr)
	}
}
