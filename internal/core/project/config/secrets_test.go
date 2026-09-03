package config

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/shared/secrets"
	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

// traceLine emits s through the real trace sink and returns what a -v run
// would print — the honest way to observe the process-global redactor.
func traceLine(t *testing.T, s string) string {
	t.Helper()
	var buf bytes.Buffer
	trace.Configure(&buf, trace.LevelVerbose)
	t.Cleanup(func() { trace.Configure(nil, trace.LevelOff) })
	trace.Decision(context.Background(), "%s", s)
	return strings.TrimRight(buf.String(), "\n")
}

// newTestIdentity mints an identity, installs it via DWE_AGE_KEY and isolates
// HOME so no test ever reads the developer's ~/.config/dwe/keys.
func newTestIdentity(t *testing.T) secrets.Identity {
	t.Helper()
	id, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv(secrets.EnvKeyFile, "")
	t.Setenv(secrets.EnvKey, id.Export())
	return id
}

// hideIdentity removes every identity source, so a marker is unresolvable.
func hideIdentity(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(secrets.EnvKey, "")
	t.Setenv(secrets.EnvKeyFile, "")
}

func mustEncrypt(t *testing.T, plain, recipient string) string {
	t.Helper()
	marker, err := secrets.Encrypt(plain, recipient)
	if err != nil {
		t.Fatalf("encrypt %q: %v", plain, err)
	}
	return marker
}

func TestLoadLayersWithSecrets_decryptsEveryLayer(t *testing.T) {
	id := newTestIdentity(t)
	r := id.Recipient()

	ws := writeLayerFixture(t,
		"secrets:\n  recipient: "+r+"\nvars:\n  ws_token: "+mustEncrypt(t, "workspace-plain", r)+"\n",
		"vars:\n  defaults_token: "+mustEncrypt(t, "defaults-plain", r)+"\n",
		"vars:\n  local_token: "+mustEncrypt(t, "local-plain", r)+"\n",
	)

	layers, state, err := LoadLayersWithSecrets(ws)
	if err != nil {
		t.Fatalf("LoadLayersWithSecrets: %v", err)
	}
	if state.Recipient != r {
		t.Errorf("state.Recipient = %q, want %q", state.Recipient, r)
	}
	if state.IdentitySource != string(secrets.SourceEnv) {
		t.Errorf("state.IdentitySource = %q, want %q", state.IdentitySource, secrets.SourceEnv)
	}
	if len(state.Unresolved) != 0 {
		t.Fatalf("want no unresolved markers, got %+v", state.Unresolved)
	}
	if len(state.Decrypted) != 3 {
		t.Fatalf("want 3 decrypted refs, got %d (%+v)", len(state.Decrypted), state.Decrypted)
	}

	want := map[string]string{
		"vars.ws_token":       "workspace-plain",
		"vars.defaults_token": "defaults-plain",
		"vars.local_token":    "local-plain",
	}
	for _, layer := range layers {
		for path, expect := range want {
			v, ok := ResolvePath(layer.Data, path)
			if !ok {
				continue
			}
			if v != expect {
				t.Errorf("%s at %s = %v, want %q", layer.Path, path, v, expect)
			}
			delete(want, path)
		}
	}
	if len(want) != 0 {
		t.Errorf("paths never seen in any layer: %v", want)
	}

	// Decrypted refs are ordered by layer, then by path within a layer.
	var order []string
	for _, ref := range state.Decrypted {
		order = append(order, ref.Layer+"#"+ref.Path)
	}
	wantOrder := []string{
		layers[0].Path + "#vars.ws_token",
		layers[1].Path + "#vars.defaults_token",
		layers[2].Path + "#vars.local_token",
	}
	if strings.Join(order, ",") != strings.Join(wantOrder, ",") {
		t.Errorf("Decrypted order = %v, want %v", order, wantOrder)
	}
}

func TestLoadLayersWithSecrets_rawLayersStayCiphertext(t *testing.T) {
	id := newTestIdentity(t)
	marker := mustEncrypt(t, "super-secret", id.Recipient())
	ws := writeLayerFixture(t, "secrets:\n  recipient: "+id.Recipient()+"\nvars:\n  tok: "+marker+"\n", "", "")

	if _, _, err := LoadLayersWithSecrets(ws); err != nil {
		t.Fatalf("LoadLayersWithSecrets: %v", err)
	}
	raw, err := LoadRawLayers(ws)
	if err != nil {
		t.Fatalf("LoadRawLayers: %v", err)
	}
	got, _ := ResolvePath(raw[0].Data, "vars.tok")
	if got != marker {
		t.Errorf("raw vars.tok = %v, want the ciphertext marker", got)
	}
}

