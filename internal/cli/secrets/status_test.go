package secrets

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/workflow/keygate"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// statusOf runs `secrets status --output json` against the fixture and decodes
// the payload. Every assertion below goes through the JSON shape rather than
// the rendered table: the table's byte-exact form is pinned by goldens in
// internal/core/ui/render, where the palette and the paths are deterministic.
func statusOf(t *testing.T, flags *cmdctx.RootFlags) statusJSON {
	t.Helper()
	jsonFlags := &cmdctx.RootFlags{ConfigPath: flags.ConfigPath, Root: flags.Root, Output: "json"}
	stdout, stderr, err := runSecrets(t, jsonFlags, "status")
	if err != nil {
		t.Fatalf("secrets status: %v (stderr: %s)", err, stderr)
	}
	if stderr != "" {
		t.Errorf("status wrote to stderr in JSON mode: %q", stderr)
	}
	var out statusJSON
	if derr := json.Unmarshal([]byte(stdout), &out); derr != nil {
		t.Fatalf("decoding status JSON %q: %v", stdout, derr)
	}
	return out
}

// TestStatus_JSON_MarkersAndFiles pins the JSON contract over the mixed
// project: a readable marker, one encrypted to somebody else, a damaged
// payload, and one readable plus one foreign .age source.
func TestStatus_JSON_MarkersAndFiles(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	foreign, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	mine, err := secrets.Encrypt("s3cret-token", recipient)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	theirs, err := secrets.Encrypt("not-for-me", foreign.Recipient())
	if err != nil {
		t.Fatalf("encrypt foreign: %v", err)
	}
	corrupt := secrets.MarkerPrefix + base64.StdEncoding.EncodeToString([]byte("garbage")) + "]"
	defaults := "vars:\n  a_token: " + mine + "\n  b_token: " + theirs + "\n  c_token: " + corrupt + "\n"
	if err := os.WriteFile(filepath.Join(root, "workspace", "defaults.yml"), []byte(defaults), 0o644); err != nil {
		t.Fatalf("writing defaults.yml: %v", err)
	}
	writeAgeFile(t, root, "app/creds.json.age", recipient, `{"ok":true}`)
	writeAgeFile(t, root, "app/foreign.env.age", foreign.Recipient(), "X=1")

	got := statusOf(t, flags)

	if got.Recipient != recipient {
		t.Errorf("recipient = %q, want %q", got.Recipient, recipient)
	}
	if got.Identity.Source != string(secrets.SourceKeyfile) {
		t.Errorf("identity source = %q, want %q", got.Identity.Source, secrets.SourceKeyfile)
	}
	if got.Identity.Keyfile == "" {
		t.Error("identity.keyfile is empty for a keyfile-sourced identity")
	}
	if got.Identity.Error != "" {
		t.Errorf("identity.error = %q, want empty when the identity loaded", got.Identity.Error)
	}

	layer := filepath.Join("workspace", "defaults.yml")
	wantMarkers := []markerRow{
		{Layer: layer, Path: "vars.a_token", State: stateDecrypted},
		{Layer: layer, Path: "vars.b_token", State: stateUnresolved, Reason: "wrong_identity"},
		{Layer: layer, Path: "vars.c_token", State: stateUnresolved, Reason: "corrupt"},
	}
	if len(got.Markers) != len(wantMarkers) {
		t.Fatalf("markers = %+v, want %d rows", got.Markers, len(wantMarkers))
	}
	for i, want := range wantMarkers {
		if got.Markers[i] != want {
			t.Errorf("marker[%d] = %+v, want %+v", i, got.Markers[i], want)
		}
	}

	packDir := filepath.Join("workspace", "templates", "config", "app")
	wantFiles := []fileRow{
		{File: filepath.Join(packDir, "creds.json.age"), State: stateDecryptable},
		{File: filepath.Join(packDir, "foreign.env.age"), State: stateNotDecryptable, Reason: "wrong_identity"},
	}
	if len(got.Files) != len(wantFiles) {
		t.Fatalf("files = %+v, want %d rows", got.Files, len(wantFiles))
	}
	for i, want := range wantFiles {
		if got.Files[i] != want {
			t.Errorf("file[%d] = %+v, want %+v", i, got.Files[i], want)
		}
	}
}

