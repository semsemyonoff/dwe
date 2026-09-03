package keygate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// --- fixtures ---------------------------------------------------------------

const fixtureWorkspace = `schema_version: "2"
project:
  name: gatetest
  prefix: dwe
`

// isolateHome points HOME at a temp dir so no test can ever read or write the
// developer's real ~/.config/dwe/keys, and clears the env overrides so a
// developer running the suite with DWE_AGE_KEY set does not change the outcome.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(secrets.EnvKey, "")
	t.Setenv(secrets.EnvKeyFile, "")
	return home
}

// installIdentity mints a key pair and stores its keyfile under the isolated
// HOME, i.e. the state of a machine that can read the project.
func installIdentity(t *testing.T) secrets.Identity {
	t.Helper()
	id, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	if _, err := secrets.WriteKeyfile(id); err != nil {
		t.Fatalf("writing keyfile: %v", err)
	}
	return id
}

func encrypt(t *testing.T, plain, recipient string) string {
	t.Helper()
	marker, err := secrets.Encrypt(plain, recipient)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	return marker
}

// writeProject lays down workspace.yml (with the recipient when one is given)
// and returns the config path.
func writeProject(t *testing.T, root, recipient string) string {
	t.Helper()
	cfg := fixtureWorkspace
	if recipient != "" {
		cfg += "secrets:\n  recipient: " + recipient + "\n"
	}
	path := filepath.Join(root, "workspace.yml")
	if err := os.MkdirAll(filepath.Join(root, "workspace"), 0o755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatalf("writing workspace.yml: %v", err)
	}
	return path
}

// layersWithMarker writes a project carrying one marker in defaults.yml and
// returns its raw layers.
func layersWithMarker(t *testing.T, root, recipient, marker string) []config.Layer {
	t.Helper()
	cfgPath := writeProject(t, root, recipient)
	if err := os.WriteFile(filepath.Join(root, "workspace", "defaults.yml"),
		[]byte("vars:\n  token: "+marker+"\n"), 0o644); err != nil {
		t.Fatalf("writing defaults.yml: %v", err)
	}
	return rawLayers(t, cfgPath)
}

func rawLayers(t *testing.T, cfgPath string) []config.Layer {
	t.Helper()
	layers, err := config.LoadRawLayers(cfgPath)
	if err != nil {
		t.Fatalf("LoadRawLayers: %v", err)
	}
	return layers
}

