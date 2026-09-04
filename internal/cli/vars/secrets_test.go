package vars

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/ui/cmdbrowser"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// The fixture below exercises every combination the origin-scoped `encrypted`
// derivation has to get right:
//
//	vars.app.name      plaintext in workspace.yml            → default
//	vars.api.token     marker in workspace.yml               → default (encrypted)
//	vars.shadowed      marker in defaults.yml, plain local   → local, NOT encrypted
//	vars.db.password   plain in defaults.yml, marker local   → local (encrypted)
const (
	secretPlainToken    = "telegram-bot-token-value"
	secretPlainPassword = "db-password-value"
	secretPlainShadowed = "shadowed-secret-value"
)

// newVarsIdentity mints a project identity, installs it via DWE_AGE_KEY and
// isolates HOME so no test reads the developer's ~/.config/dwe/keys.
func newVarsIdentity(t *testing.T) secrets.Identity {
	t.Helper()
	id, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv(secrets.EnvKeyFile, "")
	t.Setenv(secrets.EnvKey, id.Export())
	return id
}

// hideVarsIdentity removes every identity source, so markers are unresolvable.
func hideVarsIdentity(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(secrets.EnvKey, "")
	t.Setenv(secrets.EnvKeyFile, "")
}

func mustEncryptVar(t *testing.T, plain, recipient string) string {
	t.Helper()
	marker, err := secrets.Encrypt(plain, recipient)
	if err != nil {
		t.Fatalf("encrypt %q: %v", plain, err)
	}
	return marker
}

// writeSecretVarsFixture writes the three-layer fixture described above and
// returns the workspace.yml path and the project root.
func writeSecretVarsFixture(t *testing.T, recipient string) (cfgPath, root string) {
	t.Helper()
	root = t.TempDir()
	cfgPath = filepath.Join(root, "workspace.yml")
	workspace := `schema_version: "2"
project:
  name: varstest
  prefix: dwe
secrets:
  recipient: ` + recipient + `
vars:
  app:
    name: myapp
  api:
    token: ` + mustEncryptVar(t, secretPlainToken, recipient) + "\n"
	if err := os.WriteFile(cfgPath, []byte(workspace), 0o644); err != nil {
		t.Fatalf("writing workspace.yml: %v", err)
	}
	wsDir := filepath.Join(root, "workspace")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	defaults := `vars:
  shadowed: ` + mustEncryptVar(t, secretPlainShadowed, recipient) + `
  db:
    password: plain-default
`
	if err := os.WriteFile(filepath.Join(wsDir, "defaults.yml"), []byte(defaults), 0o644); err != nil {
		t.Fatalf("writing defaults.yml: %v", err)
	}
	local := `vars:
  shadowed: visible-locally
  db:
    password: ` + mustEncryptVar(t, secretPlainPassword, recipient) + "\n"
	if err := os.WriteFile(filepath.Join(wsDir, "local.yml"), []byte(local), 0o644); err != nil {
		t.Fatalf("writing local.yml: %v", err)
	}
	return cfgPath, root
}

// assertNoCiphertext pins the negative half of every masking claim: neither the
// marker nor a private key may appear in a rendered surface.
func assertNoCiphertext(t *testing.T, label, out string) {
	t.Helper()
	for _, forbidden := range []string{secrets.MarkerPrefix, "AGE-SECRET-KEY-"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("%s leaked %q:\n%s", label, forbidden, out)
		}
	}
}

func TestVarsList_UnresolvedSecretText(t *testing.T) {
	id := newVarsIdentity(t)
	cfgPath, root := writeSecretVarsFixture(t, id.Recipient())
	hideVarsIdentity(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	out, _, err := runVarsCmd(t, flags, "list")
	if err != nil {
		t.Fatalf("vars list: %v", err)
	}
	assertNoCiphertext(t, "vars list", out)

	// Origin-scoped: only the leaves whose ORIGIN layer holds the marker.
	for _, want := range []string{
		"api.token",
		"<encrypted>",
		"[default (encrypted)]",
		"[local (encrypted)]",
		"visible-locally",
		"myapp",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list missing %q:\n%s", want, out)
		}
	}
	// vars.shadowed is a plaintext local override of a defaults.yml marker: it
	// must render as plaintext with a plain badge.
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, "shadowed") && strings.Contains(line, "encrypted") {
			t.Errorf("shadowed leaf marked encrypted: %q", line)
		}
	}
}