func TestLoadLayersWithSecrets_unresolvedReasons(t *testing.T) {
	id := newTestIdentity(t)
	r := id.Recipient()
	marker := mustEncrypt(t, "plain", r)

	other, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	foreign := mustEncrypt(t, "plain", other.Recipient())

	tests := []struct {
		name       string
		setup      func(t *testing.T)
		value      string
		wantReason string
	}{
		{
			name:       "no identity",
			setup:      hideIdentity,
			value:      marker,
			wantReason: ReasonNoIdentity,
		},
		{
			name: "wrong identity",
			setup: func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				t.Setenv(secrets.EnvKeyFile, "")
				t.Setenv(secrets.EnvKey, other.Export())
			},
			value:      marker,
			wantReason: ReasonWrongIdentity,
		},
		{
			name: "encrypted to another recipient",
			setup: func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				t.Setenv(secrets.EnvKeyFile, "")
				t.Setenv(secrets.EnvKey, id.Export())
			},
			value:      foreign,
			wantReason: ReasonWrongIdentity,
		},
		{
			name: "corrupt payload",
			setup: func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				t.Setenv(secrets.EnvKeyFile, "")
				t.Setenv(secrets.EnvKey, id.Export())
			},
			value:      secrets.MarkerPrefix + "bm90LWFuLWFnZS1maWxl]",
			wantReason: ReasonCorrupt,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			ws := writeLayerFixture(t, "secrets:\n  recipient: "+r+"\nvars:\n  tok: "+tc.value+"\n", "", "")
			layers, state, err := LoadLayersWithSecrets(ws)
			if err != nil {
				t.Fatalf("LoadLayersWithSecrets: %v", err)
			}
			if len(state.Decrypted) != 0 {
				t.Errorf("want nothing decrypted, got %+v", state.Decrypted)
			}
			if len(state.Unresolved) != 1 {
				t.Fatalf("want 1 unresolved, got %+v", state.Unresolved)
			}
			u := state.Unresolved[0]
			if u.Reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", u.Reason, tc.wantReason)
			}
			if u.Layer != ws || u.Path != "vars.tok" {
				t.Errorf("ref = %+v, want layer %q path vars.tok", u.SecretRef, ws)
			}
			// The marker stays literal so nothing downstream renders "".
			got, _ := ResolvePath(layers[0].Data, "vars.tok")
			if got != tc.value {
				t.Errorf("vars.tok = %v, want the untouched marker", got)
			}
			if !state.UnresolvedAt(ws, "vars.tok") {
				t.Error("UnresolvedAt did not find the recorded marker")
			}
		})
	}
}

