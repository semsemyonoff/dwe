package local

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
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

func TestSplicer_SetScalar_RefusesAMultiLineNewValue(t *testing.T) {
	// The refusal is symmetric: a target that fits on one line is still not
	// splicable when the VALUE would need several, since the replacement text
	// is written into a single line's span.
	src := "a: 1\nb: 2\n"
	for _, value := range []string{"one\ntwo", "trailing\n"} {
		s, _ := newSpliceFixture(t, src)
		err := s.SetScalar([]string{"a"}, value)
		if !errors.Is(err, ErrMultilineScalar) {
			t.Fatalf("SetScalar(%q) error = %v, want ErrMultilineScalar", value, err)
		}
		if got := string(s.Bytes()); got != src {
			t.Errorf("bytes changed on a refused splice:\n%q", got)
		}
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

// insertedLines asserts that after is before with exactly one contiguous block
// of whole lines inserted, and returns that block with the 0-based line index it
// landed at. Every other line must be byte-identical and in the same order.
func insertedLines(t *testing.T, before, after string) ([]string, int) {
	t.Helper()
	b := strings.Split(before, "\n")
	a := strings.Split(after, "\n")
	if len(a) <= len(b) {
		t.Fatalf("expected inserted lines, count went %d -> %d\n--- after ---\n%s", len(b), len(a), after)
	}
	i := 0
	for i < len(b) && b[i] == a[i] {
		i++
	}
	n := len(a) - len(b)
	for k := i + n; k < len(a); k++ {
		if a[k] != b[k-n] {
			t.Fatalf("after is not before with one contiguous insertion at line %d\n--- before ---\n%s\n--- after ---\n%s", i+1, before, after)
		}
	}
	return a[i : i+n], i
}

// assertInsertion asserts the inserted block equals want and reports where it
// landed.
func assertInsertion(t *testing.T, before, after string, want []string) int {
	t.Helper()
	got, at := insertedLines(t, before, after)
	if len(got) != len(want) {
		t.Fatalf("inserted %d lines %q, want %d %q", len(got), got, len(want), want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("inserted lines = %q, want %q", got, want)
		}
	}
	return at
}

func TestSplicer_SetScalar_InsertsIntoNestedMapping(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		path     []string
		want     []string
		wantLine int // 0-based index the block lands at
	}{
		{
			name:     "after a plain scalar, before a trailing comment and a blank line",
			src:      "vars:\n  token: old\n  # note about vars\n\nnext: 1\n",
			path:     []string{"vars", "new", "key"},
			want:     []string{"  new:", "    key: v"},
			wantLine: 2,
		},
		{
			name:     "after a folded scalar's continuation lines",
			src:      "vars:\n  token: old\n  note: >\n    folded text\n    continues here\nnext: 1\n",
			path:     []string{"vars", "new", "key"},
			want:     []string{"  new:", "    key: v"},
			wantLine: 5,
		},
		{
			name:     "after a literal scalar whose content line starts with a hash",
			src:      "vars:\n  token: old\n  notes: |\n    first\n    # not a comment\n    last\nnext: 1\n",
			path:     []string{"vars", "new", "key"},
			want:     []string{"  new:", "    key: v"},
			wantLine: 6,
		},
		{
			name:     "two missing levels render both",
			src:      "vars:\n  a:\n    keep: 1\nnext: 2\n",
			path:     []string{"vars", "a", "b", "c"},
			want:     []string{"    b:", "      c: v"},
			wantLine: 3,
		},
		{
			name:     "four-space file keeps its indent step",
			src:      "vars:\n    token: old\nnext: 1\n",
			path:     []string{"vars", "new", "key"},
			want:     []string{"    new:", "        key: v"},
			wantLine: 2,
		},
		{
			name:     "mapping at end of file",
			src:      "vars:\n  token: old\n",
			path:     []string{"vars", "new", "key"},
			want:     []string{"  new:", "    key: v"},
			wantLine: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newSpliceFixture(t, tt.src)
			if err := s.SetScalar(tt.path, "v"); err != nil {
				t.Fatalf("SetScalar: %v", err)
			}
			after := string(s.Bytes())
			if at := assertInsertion(t, tt.src, after, tt.want); at != tt.wantLine {
				t.Errorf("inserted at line index %d, want %d\n%s", at, tt.wantLine, after)
			}
			if got, ok := scalarAt(s.doc, tt.path); !ok || got != "v" {
				t.Errorf("value at %v reads back as (%q, %v)", tt.path, got, ok)
			}
		})
	}
}

