package local

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const spliceMarker = "ENC[age:YWJjZGVmZ2hpamtsbW5vcA]"

// newSpliceFixture writes content to a temp layer file and opens a Splicer on it.
func newSpliceFixture(t *testing.T, content string) (*Splicer, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "layer.yml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	s, err := NewSplicer(path, LabelDefaults)
	if err != nil {
		t.Fatalf("NewSplicer: %v", err)
	}
	return s, path
}

// changedLines returns the 0-based indexes of the lines that differ between
// before and after. A replacement splice must never change the line count, so a
// differing count fails the test outright — the point of the writer is that
// every untouched byte stays where it was.
func changedLines(t *testing.T, before, after string) []int {
	t.Helper()
	b := strings.Split(before, "\n")
	a := strings.Split(after, "\n")
	if len(b) != len(a) {
		t.Fatalf("line count changed: %d -> %d\n--- before ---\n%s\n--- after ---\n%s", len(b), len(a), before, after)
	}
	var idx []int
	for i := range b {
		if b[i] != a[i] {
			idx = append(idx, i)
		}
	}
	return idx
}

// assertSingleLineChange asserts that exactly line wantLine (1-based) changed
// and that it now reads wantText.
func assertSingleLineChange(t *testing.T, before, after string, wantLine int, wantText string) {
	t.Helper()
	changed := changedLines(t, before, after)
	if len(changed) != 1 || changed[0] != wantLine-1 {
		t.Fatalf("expected only line %d to change, got lines %v\n--- after ---\n%s", wantLine, changed, after)
	}
	if got := strings.Split(after, "\n")[wantLine-1]; got != wantText {
		t.Errorf("line %d = %q, want %q", wantLine, got, wantText)
	}
}

func TestSplicer_SetScalar_ReplacesOnlyTheValueToken(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		path     []string
		value    string
		wantLine int
		wantText string
	}{
		{
			name:     "plain scalar",
			src:      "a: plain\nb: 2\n",
			path:     []string{"a"},
			value:    spliceMarker,
			wantLine: 1,
			wantText: "a: " + spliceMarker,
		},
		{
			name:     "empty double-quoted scalar",
			src:      "a: \"\"\nb: 2\n",
			path:     []string{"a"},
			value:    spliceMarker,
			wantLine: 1,
			wantText: "a: " + spliceMarker,
		},
		{
			name:     "implicit null leaf gains a separating space",
			src:      "a:\nb: 2\n",
			path:     []string{"a"},
			value:    spliceMarker,
			wantLine: 1,
			wantText: "a: " + spliceMarker,
		},
		{
			name:     "double-quoted value keeps the trailing comment and its spacing",
			src:      "a: \"old\"     # keep me\nb: 2\n",
			path:     []string{"a"},
			value:    spliceMarker,
			wantLine: 1,
			wantText: "a: " + spliceMarker + "     # keep me",
		},
		{
			name:     "plain value keeps the trailing comment and its spacing",
			src:      "a: old   # keep me\nb: 2\n",
			path:     []string{"a"},
			value:    spliceMarker,
			wantLine: 1,
			wantText: "a: " + spliceMarker + "   # keep me",
		},
		{
			name:     "single-quoted value with an escaped quote",
			src:      "a: 'it''s old'\nb: 2\n",
			path:     []string{"a"},
			value:    spliceMarker,
			wantLine: 1,
			wantText: "a: " + spliceMarker,
		},
		{
			name:     "double-quoted value with an escaped quote",
			src:      "a: \"he said \\\"hi\\\"\"\nb: 2\n",
			path:     []string{"a"},
			value:    spliceMarker,
			wantLine: 1,
			wantText: "a: " + spliceMarker,
		},
		{
			name:     "anchor on a plain value is preserved",
			src:      "a: &x oldvalue\nb: 2\n",
			path:     []string{"a"},
			value:    spliceMarker,
			wantLine: 1,
			wantText: "a: &x " + spliceMarker,
		},
		{
			name:     "anchor on a quoted value is preserved",
			src:      "a: &x \"oldvalue\"\nb: 2\n",
			path:     []string{"a"},
			value:    spliceMarker,
			wantLine: 1,
			wantText: "a: &x " + spliceMarker,
		},
		{
			name:     "explicit tag is preserved",
			src:      "a: !!str tagged\nb: 2\n",
			path:     []string{"a"},
			value:    spliceMarker,
			wantLine: 1,
			wantText: "a: !!str " + spliceMarker,
		},
		{
			name:     "non-ascii earlier on the same line",
			src:      "\"ключ\": значение\nb: 2\n",
			path:     []string{"ключ"},
			value:    spliceMarker,
			wantLine: 1,
			wantText: "\"ключ\": " + spliceMarker,
		},
		{
			name:     "non-ascii on earlier lines",
			src:      "# коммент с русским текстом\nа: значение\nb: старое\n",
			path:     []string{"b"},
			value:    spliceMarker,
			wantLine: 3,
			wantText: "b: " + spliceMarker,
		},
		{
			name:     "key present explicitly in a merge-carrying mapping",
			src:      "base: &base\n  restart: \"no\"\nsvc:\n  <<: *base\n  restart: always\n",
			path:     []string{"svc", "restart"},
			value:    spliceMarker,
			wantLine: 5,
			wantText: "  restart: " + spliceMarker,
		},
		{
			name:     "four-space file keeps its indentation",
			src:      "vars:\n    token: old\n    other: 1\n",
			path:     []string{"vars", "token"},
			value:    spliceMarker,
			wantLine: 2,
			wantText: "    token: " + spliceMarker,
		},
		{
			name:     "ambiguous value is written quoted so it reloads as a string",
			src:      "a: old\nb: 2\n",
			path:     []string{"a"},
			value:    "true",
			wantLine: 1,
			wantText: "a: \"true\"",
		},
		{
			name:     "nested path two levels deep",
			src:      "vars:\n  telegram:\n    token: old\n    chat: '42'\n",
			path:     []string{"vars", "telegram", "token"},
			value:    spliceMarker,
			wantLine: 3,
			wantText: "    token: " + spliceMarker,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newSpliceFixture(t, tt.src)
			if err := s.SetScalar(tt.path, tt.value); err != nil {
				t.Fatalf("SetScalar: %v", err)
			}
			assertSingleLineChange(t, tt.src, string(s.Bytes()), tt.wantLine, tt.wantText)

			got, ok := scalarAt(s.doc, tt.path)
			if !ok || got != tt.value {
				t.Errorf("value at %v reads back as (%q, %v), want %q", tt.path, got, ok, tt.value)
			}
		})
	}
}