// An identity that cannot be LOADED is never "the payload is damaged": that
// reason sends the developer to `dwe secrets rekey` over what is a local key
// problem. LoadIdentity deliberately returns a permission error unwrapped, so
// the loader needs its own mapper — and a source that WAS present but holds no
// key is invalid_identity, not no_identity: the fix is repairing that source.
func TestLoadLayersWithSecrets_identityFailureIsNeverCorrupt(t *testing.T) {
	id, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	marker := mustEncrypt(t, "plain", id.Recipient())
	truncated := id.Export()[:len(id.Export())-10]

	tests := []struct {
		name       string
		setup      func(t *testing.T)
		wantReason string
		wantSource secrets.Source
	}{
		{
			name:       "malformed keyfile",
			setup:      func(t *testing.T) { setEnvKeyFile(t, "not a key at all\n") },
			wantReason: ReasonInvalidIdentity,
			wantSource: secrets.SourceEnvFile,
		},
		{
			name:       "empty keyfile",
			setup:      func(t *testing.T) { setEnvKeyFile(t, "") },
			wantReason: ReasonInvalidIdentity,
			wantSource: secrets.SourceEnvFile,
		},
		{
			name: "truncated DWE_AGE_KEY",
			setup: func(t *testing.T) {
				hideIdentity(t)
				t.Setenv(secrets.EnvKey, truncated)
			},
			wantReason: ReasonInvalidIdentity,
			wantSource: secrets.SourceEnv,
		},
		{
			name: "env key file points nowhere",
			setup: func(t *testing.T) {
				hideIdentity(t)
				t.Setenv(secrets.EnvKeyFile, filepath.Join(t.TempDir(), "absent.key"))
			},
			wantReason: ReasonNoIdentity,
			wantSource: secrets.SourceEnvFile,
		},
		{
			name:       "no identity at all",
			setup:      hideIdentity,
			wantReason: ReasonNoIdentity,
			wantSource: secrets.SourceKeyfile,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)

			ws := writeLayerFixture(t,
				"secrets:\n  recipient: "+id.Recipient()+"\nvars:\n  tok: "+marker+"\n  tok2: "+marker+"\n", "", "")
			_, state, err := LoadLayersWithSecrets(ws)
			if err != nil {
				t.Fatalf("LoadLayersWithSecrets: %v", err)
			}
			if len(state.Unresolved) != 2 {
				t.Fatalf("want 2 unresolved, got %+v", state.Unresolved)
			}
			// Every marker carries the same reason: the identity failed once.
			for _, u := range state.Unresolved {
				if u.Reason != tc.wantReason {
					t.Errorf("%s: reason = %q, want %q", u.Path, u.Reason, tc.wantReason)
				}
			}
			// The CONSULTED source is recorded on failure too — it is what tells
			// the reader which source to repair.
			if state.IdentitySource != string(tc.wantSource) {
				t.Errorf("identity source = %q, want %q", state.IdentitySource, tc.wantSource)
			}
		})
	}
}

// setEnvKeyFile hides every identity source, then points DWE_AGE_KEY_FILE at a
// keyfile holding content.
func setEnvKeyFile(t *testing.T, content string) {
	t.Helper()
	hideIdentity(t)
	path := filepath.Join(t.TempDir(), "broken.key")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing keyfile: %v", err)
	}
	t.Setenv(secrets.EnvKeyFile, path)
}

func TestLoadLayersWithSecrets_sequenceIndexPaths(t *testing.T) {
	hideIdentity(t)
	id, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	marker := mustEncrypt(t, "list-secret", id.Recipient())
	ws := writeLayerFixture(t,
		"secrets:\n  recipient: "+id.Recipient()+"\nvars:\n  tokens:\n    - plain\n    - "+marker+"\n", "", "")

	_, state, err := LoadLayersWithSecrets(ws)
	if err != nil {
		t.Fatalf("LoadLayersWithSecrets: %v", err)
	}
	if len(state.Unresolved) != 1 || state.Unresolved[0].Path != "vars.tokens.1" {
		t.Fatalf("want one unresolved at vars.tokens.1, got %+v", state.Unresolved)
	}
}

// A single non-string key demotes the whole mapping to map[any]any in yaml.v3.
// The walk must still find markers there: the node-based rewriter behind
// `dwe secrets rekey` does, and a disagreement leaves rekey unable to converge.
func TestLoadLayersWithSecrets_nonStringMapKeys(t *testing.T) {
	id := newTestIdentity(t)
	r := id.Recipient()
	ws := writeLayerFixture(t,
		"secrets:\n  recipient: "+r+"\nvars:\n  ports:\n    8080: "+mustEncrypt(t, "port-secret", r)+
			"\n    name: "+mustEncrypt(t, "name-secret", r)+"\n", "", "")

	layers, state, err := LoadLayersWithSecrets(ws)
	if err != nil {
		t.Fatalf("LoadLayersWithSecrets: %v", err)
	}
	if len(state.Unresolved) != 0 {
		t.Fatalf("want no unresolved markers, got %+v", state.Unresolved)
	}
	gotPaths := []string{}
	for _, ref := range state.Decrypted {
		gotPaths = append(gotPaths, ref.Path)
	}
	if want := []string{"vars.ports.8080", "vars.ports.name"}; !slices.Equal(gotPaths, want) {
		t.Fatalf("decrypted paths = %v, want %v", gotPaths, want)
	}

	ports, _ := layers[0].Data["vars"].(map[string]any)["ports"].(map[any]any)
	if got := ports[8080]; got != "port-secret" {
		t.Errorf("vars.ports.8080 = %v, want the plaintext", got)
	}
	if got := ports["name"]; got != "name-secret" {
		t.Errorf("vars.ports.name = %v, want the plaintext", got)
	}

	// The raw view must still be ciphertext: the decrypt pass runs on a deep
	// copy, and map[any]any has to be cloned like every other container.
	raw, err := LoadRawLayers(ws)
	if err != nil {
		t.Fatalf("LoadRawLayers: %v", err)
	}
	rawPorts, _ := raw[0].Data["vars"].(map[string]any)["ports"].(map[any]any)
	if got, _ := rawPorts[8080].(string); !secrets.IsMarker(got) {
		t.Errorf("raw vars.ports.8080 = %v, want the ciphertext marker", rawPorts[8080])
	}
}