// TestStatus_JSON_Keyless pins the new-developer report: every value fails for
// the one actionable reason, and the identity block says why rather than the
// command failing.
// A body damaged below the age header survives CheckMarker (shape, base64 and
// header are all intact) and fails only in Decrypt. Reporting that as
// wrong_identity would contradict the loader, which calls the same value
// corrupt, and would advertise a rekey as the fix for a value no key can open.
func TestStatus_JSON_DamagedBodyIsCorruptNotWrongIdentity(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	marker, err := secrets.Encrypt("s3cret-token", recipient)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(marker[len(secrets.MarkerPrefix) : len(marker)-1])
	if err != nil {
		t.Fatalf("decoding the marker payload: %v", err)
	}
	// Drop the tail of the payload only: the header and the recipient stanza
	// stay intact, so the identity still matches and the failure is the body.
	damaged := secrets.MarkerPrefix + base64.StdEncoding.EncodeToString(raw[:len(raw)-8]) + "]"
	if err := secrets.CheckMarker(damaged); err != nil {
		t.Fatalf("the fixture must still pass CheckMarker, got %v", err)
	}

	defaults := "vars:\n  tok: " + damaged + "\n"
	if err := os.WriteFile(filepath.Join(root, "workspace", "defaults.yml"), []byte(defaults), 0o644); err != nil {
		t.Fatalf("writing defaults.yml: %v", err)
	}

	got := statusOf(t, flags)
	if len(got.Markers) != 1 {
		t.Fatalf("markers = %+v, want 1 row", got.Markers)
	}
	if got.Markers[0].Reason != "corrupt" {
		t.Errorf("reason = %q, want %q", got.Markers[0].Reason, "corrupt")
	}

	// The loader must name the same cause for the same value.
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	if len(cfg.SecretsState.Unresolved) != 1 || cfg.SecretsState.Unresolved[0].Reason != config.ReasonCorrupt {
		t.Errorf("SecretsState.Unresolved = %+v, want one corrupt", cfg.SecretsState.Unresolved)
	}
}

// TestStatus_JSON_DamagedFileIsCorruptNotWrongIdentity is the .age-file twin of
// the marker case above: a body truncated below the age header still carries the
// recipient stanza, so DecryptBytes fails as ErrCorrupt rather than as a
// recipient mismatch. Calling it wrong_identity would contradict
// `dwe validate secrets`, which reads the same bytes through the same decoder,
// and would advertise a rekey as the fix for a file that must come back from git.
func TestStatus_JSON_DamagedFileIsCorruptNotWrongIdentity(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	path := writeAgeFile(t, root, "app/creds.json.age", recipient, `{"ok":true}`)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the pack source: %v", err)
	}
	if err := os.WriteFile(path, data[:len(data)-8], 0o644); err != nil {
		t.Fatalf("truncating the pack source: %v", err)
	}

	got := statusOf(t, flags)
	if len(got.Files) != 1 {
		t.Fatalf("files = %+v, want 1 row", got.Files)
	}
	if got.Files[0].State != stateNotDecryptable || got.Files[0].Reason != "corrupt" {
		t.Errorf("file row = %+v, want not-decryptable/corrupt", got.Files[0])
	}

	// decryptBytes must name the same cause, so `secrets decrypt` and rekey's
	// read-only abort do not send the user after the recipient either.
	ids := keygate.LoadIdentitySet(recipient)
	if _, derr := ids.DecryptBytes(data[:len(data)-8]); !errors.Is(derr, secrets.ErrCorrupt) {
		t.Errorf("decryptBytes error = %v, want ErrCorrupt", derr)
	}
}