func TestSplicer_SetScalar_InsertKeepsTrailingCommentOutsideTheMapping(t *testing.T) {
	src := "vars:\n  token: old\n\n# belongs to the next block\nnext: 1\n"
	s, _ := newSpliceFixture(t, src)
	if err := s.SetScalar([]string{"vars", "new"}, "v"); err != nil {
		t.Fatalf("SetScalar: %v", err)
	}
	after := string(s.Bytes())
	assertInsertion(t, src, after, []string{"  new: v"})

	// The comment must still introduce `next`, not trail inside `vars`.
	if !strings.Contains(after, "  new: v\n\n# belongs to the next block\nnext: 1\n") {
		t.Errorf("comment moved:\n%s", after)
	}
}

func TestSplicer_SetScalar_InsertsTopLevelBlockAtEOF(t *testing.T) {
	// A top-level block is appended, so the whole prior file is the prefix: an
	// exact byte comparison is the strongest form of "nothing else changed".
	block := "secrets:\n  recipient: age1xyz\n"
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "blank-line separated file gains a separating blank line",
			src:  "project:\n  name: demo\n\nvars:\n  a: 1\n",
			want: "project:\n  name: demo\n\nvars:\n  a: 1\n\n" + block,
		},
		{
			name: "compact file gains no blank line",
			src:  "project:\n  name: demo\nvars:\n  a: 1\n",
			want: "project:\n  name: demo\nvars:\n  a: 1\n" + block,
		},
		{
			name: "file already ending in a blank line gains no second one",
			src:  "project:\n  name: demo\n\nvars:\n  a: 1\n\n",
			want: "project:\n  name: demo\n\nvars:\n  a: 1\n\n" + block,
		},
		{
			name: "file without a final newline gains one",
			src:  "project:\n  name: demo\nvars:\n  a: 1",
			want: "project:\n  name: demo\nvars:\n  a: 1\n" + block,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newSpliceFixture(t, tt.src)
			if err := s.SetScalar([]string{"secrets", "recipient"}, "age1xyz"); err != nil {
				t.Fatalf("SetScalar: %v", err)
			}
			if got := string(s.Bytes()); got != tt.want {
				t.Errorf("bytes =\n%q\nwant\n%q", got, tt.want)
			}
			if got, ok := scalarAt(s.doc, []string{"secrets", "recipient"}); !ok || got != "age1xyz" {
				t.Errorf("recipient reads back as (%q, %v)", got, ok)
			}
		})
	}
}

func TestSplicer_SetScalar_InsertKeepsKeptBlockScalarValue(t *testing.T) {
	// A `|+` scalar's value swallows every trailing line break in the file, so
	// the cosmetic separator blank line an appended top-level block would
	// normally gain would EXTEND it — a data change verify() cannot see, since
	// it only re-reads the paths this Splicer set.
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "keep-chomped literal loses the cosmetic blank line",
			src:  "project:\n  name: demo\n\nnote: |+\n  body\n",
			want: "project:\n  name: demo\n\nnote: |+\n  body\nsecrets:\n  recipient: age1xyz\n",
		},
		{
			name: "keep-chomped folded loses it too",
			src:  "project:\n  name: demo\n\nnote: >+\n  body\n",
			want: "project:\n  name: demo\n\nnote: >+\n  body\nsecrets:\n  recipient: age1xyz\n",
		},
		{
			name: "clip-chomped literal keeps the house style",
			src:  "project:\n  name: demo\n\nnote: |\n  body\n",
			want: "project:\n  name: demo\n\nnote: |\n  body\n\nsecrets:\n  recipient: age1xyz\n",
		},
		{
			// The `>` in the header comment must not be read as the block marker:
			// matching it would report clip chomping and re-arm the separator.
			name: "keep-chomped header with a comment holding a block marker",
			src:  "project:\n  name: demo\n\nnote: |+ # see a > b\n  body\n",
			want: "project:\n  name: demo\n\nnote: |+ # see a > b\n  body\nsecrets:\n  recipient: age1xyz\n",
		},
		{
			name: "keep-chomped header with an explicit indent indicator",
			src:  "project:\n  name: demo\n\nnote: |2+\n  body\n",
			want: "project:\n  name: demo\n\nnote: |2+\n  body\nsecrets:\n  recipient: age1xyz\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newSpliceFixture(t, tt.src)
			before, ok := scalarAt(s.doc, []string{"note"})
			if !ok {
				t.Fatalf("fixture has no note scalar")
			}
			if err := s.SetScalar([]string{"secrets", "recipient"}, "age1xyz"); err != nil {
				t.Fatalf("SetScalar: %v", err)
			}
			if got := string(s.Bytes()); got != tt.want {
				t.Errorf("bytes =\n%q\nwant\n%q", got, tt.want)
			}
			if after, ok := scalarAt(s.doc, []string{"note"}); !ok || after != before {
				t.Errorf("note changed from %q to (%q, %v)", before, after, ok)
			}
		})
	}
}

