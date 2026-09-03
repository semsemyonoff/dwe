package secrets

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	localpkg "github.com/semsemyonoff/dwe/internal/core/project/local"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// keyfileNames lists the keys directory, so a test can pin that an aborted run
// left it untouched.
func keyfileNames(t *testing.T) []string {
	t.Helper()
	dir, err := secrets.KeysDir()
	if err != nil {
		t.Fatalf("keys dir: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("reading keys dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	slices.Sort(names)
	return names
}

// encryptedPayloads returns every committed marker and every *.age payload, so a
// test can assert which identity opens them.
func encryptedPayloads(t *testing.T, flags *cmdctx.RootFlags) (markers []string, files [][]byte) {
	t.Helper()
	layers, err := config.LoadRawLayers(flags.ConfigPath)
	if err != nil {
		t.Fatalf("loading raw layers: %v", err)
	}
	for _, m := range config.CollectMarkers(layers) {
		markers = append(markers, m.Value)
	}
	found, err := collectAgeFiles(flags.ProjectRoot())
	if err != nil {
		t.Fatalf("scanning .age files: %v", err)
	}
	for _, f := range found {
		if f.err != nil {
			t.Fatalf("unexpected refusal for %s: %v", f.path, f.err)
		}
		data, rerr := os.ReadFile(f.path)
		if rerr != nil {
			t.Fatalf("reading %s: %v", f.path, rerr)
		}
		files = append(files, data)
	}
	return markers, files
}

// assertReadableBy checks that every encrypted value in the project opens with
// exactly the identity for recipient (want=true) or with none of it
// (want=false). It is the property `rekey` exists to move.
func assertReadableBy(t *testing.T, flags *cmdctx.RootFlags, recipient string, want bool) {
	t.Helper()
	id, _, err := secrets.LoadIdentity(recipient)
	if err != nil {
		t.Fatalf("loading identity for %s: %v", recipient, err)
	}
	markers, files := encryptedPayloads(t, flags)
	if len(markers) == 0 && len(files) == 0 {
		t.Fatal("the project holds nothing encrypted; the assertion would be vacuous")
	}
	for i, m := range markers {
		if _, derr := secrets.Decrypt(m, id); (derr == nil) != want {
			t.Errorf("marker[%d] readable by %s = %v, want %v (%v)", i, recipient, derr == nil, want, derr)
		}
	}
	for i, f := range files {
		if _, derr := secrets.DecryptBytes(f, id); (derr == nil) != want {
			t.Errorf("file[%d] readable by %s = %v, want %v (%v)", i, recipient, derr == nil, want, derr)
		}
	}
}

// seedSecrets fills the fixture with the whole encrypted surface: a marker in
// each committed layer file and two encrypted pack sources.
func seedSecrets(t *testing.T, flags *cmdctx.RootFlags, recipient string) {
	t.Helper()
	if _, _, err := runSecrets(t, flags, "set", "vars.telegram.token", "123:abc"); err != nil {
		t.Fatalf("secrets set (defaults): %v", err)
	}
	if _, _, err := runSecrets(t, flags, "set", "vars.db.password", "hunter2", "--file", fileWorkspace); err != nil {
		t.Fatalf("secrets set (workspace): %v", err)
	}
	root := flags.ProjectRoot()
	writeAgeFile(t, root, "bot/creds.json.age", recipient, `{"ok":true}`)
	writeAgeFile(t, root, "api/token.env.age", recipient, "TOKEN=abc")
}

// TestRekey_RewritesOnlyTheMarkerLines pins the splice-writer contract for phase
// 4 and phase 5 on an annotated tree: two markers in defaults.yml become two
// changed lines and nothing else, and the recipient swap in workspace.yml is one
// changed line. The node writer would have re-encoded both files whole.
func TestRekey_RewritesOnlyTheMarkerLines(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	cfgPath := filepath.Join(root, "workspace.yml")
	if err := os.WriteFile(cfgPath, []byte(readTestdata(t, "annotated_workspace.yml")), 0o644); err != nil {
		t.Fatalf("writing workspace.yml: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "workspace"), 0o755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	initProject(t, flags)

	defaultsSrc := readTestdata(t, "annotated_defaults.yml")
	if err := os.WriteFile(defaultsPath(root), []byte(defaultsSrc), 0o644); err != nil {
		t.Fatalf("writing defaults.yml: %v", err)
	}
	for _, set := range [][2]string{{"vars.telegram.bot_token", "123:abc"}, {"vars.app.name", "hunter2"}} {
		if _, _, err := runSecrets(t, flags, "set", set[0], set[1]); err != nil {
			t.Fatalf("secrets set %s: %v", set[0], err)
		}
	}
	beforeDefaults, err := os.ReadFile(defaultsPath(root))
	if err != nil {
		t.Fatalf("reading defaults.yml: %v", err)
	}
	beforeWorkspace, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reading workspace.yml: %v", err)
	}

	if _, _, err := runSecrets(t, flags, "rekey"); err != nil {
		t.Fatalf("secrets rekey: %v", err)
	}

	afterDefaults, err := os.ReadFile(defaultsPath(root))
	if err != nil {
		t.Fatalf("re-reading defaults.yml: %v", err)
	}
	assertOnlyLinesChanged(t, string(beforeDefaults), string(afterDefaults),
		lineOf(t, string(beforeDefaults), "bot_token:"),
		lineOf(t, string(beforeDefaults), "name:"))

	afterWorkspace, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("re-reading workspace.yml: %v", err)
	}
	assertOnlyLinesChanged(t, string(beforeWorkspace), string(afterWorkspace),
		lineOf(t, string(beforeWorkspace), "recipient:"))

	// The values still open, with the new identity.
	if got := readSecret(t, flags, "vars.telegram.bot_token"); got != "123:abc" {
		t.Errorf("vars.telegram.bot_token = %q after the rekey", got)
	}
	if got := readSecret(t, flags, "vars.app.name"); got != "hunter2" {
		t.Errorf("vars.app.name = %q after the rekey", got)
	}
}

// TestRekey_RefusesUnsplicableMarker pins that a marker the splice writer cannot
// edit aborts in the READ-ONLY pass with the specific secrets_write_unsupported
// code rather than the generic secrets_rekey_failed wrapper. Both refused shapes
// decrypt perfectly, so only the dry run can see them — and it has to, because a
// phase-4 abort would already have minted a key pair and re-encrypted every .age
// source to it, sealing them to an identity nobody else can import.
func TestRekey_RefusesUnsplicableMarker(t *testing.T) {
	shapes := []struct {
		name string
		src  func(marker string) string
	}{
		{
			name: "flow mapping",
			src:  func(m string) string { return "vars:\n  telegram: {token: \"" + m + "\"}\n" },
		},
		{
			name: "block scalar",
			src:  func(m string) string { return "vars:\n  token: |-\n    " + m + "\n" },
		},
	}

	for _, shape := range shapes {
		for _, mode := range []string{"text", "json"} {
			t.Run(shape.name+"/"+mode, func(t *testing.T) {
				isolateHome(t)
				cfgPath, root := writeFixture(t)
				flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
				recipient := initProject(t, flags)

				marker, err := secrets.Encrypt("flow-plaintext-value", recipient)
				if err != nil {
					t.Fatalf("encrypt: %v", err)
				}
				src := shape.src(marker)
				if err := os.WriteFile(defaultsPath(root), []byte(src), 0o644); err != nil {
					t.Fatalf("writing defaults.yml: %v", err)
				}
				agePath := writeAgeFile(t, root, "bot/token.age", recipient, "pack-source-value")
				ageBefore, err := os.ReadFile(agePath)
				if err != nil {
					t.Fatalf("reading the .age source: %v", err)
				}
				if mode == "json" {
					flags.Output = "json"
				}

				keysBefore := keyfileNames(t)

				stdout, stderr, err := runSecrets(t, flags, "rekey")
				if err == nil {
					t.Fatalf("rekey succeeded against a marker in a %s", shape.name)
				}
				coded := codedError(t, err)
				if coded.Code != "secrets_write_unsupported" {
					t.Errorf("error code = %q, want secrets_write_unsupported (message: %s)", coded.Code, coded.Message)
				}
				// The shape is detectable read-only, so the refusal carries
				// written=false and no resume text: there is no mixed tree to
				// finish, only a value to reshape before running again.
				if coded.Details["written"] != false {
					t.Errorf("details[written] = %v, want false", coded.Details["written"])
				}
				if strings.Contains(coded.Hint, rekeyResumeHint) {
					t.Errorf("hint = %q, want no resume instruction when nothing was written", coded.Hint)
				}
				if got := keyfileNames(t); !slices.Equal(got, keysBefore) {
					t.Errorf("keys dir = %v, want it untouched (%v)", got, keysBefore)
				}
				payload, merr := json.Marshal(coded)
				if merr != nil {
					t.Fatalf("marshalling the coded error: %v", merr)
				}
				assertNoSecretLeak(t, "flow-plaintext-value", stdout, stderr, coded.Message, coded.Hint, string(payload))

				after, rerr := os.ReadFile(defaultsPath(root))
				if rerr != nil {
					t.Fatalf("reading defaults.yml: %v", rerr)
				}
				if string(after) != src {
					t.Errorf("defaults.yml changed after a refused rekey:\n%s", after)
				}
				if ageAfter, _ := os.ReadFile(agePath); !bytes.Equal(ageAfter, ageBefore) {
					t.Error("the .age source was re-encrypted by a rekey that refused a layer file")
				}
			})
		}
	}
}

// TestRekey_ReencryptsEverything pins the headline property: after a rekey every
// committed value opens with the new identity and none of them with the old one,
// while the retired keyfile is deliberately left in place.
func TestRekey_ReencryptsEverything(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	oldRecipient := initProject(t, flags)
	seedSecrets(t, flags, oldRecipient)

	out, _, err := runSecrets(t, flags, "rekey")
	if err != nil {
		t.Fatalf("secrets rekey: %v", err)
	}

	newRecipient, err := requireRecipient(flags)
	if err != nil {
		t.Fatalf("reading the new recipient: %v", err)
	}
	if newRecipient == oldRecipient {
		t.Fatal("rekey left secrets.recipient unchanged")
	}
	if !strings.Contains(out, newRecipient) || !strings.Contains(out, oldRecipient) {
		t.Errorf("report names neither recipient\ngot:\n%s", out)
	}
	if strings.Contains(out, "AGE-SECRET-KEY-") {
		t.Errorf("report leaked a private key:\n%s", out)
	}

	keyfile, err := secrets.KeyfilePath(newRecipient)
	if err != nil {
		t.Fatalf("keyfile path: %v", err)
	}
	if got := fileMode(t, keyfile); got != 0o600 {
		t.Errorf("new keyfile mode = %v, want 0600", got)
	}
	if len(keyfileNames(t)) != 2 {
		t.Errorf("keys dir = %v, want the old keyfile kept beside the new one", keyfileNames(t))
	}

	assertReadableBy(t, flags, newRecipient, true)
	assertReadableBy(t, flags, oldRecipient, false)

	// The plaintext survived the round trip through the node writer.
	if got := readSecret(t, flags, "vars.telegram.token"); got != "123:abc" {
		t.Errorf("vars.telegram.token = %q, want %q", got, "123:abc")
	}
	if got := readSecret(t, flags, "vars.db.password"); got != "hunter2" {
		t.Errorf("vars.db.password = %q, want %q", got, "hunter2")
	}
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("loading config after rekey: %v", err)
	}
	if len(cfg.SecretsState.Unresolved) != 0 {
		t.Errorf("SecretsState.Unresolved = %+v, want empty after a rekey", cfg.SecretsState.Unresolved)
	}
}

// TestRekey_NonStringMapKeys pins that the read-only pass and the node rewriter
// agree on which scalars are markers. yaml.v3 demotes a mapping with any
// non-string key to map[any]any; when the marker inventory skipped that shape,
// ReplaceScalars still found the value and rekey aborted mid-write with "an
// encrypted value appeared after the read-only pass" — on every retry, since
// nothing about the tree changed. When the hidden marker was the only one in
// its file the failure was quieter and worse: rekey reported success, rotated
// secrets.recipient, and left the value encrypted to the retired key.
func TestRekey_NonStringMapKeys(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	oldRecipient := initProject(t, flags)

	// vars.db.password is string-keyed, so the layer enters the rekey plan and
	// ReplaceScalars runs over the whole file; vars.ports carries a non-string
	// key, so the marker under it lives in a map[any]any the inventory walk has
	// to reach too.
	hidden, err := secrets.Encrypt("port-secret", oldRecipient)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	visible, err := secrets.Encrypt("hunter2", oldRecipient)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	fixture := "schema_version: \"2\"\nproject:\n  name: sectest\n  prefix: dwe\n" +
		"vars:\n  db:\n    password: " + visible + "\n  ports:\n    8080: " + hidden + "\n" +
		"secrets:\n  recipient: " + oldRecipient + "\n"
	if err := os.WriteFile(cfgPath, []byte(fixture), 0o644); err != nil {
		t.Fatalf("writing workspace.yml: %v", err)
	}

	if _, _, err := runSecrets(t, flags, "rekey"); err != nil {
		t.Fatalf("secrets rekey: %v", err)
	}

	newRecipient, err := requireRecipient(flags)
	if err != nil {
		t.Fatalf("reading the new recipient: %v", err)
	}
	assertReadableBy(t, flags, newRecipient, true)
	assertReadableBy(t, flags, oldRecipient, false)

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("loading config after rekey: %v", err)
	}
	if len(cfg.SecretsState.Unresolved) != 0 {
		t.Fatalf("SecretsState.Unresolved = %+v, want empty after a rekey", cfg.SecretsState.Unresolved)
	}
	var paths []string
	for _, ref := range cfg.SecretsState.Decrypted {
		paths = append(paths, ref.Path)
	}
	if want := []string{"vars.db.password", "vars.ports.8080"}; !slices.Equal(paths, want) {
		t.Fatalf("SecretsState.Decrypted = %v, want %v", paths, want)
	}
}