func TestStatus_JSON_Keyless(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	marker, err := secrets.Encrypt("token", recipient)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "workspace", "defaults.yml"),
		[]byte("vars:\n  token: "+marker+"\n"), 0o644); err != nil {
		t.Fatalf("writing defaults.yml: %v", err)
	}
	writeAgeFile(t, root, "app/creds.age", recipient, "hello")

	keyfile, err := secrets.KeyfilePath(recipient)
	if err != nil {
		t.Fatalf("keyfile path: %v", err)
	}
	if err := os.Remove(keyfile); err != nil {
		t.Fatalf("removing keyfile: %v", err)
	}

	got := statusOf(t, flags)
	if got.Identity.Source != "" {
		t.Errorf("identity source = %q, want empty with no key installed", got.Identity.Source)
	}
	if got.Identity.Error == "" {
		t.Error("identity.error is empty with no key installed")
	}
	if len(got.Markers) != 1 || got.Markers[0].Reason != "no_identity" {
		t.Errorf("markers = %+v, want one no_identity row", got.Markers)
	}
	if len(got.Files) != 1 || got.Files[0].State != stateNotDecryptable || got.Files[0].Reason != "no_identity" {
		t.Errorf("files = %+v, want one not-decryptable no_identity row", got.Files)
	}
}

// A truncated DWE_AGE_KEY is a broken source, not a missing one. The CLI's
// identitySet.reason() is the mirror of config.identityReason; the two must
// agree, so the marker and file rows say invalid_identity here too. The header
// still reads "none (looked at …)" — that is Task 5's change.
func TestStatus_JSON_InvalidIdentity(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	marker, err := secrets.Encrypt("token", recipient)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "workspace", "defaults.yml"),
		[]byte("vars:\n  token: "+marker+"\n"), 0o644); err != nil {
		t.Fatalf("writing defaults.yml: %v", err)
	}
	writeAgeFile(t, root, "app/creds.age", recipient, "hello")

	keyfile, err := secrets.KeyfilePath(recipient)
	if err != nil {
		t.Fatalf("keyfile path: %v", err)
	}
	key, err := os.ReadFile(keyfile)
	if err != nil {
		t.Fatalf("reading keyfile: %v", err)
	}
	if err := os.Remove(keyfile); err != nil {
		t.Fatalf("removing keyfile: %v", err)
	}
	truncated := strings.TrimSpace(string(key))
	truncated = truncated[:len(truncated)-10]
	t.Setenv(secrets.EnvKey, truncated)

	got := statusOf(t, flags)
	if len(got.Markers) != 1 || got.Markers[0].Reason != config.ReasonInvalidIdentity {
		t.Errorf("markers = %+v, want one invalid_identity row", got.Markers)
	}
	if len(got.Files) != 1 || got.Files[0].State != stateNotDecryptable || got.Files[0].Reason != config.ReasonInvalidIdentity {
		t.Errorf("files = %+v, want one not-decryptable invalid_identity row", got.Files)
	}
	// The set-but-broken key must not be echoed anywhere in the payload.
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), truncated[len(truncated)-20:]) {
		t.Error("status JSON echoes the broken key text")
	}
	// The literal token name may appear in DWE-authored wording ("no
	// AGE-SECRET-KEY-1… key found"); a real key never can — one is 74 runes.
	if secretKeyRe.MatchString(string(raw)) {
		t.Error("status JSON contains key material")
	}
}

// secretKeyRe matches a whole age private key, so a leak assertion can tell one
// apart from the DWE-authored wording that merely names the token.
var secretKeyRe = regexp.MustCompile(`AGE-SECRET-KEY-1[AC-HJ-NP-Z02-9]{58}`)