func TestSplicer_SetScalar_InsertAfterKeptBlockScalarInMapping(t *testing.T) {
	// The blank lines trailing a `|+` scalar are part of its VALUE. A nested
	// insertion whose anchor mapping ends in one must land after them, or the
	// untouched neighbour silently loses a line break — and verify() cannot see
	// it, because it only re-reads the paths this Splicer set.
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "kept blank line at end of file",
			src:  "vars:\n  telegram:\n    note: |+\n      body\n\n",
			want: "vars:\n  telegram:\n    note: |+\n      body\n\n    token: dwe:secret:v1:XYZ\n",
		},
		{
			name: "kept blank line before a shallower key",
			src:  "vars:\n  telegram:\n    note: |+\n      body\n\nother: y\n",
			want: "vars:\n  telegram:\n    note: |+\n      body\n\n    token: dwe:secret:v1:XYZ\nother: y\n",
		},
		{
			name: "kept blank lines before a trailing comment",
			src:  "vars:\n  telegram:\n    note: |+\n      body\n\n\n# footer\n",
			want: "vars:\n  telegram:\n    note: |+\n      body\n\n\n    token: dwe:secret:v1:XYZ\n# footer\n",
		},
		{
			name: "clip-chomped neighbour keeps the blank line outside the value",
			src:  "vars:\n  telegram:\n    note: |\n      body\n\nother: y\n",
			want: "vars:\n  telegram:\n    note: |\n      body\n    token: dwe:secret:v1:XYZ\n\nother: y\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newSpliceFixture(t, tt.src)
			before, ok := scalarAt(s.doc, []string{"vars", "telegram", "note"})
			if !ok {
				t.Fatalf("fixture has no note scalar")
			}
			if err := s.SetScalar([]string{"vars", "telegram", "token"}, "dwe:secret:v1:XYZ"); err != nil {
				t.Fatalf("SetScalar: %v", err)
			}
			if got := string(s.Bytes()); got != tt.want {
				t.Errorf("bytes =\n%q\nwant\n%q", got, tt.want)
			}
			if after, ok := scalarAt(s.doc, []string{"vars", "telegram", "note"}); !ok || after != before {
				t.Errorf("note changed from %q to (%q, %v)", before, after, ok)
			}
			if err := s.verify(); err != nil {
				t.Errorf("verify: %v", err)
			}
		})
	}
}

func TestSplicer_SetScalar_InsertsIntoAnnotatedFixture(t *testing.T) {
	src := readTestdata(t, "annotated_defaults.yml")
	s, _ := newSpliceFixture(t, src)
	if err := s.SetScalar([]string{"secrets", "recipient"}, "age1xyz"); err != nil {
		t.Fatalf("SetScalar: %v", err)
	}
	want := src + "\nsecrets:\n  recipient: age1xyz\n"
	if got := string(s.Bytes()); got != want {
		t.Errorf("bytes =\n%q\nwant\n%q", got, want)
	}
}

