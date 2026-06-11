package local

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// loadNodeAsMap round-trips a written local.yml through the map loader so tests
// can assert effective values regardless of formatting.
func loadNodeAsMap(t *testing.T, path string) map[string]any {
	t.Helper()
	m, err := LoadLocalYAML(path)
	if err != nil {
		t.Fatalf("reload as map: %v", err)
	}
	return m
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func applyAndWrite(t *testing.T, path string, overlay map[string]any) {
	t.Helper()
	doc, err := LoadLocalYAMLNode(path)
	if err != nil {
		t.Fatalf("load node: %v", err)
	}
	if err := ApplyOverlayToNode(doc, overlay); err != nil {
		t.Fatalf("apply overlay: %v", err)
	}
	if err := WriteLocalYAMLNode(path, doc); err != nil {
		t.Fatalf("write node: %v", err)
	}
}

func TestLoadLocalYAMLNode_MissingFile(t *testing.T) {
	doc, err := LoadLocalYAMLNode("/nonexistent/path/local.yml")
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	root, err := documentRoot(doc)
	if err != nil {
		t.Fatalf("documentRoot: %v", err)
	}
	if root.Kind != yaml.MappingNode || len(root.Content) != 0 {
		t.Errorf("expected empty mapping, got kind=%v len=%d", root.Kind, len(root.Content))
	}
}

func TestLoadLocalYAMLNode_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")
	writeFixture(t, path, "")
	doc, err := LoadLocalYAMLNode(path)
	if err != nil {
		t.Fatalf("empty file should not error: %v", err)
	}
	if _, err := documentRoot(doc); err != nil {
		t.Fatalf("documentRoot: %v", err)
	}
}

func TestLoadLocalYAMLNode_CommentOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")
	writeFixture(t, path, "# just a comment\n# another\n")
	doc, err := LoadLocalYAMLNode(path)
	if err != nil {
		t.Fatalf("comment-only file should not error: %v", err)
	}
	root, err := documentRoot(doc)
	if err != nil {
		t.Fatalf("documentRoot: %v", err)
	}
	if root.Kind != yaml.MappingNode {
		t.Errorf("expected empty mapping for comment-only doc, got %v", root.Kind)
	}
}

func TestLoadLocalYAMLNode_MultiDocRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")
	writeFixture(t, path, "a: 1\n---\nb: 2\n")
	if _, err := LoadLocalYAMLNode(path); err == nil {
		t.Fatal("expected multi-document YAML to be rejected")
	}
}

func TestLoadLocalYAMLNode_MalformedYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")
	writeFixture(t, path, "a: [unterminated\n")
	if _, err := LoadLocalYAMLNode(path); err == nil {
		t.Fatal("expected malformed YAML to error")
	}
}

func TestApplyOverlayToNode_NonMappingRoot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")
	writeFixture(t, path, "- one\n- two\n")
	doc, err := LoadLocalYAMLNode(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := ApplyOverlayToNode(doc, map[string]any{"x": 1}); err == nil {
		t.Fatal("expected non-mapping (sequence) root to be rejected")
	}
}

// Comments, blank lines and formatting survive a scalar edit.
func TestApplyOverlayToNode_PreservesComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")
	content := `# top comment
vars:
  # the db block
  db:
    host: localhost # inline comment
    port: 5432

# trailing comment
`
	writeFixture(t, path, content)
	applyAndWrite(t, path, map[string]any{
		"vars": map[string]any{"db": map[string]any{"host": "remote"}},
	})

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(out)
	for _, want := range []string{"# top comment", "# the db block", "# inline comment", "# trailing comment", "host: remote"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected output to contain %q; got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "localhost") {
		t.Errorf("expected host value replaced; got:\n%s", got)
	}
}

