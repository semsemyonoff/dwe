package local

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// This file implements the position-guided byte-splice writer for dwe's YAML
// config layer files. Where the node writer (local_node.go) re-encodes the whole
// document — which reformats indentation, drops blank lines and rewrites merge
// keys — the Splicer changes only the bytes of the nodes it edits. Every other
// byte (indentation, blank lines, comments, anchors, merge keys, quoting, line
// endings) survives verbatim, so `dwe secrets set/init/rekey` on a large
// hand-annotated layer file produces a reviewable one-line diff.
//
// yaml.v3 is still the parser: it locates the target node and reports its
// (Line, Column). Those coordinates are a LOCATOR, not a byte offset — yaml.v3
// counts columns in runes, and it reports a node's position at the start of its
// properties (`&anchor`, `!tag`), not at the value. The Splicer converts the
// coordinate through a line-start table, skips the raw properties, and derives
// the value token's span from the node's Style.
//
// Shapes whose span cannot be derived from a single line — literal/folded
// scalars, plain or quoted scalars wrapped over several lines, anything inside
// a flow collection — are refused (ErrMultilineScalar / ErrUnsplicable) with the
// file left untouched, rather than guessed at.

// Sentinel errors for splice refusals. The CLI maps all three onto the
// `secrets_write_unsupported` error code.
var (
	// ErrMultilineScalar reports a scalar whose text does not live on a single
	// line (`|`, `>`, or a wrapped plain/quoted scalar).
	ErrMultilineScalar = errors.New("value does not fit on a single line")
	// ErrUnsplicable reports a document shape the splice writer refuses to
	// edit: a flow collection, an alias, a null or non-mapping parent, or a key
	// that may be inherited through a YAML merge key.
	ErrUnsplicable = errors.New("value cannot be edited in place")
	// ErrVerify reports that the spliced bytes did not read back as requested.
	// The caller's file is never written when this fires.
	ErrVerify = errors.New("spliced document failed verification")
)

// defaultSpliceIndent is used when the file has no nested block mapping to
// measure an indent step from.
const defaultSpliceIndent = 2

// Splicer edits a layer file by replacing the bytes of individual scalar nodes,
// leaving every other byte untouched. It is the writer for edits whose diff must
// be reviewable (`dwe secrets set` / `init` / `rekey`); ApplyOverlay +
// WriteYAMLNode remain the writer for structural edits (`dwe vars set`, the
// services toggle, the setup wizard).
//
// A Splicer is not safe for concurrent use.
type Splicer struct {
	path  string
	label string

	src []byte
	// doc is nil for a missing, empty or comment-only file; src is still kept
	// verbatim in that case.
	doc    *yaml.Node
	lines  []int // byte offset of each line start; line N is lines[N-1]
	eol    string
	indent int

	sets []spliceSet
}

// spliceSet records a completed SetScalar so Write can verify it reads back.
type spliceSet struct {
	path  []string
	value string
}

// NewSplicer reads and parses path. A missing, empty or comment-only file is not
// an error — it yields a Splicer with no document node and the source bytes kept
// verbatim. Multi-document YAML is rejected, as it is by LoadYAMLNode: a dwe
// config layer is always a single document.
func NewSplicer(path, label string) (*Splicer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		data = nil
	}
	s := &Splicer{path: path, label: label, src: data}
	if err := s.reparse(); err != nil {
		return nil, err
	}
	return s, nil
}

// Bytes returns a copy of the current document bytes, including every edit
// applied so far.
func (s *Splicer) Bytes() []byte {
	return bytes.Clone(s.src)
}

// Write verifies the spliced bytes and persists them atomically. Verification
// re-parses the result and checks that every SetScalar reads back as the
// requested value; on failure the target file is left untouched.
func (s *Splicer) Write(path string, policy WritePolicy) error {
	if err := s.verify(); err != nil {
		return err
	}
	return writeFileAtomic(path, s.src, policy)
}

// SetScalar sets the scalar at the dotted path to value, replacing only the
// bytes of that value token. The path is a chain of mapping keys.
func (s *Splicer) SetScalar(path []string, value string) error {
	if len(path) == 0 {
		return fmt.Errorf("%w: empty path in %s", ErrUnsplicable, s.path)
	}
	val, _, err := s.lookupPath(path)
	if err != nil {
		return err
	}
	if val == nil {
		return fmt.Errorf("%w: %q is not present in %s", ErrUnsplicable, strings.Join(path, "."), s.path)
	}
	if err := s.replaceScalarNode(val, path, value); err != nil {
		return err
	}
	s.recordSet(path, value)
	return nil
}