func TestSplicer_SetScalar_InsertsIntoEmptyDocuments(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		write bool // false: the file does not exist at all
	}{
		{name: "missing file"},
		{name: "empty file", src: "", write: true},
		{name: "whitespace-only file", src: "\n\n", write: true},
		{name: "comment-only file", src: "# only a comment\n# and another\n", write: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "layer.yml")
			if tt.write {
				if err := os.WriteFile(path, []byte(tt.src), 0o600); err != nil {
					t.Fatalf("write fixture: %v", err)
				}
			}
			s, err := NewSplicer(path, LabelDefaults)
			if err != nil {
				t.Fatalf("NewSplicer: %v", err)
			}
			if err := s.SetScalar([]string{"secrets", "recipient"}, "age1xyz"); err != nil {
				t.Fatalf("SetScalar: %v", err)
			}
			got := string(s.Bytes())
			want := tt.src + "secrets:\n  recipient: age1xyz\n"
			if got != want {
				t.Errorf("bytes =\n%q\nwant\n%q", got, want)
			}
			if err := s.Write(path, PreserveOrDefault(0o600)); err != nil {
				t.Fatalf("Write: %v", err)
			}
		})
	}
}

func TestSplicer_SetScalar_InsertUsesCRLFWhenTheFileDoes(t *testing.T) {
	src := "a: 1\r\nvars:\r\n  x: 1\r\n"
	s, _ := newSpliceFixture(t, src)
	if err := s.SetScalar([]string{"vars", "new", "key"}, "v"); err != nil {
		t.Fatalf("SetScalar: %v", err)
	}
	after := string(s.Bytes())
	if after != "a: 1\r\nvars:\r\n  x: 1\r\n  new:\r\n    key: v\r\n" {
		t.Errorf("bytes = %q", after)
	}
}

func TestSplicer_SetScalar_InsertRefusedShapes(t *testing.T) {
	tests := []struct {
		name string
		src  string
		path []string
	}{
		{name: "flow mapping parent", src: "vars: {a: 1}\n", path: []string{"vars", "b"}},
		{name: "null parent", src: "vars:\nnext: 1\n", path: []string{"vars", "b"}},
		{name: "sequence parent", src: "vars:\n  - one\nnext: 1\n", path: []string{"vars", "b"}},
		{name: "alias parent", src: "base: &b\n  x: 1\nother: *b\n", path: []string{"other", "y"}},
		{name: "merge-carrying parent", src: "base: &b\n  x: 1\nsvc:\n  <<: *b\n  y: 2\n", path: []string{"svc", "z"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newSpliceFixture(t, tt.src)
			if err := s.SetScalar(tt.path, "v"); !errors.Is(err, ErrUnsplicable) {
				t.Fatalf("SetScalar error = %v, want ErrUnsplicable", err)
			}
			if got := string(s.Bytes()); got != tt.src {
				t.Errorf("bytes changed on a refused insert:\n%q", got)
			}
		})
	}
}

// rekeyMarkers is a ReplaceScalars callback shaped like `dwe secrets rekey`'s:
// it accepts encrypted markers and declines everything else.
func rekeyMarkers(next string) func(string) (string, bool, error) {
	return func(v string) (string, bool, error) {
		if !strings.HasPrefix(v, "ENC[age:") {
			return "", false, nil
		}
		return next, true, nil
	}
}

func TestSplicer_ReplaceScalars_ReplacesEveryAcceptedMarker(t *testing.T) {
	// No trailing newline: the last marker is the file's final bytes.
	src := "# header\nvars:\n  a: ENC[age:AAA]\n  notes: |\n    line one\n    line two\n  b: \"ENC[age:BBB]\"\n  c: ENC[age:CCC]"
	s, _ := newSpliceFixture(t, src)

	n, err := s.ReplaceScalars(rekeyMarkers(spliceMarker))
	if err != nil {
		t.Fatalf("ReplaceScalars: %v", err)
	}
	if n != 3 {
		t.Fatalf("replaced %d scalars, want 3", n)
	}
	after := string(s.Bytes())
	changed := changedLines(t, src, after)
	if len(changed) != 3 {
		t.Fatalf("expected three changed lines, got %v\n%s", changed, after)
	}
	lines := strings.Split(after, "\n")
	for _, i := range changed {
		if !strings.HasSuffix(lines[i], spliceMarker) {
			t.Errorf("line %d = %q, want it to end with the new marker", i+1, lines[i])
		}
	}
	if strings.HasSuffix(after, "\n") {
		t.Error("a trailing newline was added to a file that had none")
	}
	if !strings.Contains(after, "  notes: |\n    line one\n    line two\n") {
		t.Errorf("the declined literal block was disturbed:\n%s", after)
	}
}