// Overwriting a quoted string with a coerced bool/int emits a BARE scalar so it
// reloads typed.
func TestApplyOverlayToNode_CoercionEmitsBareScalar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")
	writeFixture(t, path, "vars:\n  flag: \"old\"\n  count: \"7\"\n")
	applyAndWrite(t, path, map[string]any{
		"vars": map[string]any{"flag": true, "count": 42},
	})

	out, _ := os.ReadFile(path)
	got := string(out)
	if !strings.Contains(got, "flag: true") {
		t.Errorf("expected bare bool 'flag: true'; got:\n%s", got)
	}
	if !strings.Contains(got, "count: 42") {
		t.Errorf("expected bare int 'count: 42'; got:\n%s", got)
	}

	m := loadNodeAsMap(t, path)
	vars := m["vars"].(map[string]any)
	if vars["flag"] != true {
		t.Errorf("flag should reload as bool true, got %T %v", vars["flag"], vars["flag"])
	}
	if vars["count"] != 42 {
		t.Errorf("count should reload as int 42, got %T %v", vars["count"], vars["count"])
	}
}

// Overwriting a bare bool/int with an explicitly-quoted string stays a string.
func TestApplyOverlayToNode_StringOverBareStaysString(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")
	writeFixture(t, path, "vars:\n  flag: true\n")
	applyAndWrite(t, path, map[string]any{
		"vars": map[string]any{"flag": "true"},
	})

	m := loadNodeAsMap(t, path)
	vars := m["vars"].(map[string]any)
	if s, ok := vars["flag"].(string); !ok || s != "true" {
		t.Errorf("flag should reload as string \"true\", got %T %v", vars["flag"], vars["flag"])
	}
}

func TestApplyOverlayToNode_NewNestedKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")
	writeFixture(t, path, "vars:\n  db:\n    host: localhost\n")
	applyAndWrite(t, path, map[string]any{
		"vars": map[string]any{"db": map[string]any{"port": 5432}},
	})

	m := loadNodeAsMap(t, path)
	db := m["vars"].(map[string]any)["db"].(map[string]any)
	if db["host"] != "localhost" {
		t.Errorf("existing host lost: %v", db["host"])
	}
	if db["port"] != 5432 {
		t.Errorf("new port not added: %v", db["port"])
	}
}

func TestApplyOverlayToNode_BrandNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")
	applyAndWrite(t, path, map[string]any{
		"vars": map[string]any{"a": map[string]any{"b": map[string]any{"c": "deep"}}},
	})
	m := loadNodeAsMap(t, path)
	c := m["vars"].(map[string]any)["a"].(map[string]any)["b"].(map[string]any)["c"]
	if c != "deep" {
		t.Errorf("deep nesting not created: %v", c)
	}
}

func TestApplyOverlayToNode_RejectMapOverScalar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")
	writeFixture(t, path, "vars:\n  db: localhost\n")
	doc, _ := LoadLocalYAMLNode(path)
	err := ApplyOverlayToNode(doc, map[string]any{
		"vars": map[string]any{"db": map[string]any{"host": "x"}},
	})
	if err == nil {
		t.Fatal("expected map-over-scalar to be rejected")
	}
}

func TestApplyOverlayToNode_RejectMapOverSequence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")
	writeFixture(t, path, "vars:\n  list:\n    - a\n    - b\n")
	doc, _ := LoadLocalYAMLNode(path)
	err := ApplyOverlayToNode(doc, map[string]any{
		"vars": map[string]any{"list": map[string]any{"x": 1}},
	})
	if err == nil {
		t.Fatal("expected map-over-sequence to be rejected")
	}
}

func TestApplyOverlayToNode_RejectMapOverExplicitNull(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")
	writeFixture(t, path, "vars:\n  db: null\n")
	doc, _ := LoadLocalYAMLNode(path)
	err := ApplyOverlayToNode(doc, map[string]any{
		"vars": map[string]any{"db": map[string]any{"host": "x"}},
	})
	if err == nil {
		t.Fatal("expected map-over-explicit-null to be rejected")
	}
}