// writeAgeFile encrypts plain to recipient and stores it as a config-pack source.
func writeAgeFile(t *testing.T, root, rel, recipient, plain string) string {
	t.Helper()
	path := filepath.Join(root, "workspace", "templates", "config", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating pack dir: %v", err)
	}
	data, err := secrets.EncryptBytes([]byte(plain), recipient)
	if err != nil {
		t.Fatalf("encrypting %s: %v", rel, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing %s: %v", rel, err)
	}
	return path
}

// hostileHooks are Prompt/Confirm stubs that fail the test if they are ever
// reached. Every "no prompt in this mode" assertion installs them, so the
// negative is a real one rather than the absence of a terminal.
func hostileHooks(t *testing.T) (PromptFunc, ConfirmFunc) {
	t.Helper()
	return func(context.Context, string) (secrets.Identity, error) {
			t.Error("Prompt was called; this mode must never open a form")
			return secrets.Identity{}, errors.New("unexpected prompt")
		},
		func(context.Context, string) (bool, error) {
			t.Error("Confirm was called; this mode must never open a form")
			return false, errors.New("unexpected confirm")
		}
}

// --- the gate ---------------------------------------------------------------

// TestEnsure_NoOpCases walks every state in which the gate must do nothing at
// all and leave the caller's own failure path to speak. Prompt and Confirm are
// wired to fail the test, so "returns (false, nil)" also proves "opened no
// form".
func TestEnsure_NoOpCases(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, root string) Options
	}{
		{
			name: "nil layers",
			setup: func(t *testing.T, root string) Options {
				return Options{BaseDir: root}
			},
		},
		{
			name: "layers that would not load",
			setup: func(t *testing.T, root string) Options {
				id := installIdentityElsewhere(t)
				cfgPath := writeProject(t, root, id.Recipient())
				// secrets: is a workspace.yml-only block: ValidateLayerRoots
				// refuses it here, and that refusal is the caller's story.
				if err := os.WriteFile(filepath.Join(root, "workspace", "defaults.yml"),
					[]byte("secrets:\n  recipient: "+id.Recipient()+"\n"), 0o644); err != nil {
					t.Fatalf("writing defaults.yml: %v", err)
				}
				return Options{BaseDir: root, Layers: rawLayers(t, cfgPath)}
			},
		},
		{
			name: "no recipient",
			setup: func(t *testing.T, root string) Options {
				return Options{BaseDir: root, Layers: rawLayers(t, writeProject(t, root, ""))}
			},
		},
		{
			name: "malformed recipient",
			setup: func(t *testing.T, root string) Options {
				return Options{BaseDir: root, Layers: rawLayers(t, writeProject(t, root, "not-an-age-recipient"))}
			},
		},
		{
			name: "recipient but nothing encrypted",
			setup: func(t *testing.T, root string) Options {
				id := installIdentityElsewhere(t)
				return Options{BaseDir: root, Layers: rawLayers(t, writeProject(t, root, id.Recipient()))}
			},
		},
		{
			name: "usable keyfile",
			setup: func(t *testing.T, root string) Options {
				id := installIdentity(t)
				return Options{BaseDir: root, Layers: layersWithMarker(t, root, id.Recipient(), encrypt(t, "v", id.Recipient()))}
			},
		},
		{
			name: "usable env identity",
			setup: func(t *testing.T, root string) Options {
				id := installIdentityElsewhere(t)
				t.Setenv(secrets.EnvKey, id.Export())
				return Options{BaseDir: root, Layers: layersWithMarker(t, root, id.Recipient(), encrypt(t, "v", id.Recipient()))}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateHome(t)
			root := t.TempDir()
			opts := tt.setup(t, root)
			opts.Interactive = true
			opts.Prompt, opts.Confirm = hostileHooks(t)

			imported, err := Ensure(t.Context(), opts)
			if imported || err != nil {
				t.Fatalf("Ensure = (%v, %v), want (false, nil)", imported, err)
			}
		})
	}
}

// TestEnsure_ModesThatNeverPrompt pins R3.1: the offer exists only in an
// interactive, human-driven, text-mode run. Every other mode falls through to
// the caller's existing failure.
func TestEnsure_ModesThatNeverPrompt(t *testing.T) {
	tests := []struct {
		name  string
		apply func(o *Options)
	}{
		{"not a terminal", func(o *Options) { o.Interactive = false }},
		{"DWE_NONINTERACTIVE", func(o *Options) { o.NonInteractive = true }},
		{"--yes", func(o *Options) { o.Yes = true }},
		{"--output json", func(o *Options) { o.OutputJSON = true }},
		{"nil Prompt", func(o *Options) { o.Prompt = nil }},
		{"nil Confirm", func(o *Options) { o.Confirm = nil }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateHome(t)
			root := t.TempDir()
			id := installIdentityElsewhere(t)

			opts := Options{
				BaseDir:     root,
				Layers:      layersWithMarker(t, root, id.Recipient(), encrypt(t, "v", id.Recipient())),
				Interactive: true,
			}
			opts.Prompt, opts.Confirm = hostileHooks(t)
			tt.apply(&opts)

			imported, err := Ensure(t.Context(), opts)
			if imported || err != nil {
				t.Fatalf("Ensure = (%v, %v), want (false, nil)", imported, err)
			}
			if keyfileExists(t, id.Recipient()) {
				t.Error("a keyfile was written in a mode that must not import")
			}
		})
	}
}