func TestSplicer_ReplaceScalars_BlockSequenceElement(t *testing.T) {
	src := "tokens:\n  - ENC[age:AAA]\n  - plain\n"
	s, path := newSpliceFixture(t, src)

	n, err := s.ReplaceScalars(rekeyMarkers(spliceMarker))
	if err != nil {
		t.Fatalf("ReplaceScalars: %v", err)
	}
	if n != 1 {
		t.Fatalf("replaced %d scalars, want 1", n)
	}
	assertSingleLineChange(t, src, string(s.Bytes()), 2, "  - "+spliceMarker)
	if err := s.Write(path, PreserveOrDefault(0o600)); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

func TestSplicer_ReplaceScalars_RefusalsLeaveBytesUnchanged(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		fn      func(string) (string, bool, error)
		wantErr error
	}{
		{
			name:    "marker inside a flow sequence",
			src:     "tokens: [\"ENC[age:AAA]\", plain]\n",
			fn:      rekeyMarkers(spliceMarker),
			wantErr: ErrUnsplicable,
		},
		{
			name:    "marker inside a flow mapping",
			src:     "vars: {a: \"ENC[age:AAA]\"}\n",
			fn:      rekeyMarkers(spliceMarker),
			wantErr: ErrUnsplicable,
		},
		{
			name:    "accepted literal block scalar",
			src:     "a: |\n  ENC[age:AAA]\nb: 1\n",
			fn:      func(string) (string, bool, error) { return spliceMarker, true, nil },
			wantErr: ErrMultilineScalar,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newSpliceFixture(t, tt.src)
			n, err := s.ReplaceScalars(tt.fn)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ReplaceScalars error = %v, want %v", err, tt.wantErr)
			}
			if n != 0 {
				t.Errorf("count = %d, want 0", n)
			}
			if got := string(s.Bytes()); got != tt.src {
				t.Errorf("bytes changed on a refused bulk replace:\n%q", got)
			}
		})
	}
}

