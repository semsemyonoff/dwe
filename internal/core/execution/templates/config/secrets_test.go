package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	projectconfig "github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/generatedstore"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// mustKeygen mints a throwaway project identity.
func mustKeygen(t *testing.T) secrets.Identity {
	t.Helper()
	id, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	return id
}

// useIdentity publishes id through DWE_AGE_KEY, so no test ever reads
// ~/.config/dwe/keys.
func useIdentity(t *testing.T, id secrets.Identity) {
	t.Helper()
	t.Setenv(secrets.EnvKey, id.Export())
	t.Setenv(secrets.EnvKeyFile, "")
}

// noIdentity removes every identity source, including the keyfile directory.
func noIdentity(t *testing.T) {
	t.Helper()
	t.Setenv(secrets.EnvKey, "")
	t.Setenv(secrets.EnvKeyFile, "")
	t.Setenv("HOME", t.TempDir())
}

// mustEncryptFile returns a native age file for plain.
func mustEncryptFile(t *testing.T, plain, recipient string) string {
	t.Helper()
	out, err := secrets.EncryptBytes([]byte(plain), recipient)
	if err != nil {
		t.Fatalf("encrypt bytes: %v", err)
	}
	return string(out)
}

// cfgWithRecipient is newCfg plus a secrets: block.
func cfgWithRecipient(svcName, dir string, raw map[string]any, recipient string) *projectconfig.DweConfig {
	cfg := newCfg(svcName, dir, raw)
	cfg.Secrets = &projectconfig.SecretsConfig{Recipient: recipient}
	return cfg
}

func mustMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.Mode().Perm()
}

func TestRenderConfigs_ageSourceDecryptsAndRenders(t *testing.T) {
	root := t.TempDir()
	id := mustKeygen(t)
	useIdentity(t, id)

	writePack(t, root, "default", "render:\n  - from: creds.json.age\n    to: src/creds.json\n", map[string]string{
		"creds.json.age": mustEncryptFile(t, "{\"token\":\"${vars.token}\"}\n", id.Recipient()),
	})

	// A pre-existing world-readable target must be tightened, not inherited.
	dest := filepath.Join(root, "services", "main", "src", "creds.json")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatalf("mkdir dest: %v", err)
	}
	if err := os.WriteFile(dest, []byte("stale\n"), 0o644); err != nil {
		t.Fatalf("write stale dest: %v", err)
	}

	cfg := cfgWithRecipient("main", "services/main", map[string]any{
		"vars": map[string]any{"token": "s3cr3t-value"},
	}, id.Recipient())

	if _, err := RenderConfigs(root, cfg, "main", generatedstore.New()); err != nil {
		t.Fatalf("RenderConfigs: %v", err)
	}
	if got := mustRead(t, dest); got != "{\"token\":\"s3cr3t-value\"}\n" {
		t.Errorf("rendered content = %q, want the decrypted template with ${vars.token} substituted", got)
	}
	if mode := mustMode(t, dest); mode != 0o600 {
		t.Errorf("mode = %v, want 0600 for an .age-sourced output", mode)
	}
}

func TestRenderConfigs_ageSourceMissingIdentity(t *testing.T) {
	root := t.TempDir()
	id := mustKeygen(t)
	noIdentity(t)

	writePack(t, root, "default", "render:\n  - from: creds.json.age\n    to: src/creds.json\n", map[string]string{
		"creds.json.age": mustEncryptFile(t, "plain\n", id.Recipient()),
	})

	cfg := cfgWithRecipient("main", "services/main", map[string]any{}, id.Recipient())
	_, err := RenderConfigs(root, cfg, "main", generatedstore.New())
	if err == nil {
		t.Fatal("expected an error without an identity")
	}
	for _, want := range []string{"creds.json.age", "dwe secrets key import", id.Recipient()} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if _, statErr := os.Stat(filepath.Join(root, "services", "main", "src", "creds.json")); statErr == nil {
		t.Error("nothing must be written when the identity is missing")
	}
}