// TestEnsure_EnvSourceUnusable pins that a present-but-broken environment
// source is explained rather than prompted over: LoadIdentity is
// first-present-source-wins, so a freshly written keyfile would not even be
// consulted. The variable's VALUE must never reach the message — it is private
// key text.
func TestEnsure_EnvSourceUnusable(t *testing.T) {
	foreign, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	tests := []struct {
		name string
		set  func(t *testing.T, dir string) (envName, value string)
	}{
		{
			name: "DWE_AGE_KEY holds another project's key",
			set: func(t *testing.T, _ string) (string, string) {
				t.Setenv(secrets.EnvKey, foreign.Export())
				return secrets.EnvKey, foreign.Export()
			},
		},
		{
			name: "DWE_AGE_KEY_FILE points at another project's key",
			set: func(t *testing.T, dir string) (string, string) {
				path := filepath.Join(dir, "other.key")
				if err := os.WriteFile(path, []byte(foreign.Export()+"\n"), 0o600); err != nil {
					t.Fatalf("writing key: %v", err)
				}
				t.Setenv(secrets.EnvKeyFile, path)
				return secrets.EnvKeyFile, foreign.Export()
			},
		},
		{
			name: "DWE_AGE_KEY is truncated",
			set: func(t *testing.T, _ string) (string, string) {
				truncated := foreign.Export()[:20]
				t.Setenv(secrets.EnvKey, truncated)
				return secrets.EnvKey, truncated
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateHome(t)
			root := t.TempDir()
			id := installIdentityElsewhere(t)
			envName, value := tt.set(t, t.TempDir())

			opts := Options{
				BaseDir:     root,
				Layers:      layersWithMarker(t, root, id.Recipient(), encrypt(t, "v", id.Recipient())),
				Interactive: true,
			}
			opts.Prompt, opts.Confirm = hostileHooks(t)

			imported, err := Ensure(t.Context(), opts)
			if imported {
				t.Error("Ensure reported an import over an unusable env source")
			}
			if !errors.Is(err, ErrEnvSourceUnusable) {
				t.Fatalf("err = %v, want ErrEnvSourceUnusable", err)
			}
			if !strings.Contains(err.Error(), "$"+envName) {
				t.Errorf("error %q does not name $%s", err, envName)
			}
			assertNoKeyLeak(t, value, err.Error())
		})
	}
}

