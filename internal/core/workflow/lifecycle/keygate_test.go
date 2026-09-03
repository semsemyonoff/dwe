package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/workflow/keygate"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// --- helpers ---------------------------------------------------------------

// isolateKeyEnv points HOME at a temp dir and clears every identity source, so a
// developer running the suite with DWE_AGE_KEY set gets the same outcome as CI
// and no test ever reads the real ~/.config/dwe/keys.
func isolateKeyEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(secrets.EnvKey, "")
	t.Setenv(secrets.EnvKeyFile, "")
	t.Setenv("DWE_NONINTERACTIVE", "")
}

// writeSecretsWorkspaceYML lays down a project whose workspace.yml carries one
// marker exported to .env — the state of a fresh clone on a machine without the
// key. Returns the config path.
func writeSecretsWorkspaceYML(t *testing.T, dir, recipient, marker string) string {
	t.Helper()
	cfgPath := filepath.Join(dir, "workspace.yml")
	content := "project:\n  name: test\n  prefix: dwe\nsecrets:\n  recipient: " + recipient +
		"\nvars:\n  token: " + marker + "\nexports:\n  env:\n    - name: BOT_TOKEN\n      from: vars.token\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("writing workspace.yml: %v", err)
	}
	return cfgPath
}

// newEncryptedMarker returns a fresh identity and one marker encrypted to it.
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

// stubKeygate installs a gate stub and returns a pointer to the Options it was
// called with (nil while it has not been called).
func stubKeygate(t *testing.T, imported bool, err error) **keygate.Options {
	t.Helper()
	old := KeygateEnsureFunc
	t.Cleanup(func() { KeygateEnsureFunc = old })
	var seen *keygate.Options
	KeygateEnsureFunc = func(_ context.Context, opts keygate.Options) (bool, error) {
		seen = &opts
		return imported, err
	}
	return &seen
}

// failIfStopped installs a RunStop seam that fails the test when reached. It is
// how the restart path proves the gate refused before any container was touched.
func failIfStopped(t *testing.T) {
	t.Helper()
	old := runStopFn
	t.Cleanup(func() { runStopFn = old })
	runStopFn = func(StopContext) error {
		t.Error("RunStop must not run when the identity gate refused")
		return nil
	}
}

// --- RunRun -----------------------------------------------------------------

// TestRunRun_KeygateImportFeedsTheSameInvocation is the point of running the gate
// before LoadConfigOrWrap: an identity accepted at the offer decrypts the config
// whose .env this same invocation renders, with no reload.
func TestRunRun_KeygateImportFeedsTheSameInvocation(t *testing.T) {
	isolateKeyEnv(t)
	stubRunPhases(t)

	id, marker := newEncryptedMarker(t, "s3cr3t-value")
	dir := t.TempDir()
	cfgPath := writeSecretsWorkspaceYML(t, dir, id.Recipient(), marker)

	old := KeygateEnsureFunc
	t.Cleanup(func() { KeygateEnsureFunc = old })
	// Stands in for a completed import: the identity becomes available to the
	// process exactly as WriteKeyfile would have made it.
	KeygateEnsureFunc = func(context.Context, keygate.Options) (bool, error) {
		t.Setenv(secrets.EnvKey, id.Export())
		return true, nil
	}

	if err := RunRun(RunContext{ConfigPath: cfgPath}); err != nil {
		t.Fatalf("RunRun after a successful import: %v", err)
	}

	env, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatalf("reading .env: %v", err)
	}
	if !strings.Contains(string(env), "BOT_TOKEN=s3cr3t-value") {
		t.Errorf(".env must carry the decrypted value, got:\n%s", env)
	}
}

// TestRunRun_KeygateRefusalStopsBeforeDotEnv pins that all three gate refusals
// abort the run before anything is written.
func TestRunRun_KeygateRefusalStopsBeforeDotEnv(t *testing.T) {
	for _, sentinel := range []error{keygate.ErrAborted, keygate.ErrEnvSourceUnusable, keygate.ErrKeyfileUnusable} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			isolateKeyEnv(t)
			stubRunPhases(t)

			id, marker := newEncryptedMarker(t, "s3cr3t-value")
			dir := t.TempDir()
			cfgPath := writeSecretsWorkspaceYML(t, dir, id.Recipient(), marker)
			stubKeygate(t, false, fmt.Errorf("%w: refused", sentinel))

			err := RunRun(RunContext{ConfigPath: cfgPath})
			if !errors.Is(err, sentinel) {
				t.Fatalf("RunRun error = %v, want %v", err, sentinel)
			}
			if _, statErr := os.Stat(filepath.Join(dir, ".env")); statErr == nil {
				t.Error(".env must not be written when the gate refused")
			}
		})
	}
}

