package preflight

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

func TestStagesForPreflight(t *testing.T) {
	cases := []struct {
		stage string
		want  []string
	}{
		// deploy is the only stage with a second (post-setup) moment: the final
		// preflight runs both, so post-setup checks skipped at the early
		// pre-wizard gate execute here.
		{"deploy", []string{"deploy", "post-setup"}},
		{"run", []string{"run"}},
		{"stop", []string{"stop"}},
		{"command", []string{"command"}},
		// Empty stage → nil, which AllForStages treats as "match every check".
		{"", nil},
	}
	for _, tc := range cases {
		t.Run(tc.stage, func(t *testing.T) {
			if got := stagesForPreflight(tc.stage); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("stagesForPreflight(%q) = %v, want %v", tc.stage, got, tc.want)
			}
		})
	}
}

// TestRun_secretsUnresolvedBlocks pins the second preflight cherry-pick: an
// undecryptable secret must stop a lifecycle command with a named fix, and must
// go quiet again as soon as the identity is available. The assertions are on
// the secrets rows only — the env probes (docker, ports) report whatever the
// host happens to look like and are not this test's subject.
func TestRun_secretsUnresolvedBlocks(t *testing.T) {
	root := t.TempDir()
	id, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	marker, err := secrets.Encrypt("s3cr3t-value", id.Recipient())
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	configPath := filepath.Join(root, "workspace.yml")
	body := "project:\n  name: test\nsecrets:\n  recipient: " + id.Recipient() +
		"\nvars:\n  token: " + marker + "\n"
	if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}

	// LoadConfig registers every plaintext it decrypts with the process-global
	// trace redactor. Leaving it installed would make the "must not carry the
	// plaintext" assertions below test the redactor rather than preflight: a
	// diagnostic that started echoing the value would print `***` and pass.
	load := func(t *testing.T) *config.DweConfig {
		t.Helper()
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		trace.ResetRedaction()
		t.Cleanup(trace.ResetRedaction)
		return cfg
	}

	t.Run("without an identity", func(t *testing.T) {
		t.Setenv(secrets.EnvKey, "")
		t.Setenv(secrets.EnvKeyFile, "")
		t.Setenv("HOME", t.TempDir())

		var errOut bytes.Buffer
		err := Run(context.Background(), load(t), nil, root, "run", false, &errOut)
		if err == nil {
			t.Fatal("preflight must block on an unresolved secret")
		}
		if !strings.Contains(errOut.String(), "vars.token") {
			t.Errorf("preflight output does not name the unresolved secret:\n%s", errOut.String())
		}
		if strings.Contains(errOut.String(), "s3cr3t-value") {
			t.Error("preflight output must not carry the plaintext")
		}
	})

	t.Run("with the identity", func(t *testing.T) {
		t.Setenv(secrets.EnvKey, id.Export())
		t.Setenv(secrets.EnvKeyFile, "")

		var errOut bytes.Buffer
		_ = Run(context.Background(), load(t), nil, root, "run", false, &errOut)
		if strings.Contains(errOut.String(), "vars.token") {
			t.Errorf("preflight must be silent about a decrypted secret:\n%s", errOut.String())
		}
		if strings.Contains(errOut.String(), "s3cr3t-value") {
			t.Error("preflight output must not carry the plaintext")
		}
		// The secrets domain now affirms a healthy setup with SeverityOK rows;
		// preflight filters them, so a lifecycle command gains no extra line.
		for _, unwanted := range []string{"secrets.unresolved", "secrets.recipient", "readable via"} {
			if strings.Contains(errOut.String(), unwanted) {
				t.Errorf("preflight printed the OK row (%q):\n%s", unwanted, errOut.String())
			}
		}
	})
}