// replaceScalarNode splices value over the value token of node.
func (s *Splicer) replaceScalarNode(node *yaml.Node, path []string, value string) error {
	start, end, err := s.scalarSpan(node, path)
	if err != nil {
		return err
	}
	text, err := renderScalarText(value)
	if err != nil {
		return fmt.Errorf("encode value at %q: %w", strings.Join(path, "."), err)
	}
	if strings.ContainsAny(text, "\n\r") {
		return fmt.Errorf("%w: the new value at %q cannot be written on one line", ErrMultilineScalar, strings.Join(path, "."))
	}
	// An empty span means the node is an implicit null (`key:` with nothing
	// after the colon); the rendered value needs its own separating space.
	if start == end && start > 0 && s.src[start-1] != ' ' && s.src[start-1] != '\t' {
		text = " " + text
	}
	return s.applySplice(start, end, text)
}

// recordSet remembers a completed set for Write's verification, last write wins.
func (s *Splicer) recordSet(path []string, value string) {
	key := strings.Join(path, "\x00")
	for i := range s.sets {
		if strings.Join(s.sets[i].path, "\x00") == key {
			s.sets[i].value = value
			return
		}
	}
	s.sets = append(s.sets, spliceSet{path: append([]string(nil), path...), value: value})
}

// lookupPath resolves path against the document root. It returns the value node
// for the full path (nil when the leaf key is absent) and the mapping that would
// hold it. Shapes the splice writer refuses — flow collections, aliases,
// non-mapping parents, keys that may be merge-inherited — are errors.
func (s *Splicer) lookupPath(path []string) (val, parent *yaml.Node, err error) {
	node := spliceRoot(s.doc)
	if node == nil {
		return nil, nil, nil
	}
	for i, seg := range path {
		dotted := strings.Join(path[:i+1], ".")
		switch {
		case node.Kind == yaml.AliasNode:
			return nil, nil, fmt.Errorf("%w: %q is reached through a YAML alias in %s; materialize it as a block mapping first", ErrUnsplicable, dotted, s.path)
		case node.Kind != yaml.MappingNode:
			return nil, nil, fmt.Errorf("%w: cannot descend into the %s value at %q in %s", ErrUnsplicable, kindName(node.Kind), dotted, s.path)
		case node.Style&yaml.FlowStyle != 0:
			return nil, nil, fmt.Errorf("%w: %q sits in a flow mapping in %s; write it as a block collection first", ErrUnsplicable, dotted, s.path)
		}
		_, next := findMappingPair(node, seg)
		if next == nil {
			// A key absent from the explicit pairs may still be supplied by a
			// `<<: *anchor` merge key. Writing an explicit key here would
			// silently shadow the merge-inherited value, so refuse — same rule
			// as applyOverlayToMapping.
			if mappingHasMergeKey(node) {
				return nil, nil, fmt.Errorf("%w: cannot set %q: parent mapping uses a YAML merge key (<<) and the key may be merge-inherited; materialize it explicitly in %s first", ErrUnsplicable, dotted, s.path)
			}
			return nil, node, nil
		}
		if i == len(path)-1 {
			if next.Kind != yaml.ScalarNode {
				return nil, nil, fmt.Errorf("%w: cannot replace the %s value at %q in %s with a scalar", ErrUnsplicable, kindName(next.Kind), dotted, s.path)
			}
			return next, node, nil
		}
		node = next
	}
	return nil, nil, nil
}

// scalarSpan returns the [start, end) byte range of node's value token, with its
// properties (`&anchor`, `!tag`) and any trailing comment left outside the span.
func (s *Splicer) scalarSpan(node *yaml.Node, path []string) (int, int, error) {
	dotted := strings.Join(path, ".")
	if node.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return 0, 0, fmt.Errorf("%w: %q is a block scalar in %s; write it as a single-line value first", ErrMultilineScalar, dotted, s.path)
	}
	off, err := s.offsetAt(node.Line, node.Column)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: cannot locate %q in %s: %v", ErrUnsplicable, dotted, s.path, err)
	}
	_, contentEnd := s.lineBounds(off)
	off = s.skipProperties(off, contentEnd)

	var end int
	var ok bool
	switch {
	case node.Style&yaml.DoubleQuotedStyle != 0:
		end, ok = scanDoubleQuoted(s.src, off, contentEnd)
		ok = ok && quotedSpanMatches(s.src[off:end], node.Value)
	case node.Style&yaml.SingleQuotedStyle != 0:
		end, ok = scanSingleQuoted(s.src, off, contentEnd)
		ok = ok && quotedSpanMatches(s.src[off:end], node.Value)
	default:
		end = plainEnd(s.src, off, contentEnd)
		ok = string(s.src[off:end]) == node.Value
	}
	if !ok {
		// yaml.v3 exposes no end position and Style cannot tell a wrapped plain
		// scalar from a single-line one, so the span text having to equal the
		// decoded value IS the multi-line detector.
		return 0, 0, fmt.Errorf("%w: %q spans more than one line in %s; write it as a single-line value first", ErrMultilineScalar, dotted, s.path)
	}
	return off, end, nil
}

