package lifecycle

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/workflow/keygate"
	lifecyclepkg "github.com/semsemyonoff/dwe/internal/core/workflow/lifecycle"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// errGateProbeDone ends the command right after the gate has been observed, so a
// restart test never reaches the stop leg (which would drive docker).
var errGateProbeDone = errors.New("gate probe done")

// writeSecretsProject lays down a project whose workspace.yml carries one marker
// exported to .env, i.e. a fresh clone on a machine without the key.
func writeSecretsProject(t *testing.T, recipient, marker string) string {
	t.Helper()
	root := t.TempDir()
	cfgPath := filepath.Join(root, "workspace.yml")
	content := "project:\n  name: test\n  prefix: dwe\nsecrets:\n  recipient: " + recipient +
		"\nvars:\n  token: " + marker + "\nexports:\n  env:\n    - name: BOT_TOKEN\n      from: vars.token\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("writing workspace.yml: %v", err)
	}
	return cfgPath
}

// gateProbe records what the cli handed the SHIPPED gate, and whether the gate
// then opened the offer. The two hooks are replaced rather than stubbed away, so
// the assertions are about the real decision the shipped gate makes from those
// flags — not about a stub staying silent.
type gateProbe struct {
	opts    *keygate.Options
	offered bool
}

// probeGate installs the probe. expectOffer says whether the offer is legal in
// the mode under test: when it is not, opening one fails the test. The
// confirmation always DECLINES, so no test ever needs to feed a key; stop=true
// additionally turns a silent gate into errGateProbeDone so a restart aborts
// before it reaches the stop leg (which would drive docker).
func probeGate(t *testing.T, expectOffer, stop bool) *gateProbe {
	t.Helper()
	oldGate := lifecyclepkg.KeygateEnsureFunc
	oldInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() {
		lifecyclepkg.KeygateEnsureFunc = oldGate
		widgets.IsInteractiveFn = oldInteractive
	})
	// Without this the outcome would depend on whether the suite runs on a tty.
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }

	probe := &gateProbe{}
	lifecyclepkg.KeygateEnsureFunc = func(ctx context.Context, opts keygate.Options) (bool, error) {
		probe.opts = &opts
		gateOpts := opts
		gateOpts.Prompt = func(context.Context, string) (secrets.Identity, error) {
			t.Error("the identity form must never open in these tests")
			return secrets.Identity{}, errors.New("unexpected prompt")
		}
		gateOpts.Confirm = func(context.Context, string) (bool, error) {
			if !expectOffer {
				t.Error("no offer may open in this mode")
			}
			probe.offered = true
			return false, nil
		}
		imported, err := keygate.Ensure(ctx, gateOpts)
		if err == nil && stop {
			return imported, errGateProbeDone
		}
		return imported, err
	}
	return probe
}

// isolateKeyEnv points HOME at a temp dir and clears every identity source.
func isolateKeyEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(secrets.EnvKey, "")
	t.Setenv(secrets.EnvKeyFile, "")
	t.Setenv("DWE_NONINTERACTIVE", "")
}

func newEncryptedMarker(t *testing.T, plaintext string) (secrets.Identity, string) {
	t.Helper()
	id, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	marker, err := secrets.Encrypt(plaintext, id.Recipient())
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	return id, marker
}

