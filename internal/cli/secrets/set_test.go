package secrets

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/ask"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// defaultsPath is the workspace/defaults.yml of a fixture project.
func defaultsPath(root string) string {
	return filepath.Join(root, "workspace", "defaults.yml")
}

// readSecret decrypts whatever marker sits at path, failing the test when there
// is none. It goes through the same raw-layer inventory the command uses.
func readSecret(t *testing.T, flags *cmdctx.RootFlags, path string) string {
	t.Helper()
	layers, err := config.LoadRawLayers(flags.ConfigPath)
	if err != nil {
		t.Fatalf("loading raw layers: %v", err)
	}
	marker, _, ok := markerAt(layers, path)
	if !ok {
		t.Fatalf("no marker at %s", path)
	}
	id, _, err := secrets.LoadIdentity(config.RecipientFromLayers(layers))
	if err != nil {
		t.Fatalf("loading identity: %v", err)
	}
	plain, err := secrets.Decrypt(marker, id)
	if err != nil {
		t.Fatalf("decrypting %s: %v", path, err)
	}
	return plain
}

// TestSet_CreatesDefaultsAndRoundTrips pins the happy path on a project whose
// defaults.yml does not exist yet: the file is created 0644 (tracked, not
// local.yml's forced 0600), the value is stored as a marker rather than
// plaintext, and the project still loads — with the plaintext back in place.
func TestSet_CreatesDefaultsAndRoundTrips(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	initProject(t, flags)

	out, _, err := runSecrets(t, flags, "set", "vars.telegram.token", "123:abc")
	if err != nil {
		t.Fatalf("secrets set: %v", err)
	}
	if !strings.Contains(out, "vars.telegram.token") {
		t.Errorf("output does not name the path\ngot:\n%s", out)
	}
	if strings.Contains(out, "123:abc") {
		t.Errorf("output echoed the plaintext\ngot:\n%s", out)
	}

	raw, err := os.ReadFile(defaultsPath(root))
	if err != nil {
		t.Fatalf("reading defaults.yml: %v", err)
	}
	if strings.Contains(string(raw), "123:abc") {
		t.Errorf("defaults.yml holds the plaintext:\n%s", raw)
	}
	if !strings.Contains(string(raw), secrets.MarkerPrefix) {
		t.Errorf("defaults.yml holds no marker:\n%s", raw)
	}
	fi, err := os.Stat(defaultsPath(root))
	if err != nil {
		t.Fatalf("stat defaults.yml: %v", err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("defaults.yml mode = %v, want 0644 (tracked file)", fi.Mode().Perm())
	}

	// The decrypting loader puts the plaintext back where every ${vars.*}
	// consumer expects it.
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("loading config after set: %v", err)
	}
	if got := nestedString(cfg.Vars, "telegram", "token"); got != "123:abc" {
		t.Errorf("vars.telegram.token = %q, want %q", got, "123:abc")
	}
	if len(cfg.SecretsState.Decrypted) != 1 {
		t.Errorf("SecretsState.Decrypted = %+v, want exactly one entry", cfg.SecretsState.Decrypted)
	}
}

// TestSet_PreservesCommentsAndSiblings pins the node-writer contract at the
// defaults.yml layer and the "descend into an existing mapping" case.
func TestSet_PreservesCommentsAndSiblings(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	initProject(t, flags)

	const src = `# shared defaults, committed
vars:
  telegram:
    chat_id: "42" # keep me
  other: plain
`
	if err := os.WriteFile(defaultsPath(root), []byte(src), 0o644); err != nil {
		t.Fatalf("writing defaults.yml: %v", err)
	}

	if _, _, err := runSecrets(t, flags, "set", "vars.telegram.token", "s3cret"); err != nil {
		t.Fatalf("secrets set: %v", err)
	}

	data, err := os.ReadFile(defaultsPath(root))
	if err != nil {
		t.Fatalf("reading defaults.yml: %v", err)
	}
	got := string(data)
	for _, want := range []string{
		"# shared defaults, committed",
		"# keep me",
		`chat_id: "42"`,
		"other: plain",
		"token: " + secrets.MarkerPrefix,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("defaults.yml lost %q\ngot:\n%s", want, got)
		}
	}
	if plain := readSecret(t, flags, "vars.telegram.token"); plain != "s3cret" {
		t.Errorf("decrypted value = %q, want %q", plain, "s3cret")
	}
}