// TestRunRun_KeygateSkippedOnUnloadableConfig: a config that does not even parse
// is LoadConfigOrWrap's story. The gate gets nil layers, skips itself, and the
// user sees today's wording.
func TestRunRun_KeygateSkippedOnUnloadableConfig(t *testing.T) {
	isolateKeyEnv(t)
	stubRunPhases(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(cfgPath, []byte("project:\n  name: [unterminated\n"), 0o644); err != nil {
		t.Fatalf("writing workspace.yml: %v", err)
	}
	seen := stubKeygate(t, false, nil)

	err := RunRun(RunContext{ConfigPath: cfgPath})
	if err == nil || !strings.Contains(err.Error(), "loading config:") {
		t.Fatalf("RunRun error = %v, want today's `loading config: …` wording", err)
	}
	opts := *seen
	if opts == nil {
		t.Fatal("the gate must run on every invocation")
	}
	if opts.Layers != nil {
		t.Error("an unreadable layer set must reach the gate as nil so it skips itself")
	}
}

// TestRunRun_KeygateOptions pins the interactivity inputs RunRun evaluates for
// the gate: --yes and --output json both suppress the offer, and a caller that
// wires no hooks (the service toggle executor) can never open a form.
func TestRunRun_KeygateOptions(t *testing.T) {
	cases := []struct {
		name       string
		ctx        RunContext
		wantYes    bool
		wantJSON   bool
		wantPrompt bool
	}{
		{name: "plain"},
		{name: "yes", ctx: RunContext{Yes: true}, wantYes: true},
		{name: "json", ctx: RunContext{OutputJSON: true}, wantJSON: true},
		{
			name: "hooks wired",
			ctx: RunContext{
				KeyPrompt:  func(context.Context, string) (secrets.Identity, error) { return secrets.Identity{}, nil },
				KeyConfirm: func(context.Context, string) (bool, error) { return false, nil },
			},
			wantPrompt: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateKeyEnv(t)
			stubRunPhases(t)

			dir := t.TempDir()
			rctx := tc.ctx
			rctx.ConfigPath = makeMinimalWorkspaceYML(t, dir)
			seen := stubKeygate(t, false, nil)

			if err := RunRun(rctx); err != nil {
				t.Fatalf("RunRun: %v", err)
			}
			opts := *seen
			if opts == nil {
				t.Fatal("the gate must run on every invocation")
			}
			if opts.BaseDir != dir {
				t.Errorf("Options.BaseDir = %q, want %q", opts.BaseDir, dir)
			}
			if opts.Yes != tc.wantYes {
				t.Errorf("Options.Yes = %v, want %v", opts.Yes, tc.wantYes)
			}
			if opts.OutputJSON != tc.wantJSON {
				t.Errorf("Options.OutputJSON = %v, want %v", opts.OutputJSON, tc.wantJSON)
			}
			if (opts.Prompt != nil) != tc.wantPrompt {
				t.Errorf("Options.Prompt set = %v, want %v", opts.Prompt != nil, tc.wantPrompt)
			}
			if (opts.Confirm != nil) != tc.wantPrompt {
				t.Errorf("Options.Confirm set = %v, want %v", opts.Confirm != nil, tc.wantPrompt)
			}
		})
	}
}

// TestRunRun_KeygateNonInteractiveEnvReachesTheGate pins that DWE_NONINTERACTIVE
// is read by RunRun itself (core cannot import cmdctx) and handed over.
func TestRunRun_KeygateNonInteractiveEnvReachesTheGate(t *testing.T) {
	isolateKeyEnv(t)
	stubRunPhases(t)
	t.Setenv("DWE_NONINTERACTIVE", "1")

	dir := t.TempDir()
	seen := stubKeygate(t, false, nil)
	if err := RunRun(RunContext{ConfigPath: makeMinimalWorkspaceYML(t, dir)}); err != nil {
		t.Fatalf("RunRun: %v", err)
	}
	if opts := *seen; opts == nil || !opts.NonInteractive {
		t.Error("DWE_NONINTERACTIVE=1 must reach the gate as NonInteractive")
	}
}

