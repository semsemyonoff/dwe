package secrets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
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
	if !strings.Contains(coded.Hint, "rekey") {
		t.Errorf("hint = %q, want it to point at rekey", coded.Hint)
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