func TestSplicer_ReplaceScalars_CallbackErrorAborts(t *testing.T) {
	src := "a: ENC[age:AAA]\nb: ENC[age:BBB]\n"
	s, _ := newSpliceFixture(t, src)
	sentinel := errors.New("encrypt failed")

	n, err := s.ReplaceScalars(func(v string) (string, bool, error) {
		if v == "ENC[age:BBB]" {
			return "", false, sentinel
		}
		return spliceMarker, true, nil
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want the callback's error", err)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
	if got := string(s.Bytes()); got != src {
		t.Errorf("bytes changed after a failed callback:\n%q", got)
	}
}

func TestSplicer_ReplaceScalars_SkipsAliasesAndKeys(t *testing.T) {
	src := "anchor: &a ENC[age:AAA]\nalias: *a\n\"ENC[age:KEY]\": mapping-key-stays\n"
	s, _ := newSpliceFixture(t, src)

	n, err := s.ReplaceScalars(rekeyMarkers(spliceMarker))
	if err != nil {
		t.Fatalf("ReplaceScalars: %v", err)
	}
	if n != 1 {
		t.Fatalf("replaced %d scalars, want 1 (the anchored definition only)", n)
	}
	assertSingleLineChange(t, src, string(s.Bytes()), 1, "anchor: &a "+spliceMarker)
}

func TestSplicer_ReplaceScalars_AnnotatedFixtureKeepsEverythingElse(t *testing.T) {
	src := readTestdata(t, "annotated_defaults.yml")
	s, _ := newSpliceFixture(t, src)

	n, err := s.ReplaceScalars(func(v string) (string, bool, error) {
		if v != "placeholder-token" {
			return "", false, nil
		}
		return spliceMarker, true, nil
	})
	if err != nil {
		t.Fatalf("ReplaceScalars: %v", err)
	}
	if n != 1 {
		t.Fatalf("replaced %d scalars, want 1", n)
	}
	assertSingleLineChange(t, src, string(s.Bytes()), lineOf(t, src, "bot_token:"), "    bot_token: "+spliceMarker)
}

func TestSplicer_ReplaceScalars_WriteVerificationFailureLeavesFileUntouched(t *testing.T) {
	src := "a: ENC[age:AAA]\nb: 2\n"
	s, path := newSpliceFixture(t, src)
	if _, err := s.ReplaceScalars(rekeyMarkers(spliceMarker)); err != nil {
		t.Fatalf("ReplaceScalars: %v", err)
	}
	// Simulate a splice that did not land where the writer recorded it.
	s.bulk[0].value = "something-else"

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

// TestSplicer_ReplaceScalars_RekeyPrimitiveShape is the rekey contract the node
// writer's ReplaceScalars used to carry, ported when `secrets rekey` moved onto
// the Splicer: every value scalar the callback accepts is rewritten exactly once
// across an anchor, a block sequence element and a nested mapping; mapping keys
// are never offered; and the document still reloads with the aliases resolving
// to the rewritten anchor.
func TestSplicer_ReplaceScalars_RekeyPrimitiveShape(t *testing.T) {
	src := `# header
vars:
  tok: &tok OLD-A # inline
  alias: *tok
  list:
    - OLD-B
    - plain
  nested:
    k: OLD-C
  OLD-KEY: kept
`
	s, path := newSpliceFixture(t, src)

	var seen []string
	n, err := s.ReplaceScalars(func(v string) (string, bool, error) {
		if !strings.HasPrefix(v, "OLD-") {
			return "", false, nil
		}
		seen = append(seen, v)
		return "NEW-" + strings.TrimPrefix(v, "OLD-"), true, nil
	})
	if err != nil {
		t.Fatalf("ReplaceScalars: %v", err)
	}
	if n != 3 {
		t.Fatalf("replaced %d scalars, want 3 (anchor, sequence item, nested)", n)
	}
	slices.Sort(seen)
	if strings.Join(seen, ",") != "OLD-A,OLD-B,OLD-C" {
		t.Errorf("visited scalars = %v; want each value once and no mapping key", seen)
	}

	after := string(s.Bytes())
	changed := changedLines(t, src, after)
	if len(changed) != 3 {
		t.Fatalf("changed lines = %v, want exactly three\n%s", changed, after)
	}
	lines := strings.Split(after, "\n")
	for i, want := range map[int]string{
		lineOf(t, src, "tok: &tok"): "  tok: &tok NEW-A # inline",
		lineOf(t, src, "- OLD-B"):   "    - NEW-B",
		lineOf(t, src, "k: OLD-C"):  "    k: NEW-C",
	} {
		if got := lines[i-1]; got != want {
			t.Errorf("line %d = %q, want %q", i, got, want)
		}
	}
	if !strings.Contains(after, "  OLD-KEY: kept\n") {
		t.Errorf("a mapping key was rewritten:\n%s", after)
	}

	if err := s.Write(path, PreserveOrDefault(0o600)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	reloaded, err := LoadLocalYAML(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	vars := reloaded["vars"].(map[string]any)
	if vars["alias"] != "NEW-A" {
		t.Errorf("alias no longer resolves to the rewritten anchor: %v", vars["alias"])
	}
}

// A nil replacement function is a no-op, not a panic (ported from the node
// writer's TestReplaceScalars_NilInputs; a nil document is not reachable through
// a Splicer, which always carries its own parse).
func TestSplicer_ReplaceScalars_NilCallback(t *testing.T) {
	src := "a: 1\n"
	s, _ := newSpliceFixture(t, src)
	n, err := s.ReplaceScalars(nil)
	if err != nil {
		t.Fatalf("nil callback error = %v, want nil", err)
	}
	if n != 0 {
		t.Errorf("nil callback count = %d, want 0", n)
	}
	if got := string(s.Bytes()); got != src {
		t.Errorf("bytes changed for a nil callback: %q", got)
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