// TestStatus_JSON_HalfRekeyed pins decision 11's recovery property end to end:
// after an interrupted rekey both recipients' values still report as readable,
// because a straggler keyfile opens the stale ones — and the one that ONLY a
// straggler opens is qualified with stale_key, because the config loader tries
// the configured identity alone and therefore still blocks on it.
func TestStatus_JSON_HalfRekeyed(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	oldRecipient := initProject(t, flags)

	oldMarker, err := secrets.Encrypt("old-value", oldRecipient)
	if err != nil {
		t.Fatalf("encrypt old: %v", err)
	}
	newID, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	if _, err := secrets.WriteKeyfile(newID); err != nil {
		t.Fatalf("writing new keyfile: %v", err)
	}
	if err := writeRecipient(cfgPath, newID.Recipient()); err != nil {
		t.Fatalf("swapping recipient: %v", err)
	}
	newMarker, err := secrets.Encrypt("new-value", newID.Recipient())
	if err != nil {
		t.Fatalf("encrypt new: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "workspace", "defaults.yml"),
		[]byte("vars:\n  a: "+newMarker+"\n  b: "+oldMarker+"\n"), 0o644); err != nil {
		t.Fatalf("writing defaults.yml: %v", err)
	}

	got := statusOf(t, flags)
	if got.Recipient != newID.Recipient() {
		t.Errorf("recipient = %q, want the new one %q", got.Recipient, newID.Recipient())
	}
	if len(got.Markers) != 2 {
		t.Fatalf("markers = %+v, want 2", got.Markers)
	}
	// vars.a was re-encrypted to the new (configured) recipient; vars.b is the
	// straggler the interrupted rekey left behind.
	wantReason := map[string]string{"vars.a": "", "vars.b": reasonStaleKey}
	for _, m := range got.Markers {
		if m.State != stateDecrypted {
			t.Errorf("%s: state = %q (%s), want %q", m.Path, m.State, m.Reason, stateDecrypted)
		}
		want, ok := wantReason[m.Path]
		if !ok {
			t.Errorf("unexpected marker path %q", m.Path)
			continue
		}
		if m.Reason != want {
			t.Errorf("%s: reason = %q, want %q", m.Path, m.Reason, want)
		}
	}

	// The report must agree with the loader: the straggler value is what
	// secrets.unresolved blocks on, so its row cannot render as "all good".
	view := statusView(got)
	for _, row := range view.Markers {
		if wantOK := row.Path == "vars.a"; row.OK != wantOK {
			t.Errorf("%s: row OK = %t, want %t", row.Path, row.OK, wantOK)
		}
	}
}

// TestStatus_JSON_NoSecrets pins that a project without secrets emits empty
// arrays rather than nulls, so a consumer can iterate unconditionally.
func TestStatus_JSON_NoSecrets(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}

	stdout, stderr, err := runSecrets(t, flags, "status")
	if err != nil {
		t.Fatalf("secrets status: %v (stderr: %s)", err, stderr)
	}
	var raw map[string]json.RawMessage
	if derr := json.Unmarshal([]byte(stdout), &raw); derr != nil {
		t.Fatalf("decoding status JSON %q: %v", stdout, derr)
	}
	for _, key := range []string{"markers", "files"} {
		if string(raw[key]) != "[]" {
			t.Errorf("%s = %s, want []", key, raw[key])
		}
	}
	if string(raw["recipient"]) != `""` {
		t.Errorf("recipient = %s, want \"\"", raw["recipient"])
	}
}

// TestStatus_Text_ReportsEveryRow pins the text surface: the report names each
// marker, each file and each reason, exits 0, and writes nothing to stderr.
func TestStatus_Text_ReportsEveryRow(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	foreign, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	mine, err := secrets.Encrypt("s3cret-token", recipient)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	theirs, err := secrets.Encrypt("not-for-me", foreign.Recipient())
	if err != nil {
		t.Fatalf("encrypt foreign: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "workspace", "defaults.yml"),
		[]byte("vars:\n  a_token: "+mine+"\n  b_token: "+theirs+"\n"), 0o644); err != nil {
		t.Fatalf("writing defaults.yml: %v", err)
	}
	writeAgeFile(t, root, "app/creds.json.age", recipient, `{"ok":true}`)

	stdout, stderr, err := runSecrets(t, flags, "status")
	if err != nil {
		t.Fatalf("secrets status: %v (stderr: %s)", err, stderr)
	}
	if stderr != "" {
		t.Errorf("status wrote to stderr: %q", stderr)
	}
	for _, want := range []string{
		recipient,
		"vars.a_token", "decrypted",
		"vars.b_token", "wrong_identity",
		"creds.json.age", stateDecryptable,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output is missing %q:\n%s", want, stdout)
		}
	}
}

