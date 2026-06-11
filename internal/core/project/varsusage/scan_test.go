package varsusage

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const fixtureProj = "testdata/proj"

// loc is a compact (file, line, kind) tuple for asserting scan results without
// pinning the exact source text.
type loc struct {
	File string
	Line int
	Kind string
}

func locsOf(res ScanResult) []loc {
	out := make([]loc, 0, len(res.Usages))
	for _, u := range res.Usages {
		out = append(out, loc{u.File, u.Line, u.Kind})
	}
	return out
}

func TestScanUsages(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  []loc
	}{
		{
			name:  "exact leaf finds every syntax across files",
			query: "vars.db.host",
			want: []loc{
				{"workspace/commands/foo.yml", 4, "from"},
				{"workspace/commands/foo.yml", 9, "template"},
				{"workspace/deploy.yml", 4, "template"},
				{"workspace/services/app/render/config.tmpl", 1, "template"},
				{"workspace/services/app/service.yml", 6, "template"},
			},
		},
		{
			name:  "namespace prefix matches the leaf under it",
			query: "vars.db",
			want: []loc{
				{"workspace/commands/foo.yml", 4, "from"},
				{"workspace/commands/foo.yml", 9, "template"},
				{"workspace/deploy.yml", 4, "template"},
				{"workspace/services/app/render/config.tmpl", 1, "template"},
				{"workspace/services/app/service.yml", 6, "template"},
			},
		},
		{
			name:  "typed when.expr Go-template resolve is matched structurally",
			query: "vars.feature.flag",
			want: []loc{
				{"workspace/deploy.yml", 7, "when"},
			},
		},
		{
			name:  "default_from and render template both found",
			query: "vars.region",
			want: []loc{
				{"workspace/commands/foo.yml", 7, "default_from"},
				{"workspace/services/app/render/config.tmpl", 2, "template"},
			},
		},
		{
			name:  "project_name field templated",
			query: "vars.project.slug",
			want: []loc{
				{"workspace/docker.yml", 1, "template"},
			},
		},
		{
			name:  "reference only inside a YAML comment is not matched",
			query: "vars.commented.out",
			want:  nil,
		},
		{
			name:  "unknown var has no usages",
			query: "vars.nonexistent",
			want:  nil,
		},
		{
			name:  "empty query yields nothing",
			query: "",
			want:  nil,
		},
		{
			name:  "non-vars query yields nothing",
			query: "services.app.ports",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := ScanUsages(fixtureProj, tt.query)
			if err != nil {
				t.Fatalf("ScanUsages(%q): %v", tt.query, err)
			}
			got := locsOf(res)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ScanUsages(%q) locs =\n  %v\nwant\n  %v", tt.query, got, tt.want)
			}
		})
	}
}

// TestScanUsages_FalsePositives pins the false-positive cases: whitespace inside
// ${ }, a leading digit, the description field (not rendered), and a bare
// when: vars.x scalar (no ${...}) must NOT be matched.
func TestScanUsages_FalsePositives(t *testing.T) {
	res, err := ScanUsages(fixtureProj, "vars.db.host")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range res.Usages {
		switch {
		case u.File == "workspace/services/app/service.yml" && (u.Line == 4 || u.Line == 5):
			t.Errorf("whitespace/leading-digit ${...} should not match: %+v", u)
		case u.File == "workspace/commands/foo.yml" && u.Line == 1:
			t.Errorf("description field should not be scanned: %+v", u)
		case u.File == "workspace/commands/foo.yml" && u.Line == 10:
			t.Errorf("bare when: vars.x scalar should not match: %+v", u)
		}
	}
}

// TestScanUsages_TextCaptured verifies the matched line text is captured trimmed.
func TestScanUsages_TextCaptured(t *testing.T) {
	res, err := ScanUsages(fixtureProj, "vars.feature.flag")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Usages) != 1 {
		t.Fatalf("want 1 usage, got %d", len(res.Usages))
	}
	want := `expr: '{{ resolve .Raw "vars.feature.flag" }}'`
	if res.Usages[0].Text != want {
		t.Errorf("Text = %q, want %q", res.Usages[0].Text, want)
	}
}

// TestScanUsages_SkipsVarsBlock pins that a ${vars.x} reference appearing inside
// the vars: sandbox itself is NOT reported: vars: values are config data
// (resolved by dot-path), never re-rendered through the template engine, so a
// key named value/cmd/from inside vars: is not a runtime usage.
func TestScanUsages_SkipsVarsBlock(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(rel, content string) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// local.yml has a vars: block whose values name template-looking keys and a
	// structural from: — none of which are rendered. A real usage lives in a
	// command file so the scan isn't trivially empty.
	mustWrite("workspace/local.yml", "vars:\n  some:\n    value: \"${vars.secret}\"\n    from: vars.secret\n  secret: shh\n")
	mustWrite("workspace/commands/foo.yml", "cmd: echo ${vars.secret}\n")

	res, err := ScanUsages(dir, "vars.secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range res.Usages {
		if u.File == "workspace/local.yml" {
			t.Errorf("vars: block value should not be reported as a usage: %+v", u)
		}
	}
	// The genuine command-file usage must still be found.
	var foundCmd bool
	for _, u := range res.Usages {
		if u.File == "workspace/commands/foo.yml" {
			foundCmd = true
		}
	}
	if !foundCmd {
		t.Errorf("expected the command-file usage to be found, got %v", locsOf(res))
	}
}

func TestScanUsages_MissingWorkspace(t *testing.T) {
	res, err := ScanUsages("testdata/does-not-exist", "vars.db.host")
	if err != nil {
		t.Fatalf("missing project should not error: %v", err)
	}
	if len(res.Usages) != 0 {
		t.Errorf("want no usages, got %v", res.Usages)
	}
}

func TestRefMatches(t *testing.T) {
	tests := []struct {
		ref, query string
		want       bool
	}{
		{"vars.db.host", "vars.db.host", true},
		{"vars.db.host", "vars.db", true},   // usage under queried namespace
		{"vars.db", "vars.db.host", true},   // whole-namespace usage covers leaf query
		{"vars.dbx.host", "vars.db", false}, // dot-boundary, not substring
		{"vars.database", "vars.db", false},
		{"vars.db.host", "vars.db.port", false},
	}
	for _, tt := range tests {
		if got := refMatches(tt.ref, tt.query); got != tt.want {
			t.Errorf("refMatches(%q,%q)=%v want %v", tt.ref, tt.query, got, tt.want)
		}
	}
}
