package preflight

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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

func TestApplyOptions(t *testing.T) {
	if got := applyOptions(); got.services != nil {
		t.Errorf("no options must leave services nil, got %v", got.services)
	}
	if got := applyOptions(WithServices(nil)); got.services != nil {
		t.Errorf("an empty scope must stay unscoped, got %v", got.services)
	}
	got := applyOptions(WithServices([]string{"web", "db"}))
	if !reflect.DeepEqual(got.services, []string{"web", "db"}) {
		t.Errorf("WithServices = %v", got.services)
	}
}

// TestRun_WithServicesScopesPortsFree pins the option end to end: the scope
// reaches validate.Context.Services, where env.ports_free reads it. A port held
// by a service outside the scope must produce no diagnostic; the same port with
// no option (whole-project run) must still block.
//
// Only the ports_free line is asserted: the other env probes report whatever
// this host looks like under the isolated PATH and are not this test's subject.
func TestRun_WithServicesScopesPortsFree(t *testing.T) {
	const busy = 54321

	// A `docker` stub ahead of the real PATH: ports_free needs the binary to
	// resolve, and its `docker ps` reports the busy port as published by a
	// FOREIGN compose project. That is deliberately not a held socket — an
	// owner known from `docker ps` classifies immediately, while a real
	// EADDRINUSE would drag every busy-port subtest through the full
	// portReleaseRetries×portReleaseBackoff budget (~1.5s each) for no added
	// coverage of the scope, which is this test's subject. The rest of PATH
	// stays intact so the other env probes (git, sh) add no unrelated error rows.
	binDir := t.TempDir()
	stub := filepath.Join(binDir, "docker")
	psLine := `{"Names":"foreign-db","Ports":"0.0.0.0:` + strconv.Itoa(busy) + `->5432/tcp","Labels":"com.docker.compose.project=foreign"}`
	body := "#!/bin/sh\ncase \"$1\" in\n" +
		"  compose) echo 'Docker Compose version v2.29.0';;\n" +
		"  ps) echo '" + psLine + "';;\n" +
		"esac\nexit 0\n"
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatalf("write docker stub: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	root := t.TempDir()
	cfg := &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"db":  {Enabled: true, Ports: map[string]config.ServicePortSpec{"sql": {Port: busy}}},
			"web": {Enabled: true},
		},
	}
	want := "port " + strconv.Itoa(busy)

	t.Run("no option keeps the whole project in scope", func(t *testing.T) {
		var errOut bytes.Buffer
		if err := Run(context.Background(), cfg, nil, root, "deploy", false, &errOut); err == nil {
			t.Error("preflight must block on the busy port with no scope")
		}
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("unscoped preflight must report the busy port:\n%s", errOut.String())
		}
	})

	t.Run("scope excluding the owner silences it", func(t *testing.T) {
		var errOut bytes.Buffer
		// Only the absence of the ports_free FAILURE is asserted: whether the run
		// blocks overall depends on the host's other env probes. The two sibling
		// subtests pin that the same inputs do produce the row when the owner is
		// in scope, so this is not a vacuous negative.
		_ = Run(context.Background(), cfg, nil, root, "deploy", false, &errOut,
			WithServices([]string{"web"}))
		if strings.Contains(errOut.String(), want) {
			t.Errorf("scoped preflight must not report the out-of-scope port:\n%s", errOut.String())
		}
		if strings.Contains(errOut.String(), "foreign") {
			t.Errorf("scoped preflight must not name the out-of-scope port's holder:\n%s", errOut.String())
		}
	})

	t.Run("scope including the owner still blocks", func(t *testing.T) {
		var errOut bytes.Buffer
		if err := Run(context.Background(), cfg, nil, root, "deploy", false, &errOut,
			WithServices([]string{"db"})); err == nil {
			t.Error("preflight must block on an in-scope busy port")
		}
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("scoped preflight must report the in-scope port:\n%s", errOut.String())
		}
	})
}
