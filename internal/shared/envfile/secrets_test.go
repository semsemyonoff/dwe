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