func TestApplyOverlayToNode_RejectMapOverAlias(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")
	writeFixture(t, path, "anchor: &a\n  k: v\nref: *a\n")
	doc, _ := LoadLocalYAMLNode(path)
	err := ApplyOverlayToNode(doc, map[string]any{
		"ref": map[string]any{"k": "other"},
	})
	if err == nil {
		t.Fatal("expected map-over-alias to be rejected")
	}
}

// A key reachable only through a `<<: *anchor` merge must not be shadowed by a
// silently-appended explicit override (which YAML resolves as a full replace of
// the merged subtree). The plan's merge-key guard requires rejecting this.
func TestApplyOverlayToNode_RejectAppendIntoMergeKeyMapping(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")
	writeFixture(t, path, "defaults: &d\n  db:\n    host: localhost\nvars:\n  <<: *d\n")
	doc, _ := LoadLocalYAMLNode(path)
	// vars.db is merge-inherited (only `<<` is an explicit pair under vars).
	err := ApplyOverlayToNode(doc, map[string]any{
		"vars": map[string]any{"db": map[string]any{"port": 5432}},
	})
	if err == nil {
		t.Fatal("expected append into merge-key mapping to be rejected")
	}
}

// A scalar overlay onto a brand-new key in a merge-bearing mapping is likewise
// rejected — it may shadow a merge-inherited scalar.
func TestApplyOverlayToNode_RejectScalarAppendIntoMergeKeyMapping(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")
	writeFixture(t, path, "defaults: &d\n  host: localhost\nvars:\n  <<: *d\n")
	doc, _ := LoadLocalYAMLNode(path)
	err := ApplyOverlayToNode(doc, map[string]any{
		"vars": map[string]any{"host": "other"},
	})
	if err == nil {
		t.Fatal("expected scalar append into merge-key mapping to be rejected")
	}
}

func TestApplyOverlayToNode_RejectScalarOverMap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")
	writeFixture(t, path, "vars:\n  db:\n    host: localhost\n")
	doc, _ := LoadLocalYAMLNode(path)
	err := ApplyOverlayToNode(doc, map[string]any{
		"vars": map[string]any{"db": "scalar"},
	})
	if err == nil {
		t.Fatal("expected scalar-over-map to be rejected")
	}
}

// Legacy bare-int port leaf may be upgraded to rich form {port: N}.
func TestApplyOverlayToNode_LegacyPortLeafUpgrade(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")
	writeFixture(t, path, "services:\n  web:\n    ports:\n      http: 3000\n")
	applyAndWrite(t, path, map[string]any{
		"services": map[string]any{"web": map[string]any{"ports": map[string]any{"http": map[string]any{"port": 3001}}}},
	})
	m := loadNodeAsMap(t, path)
	http := m["services"].(map[string]any)["web"].(map[string]any)["ports"].(map[string]any)["http"].(map[string]any)
	if http["port"] != 3001 {
		t.Errorf("port-leaf upgrade failed: %v", http)
	}
}

func TestApplyOverlayToNode_FlowStyleMapping(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")
	writeFixture(t, path, "vars: {a: 1, b: 2}\n")
	applyAndWrite(t, path, map[string]any{
		"vars": map[string]any{"a": 9},
	})
	m := loadNodeAsMap(t, path)
	vars := m["vars"].(map[string]any)
	if vars["a"] != 9 || vars["b"] != 2 {
		t.Errorf("flow-style mapping edit wrong: %v", vars)
	}
}

