package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// The ide/ai/git packs render into git-tracked files, so they load a sanitized
// config: every field a template can reach carries the ENC[age:…] marker where
// the real config carries plaintext. These tests run WITH a working identity —
// the point is that a usable key must not make a tracked output leak.

const secretPlaintext = "s3cr3t-value"

// setupSecretProject writes a project whose vars.token is an encrypted marker
// and publishes the matching identity through DWE_AGE_KEY. It returns the
// project root and the marker as written to disk.
func setupSecretProject(t *testing.T) (projectRoot, marker string) {
	t.Helper()
	projectRoot = t.TempDir()

	id, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	t.Setenv(secrets.EnvKey, id.Export())
	t.Setenv(secrets.EnvKeyFile, "")

	marker, err = secrets.Encrypt(secretPlaintext, id.Recipient())
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	cfgYAML := "schema_version: \"2\"\nproject:\n  name: test-project\nsecrets:\n  recipient: " + id.Recipient() +
		"\nvars:\n  token: " + marker + "\nservices:\n  api:\n    enabled: true\n"
	if err := os.WriteFile(filepath.Join(projectRoot, "workspace.yml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}
	setupServicesConfig(t, projectRoot, `
services:
  api:
    type: app
    dir: services/api
    container: test-api
`)
	if err := os.MkdirAll(filepath.Join(projectRoot, "services", "api"), 0o755); err != nil {
		t.Fatalf("create hub dir: %v", err)
	}
	return projectRoot, marker
}

// secretTemplate reaches the config through every field a pack template has.
const secretTemplate = "raw={{ .Cfg.Raw.vars.token }}\nvars={{ .Cfg.Vars.token }}\nproject={{ .Project.Name }}\n"

// assertCiphertextOnly pins that the rendered tracked output carries the marker
// and neither the plaintext nor an identity.
func assertCiphertextOnly(t *testing.T, path, marker string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	out := string(data)
	if !strings.Contains(out, marker) {
		t.Errorf("output does not carry the marker:\n%s", out)
	}
	if strings.Contains(out, secretPlaintext) {
		t.Errorf("output leaked the plaintext:\n%s", out)
	}
	if strings.Contains(out, "AGE-SECRET-KEY-") {
		t.Errorf("output leaked an identity:\n%s", out)
	}
}

func TestNewIDECmd_rendersMarkerNotPlaintext(t *testing.T) {
	projectRoot, marker := setupSecretProject(t)
	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{
		"secret.txt.tmpl": secretTemplate,
	})

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "workspace.yml")}
	cmd := newIDECmd(flags)
	if err := cmd.RunE(cmd, []string{"api"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	assertCiphertextOnly(t, filepath.Join(projectRoot, "services", "api", "secret.txt"), marker)
}

func TestNewAICmd_rendersMarkerNotPlaintext(t *testing.T) {
	projectRoot, marker := setupSecretProject(t)
	setupAgentsPackTemplates(t, projectRoot, "default", map[string]string{
		"manifest.yml":   "render:\n  - from: AGENTS.md.tmpl\n    to: AGENTS.md\n",
		"AGENTS.md.tmpl": secretTemplate,
	})

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "workspace.yml")}
	cmd := newAICmd(flags)
	if err := cmd.RunE(cmd, []string{"api"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	assertCiphertextOnly(t, filepath.Join(projectRoot, "services", "api", "AGENTS.md"), marker)
}

func TestNewGitCmd_rendersMarkerNotPlaintext(t *testing.T) {
	projectRoot, marker := setupSecretProject(t)
	setupGitPack(t, projectRoot, "default", map[string]string{
		"manifest.yml":    "render:\n  - from: pre-commit.tmpl\n    to: pre-commit\n",
		"pre-commit.tmpl": "#!/bin/sh\n" + secretTemplate,
	})
	mkGitDir(t, filepath.Join(projectRoot, "services", "api"))

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "workspace.yml")}
	cmd := newGitCmd(flags)
	if err := cmd.RunE(cmd, []string{"api"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	assertCiphertextOnly(t, filepath.Join(projectRoot, "services", "api", "src", ".git", "hooks", "pre-commit"), marker)
}

// TestNewEnvCmd_refusesUndecryptedMarker pins the other half of the contract:
// `dwe render env` writes a gitignored file the container reads, so it must
// fail loudly rather than publish ciphertext as the credential.
func TestNewEnvCmd_refusesUndecryptedMarker(t *testing.T) {
	projectRoot := t.TempDir()
	id, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	marker, err := secrets.Encrypt(secretPlaintext, id.Recipient())
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// No identity anywhere: the marker stays unresolved.
	t.Setenv(secrets.EnvKey, "")
	t.Setenv(secrets.EnvKeyFile, "")
	t.Setenv("HOME", t.TempDir())

	cfgYAML := "schema_version: \"2\"\nproject:\n  name: test-project\nsecrets:\n  recipient: " + id.Recipient() +
		"\nvars:\n  token: " + marker + "\nexports:\n  env:\n    - name: BOT_TOKEN\n      from: vars.token\n"
	if err := os.WriteFile(filepath.Join(projectRoot, "workspace.yml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "workspace.yml")}
	cmd := newEnvCmd(flags)
	err = cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("expected an error for an undecrypted marker")
	}
	if !strings.Contains(err.Error(), "BOT_TOKEN") || !strings.Contains(err.Error(), "dwe secrets status") {
		t.Errorf("error %q does not name the export and the fix", err)
	}
	if _, statErr := os.Stat(filepath.Join(projectRoot, ".env")); statErr == nil {
		t.Error(".env must not be written when a value is an undecrypted secret")
	}
}