// TestSet_WorkspaceFile pins --file workspace: the value lands in workspace.yml
// itself, beside the recipient that encrypted it.
func TestSet_WorkspaceFile(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	initProject(t, flags)

	if _, _, err := runSecrets(t, flags, "set", "vars.app.key", "abc123", "--file", "workspace"); err != nil {
		t.Fatalf("secrets set --file workspace: %v", err)
	}
	if _, err := os.Stat(defaultsPath(root)); err == nil {
		t.Error("defaults.yml was created for a --file workspace write")
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reading workspace.yml: %v", err)
	}
	if !strings.Contains(string(data), secrets.MarkerPrefix) {
		t.Errorf("workspace.yml holds no marker:\n%s", data)
	}
	if plain := readSecret(t, flags, "vars.app.key"); plain != "abc123" {
		t.Errorf("decrypted value = %q, want %q", plain, "abc123")
	}
	// The pre-existing sibling under vars.app survived the nested write.
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	if got := nestedString(cfg.Vars, "app", "name"); got != "myapp" {
		t.Errorf("vars.app.name = %q, want myapp", got)
	}
}

// TestSet_Stdin pins that exactly one trailing newline is trimmed: a piped
// `printf 'x\n'` means "x", while a value that deliberately ends in a blank line
// keeps it.
func TestSet_Stdin(t *testing.T) {
	tests := []struct {
		name  string
		stdin string
		want  string
	}{
		{name: "one trailing newline trimmed", stdin: "s3cret\n", want: "s3cret"},
		{name: "second newline kept", stdin: "s3cret\n\n", want: "s3cret\n"},
		{name: "no newline", stdin: "s3cret", want: "s3cret"},
		{name: "crlf trimmed once", stdin: "s3cret\r\n", want: "s3cret"},
		{name: "inner newlines kept", stdin: "line1\nline2\n", want: "line1\nline2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateHome(t)
			cfgPath, root := writeFixture(t)
			flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
			initProject(t, flags)

			if _, _, err := runSecretsStdin(t, flags, tt.stdin, "set", "vars.token", "--stdin"); err != nil {
				t.Fatalf("secrets set --stdin: %v", err)
			}
			if got := readSecret(t, flags, "vars.token"); got != tt.want {
				t.Errorf("decrypted value = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSet_Prompt pins the hidden-prompt path: with no argument and no --stdin on
// a terminal, the masked ask form supplies the value.
func TestSet_Prompt(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	initProject(t, flags)

	origInteractive := widgets.IsInteractiveFn
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	t.Cleanup(func() { widgets.IsInteractiveFn = origInteractive })

	var gotFields []ask.Field
	origAsk := runAsk
	runAsk = func(_ context.Context, _ string, fields []ask.Field, _ ask.RunOptions) (ask.Result, error) {
		gotFields = fields
		return ask.NewResultForTest(map[string]any{"value": "typed-secret"}), nil
	}
	t.Cleanup(func() { runAsk = origAsk })

	if _, _, err := runSecrets(t, flags, "set", "vars.token"); err != nil {
		t.Fatalf("secrets set (prompt): %v", err)
	}
	if len(gotFields) != 1 || gotFields[0].Kind != ask.FieldPassword {
		t.Fatalf("form fields = %+v, want a single FieldPassword", gotFields)
	}
	if !gotFields[0].Required {
		t.Error("the prompt field is not Required; an empty secret would be stored")
	}
	if got := readSecret(t, flags, "vars.token"); got != "typed-secret" {
		t.Errorf("decrypted value = %q, want %q", got, "typed-secret")
	}
}

// TestSet_PromptAbortIsNoOp pins that cancelling the prompt writes nothing and
// fails nothing — the same contract `dwe vars set` has.
func TestSet_PromptAbortIsNoOp(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
	initProject(t, flags)

	origInteractive := widgets.IsInteractiveFn
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	t.Cleanup(func() { widgets.IsInteractiveFn = origInteractive })

	origAsk := runAsk
	runAsk = func(context.Context, string, []ask.Field, ask.RunOptions) (ask.Result, error) {
		return ask.Result{}, widgets.ErrCancelled
	}
	t.Cleanup(func() { runAsk = origAsk })

	out, _, err := runSecrets(t, flags, "set", "vars.token")
	if err != nil {
		t.Fatalf("aborted prompt returned an error: %v", err)
	}
	if out != "" {
		t.Errorf("aborted prompt wrote %q to stdout", out)
	}
	if _, err := os.Stat(defaultsPath(root)); err == nil {
		t.Error("defaults.yml was created by an aborted prompt")
	}
}

// TestSet_Refusals pins every input the command must reject, and that each
// refusal leaves the layer files exactly as they were.
func TestSet_Refusals(t *testing.T) {
	// A defaults.yml whose vars.db.host is a scalar: descending a map overlay
	// through it would discard the developer's value.
	const existing = `vars:
  db:
    host: db.internal
`
	tests := []struct {
		name string
		args []string
		code string
		hint string
	}{
		{
			name: "non-vars path",
			args: []string{"set", "project.name", "x"},
			code: "secrets_path_invalid",
		},
		{
			name: "bare vars namespace",
			args: []string{"set", "vars", "x"},
			code: "secrets_path_invalid",
		},
		{
			name: "empty segment",
			args: []string{"set", "vars..token", "x"},
			code: "secrets_path_invalid",
		},
		{
			name: "through a scalar node",
			args: []string{"set", "vars.db.host.port", "x"},
			code: "secrets_write_failed",
		},
		{
			name: "file local",
			args: []string{"set", "vars.token", "x", "--file", "local"},
			code: "secrets_file_invalid",
			hint: "dwe vars set",
		},
		{
			name: "unknown file",
			args: []string{"set", "vars.token", "x", "--file", "nowhere"},
			code: "secrets_file_invalid",
		},
		{
			name: "value and stdin",
			args: []string{"set", "vars.token", "x", "--stdin"},
			code: "secrets_value_ambiguous",
		},
		{
			name: "no value, not a terminal",
			args: []string{"set", "vars.token"},
			code: "secrets_value_required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateHome(t)
			cfgPath, root := writeFixture(t)
			flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
			initProject(t, flags)
			if err := os.WriteFile(defaultsPath(root), []byte(existing), 0o644); err != nil {
				t.Fatalf("writing defaults.yml: %v", err)
			}
			beforeDefaults, err := os.ReadFile(defaultsPath(root))
			if err != nil {
				t.Fatalf("reading defaults.yml: %v", err)
			}
			beforeWorkspace, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatalf("reading workspace.yml: %v", err)
			}

			_, _, err = runSecrets(t, flags, tt.args...)
			if err == nil {
				t.Fatalf("%v succeeded; want a refusal", tt.args)
			}
			coded := codedError(t, err)
			if coded.Code != tt.code {
				t.Errorf("error code = %q, want %q (message: %s)", coded.Code, tt.code, coded.Message)
			}
			if tt.hint != "" && !strings.Contains(coded.Hint, tt.hint) {
				t.Errorf("hint = %q, want it to mention %q", coded.Hint, tt.hint)
			}

			afterDefaults, err := os.ReadFile(defaultsPath(root))
			if err != nil {
				t.Fatalf("re-reading defaults.yml: %v", err)
			}
			if string(beforeDefaults) != string(afterDefaults) {
				t.Errorf("defaults.yml changed on a refused set\nbefore:\n%s\nafter:\n%s", beforeDefaults, afterDefaults)
			}
			afterWorkspace, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatalf("re-reading workspace.yml: %v", err)
			}
			if string(beforeWorkspace) != string(afterWorkspace) {
				t.Errorf("workspace.yml changed on a refused set")
			}
		})
	}
}

// TestSet_WithoutRecipient pins that a project with no key pair is sent to
// `init` rather than told something vague about encryption.
func TestSet_WithoutRecipient(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	_, _, err := runSecrets(t, flags, "set", "vars.token", "x")
	if err == nil {
		t.Fatal("set succeeded without a recipient")
	}
	coded := codedError(t, err)
	if coded.Code != "secrets_no_recipient" {
		t.Fatalf("error code = %q, want secrets_no_recipient", coded.Code)
	}
	if !strings.Contains(coded.Hint, "init") {
		t.Errorf("hint = %q, want it to point at init", coded.Hint)
	}
}

// TestSet_NoCoercion pins decision 3: plaintext is always a string, so a secret
// that looks like a number or a bool survives the round-trip as text.
func TestSet_NoCoercion(t *testing.T) {
	for _, value := range []string{"123", "true", "1.5", "null"} {
		t.Run(value, func(t *testing.T) {
			isolateHome(t)
			cfgPath, root := writeFixture(t)
			flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}
			initProject(t, flags)

			if _, _, err := runSecrets(t, flags, "set", "vars.token", value); err != nil {
				t.Fatalf("secrets set: %v", err)
			}
			if got := readSecret(t, flags, "vars.token"); got != value {
				t.Errorf("decrypted value = %q, want %q", got, value)
			}
			cfg, err := config.LoadConfig(cfgPath)
			if err != nil {
				t.Fatalf("loading config: %v", err)
			}
			if got, ok := cfg.Vars["token"].(string); !ok || got != value {
				t.Errorf("vars.token = %#v, want the string %q", cfg.Vars["token"], value)
			}
		})
	}
}

// TestSet_JSON pins the DTO and the clean-stdout contract.
func TestSet_JSON(t *testing.T) {
	isolateHome(t)
	cfgPath, root := writeFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root, Output: "json"}
	initProject(t, flags)

	out, errOut, err := runSecrets(t, flags, "set", "vars.token", "s3cret")
	if err != nil {
		t.Fatalf("secrets set --output json: %v", err)
	}
	if errOut != "" {
		t.Errorf("stderr should be empty in JSON mode, got: %q", errOut)
	}
	var data secretSetJSON
	if e := json.Unmarshal([]byte(out), &data); e != nil {
		t.Fatalf("unmarshal set json: %v\nraw: %s", e, out)
	}
	if data.Path != "vars.token" {
		t.Errorf("path = %q, want vars.token", data.Path)
	}
	if want := filepath.Join("workspace", "defaults.yml"); data.File != want {
		t.Errorf("file = %q, want %q", data.File, want)
	}
	if strings.Contains(out, "s3cret") {
		t.Errorf("set JSON leaked the plaintext: %s", out)
	}
}