func TestVarsList_UnresolvedSecretJSON(t *testing.T) {
	id := newVarsIdentity(t)
	cfgPath, root := writeSecretVarsFixture(t, id.Recipient())
	hideVarsIdentity(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}

	out, _, err := runVarsCmd(t, flags, "list")
	if err != nil {
		t.Fatalf("vars list --output json: %v", err)
	}
	assertNoCiphertext(t, "vars list json", out)

	var payload struct {
		Vars []struct {
			Path      string `json:"path"`
			Value     any    `json:"value"`
			Layer     string `json:"layer"`
			Encrypted bool   `json:"encrypted"`
		} `json:"vars"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	want := map[string]struct {
		value     any
		layer     string
		encrypted bool
	}{
		"vars.app.name":    {value: "myapp", layer: "default"},
		"vars.api.token":   {value: "<encrypted>", layer: "default", encrypted: true},
		"vars.shadowed":    {value: "visible-locally", layer: "local"},
		"vars.db.password": {value: "<encrypted>", layer: "local", encrypted: true},
	}
	seen := map[string]bool{}
	for _, entry := range payload.Vars {
		exp, ok := want[entry.Path]
		if !ok {
			continue
		}
		seen[entry.Path] = true
		if entry.Value != exp.value {
			t.Errorf("%s value = %v, want %v", entry.Path, entry.Value, exp.value)
		}
		if entry.Layer != exp.layer {
			t.Errorf("%s layer = %q, want %q", entry.Path, entry.Layer, exp.layer)
		}
		if entry.Encrypted != exp.encrypted {
			t.Errorf("%s encrypted = %v, want %v", entry.Path, entry.Encrypted, exp.encrypted)
		}
	}
	for path := range want {
		if !seen[path] {
			t.Errorf("missing entry %q in %s", path, out)
		}
	}
}

// TestVarsList_JSONUnchangedWithoutSecrets pins the backward-compatibility
// promise: a project with no secrets emits exactly the historical shape (no
// "encrypted" key anywhere).
func TestVarsList_JSONUnchangedWithoutSecrets(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}

	out, _, err := runVarsCmd(t, flags, "list")
	if err != nil {
		t.Fatalf("vars list --output json: %v", err)
	}
	if strings.Contains(out, "encrypted") {
		t.Errorf("secret-free project grew an encrypted field:\n%s", out)
	}
}

func TestVarsList_DecryptedSecretShowsPlaintext(t *testing.T) {
	id := newVarsIdentity(t)
	cfgPath, root := writeSecretVarsFixture(t, id.Recipient())
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	out, _, err := runVarsCmd(t, flags, "list")
	if err != nil {
		t.Fatalf("vars list: %v", err)
	}
	assertNoCiphertext(t, "vars list", out)
	if !strings.Contains(out, secretPlainToken) {
		t.Errorf("decrypted value missing:\n%s", out)
	}
	if strings.Contains(out, "<encrypted>") || strings.Contains(out, "(encrypted)") {
		t.Errorf("decrypted project reported an encrypted leaf:\n%s", out)
	}
}

func TestVarsGet_UnresolvedLeafAndNamespace(t *testing.T) {
	id := newVarsIdentity(t)
	cfgPath, root := writeSecretVarsFixture(t, id.Recipient())
	hideVarsIdentity(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	leaf, _, err := runVarsCmd(t, flags, "get", "api.token")
	if err != nil {
		t.Fatalf("vars get api.token: %v", err)
	}
	assertNoCiphertext(t, "vars get leaf", leaf)
	if strings.TrimSpace(leaf) != "<encrypted>" {
		t.Errorf("leaf = %q, want <encrypted>", strings.TrimSpace(leaf))
	}

	ns, _, err := runVarsCmd(t, flags, "get", "db")
	if err != nil {
		t.Fatalf("vars get db: %v", err)
	}
	assertNoCiphertext(t, "vars get namespace", ns)
	if !strings.Contains(ns, "password: <encrypted>") {
		t.Errorf("namespace subtree not masked:\n%s", ns)
	}
}

func TestVarsGet_UnresolvedJSON(t *testing.T) {
	id := newVarsIdentity(t)
	cfgPath, root := writeSecretVarsFixture(t, id.Recipient())
	hideVarsIdentity(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}

	out, _, err := runVarsCmd(t, flags, "get", "api.token")
	if err != nil {
		t.Fatalf("vars get --output json: %v", err)
	}
	assertNoCiphertext(t, "vars get json", out)
	var payload struct {
		Var       string `json:"var"`
		Value     any    `json:"value"`
		Encrypted bool   `json:"encrypted"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if payload.Value != "<encrypted>" || !payload.Encrypted {
		t.Errorf("got %+v, want masked value with encrypted=true", payload)
	}

	// A plaintext var keeps the historical shape (no encrypted key).
	plain, _, err := runVarsCmd(t, flags, "get", "app.name")
	if err != nil {
		t.Fatalf("vars get app.name: %v", err)
	}
	if strings.Contains(plain, "encrypted") {
		t.Errorf("plaintext var grew an encrypted field:\n%s", plain)
	}
}

func TestVarsInspect_UnresolvedSecret(t *testing.T) {
	id := newVarsIdentity(t)
	cfgPath, root := writeSecretVarsFixture(t, id.Recipient())
	hideVarsIdentity(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	out, _, err := runVarsCmd(t, flags, "inspect", "api.token")
	if err != nil {
		t.Fatalf("vars inspect: %v", err)
	}
	assertNoCiphertext(t, "vars inspect", out)
	for _, want := range []string{"<encrypted>", "Secret", "no identity for " + id.Recipient()} {
		if !strings.Contains(out, want) {
			t.Errorf("inspect missing %q:\n%s", want, out)
		}
	}
}

// invalid_identity has no note of its own: unresolvedNote's default: arm words
// any new reason generically, so `vars inspect` degrades instead of going
// silent. Pinned here so the fallback is a decision, not an accident.
func TestVarsInspect_InvalidIdentityUsesGenericNote(t *testing.T) {
	id := newVarsIdentity(t)
	cfgPath, root := writeSecretVarsFixture(t, id.Recipient())
	hideVarsIdentity(t)
	truncated := id.Export()[:len(id.Export())-10]
	t.Setenv(secrets.EnvKey, truncated)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	out, _, err := runVarsCmd(t, flags, "inspect", "api.token")
	if err != nil {
		t.Fatalf("vars inspect: %v", err)
	}
	assertNoCiphertext(t, "vars inspect", out)
	if !strings.Contains(out, "unresolved (invalid_identity)") {
		t.Errorf("inspect missing the generic invalid_identity note:\n%s", out)
	}
	if strings.Contains(out, truncated[len(truncated)-20:]) {
		t.Errorf("inspect echoed the broken key text:\n%s", out)
	}
}

func TestVarsInspect_UnresolvedSecretJSON(t *testing.T) {
	id := newVarsIdentity(t)
	cfgPath, root := writeSecretVarsFixture(t, id.Recipient())
	hideVarsIdentity(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}

	out, _, err := runVarsCmd(t, flags, "inspect", "api.token")
	if err != nil {
		t.Fatalf("vars inspect --output json: %v", err)
	}
	assertNoCiphertext(t, "vars inspect json", out)
	var payload struct {
		Layers struct {
			Default any `json:"default"`
			Current any `json:"current"`
		} `json:"layers"`
		Encrypted bool   `json:"encrypted"`
		Secret    string `json:"secret"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if payload.Layers.Default != "<encrypted>" || payload.Layers.Current != "<encrypted>" {
		t.Errorf("layers not masked: %+v", payload.Layers)
	}
	if !payload.Encrypted || !strings.Contains(payload.Secret, "no identity") {
		t.Errorf("got %+v, want encrypted with an unresolved note", payload)
	}
}

func TestVarsInspect_DecryptedSecretNote(t *testing.T) {
	id := newVarsIdentity(t)
	cfgPath, root := writeSecretVarsFixture(t, id.Recipient())
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	out, _, err := runVarsCmd(t, flags, "inspect", "api.token")
	if err != nil {
		t.Fatalf("vars inspect: %v", err)
	}
	assertNoCiphertext(t, "vars inspect", out)
	if !strings.Contains(out, "decrypted via $"+secrets.EnvKey) {
		t.Errorf("inspect missing the decrypted note:\n%s", out)
	}
	if !strings.Contains(out, secretPlainToken) {
		t.Errorf("inspect missing the plaintext value:\n%s", out)
	}
}

// TestVarsInspect_PlainVarHasNoSecretNote pins that a project's ordinary vars
// keep their historical inspect output even when the project uses secrets.
func TestVarsInspect_PlainVarHasNoSecretNote(t *testing.T) {
	id := newVarsIdentity(t)
	cfgPath, root := writeSecretVarsFixture(t, id.Recipient())
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}

	out, _, err := runVarsCmd(t, flags, "inspect", "app.name")
	if err != nil {
		t.Fatalf("vars inspect app.name: %v", err)
	}
	if strings.Contains(out, `"secret"`) || strings.Contains(out, `"encrypted"`) {
		t.Errorf("plain var grew secret fields:\n%s", out)
	}
}

func TestVarsSet_ShadowedSecretNote(t *testing.T) {
	id := newVarsIdentity(t)
	cfgPath, root := writeSecretVarsFixture(t, id.Recipient())
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	_, stderr, err := runVarsCmd(t, flags, "set", "api.token", "plaintext-override")
	if err != nil {
		t.Fatalf("vars set: %v", err)
	}
	want := "note: vars.api.token is an encrypted secret in workspace.yml; this plaintext override wins locally"
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr missing %q:\ngot: %s", want, stderr)
	}
}

func TestVarsSet_NoNoteWhenNotShadowing(t *testing.T) {
	id := newVarsIdentity(t)
	cfgPath, root := writeSecretVarsFixture(t, id.Recipient())
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	_, stderr, err := runVarsCmd(t, flags, "set", "app.name", "renamed")
	if err != nil {
		t.Fatalf("vars set: %v", err)
	}
	if strings.Contains(stderr, "encrypted secret") {
		t.Errorf("unexpected shadow note: %s", stderr)
	}
}

// TestVarsSet_ShadowNoteSuppressedInJSON pins the JSON-mode rule: stdout must
// stay the only channel a parser reads, so the note is dropped entirely.
func TestVarsSet_ShadowNoteSuppressedInJSON(t *testing.T) {
	id := newVarsIdentity(t)
	cfgPath, root := writeSecretVarsFixture(t, id.Recipient())
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}

	stdout, stderr, err := runVarsCmd(t, flags, "set", "api.token", "plaintext-override")
	if err != nil {
		t.Fatalf("vars set --output json: %v", err)
	}
	if strings.Contains(stderr, "encrypted secret") {
		t.Errorf("note not suppressed in JSON mode: %s", stderr)
	}
	if !json.Valid([]byte(stdout)) {
		t.Errorf("stdout is not valid JSON:\n%s", stdout)
	}
}

// TestBuildVarsBrowserItems_EncryptedBadge pins the TUI browser's share of the
// masking contract: the row description is the placeholder, the type badge
// carries the suffix, and the inspect overlay carries the `secret:` note.
func TestBuildVarsBrowserItems_EncryptedBadge(t *testing.T) {
	id := newVarsIdentity(t)
	cfgPath, root := writeSecretVarsFixture(t, id.Recipient())
	hideVarsIdentity(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	cfg, err := loadConfigForVars(flags)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	items, _, _ := buildVarsBrowserItems(cfg, flags)

	var token, shadowed *cmdbrowser.Item
	for i := range items {
		switch items[i].ID {
		case "api.token":
			token = &items[i]
		case "shadowed":
			shadowed = &items[i]
		}
	}
	if token == nil || shadowed == nil {
		t.Fatalf("fixture leaves missing from items: %+v", items)
	}
	if token.Description != "<encrypted>" {
		t.Errorf("api.token description = %q, want <encrypted>", token.Description)
	}
	if token.Type != "default (encrypted)" {
		t.Errorf("api.token badge = %q, want \"default (encrypted)\"", token.Type)
	}
	// A plaintext local override of a defaults.yml marker is not encrypted.
	if shadowed.Type != "local" {
		t.Errorf("shadowed badge = %q, want local", shadowed.Type)
	}

	overlay := token.Inspect(80)
	assertNoCiphertext(t, "browser inspect overlay", overlay)
	if !strings.Contains(overlay, "no identity for "+id.Recipient()) {
		t.Errorf("overlay missing the secret note:\n%s", overlay)
	}
}