// TestEnsure_KeyfileUnusable pins the second no-prompt refusal: a canonical
// keyfile that exists but holds another key. WriteKeyfile is O_EXCL, so an
// import could not replace it — the message has to name the removal.
func TestEnsure_KeyfileUnusable(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	id := installIdentityElsewhere(t)
	foreign, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	// A keyfile named after the project's recipient, holding somebody else's key.
	path, err := secrets.KeyfilePath(id.Recipient())
	if err != nil {
		t.Fatalf("keyfile path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("creating keys dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(foreign.Export()+"\n"), 0o600); err != nil {
		t.Fatalf("writing keyfile: %v", err)
	}

	opts := Options{
		BaseDir:     root,
		Layers:      layersWithMarker(t, root, id.Recipient(), encrypt(t, "v", id.Recipient())),
		Interactive: true,
	}
	opts.Prompt, opts.Confirm = hostileHooks(t)

	imported, gerr := Ensure(t.Context(), opts)
	if imported {
		t.Error("Ensure reported an import over an unusable keyfile")
	}
	if !errors.Is(gerr, ErrKeyfileUnusable) {
		t.Fatalf("err = %v, want ErrKeyfileUnusable", gerr)
	}
	if !strings.Contains(gerr.Error(), "key remove") || !strings.Contains(gerr.Error(), path) {
		t.Errorf("error %q must name the keyfile and 'dwe secrets key remove'", gerr)
	}
	assertNoKeyLeak(t, foreign.Export(), gerr.Error())
}

// TestEnsure_DanglingKeyfileSymlinkUnusable pins that the refusal is about the
// path ENTRY, not the file behind it: O_EXCL fails on a dangling symlink too,
// so a gate that followed the link would open a form whose write is already
// doomed and collect a private key for nothing.
func TestEnsure_DanglingKeyfileSymlinkUnusable(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	id := installIdentityElsewhere(t)

	path, err := secrets.KeyfilePath(id.Recipient())
	if err != nil {
		t.Fatalf("keyfile path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("creating keys dir: %v", err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone.key"), path); err != nil {
		t.Fatalf("creating dangling symlink: %v", err)
	}

	opts := Options{
		BaseDir:     root,
		Layers:      layersWithMarker(t, root, id.Recipient(), encrypt(t, "v", id.Recipient())),
		Interactive: true,
	}
	opts.Prompt, opts.Confirm = hostileHooks(t)

	imported, gerr := Ensure(t.Context(), opts)
	if imported {
		t.Error("Ensure reported an import over a dangling keyfile symlink")
	}
	if !errors.Is(gerr, ErrKeyfileUnusable) {
		t.Fatalf("err = %v, want ErrKeyfileUnusable", gerr)
	}
	if !strings.Contains(gerr.Error(), "key remove") || !strings.Contains(gerr.Error(), path) {
		t.Errorf("error %q must name the keyfile and 'dwe secrets key remove'", gerr)
	}
}

// TestEnsure_ImportsAndReports is the happy path: confirm, type the key, and
// the gate writes the keyfile 0600, verifies it through the real lookup and
// reports what became readable.
func TestEnsure_ImportsAndReports(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	id := installIdentityElsewhere(t)

	layers := layersWithMarker(t, root, id.Recipient(), encrypt(t, "v", id.Recipient()))
	writeAgeFile(t, root, "app/creds.age", id.Recipient(), "hello")

	var explanation string
	var out strings.Builder
	imported, err := Ensure(t.Context(), Options{
		BaseDir: root, Layers: layers, Interactive: true, Out: &out,
		Confirm: func(_ context.Context, why string) (bool, error) { explanation = why; return true, nil },
		Prompt:  func(context.Context, string) (secrets.Identity, error) { return id, nil },
	})
	if err != nil || !imported {
		t.Fatalf("Ensure = (%v, %v), want (true, nil)", imported, err)
	}
	if !strings.Contains(explanation, id.Recipient()) {
		t.Errorf("explanation %q does not name the recipient", explanation)
	}

	path, err := secrets.KeyfilePath(id.Recipient())
	if err != nil {
		t.Fatalf("keyfile path: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("keyfile not written: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("keyfile mode = %v, want 0600", fi.Mode().Perm())
	}
	if _, _, err := secrets.LoadIdentity(id.Recipient()); err != nil {
		t.Errorf("the identity does not load after the import: %v", err)
	}

	report := out.String()
	if !strings.Contains(report, path) {
		t.Errorf("report %q does not name the keyfile", report)
	}
	if !strings.Contains(report, "1 encrypted value(s) and 1 .age file(s) are now readable") {
		t.Errorf("report %q lacks the readability counts", report)
	}
	assertNoKeyLeak(t, id.Export(), report)
}

// TestEnsure_Declined pins the binary choice: declining aborts with the fix
// instruction and writes nothing.
func TestEnsure_Declined(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	id := installIdentityElsewhere(t)

	imported, err := Ensure(t.Context(), Options{
		BaseDir:     root,
		Layers:      layersWithMarker(t, root, id.Recipient(), encrypt(t, "v", id.Recipient())),
		Interactive: true,
		Confirm:     func(context.Context, string) (bool, error) { return false, nil },
		Prompt: func(context.Context, string) (secrets.Identity, error) {
			t.Error("Prompt was called after the offer was declined")
			return secrets.Identity{}, nil
		},
	})
	if imported {
		t.Error("Ensure reported an import after a decline")
	}
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("err = %v, want ErrAborted", err)
	}
	if !strings.Contains(err.Error(), secrets.IdentityHint(id.Recipient())) {
		t.Errorf("error %q does not carry the fix hint", err)
	}
	if keyfileExists(t, id.Recipient()) {
		t.Error("a keyfile was written after a decline")
	}
}

// TestEnsure_PromptCancelled pins that a cancelled form is the same outcome as
// a decline, and that neither the cancel error nor the typed text travels into
// the message.
func TestEnsure_PromptCancelled(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	id := installIdentityElsewhere(t)

	imported, err := Ensure(t.Context(), Options{
		BaseDir:     root,
		Layers:      layersWithMarker(t, root, id.Recipient(), encrypt(t, "v", id.Recipient())),
		Interactive: true,
		Confirm:     func(context.Context, string) (bool, error) { return true, nil },
		Prompt: func(context.Context, string) (secrets.Identity, error) {
			return secrets.Identity{}, errors.New("cancelled: " + id.Export())
		},
	})
	if imported {
		t.Error("Ensure reported an import after a cancelled prompt")
	}
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("err = %v, want ErrAborted", err)
	}
	assertNoKeyLeak(t, id.Export(), err.Error())
	if keyfileExists(t, id.Recipient()) {
		t.Error("a keyfile was written after a cancelled prompt")
	}
}