func TestSplicer_SetScalar_RefusedShapes(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		path    []string
		wantErr error
	}{
		{
			name:    "literal block scalar",
			src:     "a: |\n  one\n  two\nb: 2\n",
			path:    []string{"a"},
			wantErr: ErrMultilineScalar,
		},
		{
			name:    "folded block scalar",
			src:     "a: >\n  one\n  two\nb: 2\n",
			path:    []string{"a"},
			wantErr: ErrMultilineScalar,
		},
		{
			name:    "wrapped plain scalar",
			src:     "a: this is a\n  wrapped scalar\nb: 2\n",
			path:    []string{"a"},
			wantErr: ErrMultilineScalar,
		},
		{
			name:    "wrapped double-quoted scalar",
			src:     "a: \"line one\n  line two\"\nb: 2\n",
			path:    []string{"a"},
			wantErr: ErrMultilineScalar,
		},
		{
			name:    "scalar inside a flow mapping",
			src:     "a: {x: 1, y: 2}\nb: 2\n",
			path:    []string{"a", "x"},
			wantErr: ErrUnsplicable,
		},
		{
			name:    "scalar inside a flow sequence",
			src:     "a: [1, 2]\nb: 2\n",
			path:    []string{"a", "0"},
			wantErr: ErrUnsplicable,
		},
		{
			name:    "key reachable only through a merge key",
			src:     "base: &base\n  restart: \"no\"\nsvc:\n  <<: *base\n  image: app\n",
			path:    []string{"svc", "restart"},
			wantErr: ErrUnsplicable,
		},
		{
			name:    "missing key",
			src:     "a: 1\nb: 2\n",
			path:    []string{"c"},
			wantErr: ErrUnsplicable,
		},
		{
			name:    "leaf is a mapping",
			src:     "a:\n  b: 1\n",
			path:    []string{"a"},
			wantErr: ErrUnsplicable,
		},
		{
			name:    "leaf is an alias",
			src:     "a: &x 1\nb: *x\n",
			path:    []string{"b"},
			wantErr: ErrUnsplicable,
		},
		{
			name:    "parent is an alias",
			src:     "base: &b\n  x: 1\nother: *b\n",
			path:    []string{"other", "x"},
			wantErr: ErrUnsplicable,
		},
		{
			name:    "parent is a scalar",
			src:     "a: 1\nb: 2\n",
			path:    []string{"a", "b"},
			wantErr: ErrUnsplicable,
		},
		{
			name:    "empty path",
			src:     "a: 1\n",
			path:    nil,
			wantErr: ErrUnsplicable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newSpliceFixture(t, tt.src)
			err := s.SetScalar(tt.path, spliceMarker)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("SetScalar error = %v, want %v", err, tt.wantErr)
			}
			if got := string(s.Bytes()); got != tt.src {
				t.Errorf("bytes changed on a refused splice:\n%q", got)
			}
		})
	}
}