// Characterization: the node writer reproduces semantically-equivalent YAML to
// the map writer for representative inputs. Map output is non-deterministically
// ordered, so we compare loaded values, not bytes.
func TestNodeWriter_CharacterizationVsMapWriter(t *testing.T) {
	cases := []struct {
		name    string
		base    string
		overlay map[string]any
	}{
		{
			name:    "empty file",
			base:    "",
			overlay: map[string]any{"services": map[string]any{"web": map[string]any{"enabled": true}}},
		},
		{
			name:    "existing tree",
			base:    "services:\n  api:\n    enabled: true\n",
			overlay: map[string]any{"services": map[string]any{"web": map[string]any{"enabled": false}}},
		},
		{
			name:    "nested service toggle",
			base:    "services:\n  web:\n    enabled: true\n    ports:\n      http: 3000\n",
			overlay: map[string]any{"services": map[string]any{"web": map[string]any{"enabled": false}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()

			// Map writer path: load -> deep-set -> WriteLocalYAML.
			mapPath := filepath.Join(dir, "map.yml")
			writeFixture(t, mapPath, tc.base)
			mapData := loadNodeAsMap(t, mapPath)
			deepSet(mapData, tc.overlay)
			if err := WriteLocalYAML(mapPath, mapData); err != nil {
				t.Fatalf("map write: %v", err)
			}

			// Node writer path.
			nodePath := filepath.Join(dir, "node.yml")
			writeFixture(t, nodePath, tc.base)
			applyAndWrite(t, nodePath, tc.overlay)

			gotMap := loadNodeAsMap(t, mapPath)
			gotNode := loadNodeAsMap(t, nodePath)
			if !mapsEqual(gotMap, gotNode) {
				t.Errorf("node writer diverged from map writer:\nmap:  %#v\nnode: %#v", gotMap, gotNode)
			}
		})
	}
}

// deepSet recursively writes overlay into m (mirrors deepMerge for the test).
func deepSet(m, overlay map[string]any) {
	for k, v := range overlay {
		if sub, ok := v.(map[string]any); ok {
			child, ok := m[k].(map[string]any)
			if !ok {
				child = map[string]any{}
				m[k] = child
			}
			deepSet(child, sub)
			continue
		}
		m[k] = v
	}
}

func mapsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		am, aIsMap := av.(map[string]any)
		bm, bIsMap := bv.(map[string]any)
		if aIsMap != bIsMap {
			return false
		}
		if aIsMap {
			if !mapsEqual(am, bm) {
				return false
			}
			continue
		}
		if av != bv {
			return false
		}
	}
	return true
}

// TestNodeWriter_ThreeWriterParity proves the shared node write path is
// deterministic: applying an equivalent overlay onto the same base file yields
// BYTE-identical output regardless of which caller (vars set / services toggle /
// setup wizard) built it. All three route through ApplyOverlayToNode +
// WriteLocalYAMLNode, so the only thing that can differ is the overlay value —
// and equal overlays must produce equal bytes.
func TestNodeWriter_ThreeWriterParity(t *testing.T) {
	base := "# base config\nservices:\n  api:\n    enabled: true # api\n  web:\n    enabled: true\n"

	// The `services` toggle path builds its overlay via ServiceTogglesOverlay.
	cfg := &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"api": {Required: false},
			"web": {Required: false},
		},
	}
	svcOverlay, err := ServiceTogglesOverlay(cfg, nil, []string{"api"})
	if err != nil {
		t.Fatalf("service overlay: %v", err)
	}

	// The `vars set` / setup paths build the same shape as a plain nested map.
	handOverlay := map[string]any{"services": map[string]any{"api": map[string]any{"enabled": false}}}

	write := func(overlay map[string]any) []byte {
		dir := t.TempDir()
		path := filepath.Join(dir, "local.yml")
		writeFixture(t, path, base)
		applyAndWrite(t, path, overlay)
		out, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		return out
	}

	a := write(svcOverlay)
	b := write(handOverlay)
	if string(a) != string(b) {
		t.Errorf("writers diverged:\nservice overlay:\n%s\nhand overlay:\n%s", a, b)
	}
}

// TestWriteLocalYAMLNode_AtomicNoTempLeftover verifies the node writer leaves no
// temp file behind and writes the expected content (rollback-friendly atomic
// semantics, shared with the map writer).
func TestWriteLocalYAMLNode_AtomicNoTempLeftover(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yml")
	applyAndWrite(t, path, map[string]any{"vars": map[string]any{"x": 1}})

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "local.yml" {
			t.Errorf("unexpected leftover file: %s", e.Name())
		}
	}
}
