package secrets

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
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