// TestStatus_Text_StableAcrossRuns pins byte-stable row order: the inventory
// walks maps, so an unsorted traversal would surface as an intermittent diff.
func TestStatus_Text_StableAcrossRuns(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	var lines []string
	for _, name := range []string{"zeta", "alpha", "mid", "beta"} {
		marker, err := secrets.Encrypt(name+"-value", recipient)
		if err != nil {
			t.Fatalf("encrypt %s: %v", name, err)
		}
		lines = append(lines, "  "+name+": "+marker)
		writeAgeFile(t, root, "app/"+name+".age", recipient, name)
	}
	if err := os.WriteFile(filepath.Join(root, "workspace", "defaults.yml"),
		[]byte("vars:\n"+strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("writing defaults.yml: %v", err)
	}

	first, _, err := runSecrets(t, flags, "status")
	if err != nil {
		t.Fatalf("secrets status: %v", err)
	}
	for range 3 {
		next, _, nerr := runSecrets(t, flags, "status")
		if nerr != nil {
			t.Fatalf("secrets status: %v", nerr)
		}
		if next != first {
			t.Fatalf("status output differs between runs:\nfirst:\n%s\nlater:\n%s", first, next)
		}
	}
	// And the order is the sorted one, not merely repeatable.
	got := statusOf(t, flags)
	var paths []string
	for _, m := range got.Markers {
		paths = append(paths, m.Path)
	}
	want := []string{"vars.alpha", "vars.beta", "vars.mid", "vars.zeta"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Errorf("marker order = %v, want %v", paths, want)
	}
}

// TestStatus_NeverPrintsKeyMaterial is the negative pin: neither surface may
// leak the identity or the ciphertext it reports on.
func TestStatus_NeverPrintsKeyMaterial(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	const plaintext = "s3cret-token-value"
	marker, err := secrets.Encrypt(plaintext, recipient)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "workspace", "defaults.yml"),
		[]byte("vars:\n  token: "+marker+"\n"), 0o644); err != nil {
		t.Fatalf("writing defaults.yml: %v", err)
	}

	text, textErr, err := runSecrets(t, flags, "status")
	if err != nil {
		t.Fatalf("secrets status: %v", err)
	}
	jsonFlags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}
	raw, rawErr, err := runSecrets(t, jsonFlags, "status")
	if err != nil {
		t.Fatalf("secrets status --output json: %v", err)
	}

	all := text + textErr + raw + rawErr
	for _, forbidden := range []string{"AGE-SECRET-KEY-", secrets.MarkerPrefix, plaintext} {
		if strings.Contains(all, forbidden) {
			t.Errorf("status output contains %q:\n%s", forbidden, all)
		}
	}
}

// TestStatus_ExitsZeroWithUnresolvedSecrets pins that status is a report, not a
// gate: it is the command a blocked developer runs, so it must not itself fail.
func TestStatus_ExitsZeroWithUnresolvedSecrets(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	recipient := initProject(t, flags)

	marker, err := secrets.Encrypt("token", recipient)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "workspace", "defaults.yml"),
		[]byte("vars:\n  token: "+marker+"\n"), 0o644); err != nil {
		t.Fatalf("writing defaults.yml: %v", err)
	}
	keyfile, err := secrets.KeyfilePath(recipient)
	if err != nil {
		t.Fatalf("keyfile path: %v", err)
	}
	if err := os.Remove(keyfile); err != nil {
		t.Fatalf("removing keyfile: %v", err)
	}

	stdout, _, err := runSecrets(t, flags, "status")
	if err != nil {
		t.Fatalf("secrets status returned an error on an unresolved project: %v", err)
	}
	if !strings.Contains(stdout, "no_identity") {
		t.Errorf("output does not name the reason:\n%s", stdout)
	}
}