// TestRunRun_RealGateNeverPromptsWithoutHooks runs the SHIPPED gate against an
// unresolved project with no hooks wired: it must fall through to today's
// undecrypted-marker refusal rather than opening a form.
func TestRunRun_RealGateNeverPromptsWithoutHooks(t *testing.T) {
	isolateKeyEnv(t)
	stubRunPhases(t)

	id, marker := newEncryptedMarker(t, "s3cr3t-value")
	dir := t.TempDir()
	cfgPath := writeSecretsWorkspaceYML(t, dir, id.Recipient(), marker)

	err := RunRun(RunContext{ConfigPath: cfgPath})
	if err == nil {
		t.Fatal("expected the undecrypted-marker refusal")
	}
	if !strings.Contains(err.Error(), "dwe secrets status") {
		t.Errorf("error %q is not today's undecrypted-marker refusal", err)
	}
}

// TestRunRun_KeygateIsInertWithoutSecrets is the backward-compatibility pin: on a
// project with no secrets the REAL gate runs and must leave the run byte-identical
// to the same run with the gate stubbed out.
func TestRunRun_KeygateIsInertWithoutSecrets(t *testing.T) {
	isolateKeyEnv(t)
	stubRunPhases(t)

	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)

	// The real gate: it must reach its "no encrypted surface" exit silently.
	if err := RunRun(RunContext{ConfigPath: cfgPath}); err != nil {
		t.Fatalf("RunRun with the real gate: %v", err)
	}
	realEnv, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatalf("reading .env: %v", err)
	}

	stubKeygate(t, false, nil)
	if err := RunRun(RunContext{ConfigPath: cfgPath}); err != nil {
		t.Fatalf("RunRun with the stubbed gate: %v", err)
	}
	stubbedEnv, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatalf("reading .env: %v", err)
	}
	if string(realEnv) != string(stubbedEnv) {
		t.Errorf("a project without secrets must render an identical .env\nreal:\n%s\nstubbed:\n%s", realEnv, stubbedEnv)
	}
}

// TestRunRun_KeygateRefusalDoesNotNotify: the three refusals are not run
// failures — nothing started — so they stay silent like a preflight block.
func TestRunRun_KeygateRefusalDoesNotNotify(t *testing.T) {
	isolateKeyEnv(t)
	stubRunPhases(t)
	rec := installRecordingNotifier(t)

	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)
	stubKeygate(t, false, fmt.Errorf("%w: declined", keygate.ErrAborted))

	if err := RunRun(RunContext{ConfigPath: cfgPath}); !errors.Is(err, keygate.ErrAborted) {
		t.Fatalf("RunRun error = %v, want ErrAborted", err)
	}
	if events := rec.snapshot(); len(events) != 0 {
		t.Errorf("gate refusal fired %d notification(s), want 0", len(events))
	}
}

// --- RunRestart -------------------------------------------------------------

// TestRunRestart_KeygateRefusesBeforeStop is the ordering pin: a missing key must
// not tear the stack down and only then fail.
func TestRunRestart_KeygateRefusesBeforeStop(t *testing.T) {
	isolateKeyEnv(t)
	stubRunPhases(t)
	failIfStopped(t)

	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)
	stubKeygate(t, false, fmt.Errorf("%w: declined", keygate.ErrAborted))

	if err := RunRestart(RunContext{ConfigPath: cfgPath}); !errors.Is(err, keygate.ErrAborted) {
		t.Fatalf("RunRestart error = %v, want ErrAborted", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".env")); statErr == nil {
		t.Error(".env must not be written when the gate refused")
	}
}

// TestRunRestart_KeygateRunsBeforeStopOnTheHappyPath records the call order: the
// offer comes first, the stop leg second, and the run leg's own gate call is the
// harmless short-circuit.
func TestRunRestart_KeygateRunsBeforeStopOnTheHappyPath(t *testing.T) {
	isolateKeyEnv(t)
	stubRunPhases(t)

	var order []string
	oldGate, oldStop := KeygateEnsureFunc, runStopFn
	t.Cleanup(func() { KeygateEnsureFunc, runStopFn = oldGate, oldStop })
	KeygateEnsureFunc = func(context.Context, keygate.Options) (bool, error) {
		order = append(order, "gate")
		return false, nil
	}
	runStopFn = func(StopContext) error {
		order = append(order, "stop")
		return nil
	}

	dir := t.TempDir()
	if err := RunRestart(RunContext{ConfigPath: makeMinimalWorkspaceYML(t, dir)}); err != nil {
		t.Fatalf("RunRestart: %v", err)
	}
	if len(order) < 2 || order[0] != "gate" || order[1] != "stop" {
		t.Errorf("call order = %v, want the gate before the stop leg", order)
	}
}