func TestRenderConfigs_ageSourceNoRecipient(t *testing.T) {
	root := t.TempDir()
	id := mustKeygen(t)
	useIdentity(t, id)

	writePack(t, root, "default", "render:\n  - from: creds.json.age\n    to: src/creds.json\n", map[string]string{
		"creds.json.age": mustEncryptFile(t, "plain\n", id.Recipient()),
	})

	// No secrets: block at all — the identity in the environment must not be
	// silently adopted; the project has to declare its recipient.
	cfg := newCfg("main", "services/main", map[string]any{})
	_, err := RenderConfigs(root, cfg, "main", generatedstore.New())
	if err == nil {
		t.Fatal("expected an error without secrets.recipient")
	}
	for _, want := range []string{"creds.json.age", "secrets.recipient", "secrets:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestRenderConfigs_ageSourceLocalOverrideWins(t *testing.T) {
	root := t.TempDir()
	id := mustKeygen(t)
	useIdentity(t, id)

	writePack(t, root, "default", "render:\n  - from: creds.json.age\n    to: src/creds.json\n", map[string]string{
		"creds.json.age": mustEncryptFile(t, "canonical\n", id.Recipient()),
	})
	writeOverride(t, root, "default", "creds.json.age", mustEncryptFile(t, "overridden\n", id.Recipient()))

	cfg := cfgWithRecipient("main", "services/main", map[string]any{}, id.Recipient())
	res, err := RenderConfigs(root, cfg, "main", generatedstore.New())
	if err != nil {
		t.Fatalf("RenderConfigs: %v", err)
	}
	if len(res.Rendered) != 1 || !res.Rendered[0].FromOverride {
		t.Fatalf("expected fromOverride=true, got %+v", res.Rendered)
	}
	if got := mustRead(t, filepath.Join(root, "services", "main", "src", "creds.json")); got != "overridden\n" {
		t.Errorf("content = %q, want the .local override", got)
	}
}

func TestRenderConfigs_refusesToWriteMarker(t *testing.T) {
	root := t.TempDir()
	id := mustKeygen(t)
	marker, err := secrets.Encrypt("s3cr3t-value", id.Recipient())
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	writePack(t, root, "default", "render:\n  - from: env.tmpl\n    to: src/.env\n", map[string]string{
		"env.tmpl": "TOKEN=${vars.token}\n",
	})

	// An unresolved marker reached the render context: writing it would publish
	// ciphertext into the hub dir as if it were the credential.
	cfg := newCfg("main", "services/main", map[string]any{
		"vars": map[string]any{"token": marker},
	})
	_, err = RenderConfigs(root, cfg, "main", generatedstore.New())
	if err == nil {
		t.Fatal("expected an error for an undecrypted marker in the output")
	}
	for _, want := range []string{"src/.env", "dwe secrets status"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if _, statErr := os.Stat(filepath.Join(root, "services", "main", "src", ".env")); statErr == nil {
		t.Error("the guarded output must not be written")
	}
}

func TestRenderConfigs_ageSourceRenderErrorWithholdsPlaintext(t *testing.T) {
	root := t.TempDir()
	id := mustKeygen(t)
	useIdentity(t, id)

	// A stray `{{` survives CompileVarSyntax and fails template.Parse, whose
	// error embeds the whole template text — here the decrypted credential.
	const plaintext = "{\"token\":\"top-secret-plaintext\",\"bad\":\"{{\"}\n"
	writePack(t, root, "default", "render:\n  - from: creds.json.age\n    to: src/creds.json\n", map[string]string{
		"creds.json.age": mustEncryptFile(t, plaintext, id.Recipient()),
	})

	cfg := cfgWithRecipient("main", "services/main", map[string]any{}, id.Recipient())
	_, err := RenderConfigs(root, cfg, "main", generatedstore.New())
	if err == nil {
		t.Fatal("expected a render error for an unparseable decrypted source")
	}
	if strings.Contains(err.Error(), "top-secret-plaintext") {
		t.Errorf("error %q leaks the decrypted source", err)
	}
	for _, want := range []string{"creds.json.age", "the source is a secret"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if _, statErr := os.Stat(filepath.Join(root, "services", "main", "src", "creds.json")); statErr == nil {
		t.Error("nothing must be written when the render fails")
	}
}

func TestRenderConfigs_plainSourceKeepsMode(t *testing.T) {
	root := t.TempDir()
	writePack(t, root, "default", "render:\n  - from: env.tmpl\n    to: src/.env\n", map[string]string{
		"env.tmpl": "APP=${project.name}\n",
	})

	cfg := newCfg("main", "services/main", map[string]any{
		"project": map[string]any{"name": "demo"},
	})
	if _, err := RenderConfigs(root, cfg, "main", generatedstore.New()); err != nil {
		t.Fatalf("RenderConfigs: %v", err)
	}
	dest := filepath.Join(root, "services", "main", "src", ".env")
	if got := mustRead(t, dest); got != "APP=demo\n" {
		t.Errorf("content = %q, want the unchanged plain render", got)
	}
	if mode := mustMode(t, dest); mode != 0o644 {
		t.Errorf("mode = %v, want 0644 for a plain source", mode)
	}
}