// TestRekey_PreservesCommentsAndAnchors pins that the layer rewrite goes through
// the node writer: comments, key order and anchors survive, the anchored marker
// is re-encrypted exactly once, and the alias still points at it.
func TestRekey_PreservesCommentsAndAnchors(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	oldRecipient := initProject(t, flags)

	marker, err := secrets.Encrypt("shared-token", oldRecipient)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	writeDefaults(t, root, `# top-level note
vars:
  # the shared bot token
  primary: &tok `+marker+`
  mirror: *tok
  plain: keep-me
`)

	if _, _, err := runSecrets(t, flags, "rekey"); err != nil {
		t.Fatalf("secrets rekey: %v", err)
	}

	raw, err := os.ReadFile(defaultsPath(root))
	if err != nil {
		t.Fatalf("reading defaults.yml: %v", err)
	}
	body := string(raw)
	for _, want := range []string{"# top-level note", "# the shared bot token", "&tok", "*tok", "plain: keep-me"} {
		if !strings.Contains(body, want) {
			t.Errorf("defaults.yml lost %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, marker) {
		t.Errorf("the old marker survived the rekey:\n%s", body)
	}
	if n := strings.Count(body, secrets.MarkerPrefix); n != 1 {
		t.Errorf("marker count = %d, want 1 (the alias must not be expanded)", n)
	}

	newRecipient, err := requireRecipient(flags)
	if err != nil {
		t.Fatalf("reading the new recipient: %v", err)
	}
	assertReadableBy(t, flags, newRecipient, true)
	if got := readSecret(t, flags, "vars.primary"); got != "shared-token" {
		t.Errorf("vars.primary = %q, want %q", got, "shared-token")
	}
}

// TestRekey_LocalLayerKeepsItsMode pins the per-file write policy: local.yml is
// gitignored developer state and stays forced to 0600 even when the file on disk
// was looser, while the tracked defaults.yml keeps its own mode.
func TestRekey_LocalLayerKeepsItsMode(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	oldRecipient := initProject(t, flags)

	marker, err := secrets.Encrypt("personal", oldRecipient)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	localPath := filepath.Join(root, "workspace", "local.yml")
	if err := os.WriteFile(localPath, []byte("vars:\n  token: "+marker+"\n"), 0o644); err != nil {
		t.Fatalf("writing local.yml: %v", err)
	}
	writeDefaults(t, root, "vars:\n  other: "+marker+"\n")
	if err := os.Chmod(defaultsPath(root), 0o640); err != nil {
		t.Fatalf("chmod defaults.yml: %v", err)
	}

	if _, _, err := runSecrets(t, flags, "rekey"); err != nil {
		t.Fatalf("secrets rekey: %v", err)
	}
	if got := fileMode(t, localPath); got != 0o600 {
		t.Errorf("local.yml mode = %v, want it forced back to 0600", got)
	}
	if got := fileMode(t, defaultsPath(root)); got != 0o640 {
		t.Errorf("defaults.yml mode = %v, want its own 0640 preserved", got)
	}
}

// TestRekey_AbortsBeforeAnyWrite pins decision 11's read-only pass: a value this
// machine cannot open stops the command with the keys directory and every layer
// file byte-identical.
func TestRekey_AbortsBeforeAnyWrite(t *testing.T) {
	foreign, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	cases := []struct {
		name string
		seed func(t *testing.T, root, recipient string)
	}{
		{
			name: "corrupt marker",
			seed: func(t *testing.T, root, recipient string) {
				corrupt := secrets.MarkerPrefix + base64.StdEncoding.EncodeToString([]byte("garbage")) + "]"
				writeDefaults(t, root, "vars:\n  broken: "+corrupt+"\n")
			},
		},
		{
			name: "foreign recipient age file",
			seed: func(t *testing.T, root, recipient string) {
				good, err := secrets.Encrypt("fine", recipient)
				if err != nil {
					t.Fatalf("encrypt: %v", err)
				}
				writeDefaults(t, root, "vars:\n  ok: "+good+"\n")
				writeAgeFile(t, root, "bot/theirs.age", foreign.Recipient(), "not mine")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			cfgPath, root := writeFixture(t)
			flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
			recipient := initProject(t, flags)
			tc.seed(t, root, recipient)

			keysBefore := keyfileNames(t)
			defaultsBefore, err := os.ReadFile(defaultsPath(root))
			if err != nil {
				t.Fatalf("reading defaults.yml: %v", err)
			}
			workspaceBefore, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatalf("reading workspace.yml: %v", err)
			}

			_, _, err = runSecrets(t, flags, "rekey")
			if err == nil {
				t.Fatal("rekey succeeded over a value it cannot read")
			}
			coded := codedError(t, err)
			if coded.Code != "secrets_rekey_blocked" {
				t.Errorf("error code = %q, want secrets_rekey_blocked", coded.Code)
			}
			if coded.Details["written"] != false {
				t.Errorf("details = %+v, want written=false", coded.Details)
			}

			if got := keyfileNames(t); !slices.Equal(got, keysBefore) {
				t.Errorf("keys dir = %v, want it untouched (%v)", got, keysBefore)
			}
			if after, _ := os.ReadFile(defaultsPath(root)); string(after) != string(defaultsBefore) {
				t.Errorf("defaults.yml was rewritten by an aborted rekey:\n%s", after)
			}
			if after, _ := os.ReadFile(cfgPath); string(after) != string(workspaceBefore) {
				t.Errorf("workspace.yml was rewritten by an aborted rekey:\n%s", after)
			}
		})
	}
}

// TestRekey_ResumesAfterInterruption pins the recovery half of decision 11. Each
// case reconstructs the tree a crash would leave after one of the write phases —
// the new keyfile is always on disk, the retired one is kept — and asserts that a
// second rekey converges on a tree readable by exactly one identity.
func TestRekey_ResumesAfterInterruption(t *testing.T) {
	cases := []string{"after keyfile", "after age files", "after layers"}

	for _, stage := range cases {
		t.Run(stage, func(t *testing.T) {
			isolateHome(t)
			cfgPath, root := writeFixture(t)
			flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
			oldRecipient := initProject(t, flags)
			seedSecrets(t, flags, oldRecipient)

			// The interrupted run's new key pair: written first, as the real
			// command does, and never recorded in workspace.yml.
			interrupted, err := secrets.Keygen()
			if err != nil {
				t.Fatalf("keygen: %v", err)
			}
			if _, err := secrets.WriteKeyfile(interrupted); err != nil {
				t.Fatalf("writing the interrupted keyfile: %v", err)
			}

			if stage != "after keyfile" {
				// Phase 3 completed: the .age sources already moved.
				writeAgeFile(t, root, "bot/creds.json.age", interrupted.Recipient(), `{"ok":true}`)
				writeAgeFile(t, root, "api/token.env.age", interrupted.Recipient(), "TOKEN=abc")
			}
			if stage == "after layers" {
				// Phase 4 completed too: the markers moved, the recipient did not.
				for _, path := range []string{defaultsPath(root), cfgPath} {
					reencryptFileForTest(t, path, oldRecipient, interrupted.Recipient(), []config.Layer{{Path: cfgPath}})
				}
			}

			if _, _, err := runSecrets(t, flags, "rekey"); err != nil {
				t.Fatalf("resuming rekey after %q: %v", stage, err)
			}

			final, err := requireRecipient(flags)
			if err != nil {
				t.Fatalf("reading the final recipient: %v", err)
			}
			if final == oldRecipient || final == interrupted.Recipient() {
				t.Fatalf("the resumed rekey did not mint a fresh recipient (got %s)", final)
			}
			assertReadableBy(t, flags, final, true)
			assertReadableBy(t, flags, oldRecipient, false)
			assertReadableBy(t, flags, interrupted.Recipient(), false)
			if got := readSecret(t, flags, "vars.telegram.token"); got != "123:abc" {
				t.Errorf("vars.telegram.token = %q, want the value to survive the resume", got)
			}
		})
	}
}

// reencryptFileForTest rewrites every marker in a layer file from one recipient
// to another, the way phase 4 of a rekey would, so a test can construct the state
// a crash between phases leaves behind.
func reencryptFileForTest(t *testing.T, path, from, to string, layers []config.Layer) {
	t.Helper()
	id, _, err := secrets.LoadIdentity(from)
	if err != nil {
		t.Fatalf("loading the identity for %s: %v", from, err)
	}
	label, policy := layerWritePolicy(path, layers)
	splicer, err := localpkg.NewSplicer(path, label)
	if err != nil {
		t.Fatalf("loading %s: %v", path, err)
	}
	count, err := splicer.ReplaceScalars(func(s string) (string, bool, error) {
		if !secrets.IsMarker(s) {
			return s, false, nil
		}
		plain, derr := secrets.Decrypt(s, id)
		if derr != nil {
			return s, false, fmt.Errorf("decrypting a marker in %s: %w", path, derr)
		}
		marker, eerr := secrets.Encrypt(plain, to)
		if eerr != nil {
			return s, false, fmt.Errorf("re-encrypting a marker in %s: %w", path, eerr)
		}
		return marker, true, nil
	})
	if err != nil {
		t.Fatalf("re-encrypting %s: %v", path, err)
	}
	if count == 0 {
		return
	}
	if err := splicer.Write(path, policy); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// TestRekey_NoRecipient pins that an uninitialized project is sent to `init`.
func TestRekey_NoRecipient(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	_, _, err := runSecrets(t, flags, "rekey")
	if err == nil {
		t.Fatal("rekey succeeded on a project with no key pair")
	}
	coded := codedError(t, err)
	if coded.Code != "secrets_no_recipient" {
		t.Errorf("error code = %q, want secrets_no_recipient", coded.Code)
	}
	if len(keyfileNames(t)) != 0 {
		t.Errorf("keys dir = %v, want nothing minted", keyfileNames(t))
	}
}

// TestRekey_JSON pins the DTO.
func TestRekey_JSON(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	oldRecipient := initProject(t, flags)
	seedSecrets(t, flags, oldRecipient)

	flags.Output = "json"
	out, errOut, err := runSecrets(t, flags, "rekey")
	if err != nil {
		t.Fatalf("secrets rekey --output json: %v", err)
	}
	if errOut != "" {
		t.Errorf("stderr should be empty in JSON mode, got: %q", errOut)
	}
	var data rekeyJSON
	if e := json.Unmarshal([]byte(out), &data); e != nil {
		t.Fatalf("unmarshal rekey json: %v\nraw: %s", e, out)
	}
	if data.OldRecipient != oldRecipient || data.Recipient == oldRecipient {
		t.Errorf("payload = %+v, want the old recipient reported and a new one minted", data)
	}
	if data.Markers != 2 {
		t.Errorf("markers = %d, want 2", data.Markers)
	}
	if len(data.Layers) != 2 {
		t.Errorf("layers = %v, want both committed layer files", data.Layers)
	}
	if len(data.Files) != 2 {
		t.Errorf("files = %v, want both .age sources", data.Files)
	}
	if !slices.IsSorted(data.Files) {
		t.Errorf("files = %v, want a stable sorted order", data.Files)
	}
	if strings.Contains(out, "AGE-SECRET-KEY-") {
		t.Errorf("rekey JSON leaked a private key: %s", out)
	}
}