// TestRunCmd_KeygateWiring pins what `dwe run` hands the gate, and that
// `--output json` keeps the offer shut even at a terminal: a huh form on stdout
// would corrupt the parseable stream.
func TestRunCmd_KeygateWiring(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		output      string
		env         string
		wantJSON    bool
		wantYes     bool
		expectOffer bool
	}{
		// At a terminal, in text mode, the offer is the whole point.
		{name: "text", args: []string{"run"}, expectOffer: true},
		{name: "json", args: []string{"run"}, output: "json", wantJSON: true},
		{name: "yes", args: []string{"run", "--yes"}, wantYes: true},
		{name: "DWE_NONINTERACTIVE", args: []string{"run"}, env: "1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateKeyEnv(t)
			t.Setenv("DWE_NONINTERACTIVE", tc.env)
			stubRunPhases(t)
			id, marker := newEncryptedMarker(t, "s3cr3t-value")
			cfgPath := writeSecretsProject(t, id.Recipient(), marker)

			probe := probeGate(t, tc.expectOffer, false)

			flags := &cmdctx.RootFlags{Output: tc.output}
			root := buildLifecycleTestRoot(flags)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs(append(tc.args, "--config", cfgPath))

			err := root.Execute()
			switch {
			case tc.expectOffer:
				// The probe declines, so the run stops at the gate's refusal.
				if !errors.Is(err, keygate.ErrAborted) {
					t.Fatalf("run error = %v, want ErrAborted after a declined offer", err)
				}
			default:
				// The gate stays silent and the run dies on the marker exactly
				// as it does today.
				if err == nil || !strings.Contains(err.Error(), "dwe secrets status") {
					t.Fatalf("run error = %v, want today's undecrypted-marker refusal", err)
				}
			}
			if probe.offered != tc.expectOffer {
				t.Errorf("offer opened = %v, want %v", probe.offered, tc.expectOffer)
			}

			opts := probe.opts
			if opts == nil {
				t.Fatal("the gate must run on every `dwe run`")
			}
			if opts.NonInteractive != (tc.env == "1") {
				t.Errorf("Options.NonInteractive = %v, want %v", opts.NonInteractive, tc.env == "1")
			}
			if opts.BaseDir != filepath.Dir(cfgPath) {
				t.Errorf("Options.BaseDir = %q, want %q", opts.BaseDir, filepath.Dir(cfgPath))
			}
			if len(opts.Layers) == 0 {
				t.Error("the gate decides on the raw layers")
			}
			if opts.OutputJSON != tc.wantJSON {
				t.Errorf("Options.OutputJSON = %v, want %v", opts.OutputJSON, tc.wantJSON)
			}
			if opts.Yes != tc.wantYes {
				t.Errorf("Options.Yes = %v, want %v", opts.Yes, tc.wantYes)
			}
			if opts.Prompt == nil || opts.Confirm == nil {
				t.Error("both interactive hooks must be wired by the cli layer")
			}
			if opts.Out == nil {
				t.Error("the import report needs a sink")
			}
		})
	}
}

// TestRestartCmd_KeygateWiring is the same pin for `dwe restart`, which reaches
// the gate BEFORE its stop leg.
func TestRestartCmd_KeygateWiring(t *testing.T) {
	for _, tc := range []struct {
		name        string
		output      string
		wantJSON    bool
		expectOffer bool
	}{
		{name: "text", expectOffer: true},
		{name: "json", output: "json", wantJSON: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateKeyEnv(t)
			stubRunPhases(t)
			id, marker := newEncryptedMarker(t, "s3cr3t-value")
			cfgPath := writeSecretsProject(t, id.Recipient(), marker)

			probe := probeGate(t, tc.expectOffer, true)

			flags := &cmdctx.RootFlags{Output: tc.output}
			root := buildLifecycleTestRoot(flags)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs([]string{"restart", "--config", cfgPath})

			err := root.Execute()
			// Either way the stop leg is never reached: a declined offer refuses,
			// and a silent gate is short-circuited by the probe sentinel.
			if tc.expectOffer {
				if !errors.Is(err, keygate.ErrAborted) {
					t.Fatalf("restart error = %v, want ErrAborted after a declined offer", err)
				}
			} else if !errors.Is(err, errGateProbeDone) {
				t.Fatalf("restart error = %v, want the gate probe sentinel", err)
			}
			if probe.offered != tc.expectOffer {
				t.Errorf("offer opened = %v, want %v", probe.offered, tc.expectOffer)
			}
			opts := probe.opts
			if opts == nil {
				t.Fatal("the gate must run on every whole-stack `dwe restart`")
			}
			if opts.OutputJSON != tc.wantJSON {
				t.Errorf("Options.OutputJSON = %v, want %v", opts.OutputJSON, tc.wantJSON)
			}
			if opts.Prompt == nil || opts.Confirm == nil {
				t.Error("both interactive hooks must be wired by the cli layer")
			}
		})
	}
}

// TestRestartCmd_PerServiceRestartSkipsTheGate: `dwe restart <name>` is the
// container-level path — no preflight, no locks, and no identity offer either.
func TestRestartCmd_PerServiceRestartSkipsTheGate(t *testing.T) {
	isolateKeyEnv(t)
	oldGate := lifecyclepkg.KeygateEnsureFunc
	t.Cleanup(func() { lifecyclepkg.KeygateEnsureFunc = oldGate })
	lifecyclepkg.KeygateEnsureFunc = func(context.Context, keygate.Options) (bool, error) {
		t.Error("a per-service restart must not run the identity gate")
		return false, nil
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(cfgPath, []byte("project:\n  name: test\n  prefix: dwe\n"), 0o644); err != nil {
		t.Fatalf("writing workspace.yml: %v", err)
	}

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"restart", "web", "--config", cfgPath})
	// The service does not exist; the error is beside the point here.
	_ = root.Execute()
}
