package local

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
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
//
// A key the file does not hold yet is INSERTED rather than replaced: at the end
// of the file for a new top-level block, otherwise after the physical end of the
// nearest existing ancestor mapping. That end is found on the RAW lines (blank,
// comment and deeper-indented lines belong to the mapping), because a block
// scalar's continuation lines are invisible in the decoded node.

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
	bulk []spliceBulk
}

// spliceSet records a completed SetScalar so Write can verify it reads back.
type spliceSet struct {
	path  []string
	value string
}

// spliceBulk records one accepted ReplaceScalars edit for Write's verification.
// It is keyed by the dotted node path (sequence elements by index) rather than
// by walk position, so a later SetScalar insertion cannot invalidate it.
type spliceBulk struct {
	dotted string
	value  string
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

// SetScalar sets the scalar at the dotted path to value. An existing scalar has
// only the bytes of its value token replaced; a path the file does not hold yet
// is inserted as a block subtree. The path is a chain of mapping keys.
func (s *Splicer) SetScalar(path []string, value string) error {
	if len(path) == 0 {
		return fmt.Errorf("%w: empty path in %s", ErrUnsplicable, s.path)
	}
	val, parent, missIdx, err := s.lookupPath(path)
	if err != nil {
		return err
	}
	if val != nil {
		err = s.replaceScalarNode(val, path, value)
	} else {
		err = s.insertScalar(parent, path, missIdx, value)
	}
	if err != nil {
		return err
	}
	s.recordSet(path, value)
	return nil
}

// replaceScalarNode splices value over the value token of node.
func (s *Splicer) replaceScalarNode(node *yaml.Node, path []string, value string) error {
	start, end, text, err := s.valueSplice(node, path, value)
	if err != nil {
		return err
	}
	return s.applySplice(start, end, text)
}

// valueSplice resolves the byte span of node's value token and the replacement
// text for value, without touching src.
func (s *Splicer) valueSplice(node *yaml.Node, path []string, value string) (start, end int, text string, err error) {
	start, end, err = s.scalarSpan(node, path)
	if err != nil {
		return 0, 0, "", err
	}
	text, err = renderScalarText(value)
	if err != nil {
		return 0, 0, "", fmt.Errorf("encode value at %q: %w", strings.Join(path, "."), err)
	}
	if strings.ContainsAny(text, "\n\r") {
		return 0, 0, "", fmt.Errorf("%w: the new value at %q cannot be written on one line", ErrMultilineScalar, strings.Join(path, "."))
	}
	// An empty span means the node is an implicit null (`key:` with nothing
	// after the colon); the rendered value needs its own separating space.
	if start == end && start > 0 && s.src[start-1] != ' ' && s.src[start-1] != '\t' {
		text = " " + text
	}
	return start, end, text, nil
}

// insertScalar writes the missing tail of path (path[missIdx:]) as a new block
// subtree. parent is the deepest existing mapping, or nil when the file holds no
// document at all.
func (s *Splicer) insertScalar(parent *yaml.Node, path []string, missIdx int, value string) error {
	column := 1
	if parent != nil && len(parent.Content) > 0 {
		column = parent.Content[0].Column
	}
	block, err := s.renderBlock(path[missIdx:], value, column)
	if err != nil {
		return fmt.Errorf("encode value at %q: %w", strings.Join(path, "."), err)
	}

	var off int
	var lead string
	switch {
	case parent == nil || missIdx == 0:
		// A new top-level block (or a file with no document at all) goes at the
		// end: appending never disturbs the reading order of what is already
		// there, and there is no enclosing mapping whose end to find.
		off = len(s.src)
		if off > 0 && !endsWithNewline(s.src) {
			lead = s.eol
		}
		if parent != nil && s.topLevelBlocksSeparated() && !s.endsWithBlankLine() {
			lead += s.eol
		}
	default:
		off = s.mappingInsertOffset(parent)
		if off == len(s.src) && off > 0 && !endsWithNewline(s.src) {
			lead = s.eol
		}
	}
	return s.applySplice(off, off, lead+block)
}

// renderBlock renders the key chain and its leaf value as block YAML indented to
// start at column, using the file's own indent step and line ending.
func (s *Splicer) renderBlock(keys []string, value string, column int) (string, error) {
	node, err := encodeValueNode(value)
	if err != nil {
		return "", err
	}
	for _, key := range slices.Backward(keys) {
		node = &yaml.Node{
			Kind:    yaml.MappingNode,
			Tag:     "!!map",
			Content: []*yaml.Node{scalarKeyNode(key), node},
		}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(s.indent)
	if err := enc.Encode(node); err != nil {
		_ = enc.Close()
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}

	pad := strings.Repeat(" ", column-1)
	var out strings.Builder
	for line := range strings.SplitSeq(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line != "" {
			out.WriteString(pad)
		}
		out.WriteString(line)
		out.WriteString(s.eol)
	}
	return out.String(), nil
}

// mappingInsertOffset returns the byte offset at which a new pair for mapping
// belongs: the start of the line after the mapping's physical end.
//
// The end is derived from the raw lines rather than from the decoded nodes,
// because a block scalar's continuation lines carry no node of their own. A line
// is tested for "indented deeper than the mapping's keys" BEFORE "is a comment",
// so a literal scalar's content line that happens to start with `#` counts as
// part of the block. Blank and shallower comment lines are scanned past but do
// not extend the mapping, which keeps a trailing comment attached to whatever
// follows the mapping.
func (s *Splicer) mappingInsertOffset(mapping *yaml.Node) int {
	keyColumn := mapping.Content[0].Column
	lastContent := maxNodeLine(mapping)
scan:
	for line := lastContent + 1; line <= len(s.lines); line++ {
		text := s.lineText(line)
		trimmed := strings.TrimLeft(text, " \t")
		switch {
		case trimmed == "":
		case len(text)-len(trimmed) >= keyColumn:
			lastContent = line
		case strings.HasPrefix(trimmed, "#"):
		default:
			break scan // a shallower key ends the mapping
		}
	}
	if lastContent < len(s.lines) {
		return s.lines[lastContent]
	}
	return len(s.src)
}

// topLevelBlocksSeparated reports whether the file already puts a blank line
// before some top-level key, so an appended block matches the house style.
func (s *Splicer) topLevelBlocksSeparated() bool {
	blank := false
	for line := 1; line <= len(s.lines); line++ {
		text := s.lineText(line)
		switch {
		case strings.TrimSpace(text) == "":
			blank = true
		case text[0] == ' ' || text[0] == '\t' || text[0] == '#':
			blank = false
		case blank:
			return true
		default:
			blank = false
		}
	}
	return false
}

// endsWithBlankLine reports whether the file's last line is empty.
func (s *Splicer) endsWithBlankLine() bool {
	trimmed := strings.TrimSuffix(strings.TrimSuffix(string(s.src), "\n"), "\r")
	return strings.HasSuffix(trimmed, "\n")
}

// lineText returns the content of a 1-based line, without its line ending.
func (s *Splicer) lineText(line int) string {
	start := s.lines[line-1]
	_, end := s.lineBounds(start)
	return string(s.src[start:end])
}

// maxNodeLine returns the highest line number reported anywhere in node's
// subtree.
func maxNodeLine(node *yaml.Node) int {
	if node == nil {
		return 0
	}
	last := node.Line
	for _, child := range node.Content {
		if n := maxNodeLine(child); n > last {
			last = n
		}
	}
	return last
}

// endsWithNewline reports whether src's last byte terminates a line.
func endsWithNewline(src []byte) bool {
	return len(src) > 0 && src[len(src)-1] == '\n'
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
// for the full path (nil when some segment is absent), the deepest existing
// mapping (nil when the file holds no document), and the index of the first
// missing segment. Shapes the splice writer refuses — flow collections, aliases,
// non-mapping parents, keys that may be merge-inherited — are errors.
func (s *Splicer) lookupPath(path []string) (val, parent *yaml.Node, missIdx int, err error) {
	node := spliceRoot(s.doc)
	if node == nil {
		return nil, nil, 0, nil
	}
	for i, seg := range path {
		dotted := strings.Join(path[:i+1], ".")
		switch {
		case node.Kind == yaml.AliasNode:
			return nil, nil, 0, fmt.Errorf("%w: %q is reached through a YAML alias in %s; materialize it as a block mapping first", ErrUnsplicable, dotted, s.path)
		case node.Kind != yaml.MappingNode:
			where := "the document root"
			if i > 0 {
				where = fmt.Sprintf("%q", strings.Join(path[:i], "."))
			}
			return nil, nil, 0, fmt.Errorf("%w: %s in %s is a %s, not a block mapping; materialize it as a block mapping first", ErrUnsplicable, where, s.path, kindName(node.Kind))
		case node.Style&yaml.FlowStyle != 0:
			return nil, nil, 0, fmt.Errorf("%w: %q sits in a flow mapping in %s; write it as a block collection first", ErrUnsplicable, dotted, s.path)
		}
		_, next := findMappingPair(node, seg)
		if next == nil {
			// A key absent from the explicit pairs may still be supplied by a
			// `<<: *anchor` merge key. Writing an explicit key here would
			// silently shadow the merge-inherited value, so refuse — same rule
			// as applyOverlayToMapping.
			if mappingHasMergeKey(node) {
				return nil, nil, 0, fmt.Errorf("%w: cannot set %q: parent mapping uses a YAML merge key (<<) and the key may be merge-inherited; materialize it explicitly in %s first", ErrUnsplicable, dotted, s.path)
			}
			return nil, node, i, nil
		}
		if i == len(path)-1 {
			if next.Kind != yaml.ScalarNode {
				return nil, nil, 0, fmt.Errorf("%w: cannot replace the %s value at %q in %s with a scalar", ErrUnsplicable, kindName(next.Kind), dotted, s.path)
			}
			return next, node, 0, nil
		}
		node = next
	}
	return nil, nil, 0, nil
}

// ReplaceScalars rewrites every value scalar fn accepts, splicing each one in
// place. fn is offered every value scalar of the document in reading order —
// mapping KEYS are never offered (a rewrite there would rename config keys) and
// alias nodes are skipped, since the anchored definition is visited once at its
// own site. It returns (replacement, true, nil) to accept, (_, false, nil) to
// decline, or an error to abort.
//
// Splice-ability is checked only for scalars fn ACCEPTED: a declined block
// scalar — every real annotated layer file has some — is simply left alone,
// while an accepted one that cannot be spliced is an error, never a silent skip.
// The first error leaves Bytes() unchanged.
func (s *Splicer) ReplaceScalars(fn func(string) (string, bool, error)) (int, error) {
	if fn == nil {
		return 0, nil
	}
	type spliceEdit struct {
		start, end int
		text       string
	}
	var (
		edits   []spliceEdit
		accepts []spliceBulk
	)
	for _, site := range collectValueScalars(spliceRoot(s.doc)) {
		next, ok, err := fn(site.node.Value)
		if err != nil {
			return 0, err
		}
		if !ok {
			continue
		}
		if site.flow {
			return 0, fmt.Errorf("%w: %q sits in a flow collection in %s; write it as a block collection first", ErrUnsplicable, site.dotted(), s.path)
		}
		start, end, text, err := s.valueSplice(site.node, site.path, next)
		if err != nil {
			return 0, err
		}
		edits = append(edits, spliceEdit{start: start, end: end, text: text})
		accepts = append(accepts, spliceBulk{dotted: site.dotted(), value: next})
	}
	if len(edits) == 0 {
		return 0, nil
	}

	// Bottom-up, so an earlier edit's offsets stay valid while later ones apply.
	slices.SortFunc(edits, func(a, b spliceEdit) int { return b.start - a.start })
	prev := s.src
	next := bytes.Clone(s.src)
	for _, e := range edits {
		spliced := make([]byte, 0, len(next)-(e.end-e.start)+len(e.text))
		spliced = append(spliced, next[:e.start]...)
		spliced = append(spliced, e.text...)
		spliced = append(spliced, next[e.end:]...)
		next = spliced
	}
	s.src = next
	if err := s.reparse(); err != nil {
		s.src = prev
		if rollback := s.reparse(); rollback != nil {
			return 0, fmt.Errorf("%w: %v", ErrVerify, rollback)
		}
		return 0, fmt.Errorf("%w: %v", ErrVerify, err)
	}
	s.bulk = append(s.bulk, accepts...)
	return len(edits), nil
}

// scalarSite is one value scalar of a parsed document, with the dotted path that
// locates it and whether any ancestor collection is written in flow style.
type scalarSite struct {
	node *yaml.Node
	path []string
	flow bool
}

func (site scalarSite) dotted() string { return strings.Join(site.path, ".") }

// collectValueScalars walks a document's value scalars in reading order. Mapping
// keys are not visited; alias nodes are skipped so an anchored scalar is offered
// exactly once, at its definition.
func collectValueScalars(node *yaml.Node) []scalarSite {
	var out []scalarSite
	var walk func(n *yaml.Node, path []string, flow bool)
	walk = func(n *yaml.Node, path []string, flow bool) {
		if n == nil {
			return
		}
		switch n.Kind {
		case yaml.AliasNode:
			return
		case yaml.ScalarNode:
			out = append(out, scalarSite{node: n, path: path, flow: flow})
		case yaml.MappingNode:
			flow = flow || n.Style&yaml.FlowStyle != 0
			for i := 0; i+1 < len(n.Content); i += 2 {
				walk(n.Content[i+1], append(append([]string{}, path...), n.Content[i].Value), flow)
			}
		case yaml.SequenceNode:
			flow = flow || n.Style&yaml.FlowStyle != 0
			for i, child := range n.Content {
				walk(child, append(append([]string{}, path...), strconv.Itoa(i)), flow)
			}
		default:
			for _, child := range n.Content {
				walk(child, path, flow)
			}
		}
	}
	walk(node, nil, false)
	return out
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
	if len(s.bulk) > 0 {
		values := make(map[string]string)
		for _, site := range collectValueScalars(spliceRoot(probe.doc)) {
			values[site.dotted()] = site.node.Value
		}
		for _, b := range s.bulk {
			if got, found := values[b.dotted]; !found || got != b.value {
				return fmt.Errorf("%w: %q does not read back as the requested value in %s", ErrVerify, b.dotted, s.path)
			}
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