// CollectMarkers feeds `dwe secrets status` and the rekey read-only pass; it
// must see exactly what the decrypt pass and ReplaceScalars see.
func TestCollectMarkers_nonStringMapKeys(t *testing.T) {
	hideIdentity(t)
	id, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	marker := mustEncrypt(t, "port-secret", id.Recipient())
	ws := writeLayerFixture(t,
		"secrets:\n  recipient: "+id.Recipient()+"\nvars:\n  ports:\n    8080: "+marker+"\n", "", "")

	raw, err := LoadRawLayers(ws)
	if err != nil {
		t.Fatalf("LoadRawLayers: %v", err)
	}
	found := CollectMarkers(raw)
	if len(found) != 1 || found[0].Path != "vars.ports.8080" || found[0].Value != marker {
		t.Fatalf("CollectMarkers = %+v, want one marker at vars.ports.8080", found)
	}
}

func TestLoadLayersWithSecrets_embeddedMarkerTextIsData(t *testing.T) {
	id := newTestIdentity(t)
	// A string that merely CONTAINS "ENC[" is data — only a whole-scalar
	// marker is a secret.
	text := "see ENC[age:abc] in the docs"
	ws := writeLayerFixture(t, "secrets:\n  recipient: "+id.Recipient()+"\nvars:\n  note: \""+text+"\"\n", "", "")

	layers, state, err := LoadLayersWithSecrets(ws)
	if err != nil {
		t.Fatalf("LoadLayersWithSecrets: %v", err)
	}
	if state.HasSecrets() {
		t.Errorf("want no secrets recorded, got %+v", state)
	}
	if got, _ := ResolvePath(layers[0].Data, "vars.note"); got != text {
		t.Errorf("vars.note = %v, want it untouched", got)
	}
}

func TestLoadLayersWithSecrets_noSecretsEmptyState(t *testing.T) {
	hideIdentity(t)
	ws := writeLayerFixture(t, "vars:\n  a: 1\n", "", "")
	_, state, err := LoadLayersWithSecrets(ws)
	if err != nil {
		t.Fatalf("LoadLayersWithSecrets: %v", err)
	}
	if state.HasSecrets() || state.Recipient != "" || state.IdentitySource != "" {
		t.Errorf("want a zero SecretsState, got %+v", state)
	}
}

func TestLoadConfig_decryptsMarkers(t *testing.T) {
	id := newTestIdentity(t)
	r := id.Recipient()
	ws := writeLayerFixture(t,
		"project:\n  name: demo\nsecrets:\n  recipient: "+r+"\nvars:\n  token: "+mustEncrypt(t, "bot-token-value", r)+"\n",
		"", "")

	cfg, err := LoadConfig(ws)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.Vars["token"]; got != "bot-token-value" {
		t.Errorf("cfg.Vars[token] = %v, want the plaintext", got)
	}
	if got, _ := ResolvePath(cfg.Raw, "vars.token"); got != "bot-token-value" {
		t.Errorf("Raw vars.token = %v, want the plaintext", got)
	}
	if cfg.Secrets == nil || cfg.Secrets.Recipient != r {
		t.Errorf("cfg.Secrets = %+v, want recipient %q", cfg.Secrets, r)
	}
	if SecretsRecipient(cfg) != r {
		t.Errorf("SecretsRecipient = %q, want %q", SecretsRecipient(cfg), r)
	}
	if len(cfg.SecretsState.Decrypted) != 1 {
		t.Errorf("SecretsState.Decrypted = %+v, want one entry", cfg.SecretsState.Decrypted)
	}
}