func TestSplicer_SetScalar_KeepsCRLFOnTheTouchedLine(t *testing.T) {
	src := "# header\r\na: 1\r\nb: \"old\"  # note\r\nc: 3\r\n"
	s, _ := newSpliceFixture(t, src)
	if err := s.SetScalar([]string{"b"}, spliceMarker); err != nil {
		t.Fatalf("SetScalar: %v", err)
	}
	after := string(s.Bytes())
	assertSingleLineChange(t, src, after, 3, "b: "+spliceMarker+"  # note\r")
	if strings.Contains(strings.ReplaceAll(after, "\r\n", ""), "\r") {
		t.Errorf("stray carriage return in result: %q", after)
	}
}

func TestSplicer_SetScalar_TwoEditsBothLand(t *testing.T) {
	src := "a: one\n\nb: two\nc: three\n"
	s, _ := newSpliceFixture(t, src)
	if err := s.SetScalar([]string{"a"}, spliceMarker); err != nil {
		t.Fatalf("SetScalar a: %v", err)
	}
	if err := s.SetScalar([]string{"c"}, "second-"+spliceMarker); err != nil {
		t.Fatalf("SetScalar c: %v", err)
	}
	after := string(s.Bytes())
	changed := changedLines(t, src, after)
	if len(changed) != 2 || changed[0] != 0 || changed[1] != 3 {
		t.Fatalf("expected lines 1 and 4 to change, got %v\n%s", changed, after)
	}
	lines := strings.Split(after, "\n")
	if lines[0] != "a: "+spliceMarker {
		t.Errorf("line 1 = %q", lines[0])
	}
	if lines[3] != "c: second-"+spliceMarker {
		t.Errorf("line 4 = %q", lines[3])
	}
}

func TestSplicer_SetScalar_AnnotatedFixtureChangesOneLine(t *testing.T) {
	src := readTestdata(t, "annotated_defaults.yml")
	s, _ := newSpliceFixture(t, src)

	if err := s.SetScalar([]string{"vars", "telegram", "bot_token"}, spliceMarker); err != nil {
		t.Fatalf("SetScalar: %v", err)
	}
	after := string(s.Bytes())

	lines := strings.Split(src, "\n")
	want := -1
	for i, line := range lines {
		if strings.Contains(line, "bot_token:") {
			want = i + 1
			break
		}
	}
	if want < 0 {
		t.Fatal("fixture no longer contains a bot_token key")
	}
	assertSingleLineChange(t, src, after, want, "    bot_token: "+spliceMarker)
}

func TestSplicer_AnnotatedFixture_MergeKeyRules(t *testing.T) {
	src := readTestdata(t, "annotated_defaults.yml")

	// `services.app.restart` is explicit next to a `<<:` merge — replaceable.
	s, _ := newSpliceFixture(t, src)
	if err := s.SetScalar([]string{"services", "app", "restart"}, "no"); err != nil {
		t.Fatalf("explicit key in a merge mapping: %v", err)
	}
	changed := changedLines(t, src, string(s.Bytes()))
	if len(changed) != 1 {
		t.Fatalf("expected one changed line, got %v", changed)
	}
	if got := strings.Split(string(s.Bytes()), "\n")[changed[0]]; got != `    restart: "no"        # overrides the anchor` {
		t.Errorf("changed line = %q", got)
	}

	// `services.db.restart` only exists through the merge — refused.
	s2, _ := newSpliceFixture(t, src)
	err := s2.SetScalar([]string{"services", "db", "restart"}, "no")
	if !errors.Is(err, ErrUnsplicable) {
		t.Fatalf("merge-inherited key error = %v, want ErrUnsplicable", err)
	}
	if string(s2.Bytes()) != src {
		t.Error("bytes changed on a refused splice")
	}

	// The literal block elsewhere in the file is refused, not mangled.
	s3, _ := newSpliceFixture(t, src)
	if err := s3.SetScalar([]string{"vars", "notes"}, "short"); !errors.Is(err, ErrMultilineScalar) {
		t.Fatalf("literal block error = %v, want ErrMultilineScalar", err)
	}
}