// TestEnsure_RefusesForeignIdentityFromPrompt pins the last-resort check: the
// form validates the recipient, but a hand-rolled PromptFunc might not, and a
// keyfile stored under a foreign recipient's name would look installed and
// open nothing.
func TestEnsure_RefusesForeignIdentityFromPrompt(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	id := installIdentityElsewhere(t)
	foreign, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	imported, gerr := Ensure(t.Context(), Options{
		BaseDir:     root,
		Layers:      layersWithMarker(t, root, id.Recipient(), encrypt(t, "v", id.Recipient())),
		Interactive: true,
		Confirm:     func(context.Context, string) (bool, error) { return true, nil },
		Prompt:      func(context.Context, string) (secrets.Identity, error) { return foreign, nil },
	})
	if imported || gerr == nil {
		t.Fatalf("Ensure = (%v, %v), want a refusal", imported, gerr)
	}
	if keyfileExists(t, id.Recipient()) || keyfileExists(t, foreign.Recipient()) {
		t.Error("a keyfile was written for a mismatching identity")
	}
	assertNoKeyLeak(t, foreign.Export(), gerr.Error())
}

// TestNonInteractiveEnv pins the truthiness set. The equality with
// cmdctx.NonInteractiveEnv() is pinned on the cli side, so this package's test
// binary never imports the cli layer.
func TestNonInteractiveEnv(t *testing.T) {
	for value, want := range map[string]bool{"1": true, "true": true, "": false, "0": false, "yes": false, "TRUE": false} {
		t.Setenv("DWE_NONINTERACTIVE", value)
		if got := NonInteractiveEnv(); got != want {
			t.Errorf("NonInteractiveEnv() with %q = %v, want %v", value, got, want)
		}
	}
}

// --- helpers ----------------------------------------------------------------

// installIdentityElsewhere mints a key pair WITHOUT storing it: the state of a
// freshly cloned project whose recipient is committed and whose identity is not
// on this machine.
func installIdentityElsewhere(t *testing.T) secrets.Identity {
	t.Helper()
	id, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	return id
}

func keyfileExists(t *testing.T, recipient string) bool {
	t.Helper()
	path, err := secrets.KeyfilePath(recipient)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// assertNoKeyLeak pins that a surface carries neither the key text nor its
// tail: the prefix alone would pass on a truncated echo.
func assertNoKeyLeak(t *testing.T, key string, surfaces ...string) {
	t.Helper()
	for _, s := range surfaces {
		if strings.Contains(s, "AGE-SECRET-KEY-") {
			t.Errorf("output leaked private key material:\n%s", s)
		}
		if len(key) >= 20 && strings.Contains(s, key[len(key)-20:]) {
			t.Errorf("output leaked the tail of the key:\n%s", s)
		}
	}
}