func TestLoadConfigSanitized_keepsMarkers(t *testing.T) {
	id := newTestIdentity(t)
	r := id.Recipient()
	nameMarker := mustEncrypt(t, "secret-name", r)
	varMarker := mustEncrypt(t, "secret-var-value", r)
	ws := writeLayerFixture(t,
		"project:\n  name: "+nameMarker+"\nsecrets:\n  recipient: "+r+"\nvars:\n  token: "+varMarker+"\n",
		"", "")

	trace.ResetRedaction()
	t.Cleanup(trace.ResetRedaction)

	sanitized, err := LoadConfigSanitized(ws)
	if err != nil {
		t.Fatalf("LoadConfigSanitized: %v", err)
	}
	if sanitized.Vars["token"] != varMarker {
		t.Errorf("sanitized Vars[token] = %v, want the marker", sanitized.Vars["token"])
	}
	if got, _ := ResolvePath(sanitized.Raw, "vars.token"); got != varMarker {
		t.Errorf("sanitized Raw vars.token = %v, want the marker", got)
	}
	if sanitized.Project.Name != nameMarker {
		t.Errorf("sanitized Project.Name = %q, want the marker", sanitized.Project.Name)
	}
	if sanitized.SecretsState.HasSecrets() {
		t.Errorf("sanitized SecretsState = %+v, want empty", sanitized.SecretsState)
	}
	// Nothing was decrypted, so nothing may have been registered for redaction.
	if got := traceLine(t, "secret-var-value"); got != "secret-var-value" {
		t.Errorf("LoadConfigSanitized registered a redaction: %q", got)
	}

	// The real loader decrypts the same file.
	cfg, err := LoadConfig(ws)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Vars["token"] != "secret-var-value" || cfg.Project.Name != "secret-name" {
		t.Fatalf("LoadConfig did not decrypt: vars=%v name=%q", cfg.Vars["token"], cfg.Project.Name)
	}
	if got := traceLine(t, "value is secret-var-value here"); strings.Contains(got, "secret-var-value") {
		t.Errorf("LoadConfig did not register the plaintext with trace: %q", got)
	}
}

func TestValidateLayerRoots_secretsRules(t *testing.T) {
	id, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	r := id.Recipient()

	tests := []struct {
		name     string
		ws       string
		defaults string
		local    string
		wantErr  string // substring; the offending file is appended by the test
		wantFile string // "ws" | "defaults" | "local"
	}{
		{
			name:     "secrets in defaults.yml",
			ws:       "vars:\n  a: 1\n",
			defaults: "secrets:\n  recipient: " + r + "\n",
			wantErr:  "secrets: is only valid in workspace.yml",
			wantFile: "defaults",
		},
		{
			name:     "secrets in local.yml",
			ws:       "vars:\n  a: 1\n",
			local:    "secrets:\n  recipient: " + r + "\n",
			wantErr:  "secrets: is only valid in workspace.yml",
			wantFile: "local",
		},
		{
			name:     "non-mapping secrets",
			ws:       "secrets: nope\n",
			wantErr:  "secrets: must be a mapping",
			wantFile: "ws",
		},
		{
			name:     "non-string recipient",
			ws:       "secrets:\n  recipient:\n    - a\n",
			wantErr:  "secrets.recipient must be a string",
			wantFile: "ws",
		},
		{
			name:     "malformed recipient",
			ws:       "secrets:\n  recipient: not-an-age-key\n",
			wantErr:  "secrets.recipient is malformed",
			wantFile: "ws",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hideIdentity(t)
			ws := writeLayerFixture(t, tc.ws, tc.defaults, tc.local)
			wantPath := ws
			switch tc.wantFile {
			case "defaults":
				wantPath = filepath.Join(filepath.Dir(ws), "workspace", "defaults.yml")
			case "local":
				wantPath = LocalLayerPath(ws)
			}

			// All three entry points must reject identically.
			raw, err := LoadRawLayers(ws)
			if err != nil {
				t.Fatalf("LoadRawLayers: %v", err)
			}
			for name, err := range map[string]error{
				"ValidateLayerRoots": ValidateLayerRoots(raw),
				"LoadConfig":         secondErr(LoadConfig(ws)),
				"ResolveLayeredPath": resolveErr(ws),
			} {
				if err == nil {
					t.Errorf("%s: want an error, got nil", name)
					continue
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("%s: error %q does not contain %q", name, err, tc.wantErr)
				}
				if !strings.Contains(err.Error(), wantPath) {
					t.Errorf("%s: error %q does not name %s", name, err, wantPath)
				}
			}
		})
	}
}