// TestStageLayers pins the staging list the pre-write validation runs on. The
// insert position is load-bearing: ValidateLayerRoots accepts a secrets: block
// in the FIRST layer only, so a newly created defaults.yml appended at the end
// (or prepended) would be validated as the wrong file.
func TestStageLayers(t *testing.T) {
	workspace := config.Layer{Path: "/p/workspace.yml", Data: map[string]any{"a": 1}}
	defaults := config.Layer{Path: "/p/workspace/defaults.yml", Data: map[string]any{"b": 2}}
	local := config.Layer{Path: "/p/workspace/local.yml", Data: map[string]any{"c": 3}}
	staged := map[string]any{"vars": map[string]any{"token": "ENC[age:x]"}}

	t.Run("replaces an existing layer in place", func(t *testing.T) {
		got := stageLayers([]config.Layer{workspace, defaults, local}, defaults.Path, staged)
		if len(got) != 3 || got[1].Path != defaults.Path {
			t.Fatalf("layers = %+v, want defaults still second", got)
		}
		if got[1].Data["vars"] == nil {
			t.Error("the defaults layer was not replaced by the staged data")
		}
		if got[0].Data["a"] != 1 || got[2].Data["c"] != 3 {
			t.Error("a sibling layer was disturbed")
		}
	})

	t.Run("inserts a new defaults layer after workspace.yml", func(t *testing.T) {
		got := stageLayers([]config.Layer{workspace, local}, defaults.Path, staged)
		if len(got) != 3 {
			t.Fatalf("layers = %+v, want 3", got)
		}
		if got[0].Path != workspace.Path || got[1].Path != defaults.Path || got[2].Path != local.Path {
			t.Errorf("order = %s, %s, %s; want workspace, defaults, local", got[0].Path, got[1].Path, got[2].Path)
		}
	})

	t.Run("does not mutate the caller's slice", func(t *testing.T) {
		in := []config.Layer{workspace, defaults}
		stageLayers(in, defaults.Path, staged)
		if in[1].Data["b"] != 2 {
			t.Errorf("the input layer was mutated: %+v", in[1].Data)
		}
	})
}

// nestedString reads a string at a nested map path, returning "" when absent.
func nestedString(m map[string]any, keys ...string) string {
	var cur any = m
	for _, k := range keys {
		node, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = node[k]
	}
	s, _ := cur.(string)
	return s
}