// TestIdentityDisplay names each source. The env cases cannot be reached
// through the fixture without a real keyfile, so the wording is pinned directly.
func TestIdentityDisplay(t *testing.T) {
	const recipient = "age1qyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqs3fgh2p"
	tests := []struct {
		name string
		in   statusJSON
		want string
	}{
		{"env", statusJSON{Recipient: recipient, Identity: identityJSON{Source: string(secrets.SourceEnv)}}, "$" + secrets.EnvKey},
		{"env-file", statusJSON{Recipient: recipient, Identity: identityJSON{Source: string(secrets.SourceEnvFile)}}, "$" + secrets.EnvKeyFile},
		{"keyfile", statusJSON{Recipient: recipient, Identity: identityJSON{Source: string(secrets.SourceKeyfile), Keyfile: "/k/age1.key"}}, "keyfile (/k/age1.key)"},
		{"keyfile without path", statusJSON{Recipient: recipient, Identity: identityJSON{Source: string(secrets.SourceKeyfile)}}, "keyfile"},
		{"no recipient", statusJSON{}, "none"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := identityDisplay(tc.in); got != tc.want {
				t.Errorf("identityDisplay = %q, want %q", got, tc.want)
			}
		})
	}

	// The keyless case names every place the lookup looked, so the fix does not
	// depend on knowing the precedence rules.
	got := identityDisplay(statusJSON{Recipient: recipient})
	for _, want := range []string{"none", secrets.EnvKey, secrets.EnvKeyFile, ".key"} {
		if !strings.Contains(got, want) {
			t.Errorf("keyless identity line %q is missing %q", got, want)
		}
	}
}

// TestStatusView_OKFlags pins the state → OK mapping the renderer colors from:
// only the two readable states are OK.
func TestStatusView_OKFlags(t *testing.T) {
	d := statusJSON{
		Markers: []markerRow{
			{Path: "vars.a", State: stateDecrypted},
			{Path: "vars.b", State: stateUnresolved, Reason: "no_identity"},
		},
		Files: []fileRow{
			{File: "a.age", State: stateDecryptable},
			{File: "b.age", State: stateNotDecryptable, Reason: "corrupt"},
		},
	}
	v := statusView(d)
	if !v.Markers[0].OK || v.Markers[1].OK {
		t.Errorf("marker OK flags = %v/%v, want true/false", v.Markers[0].OK, v.Markers[1].OK)
	}
	if !v.Files[0].OK || v.Files[1].OK {
		t.Errorf("file OK flags = %v/%v, want true/false", v.Files[0].OK, v.Files[1].OK)
	}
	if v.Markers[1].Reason != "no_identity" || v.Files[1].Reason != "corrupt" {
		t.Errorf("reasons dropped in the view: %+v / %+v", v.Markers[1], v.Files[1])
	}
}

// TestStatus_InvalidConfigIsTyped pins that an unloadable layer set still comes
// back as a machine-readable envelope rather than a bare error.
func TestStatus_InvalidConfigIsTyped(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	// secrets: is legal in workspace.yml only; defaults.yml must be refused by
	// the same ValidateLayerRoots pass LoadConfig runs.
	if err := os.WriteFile(filepath.Join(root, "workspace", "defaults.yml"),
		[]byte("secrets:\n  recipient: age1nope\n"), 0o644); err != nil {
		t.Fatalf("writing defaults.yml: %v", err)
	}

	_, _, err := runSecrets(t, flags, "status")
	if err == nil {
		t.Fatal("secrets status succeeded on an invalid layer set")
	}
	if code := codedError(t, err).Code; code != "project_invalid_config" {
		t.Errorf("error code = %q, want project_invalid_config", code)
	}
}