// applySplice replaces src[start:end) with text and re-parses. A splice that
// produces unparseable YAML is rolled back.
func (s *Splicer) applySplice(start, end int, text string) error {
	prev := s.src
	next := make([]byte, 0, len(prev)-(end-start)+len(text))
	next = append(next, prev[:start]...)
	next = append(next, text...)
	next = append(next, prev[end:]...)
	s.src = next
	if err := s.reparse(); err != nil {
		s.src = prev
		if rollback := s.reparse(); rollback != nil {
			return fmt.Errorf("%w: %v", ErrVerify, rollback)
		}
		return fmt.Errorf("%w: %v", ErrVerify, err)
	}
	return nil
}

// verify re-parses the current bytes and checks every recorded set reads back.
func (s *Splicer) verify() error {
	probe := &Splicer{path: s.path, label: s.label, src: s.src}
	if err := probe.reparse(); err != nil {
		return fmt.Errorf("%w: %v", ErrVerify, err)
	}
	for _, set := range s.sets {
		got, found := scalarAt(probe.doc, set.path)
		if !found || got != set.value {
			return fmt.Errorf("%w: %q does not read back as the requested value in %s", ErrVerify, strings.Join(set.path, "."), s.path)
		}
	}
	return nil
}

// reparse rebuilds the parsed view and the byte/line metadata from src.
func (s *Splicer) reparse() error {
	s.lines = lineStarts(s.src)
	s.eol = dominantEOL(s.src)

	dec := yaml.NewDecoder(bytes.NewReader(s.src))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			// Missing, empty or comment-only file: no node to anchor edits to,
			// but the bytes are kept so an insertion can preserve the comments.
			s.doc = nil
			s.indent = defaultSpliceIndent
			return nil
		}
		return fmt.Errorf("parse %s: %w", s.path, err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return fmt.Errorf("parse %s: multi-document YAML is not supported; %s must be a single document", s.path, s.label)
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("parse %s: %w", s.path, err)
	}
	s.doc = &doc
	s.indent = detectIndent(&doc)
	return nil
}

// offsetAt converts a yaml.v3 (line, column) coordinate into a byte offset.
// Columns are counted in runes by yaml.v3's scanner, so they are advanced rune
// by rune from the line start.
func (s *Splicer) offsetAt(line, col int) (int, error) {
	if line < 1 || line > len(s.lines) {
		return 0, fmt.Errorf("line %d out of range", line)
	}
	off := s.lines[line-1]
	_, end := s.lineBounds(off)
	for i := 1; i < col; i++ {
		if off >= end {
			return 0, fmt.Errorf("column %d past end of line %d", col, line)
		}
		_, size := utf8.DecodeRune(s.src[off:end])
		off += size
	}
	return off, nil
}

// lineBounds returns the start offset of the line containing off and the offset
// just past its last content byte (the trailing "\r" of a CRLF line is content
// end, not content).
func (s *Splicer) lineBounds(off int) (start, contentEnd int) {
	start = bytes.LastIndexByte(s.src[:off], '\n') + 1
	if idx := bytes.IndexByte(s.src[off:], '\n'); idx >= 0 {
		contentEnd = off + idx
	} else {
		contentEnd = len(s.src)
	}
	if contentEnd > start && s.src[contentEnd-1] == '\r' {
		contentEnd--
	}
	return start, contentEnd
}

// skipProperties advances past a node's raw `&anchor` and `!tag` tokens, which
// yaml.v3 includes in the reported node position but which must be preserved.
func (s *Splicer) skipProperties(off, limit int) int {
	for off < limit && (s.src[off] == '&' || s.src[off] == '!') {
		j := off
		for j < limit && s.src[j] != ' ' && s.src[j] != '\t' {
			j++
		}
		for j < limit && (s.src[j] == ' ' || s.src[j] == '\t') {
			j++
		}
		off = j
	}
	return off
}