func secondErr(_ *DweConfig, err error) error { return err }

func resolveErr(ws string) error {
	_, err := ResolveLayeredPath(ws, "vars.a")
	return err
}

func TestValidateLayerRoots_secretsBlockAccepted(t *testing.T) {
	id, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	for _, body := range []string{
		"secrets:\n",                                    // present but null
		"secrets: {}\n",                                 // empty mapping
		"secrets:\n  recipient:\n",                      // present but null recipient
		"secrets:\n  recipient: \"\"\n",                 // empty recipient
		"secrets:\n  recipient: " + id.Recipient() + "", // real one
	} {
		hideIdentity(t)
		ws := writeLayerFixture(t, body+"\nvars:\n  a: 1\n", "", "")
		raw, err := LoadRawLayers(ws)
		if err != nil {
			t.Fatalf("LoadRawLayers(%q): %v", body, err)
		}
		if err := ValidateLayerRoots(raw); err != nil {
			t.Errorf("ValidateLayerRoots(%q) = %v, want nil", body, err)
		}
	}
}

func TestResolveLayeredPath_agreesWithLoadConfig(t *testing.T) {
	id := newTestIdentity(t)
	r := id.Recipient()
	ws := writeLayerFixture(t,
		"secrets:\n  recipient: "+r+"\nvars:\n  token: "+mustEncrypt(t, "shared-plaintext", r)+"\n", "", "")

	lv, err := ResolveLayeredPath(ws, "vars.token")
	if err != nil {
		t.Fatalf("ResolveLayeredPath: %v", err)
	}
	if lv.Current != "shared-plaintext" {
		t.Errorf("ResolveLayeredPath current = %v, want the plaintext", lv.Current)
	}
	cfg, err := LoadConfig(ws)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Vars["token"] != lv.Current {
		t.Errorf("LoadConfig (%v) and ResolveLayeredPath (%v) disagree", cfg.Vars["token"], lv.Current)
	}
}

func TestResolveLayeredPath_unresolvedMarkerStaysLiteral(t *testing.T) {
	id, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	hideIdentity(t)
	marker := mustEncrypt(t, "unreachable", id.Recipient())
	ws := writeLayerFixture(t,
		"secrets:\n  recipient: "+id.Recipient()+"\nvars:\n  token: "+marker+"\n", "", "")

	lv, err := ResolveLayeredPath(ws, "vars.token")
	if err != nil {
		t.Fatalf("ResolveLayeredPath: %v", err)
	}
	if lv.Current != marker {
		t.Errorf("current = %v, want the untouched marker", lv.Current)
	}
	cfg, err := LoadConfig(ws)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Vars["token"] != marker {
		t.Errorf("cfg.Vars[token] = %v, want the untouched marker", cfg.Vars["token"])
	}
	if len(cfg.SecretsState.Unresolved) != 1 || cfg.SecretsState.Unresolved[0].Reason != ReasonNoIdentity {
		t.Errorf("SecretsState.Unresolved = %+v, want one no_identity entry", cfg.SecretsState.Unresolved)
	}
}

// TestLoadConfig_noSecretsBlockUnchanged pins the backward-compatibility
// promise: a project with neither secrets: nor markers loads exactly as before
// and reports an empty state.
func TestLoadConfig_noSecretsBlockUnchanged(t *testing.T) {
	hideIdentity(t)
	ws := writeLayerFixture(t, "project:\n  name: demo\nvars:\n  a: 1\n", "", "")
	cfg, err := LoadConfig(ws)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Secrets != nil {
		t.Errorf("cfg.Secrets = %+v, want nil", cfg.Secrets)
	}
	if cfg.SecretsState.HasSecrets() {
		t.Errorf("SecretsState = %+v, want empty", cfg.SecretsState)
	}
	if SecretsRecipient(cfg) != "" || SecretsRecipient(nil) != "" {
		t.Error("SecretsRecipient should be empty without a secrets: block")
	}
}

