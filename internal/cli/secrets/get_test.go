package secrets

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// writeDefaults writes workspace/defaults.yml for a fixture project.
func writeDefaults(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(defaultsPath(root), []byte(body), 0o644); err != nil {
		t.Fatalf("writing defaults.yml: %v", err)
	}
}

// TestGet_RoundTripsSet pins the pair: what `set` encrypted, `get` prints back.
func TestGet_RoundTripsSet(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	initProject(t, flags)

	if _, _, err := runSecrets(t, flags, "set", "vars.telegram.token", "123:abc"); err != nil {
		t.Fatalf("secrets set: %v", err)
	}
	out, _, err := runSecrets(t, flags, "get", "vars.telegram.token")
	if err != nil {
		t.Fatalf("secrets get: %v", err)
	}
	if strings.TrimRight(out, "\n") != "123:abc" {
		t.Errorf("get printed %q, want %q", out, "123:abc")
	}
	if strings.Contains(out, secrets.MarkerPrefix) {
		t.Errorf("get printed the marker instead of the plaintext: %q", out)
	}
}

// TestGet_HighestLayerWins pins the precedence: with the same path encrypted in
// two layers, `get` reports the one the merged config would have used.
func TestGet_HighestLayerWins(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	low, err := secrets.Encrypt("from-defaults", recipient)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	high, err := secrets.Encrypt("from-local", recipient)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	writeDefaults(t, root, "vars:\n  token: "+low+"\n")
	if err := os.WriteFile(filepath.Join(root, "workspace", "local.yml"),
		[]byte("vars:\n  token: "+high+"\n"), 0o600); err != nil {
		t.Fatalf("writing local.yml: %v", err)
	}

	out, _, err := runSecrets(t, flags, "get", "vars.token")
	if err != nil {
		t.Fatalf("secrets get: %v", err)
	}
	if strings.TrimRight(out, "\n") != "from-local" {
		t.Errorf("get printed %q, want the highest layer's value %q", out, "from-local")
	}
}

// TestGet_PlaintextPathIsAnError pins that `get` reports only secrets: a
// plaintext value is `dwe vars get`'s job, and silently printing it here would
// blur the two.
func TestGet_PlaintextPathIsAnError(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	initProject(t, flags)

	for _, path := range []string{"vars.app.name", "vars.nothing.here"} {
		t.Run(path, func(t *testing.T) {
			_, _, err := runSecrets(t, flags, "get", path)
			if err == nil {
				t.Fatalf("get %s succeeded; want a refusal", path)
			}
			coded := codedError(t, err)
			if coded.Code != "secrets_not_encrypted" {
				t.Errorf("error code = %q, want secrets_not_encrypted", coded.Code)
			}
			if !strings.Contains(coded.Hint, "vars get") {
				t.Errorf("hint = %q, want it to point at 'dwe vars get'", coded.Hint)
			}
		})
	}
}

// TestGet_NoIdentity pins the keyless machine: the failure names the recipient
// and every place the lookup looked, and prints nothing.
func TestGet_NoIdentity(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	marker, err := secrets.Encrypt("s3cret", recipient)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	writeDefaults(t, root, "vars:\n  token: "+marker+"\n")

	keyfile, err := secrets.KeyfilePath(recipient)
	if err != nil {
		t.Fatalf("keyfile path: %v", err)
	}
	if err := os.Remove(keyfile); err != nil {
		t.Fatalf("removing keyfile: %v", err)
	}

	out, _, err := runSecrets(t, flags, "get", "vars.token")
	if err == nil {
		t.Fatal("get succeeded with no identity on this machine")
	}
	if out != "" {
		t.Errorf("get wrote %q to stdout on failure", out)
	}
	coded := codedError(t, err)
	if coded.Code != "secrets_no_identity" {
		t.Fatalf("error code = %q, want secrets_no_identity", coded.Code)
	}
	if !strings.Contains(coded.Hint, "key import") {
		t.Errorf("hint = %q, want it to point at 'dwe secrets key import'", coded.Hint)
	}
	if coded.Details["path"] != "vars.token" {
		t.Errorf("details = %+v, want the path named", coded.Details)
	}
}

// TestGet_ForeignAndCorrupt pins the two failures a present identity cannot fix,
// and that each keeps its own cause rather than being reported as a missing key.
func TestGet_ForeignAndCorrupt(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	initProject(t, flags)

	foreign, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	theirs, err := secrets.Encrypt("not-for-me", foreign.Recipient())
	if err != nil {
		t.Fatalf("encrypt foreign: %v", err)
	}
	corrupt := secrets.MarkerPrefix + base64.StdEncoding.EncodeToString([]byte("garbage")) + "]"
	writeDefaults(t, root, "vars:\n  foreign: "+theirs+"\n  broken: "+corrupt+"\n")

	for _, path := range []string{"vars.foreign", "vars.broken"} {
		t.Run(path, func(t *testing.T) {
			_, _, err := runSecrets(t, flags, "get", path)
			if err == nil {
				t.Fatalf("get %s succeeded", path)
			}
			coded := codedError(t, err)
			if coded.Code != "secrets_decrypt_failed" {
				t.Errorf("error code = %q, want secrets_decrypt_failed", coded.Code)
			}
			if coded.Details["path"] != path {
				t.Errorf("details = %+v, want the path named", coded.Details)
			}
			if coded.Details["layer"] == "" {
				t.Errorf("details = %+v, want the layer file named", coded.Details)
			}
		})
	}
}

// TestGet_HalfRekeyed pins decision 11's recovery property on the read path: a
// value still encrypted to the retired recipient is opened by its straggler
// keyfile instead of failing the command.
func TestGet_HalfRekeyed(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	oldRecipient := initProject(t, flags)

	oldMarker, err := secrets.Encrypt("old-value", oldRecipient)
	if err != nil {
		t.Fatalf("encrypt old: %v", err)
	}
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
	writeDefaults(t, root, "vars:\n  token: "+oldMarker+"\n")

	out, _, err := runSecrets(t, flags, "get", "vars.token")
	if err != nil {
		t.Fatalf("secrets get on a half-rekeyed tree: %v", err)
	}
	if strings.TrimRight(out, "\n") != "old-value" {
		t.Errorf("get printed %q, want %q", out, "old-value")
	}
}

// TestGet_JSON pins the DTO.
func TestGet_JSON(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}
	initProject(t, flags)

	if _, _, err := runSecrets(t, flags, "set", "vars.token", "s3cret"); err != nil {
		t.Fatalf("secrets set: %v", err)
	}
	out, errOut, err := runSecrets(t, flags, "get", "vars.token")
	if err != nil {
		t.Fatalf("secrets get --output json: %v", err)
	}
	if errOut != "" {
		t.Errorf("stderr should be empty in JSON mode, got: %q", errOut)
	}
	var data secretGetJSON
	if e := json.Unmarshal([]byte(out), &data); e != nil {
		t.Fatalf("unmarshal get json: %v\nraw: %s", e, out)
	}
	if data.Path != "vars.token" || data.Value != "s3cret" {
		t.Errorf("payload = %+v, want {vars.token s3cret}", data)
	}
	if strings.Contains(out, "AGE-SECRET-KEY-") {
		t.Errorf("get JSON leaked a private key: %s", out)
	}
}