// scanDoubleQuoted returns the offset just past the closing quote of a
// double-quoted scalar starting at off, honouring backslash escapes. The closing
// quote must lie on the same line.
func scanDoubleQuoted(src []byte, off, limit int) (int, bool) {
	if off >= limit || src[off] != '"' {
		return 0, false
	}
	for i := off + 1; i < limit; i++ {
		switch src[i] {
		case '\\':
			i++
		case '"':
			return i + 1, true
		}
	}
	return 0, false
}

// scanSingleQuoted returns the offset just past the closing quote of a
// single-quoted scalar starting at off, treating `”` as an escaped quote. The
// closing quote must lie on the same line.
func scanSingleQuoted(src []byte, off, limit int) (int, bool) {
	if off >= limit || src[off] != '\'' {
		return 0, false
	}
	for i := off + 1; i < limit; i++ {
		if src[i] != '\'' {
			continue
		}
		if i+1 < limit && src[i+1] == '\'' {
			i++
			continue
		}
		return i + 1, true
	}
	return 0, false
}

// plainEnd returns the end offset of a plain scalar starting at off: end of the
// line's content, minus a trailing ` #` comment and any trailing blanks. A `#`
// only starts a comment when it follows whitespace.
func plainEnd(src []byte, off, limit int) int {
	end := limit
	for i := off + 1; i < limit; i++ {
		if src[i] == '#' && (src[i-1] == ' ' || src[i-1] == '\t') {
			end = i
			break
		}
	}
	for end > off && (src[end-1] == ' ' || src[end-1] == '\t') {
		end--
	}
	return end
}

// quotedSpanMatches reports whether the raw quoted span decodes to want. Letting
// yaml.v3 unquote the candidate keeps escape handling in one place.
func quotedSpanMatches(span []byte, want string) bool {
	var doc yaml.Node
	if err := yaml.Unmarshal(span, &doc); err != nil {
		return false
	}
	root := spliceRoot(&doc)
	if root == nil || root.Kind != yaml.ScalarNode {
		return false
	}
	return root.Value == want
}

// renderScalarText renders value the way the node writer would, as a single
// line: an ambiguous string ("true", "42") comes back quoted so it reloads as a
// string, everything else stays plain.
func renderScalarText(value string) (string, error) {
	node, err := encodeValueNode(value)
	if err != nil {
		return "", err
	}
	out, err := yaml.Marshal(node)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// spliceRoot returns the root content node of a document node, or nil when there
// is none.
func spliceRoot(doc *yaml.Node) *yaml.Node {
	if doc == nil {
		return nil
	}
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return nil
		}
		return doc.Content[0]
	}
	return doc
}

// scalarAt reads the scalar value at a dotted path, for Write's verification.
func scalarAt(doc *yaml.Node, path []string) (string, bool) {
	node := spliceRoot(doc)
	for i, seg := range path {
		if node == nil || node.Kind != yaml.MappingNode {
			return "", false
		}
		_, next := findMappingPair(node, seg)
		if next == nil {
			return "", false
		}
		if i == len(path)-1 {
			if next.Kind != yaml.ScalarNode {
				return "", false
			}
			return next.Value, true
		}
		node = next
	}
	return "", false
}

// lineStarts records the byte offset of every line start in src.
func lineStarts(src []byte) []int {
	starts := make([]int, 1, bytes.Count(src, []byte("\n"))+1)
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// dominantEOL reports the line ending used by the majority of src's lines, so
// inserted lines match the file rather than the platform.
func dominantEOL(src []byte) string {
	lf := bytes.Count(src, []byte("\n"))
	crlf := bytes.Count(src, []byte("\r\n"))
	if crlf*2 > lf {
		return "\r\n"
	}
	return "\n"
}

// detectIndent measures the indent step of the file from its first nested block
// mapping pair, falling back to two spaces.
func detectIndent(doc *yaml.Node) int {
	if n := indentFromMapping(spliceRoot(doc)); n > 0 {
		return n
	}
	return defaultSpliceIndent
}

func indentFromMapping(m *yaml.Node) int {
	if m == nil || m.Kind != yaml.MappingNode || m.Style&yaml.FlowStyle != 0 {
		return 0
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		key, val := m.Content[i], m.Content[i+1]
		if val.Kind != yaml.MappingNode || val.Style&yaml.FlowStyle != 0 || len(val.Content) == 0 {
			continue
		}
		if delta := val.Content[0].Column - key.Column; delta > 0 {
			return delta
		}
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if n := indentFromMapping(m.Content[i+1]); n > 0 {
			return n
		}
	}
	return 0
}