// TestSecretsRecipientIsAllowedRootKey guards the strict-root allowlist: a
// project declaring secrets: must load.
func TestSecretsRecipientIsAllowedRootKey(t *testing.T) {
	if _, ok := allowedRootKeySet["secrets"]; !ok {
		t.Fatal("secrets missing from allowedRootKeys")
	}
}

func TestLoadConfig_encryptedProjectNameDecrypts(t *testing.T) {
	id := newTestIdentity(t)
	r := id.Recipient()
	ws := writeLayerFixture(t,
		"project:\n  name: "+mustEncrypt(t, "hidden-project", r)+"\nsecrets:\n  recipient: "+r+"\n", "", "")

	cfg, err := LoadConfig(ws)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Project.Name != "hidden-project" {
		t.Errorf("cfg.Project.Name = %q, want the decrypted value", cfg.Project.Name)
	}
	// The decrypt pass is path-agnostic: it does not know about vars:.
	if len(cfg.SecretsState.Decrypted) != 1 || cfg.SecretsState.Decrypted[0].Path != "project.name" {
		t.Errorf("Decrypted = %+v, want project.name", cfg.SecretsState.Decrypted)
	}
}

// CollectMarkers is the single marker inventory shared by the secrets
// validators, `dwe secrets status` and rekey: it must list every marker in
// layer order then path order, and carry the ciphertext as written.
func TestCollectMarkers_orderAndValues(t *testing.T) {
	id := newTestIdentity(t)
	r := id.Recipient()
	wsToken := mustEncrypt(t, "ws-token", r)
	defB := mustEncrypt(t, "def-b", r)
	defA := mustEncrypt(t, "def-a", r)
	localItem := mustEncrypt(t, "list-item", r)

	ws := writeLayerFixture(t,
		"secrets:\n  recipient: "+r+"\nvars:\n  token: "+wsToken+"\n",
		"vars:\n  b: "+defB+"\n  a: "+defA+"\n  plain: not-a-secret\n",
		"vars:\n  tokens:\n    - "+localItem+"\n    - plain\n",
	)

	raw, err := LoadRawLayers(ws)
	if err != nil {
		t.Fatalf("LoadRawLayers: %v", err)
	}
	markers := CollectMarkers(raw)

	var got []string
	for _, m := range markers {
		got = append(got, filepath.Base(m.Layer)+":"+m.Path)
	}
	want := []string{
		"workspace.yml:vars.token",
		"defaults.yml:vars.a",
		"defaults.yml:vars.b",
		"local.yml:vars.tokens.0",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("marker order = %v, want %v", got, want)
	}
	if markers[0].Value != wsToken {
		t.Errorf("marker value = %q, want the ciphertext as written", markers[0].Value)
	}
}

// The decrypt pass runs on a copy, so a decrypting load leaves the raw layers
// (and therefore the inventory) untouched.
func TestCollectMarkers_emptyOnDecryptedLayers(t *testing.T) {
	id := newTestIdentity(t)
	ws := writeLayerFixture(t,
		"secrets:\n  recipient: "+id.Recipient()+"\nvars:\n  token: "+mustEncrypt(t, "v", id.Recipient())+"\n", "", "")

	decrypted, err := LoadLayers(ws)
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	if got := CollectMarkers(decrypted); len(got) != 0 {
		t.Errorf("CollectMarkers on decrypted layers = %v, want none", got)
	}
	raw, err := LoadRawLayers(ws)
	if err != nil {
		t.Fatalf("LoadRawLayers: %v", err)
	}
	if got := CollectMarkers(raw); len(got) != 1 {
		t.Errorf("CollectMarkers on raw layers = %d markers, want 1", len(got))
	}
}

