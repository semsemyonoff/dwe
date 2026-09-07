package service

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// TestSingleToggle_UnresolvedSecretBlocksEnvRegeneration pins that the .env
// regeneration inside `dwe services enable` obeys the same guard as `dwe render
// env`: a toggle must not publish ciphertext as if it were the credential, and
// the failure must roll the local.yml write back.
func TestSingleToggle_UnresolvedSecretBlocksEnvRegeneration(t *testing.T) {
	configPath, baseDir := writeServiceProject(t, "type: app\ncontainer: c\n")

	id, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	marker, err := secrets.Encrypt("s3cr3t-value", id.Recipient())
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// No identity anywhere: the marker cannot resolve.
	t.Setenv(secrets.EnvKey, "")
	t.Setenv(secrets.EnvKeyFile, "")
	t.Setenv("HOME", t.TempDir())

	cfgYAML := "project:\n  name: test\n  prefix: t\nsecrets:\n  recipient: " + id.Recipient() +
		"\nvars:\n  token: " + marker + "\nexports:\n  env:\n    - name: BOT_TOKEN\n      from: vars.token\n"
	if err := os.WriteFile(configPath, []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("rewrite workspace.yml: %v", err)
	}

	oldInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() { widgets.IsInteractiveFn = oldInteractive })
	widgets.IsInteractiveFn = func(_ io.Reader) bool { return false }

	flags := &cmdctx.RootFlags{ConfigPath: configPath}
	cmd := newServiceEnableCmd(flags)
	cmd.SetArgs([]string{"svc"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	err = cmd.Execute()
	if err == nil {
		t.Fatal("expected the toggle to fail on an undecrypted secret")
	}
	if !strings.Contains(err.Error(), "BOT_TOKEN") || !strings.Contains(err.Error(), "dwe secrets status") {
		t.Errorf("error %q does not name the export and the fix", err)
	}

	if _, statErr := os.Stat(filepath.Join(baseDir, "workspace", "local.yml")); statErr == nil {
		t.Error("local.yml must be rolled back when .env regeneration fails")
	}
	if _, statErr := os.Stat(filepath.Join(baseDir, ".env")); statErr == nil {
		t.Error(".env must not be written when a value is an undecrypted secret")
	}
}