func TestNewSplicer_MissingFile(t *testing.T) {
	s, err := NewSplicer(filepath.Join(t.TempDir(), "absent.yml"), LabelDefaults)
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(s.Bytes()) != 0 {
		t.Errorf("expected empty bytes, got %q", s.Bytes())
	}
	if s.doc != nil {
		t.Error("expected no document node for a missing file")
	}
}

func TestNewSplicer_CommentOnlyFileKeepsBytes(t *testing.T) {
	src := "# only a comment\n# and another\n"
	s, _ := newSpliceFixture(t, src)
	if string(s.Bytes()) != src {
		t.Errorf("bytes = %q, want %q", s.Bytes(), src)
	}
	if s.doc != nil {
		t.Error("expected no document node for a comment-only file")
	}
}

func TestNewSplicer_MultiDocumentRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "layer.yml")
	if err := os.WriteFile(path, []byte("a: 1\n---\nb: 2\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := NewSplicer(path, LabelDefaults); err == nil {
		t.Fatal("expected multi-document YAML to be rejected")
	} else if !strings.Contains(err.Error(), "multi-document") {
		t.Errorf("error = %v, want a multi-document message", err)
	}
}

func TestNewSplicer_InvalidYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "layer.yml")
	if err := os.WriteFile(path, []byte("a: [1, 2\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := NewSplicer(path, LabelDefaults); err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestSplicer_WritePersistsAndPreservesMode(t *testing.T) {
	src := readTestdata(t, "annotated_defaults.yml")
	s, path := newSpliceFixture(t, src)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := s.SetScalar([]string{"vars", "telegram", "bot_token"}, spliceMarker); err != nil {
		t.Fatalf("SetScalar: %v", err)
	}
	if err := s.Write(path, PreserveOrDefault(0o600)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(onDisk) != string(s.Bytes()) {
		t.Error("on-disk bytes differ from Bytes()")
	}
	assertSingleLineChange(t, src, string(onDisk), lineOf(t, src, "bot_token:"), "    bot_token: "+spliceMarker)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644 preserved", info.Mode().Perm())
	}
}

func TestSplicer_WriteRefusesWhenVerificationFails(t *testing.T) {
	src := "a: old\nb: 2\n"
	s, path := newSpliceFixture(t, src)
	if err := s.SetScalar([]string{"a"}, spliceMarker); err != nil {
		t.Fatalf("SetScalar: %v", err)
	}
	// Simulate a splice that did not land: the recorded expectation no longer
	// matches the bytes, which is exactly what Write must catch.
	s.sets[0].value = "something-else"

	if err := s.Write(path, PreserveOrDefault(0o600)); !errors.Is(err, ErrVerify) {
		t.Fatalf("Write error = %v, want ErrVerify", err)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(onDisk) != src {
		t.Errorf("file was written despite a failed verification:\n%s", onDisk)
	}
}

func TestSplicer_DetectIndent(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want int
	}{
		{name: "two spaces", src: "vars:\n  a: 1\n", want: 2},
		{name: "four spaces", src: "vars:\n    a: 1\n", want: 4},
		{name: "nested only", src: "a: 1\nvars:\n   deep:\n      b: 2\n", want: 3},
		{name: "flat file falls back", src: "a: 1\nb: 2\n", want: defaultSpliceIndent},
		{name: "empty file falls back", src: "", want: defaultSpliceIndent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newSpliceFixture(t, tt.src)
			if s.indent != tt.want {
				t.Errorf("indent = %d, want %d", s.indent, tt.want)
			}
		})
	}
}

func TestSplicer_DominantEOL(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{name: "lf", src: "a: 1\nb: 2\n", want: "\n"},
		{name: "crlf", src: "a: 1\r\nb: 2\r\n", want: "\r\n"},
		{name: "mostly lf", src: "a: 1\r\nb: 2\nc: 3\nd: 4\n", want: "\n"},
		{name: "empty falls back to lf", src: "", want: "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newSpliceFixture(t, tt.src)
			if s.eol != tt.want {
				t.Errorf("eol = %q, want %q", s.eol, tt.want)
			}
		})
	}
}

// readTestdata loads a fixture file from testdata/.
func readTestdata(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}
	return string(data)
}

// lineOf returns the 1-based line number of the first line containing needle.
func lineOf(t *testing.T, src, needle string) int {
	t.Helper()
	for i, line := range strings.Split(src, "\n") {
		if strings.Contains(line, needle) {
			return i + 1
		}
	}
	t.Fatalf("%q not found in fixture", needle)
	return 0
}
