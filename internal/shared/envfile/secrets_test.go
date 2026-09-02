package envfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// testMarker returns a marker for plain, encrypted to a throwaway recipient.
// The tests never decrypt it: the point is that an UNDECRYPTED marker must
// never reach the .env file.
func testMarker(t *testing.T, plain string) string {
	t.Helper()
	id, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	marker, err := secrets.Encrypt(plain, id.Recipient())
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	return marker
}

func TestBuildContent_refusesMarkerInExportedValue(t *testing.T) {
	marker := testMarker(t, "s3cr3t-value")
	cfg := makeEnvCfg([]config.ExportRule{
		{Name: "BOT_TOKEN", From: "vars.telegram.token"},
	}, map[string]any{
		"vars": map[string]any{"telegram": map[string]any{"token": marker}},
	})

	out, err := BuildContent(cfg)
	if err == nil {
		t.Fatalf("expected an error, got output:\n%s", out)
	}
	for _, want := range []string{"BOT_TOKEN", "vars.telegram.token", "dwe secrets status"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestBuildContent_refusesMarkerInProjectName(t *testing.T) {
	cfg := makeEnvCfg(nil, map[string]any{})
	cfg.Project.Name = testMarker(t, "encrypted-name")

	out, err := BuildContent(cfg)
	if err == nil {
		t.Fatalf("expected an error, got output:\n%s", out)
	}
	for _, want := range []string{"PROJECT", "project.name", "dwe secrets status"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestBuildContent_decryptedValueRendersNormally(t *testing.T) {
	// What LoadConfig hands over once the identity is present: plaintext.
	cfg := makeEnvCfg([]config.ExportRule{
		{Name: "BOT_TOKEN", From: "vars.telegram.token"},
	}, map[string]any{
		"vars": map[string]any{"telegram": map[string]any{"token": "s3cr3t-value"}},
	})

	out, err := BuildContent(cfg)
	if err != nil {
		t.Fatalf("BuildContent: %v", err)
	}
	if !strings.Contains(out, "BOT_TOKEN=s3cr3t-value") {
		t.Errorf("expected the decrypted value in the output, got:\n%s", out)
	}
}

func TestWrite_tightensPreExistingMode(t *testing.T) {
	dir := t.TempDir()
	outputPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(outputPath, []byte("stale\n"), 0o644); err != nil {
		t.Fatalf("write stale .env: %v", err)
	}

	if err := Write(makeEnvCfg(nil, map[string]any{}), outputPath); err != nil {
		t.Fatalf("Write: %v", err)
	}

	fi, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %v, want 0600", perm)
	}
}

// TestBuildContent_refusesMarkerInsideCompositeValue pins that the guard is a
// contains-test, not an exact-match one. A rule whose from: resolves to a map or
// a sequence is rendered with %v, so the marker arrives embedded in
// `map[password:ENC[age:…]]` — an exact match would write the ciphertext into
// the one plaintext sink the container reads.
func TestBuildContent_refusesMarkerInsideCompositeValue(t *testing.T) {
	marker := testMarker(t, "s3cr3t-value")
	for name, raw := range map[string]map[string]any{
		"map":      {"vars": map[string]any{"db": map[string]any{"password": marker}}},
		"sequence": {"vars": map[string]any{"db": []any{marker}}},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := makeEnvCfg([]config.ExportRule{{Name: "DB", From: "vars.db"}}, raw)
			out, err := BuildContent(cfg)
			if err == nil {
				t.Fatalf("expected an error, got output:\n%s", out)
			}
			if !strings.Contains(err.Error(), "DB") || !strings.Contains(err.Error(), "vars.db") {
				t.Errorf("error %q does not name the variable and its source", err)
			}
			if strings.Contains(out, secrets.MarkerPrefix) {
				t.Errorf("output carries the marker: %s", out)
			}
		})
	}
}

// TestBuildContent_refusesMultiLineValue pins the sibling guard: values are
// written unquoted, so a multi-line one (what `dwe secrets set --stdin` accepts
// — a PEM key, a service-account blob) would be truncated to its first line by
// compose, and any `NAME=…` line inside it injected as a variable of its own.
func TestBuildContent_refusesMultiLineValue(t *testing.T) {
	for name, value := range map[string]string{
		"pem":      "-----BEGIN PRIVATE KEY-----\nMIIE\n-----END PRIVATE KEY-----",
		"injected": "harmless\nDOCKER_HOST=tcp://attacker:2375",
		"carriage": "first\rsecond",
	} {
		t.Run(name, func(t *testing.T) {
			cfg := makeEnvCfg([]config.ExportRule{
				{Name: "SSH_KEY", From: "vars.ssh.key"},
			}, map[string]any{
				"vars": map[string]any{"ssh": map[string]any{"key": value}},
			})

			out, err := BuildContent(cfg)
			if err == nil {
				t.Fatalf("expected an error, got output:\n%s", out)
			}
			for _, want := range []string{"SSH_KEY", "vars.ssh.key", "multiple lines"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
			if out != "" {
				t.Errorf("expected no output on refusal, got:\n%s", out)
			}
		})
	}
}

// TestBuildContent_refusesMultiLineProjectName covers the system block, which
// runs the same guard before the export rules.
func TestBuildContent_refusesMultiLineProjectName(t *testing.T) {
	cfg := makeEnvCfg(nil, map[string]any{})
	cfg.Project.Name = "demo\nUID=0"

	out, err := BuildContent(cfg)
	if err == nil {
		t.Fatalf("expected an error, got output:\n%s", out)
	}
	for _, want := range []string{"PROJECT", "project.name", "multiple lines"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