func TestRecipientFromLayers(t *testing.T) {
	id := newTestIdentity(t)

	ws := writeLayerFixture(t, "secrets:\n  recipient: "+id.Recipient()+"\n", "", "")
	raw, err := LoadRawLayers(ws)
	if err != nil {
		t.Fatalf("LoadRawLayers: %v", err)
	}
	if got := RecipientFromLayers(raw); got != id.Recipient() {
		t.Errorf("RecipientFromLayers = %q, want %q", got, id.Recipient())
	}

	// No secrets: block at all.
	plain := writeLayerFixture(t, "vars:\n  a: 1\n", "", "")
	rawPlain, err := LoadRawLayers(plain)
	if err != nil {
		t.Fatalf("LoadRawLayers: %v", err)
	}
	if got := RecipientFromLayers(rawPlain); got != "" {
		t.Errorf("RecipientFromLayers = %q, want empty", got)
	}

	// A malformed value is returned as written — validity is the caller's
	// question, and the secrets.recipient validator is the one that asks it.
	bad := writeLayerFixture(t, "secrets:\n  recipient: age1-nope\n", "", "")
	rawBad, err := LoadRawLayers(bad)
	if err != nil {
		t.Fatalf("LoadRawLayers: %v", err)
	}
	if got := RecipientFromLayers(rawBad); got != "age1-nope" {
		t.Errorf("RecipientFromLayers = %q, want the raw value", got)
	}
}

// TestLoadConfig_redactsTheSecretNotItsShadow pins that the plaintexts are
// harvested BEFORE assembleConfig's deepMerge. deepMerge stores the first
// layer's nested maps into the merged map by reference and then mutates them in
// place, so a post-merge re-walk of workspace.yml reads back whatever local.yml
// shadowed the marker with — which used to register the unrelated override for
// global redaction and leave the secret itself unregistered.
func TestLoadConfig_redactsTheSecretNotItsShadow(t *testing.T) {
	id := newTestIdentity(t)
	r := id.Recipient()
	trace.ResetRedaction()
	t.Cleanup(trace.ResetRedaction)

	ws := writeLayerFixture(t,
		"secrets:\n  recipient: "+r+"\nvars:\n"+
			"  shadowed: "+mustEncrypt(t, "shadowed-secret", r)+"\n"+
			"  live: "+mustEncrypt(t, "live-secret", r)+"\n",
		"",
		"vars:\n  shadowed: an-override-value\n")

	if _, err := LoadConfig(ws); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// The secret that lost the merge is still a secret on this machine, and the
	// one that won must be redacted.
	for _, plain := range []string{"shadowed-secret", "live-secret"} {
		if got := traceLine(t, plain); strings.Contains(got, plain) {
			t.Errorf("trace of %q = %q, want it redacted", plain, got)
		}
	}
	// The plaintext override is NOT a secret and must not be redacted — doing so
	// replaces every unrelated occurrence of a common word in -v output.
	if got := traceLine(t, "an-override-value"); !strings.Contains(got, "an-override-value") {
		t.Errorf("trace of the plaintext override = %q, want it verbatim", got)
	}
}

// TestUnresolvedAt_coversSequenceElements pins the dot-boundary coverage rule:
// the decrypt walk records a sequence element with its index, while
// varsusage.EnumerateVars stops at the sequence, so `dwe vars list` asks about
// the parent path and must still get true.
func TestUnresolvedAt_coversSequenceElements(t *testing.T) {
	hideIdentity(t)
	id, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	r := id.Recipient()
	ws := writeLayerFixture(t,
		"secrets:\n  recipient: "+r+"\nvars:\n  tokens:\n    - "+mustEncrypt(t, "seq-secret", r)+"\n", "", "")

	_, state, err := LoadLayersWithSecrets(ws)
	if err != nil {
		t.Fatalf("LoadLayersWithSecrets: %v", err)
	}
	if len(state.Unresolved) != 1 || state.Unresolved[0].Path != "vars.tokens.0" {
		t.Fatalf("Unresolved = %+v, want one entry at vars.tokens.0", state.Unresolved)
	}
	if !state.UnresolvedAt(ws, "vars.tokens") {
		t.Errorf("UnresolvedAt(vars.tokens) = false, want true (the element is beneath it)")
	}
	if !state.UnresolvedAt(ws, "vars.tokens.0") {
		t.Errorf("UnresolvedAt(vars.tokens.0) = false, want true")
	}
	// Only at a real dot boundary.
	if state.UnresolvedAt(ws, "vars.token") {
		t.Errorf("UnresolvedAt(vars.token) = true, want false — prefix without a dot boundary")
	}
	if state.UnresolvedAt(ws, "vars.tokensx") {
		t.Errorf("UnresolvedAt(vars.tokensx) = true, want false")
	}
}
