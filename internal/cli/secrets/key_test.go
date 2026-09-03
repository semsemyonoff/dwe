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
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("import output = %q, want two lines", out)
	}
	if !strings.Contains(lines[0], recipient) || !strings.Contains(lines[0], keyfile) {
		t.Errorf("first line %q should name the recipient and the keyfile", lines[0])
	}
	if lines[1] != "1 encrypted value(s) and 0 .age file(s) are now readable" {
		t.Errorf("report line = %q", lines[1])
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
	want := keyImportJSON{Recipient: recipient, Keyfile: keyfile, MarkersReadable: 2, FilesReadable: 1}
	if data != want {
		t.Errorf("json = %+v, want %+v", data, want)
	}
	// The counters carry no omitempty: a zero must still be a present field.
	for _, key := range []string{`"markers_readable"`, `"files_readable"`} {
		if !strings.Contains(out, key) {
			t.Errorf("json %s is missing %s", out, key)
		}
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
