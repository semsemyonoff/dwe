package tests

import (
	"path"
	"slices"
	"strings"
)

// hit is one compose project name the scanner judged self-built: Line is the
// 1-based physical line the logical line containing the compose invocation
// (or COMPOSE_PROJECT_NAME assignment) starts on, relative to the scanned
// text; Value is the offending value with shell quoting removed — for a
// one-hop `$X` reference, the value assigned to X.
type hit struct {
	Line  int
	Value string
}

// simpleCmd is one simple command: its raw words (quotes and expansions kept
// verbatim) and the first physical line of the logical line it sits on.
type simpleCmd struct {
	line  int
	words []string
}

// scanShellText reports every place where text hands docker compose a project
// name not derived from $COMPOSE_PROJECT_NAME: a `-p`/`--project-name` global
// flag of a compose invocation in command position, or a
// COMPOSE_PROJECT_NAME= assignment. It never guesses — literals, positionals,
// command substitutions and values untraceable within one `X=` hop are
// skipped. Pure function; the lexer is a deliberately small subset of POSIX
// sh, enough to find command boundaries without being fooled by quotes,
// comments and here-docs.
func scanShellText(text string) []hit {
	cmds := splitSimpleCommands(text)

	assigns := map[string][]string{}
	for _, c := range cmds {
		for _, a := range variableAssignments(c.words) {
			assigns[a.name] = append(assigns[a.name], a.value)
		}
	}

	var hits []hit
	for _, c := range cmds {
		for _, v := range composeProjectValues(c.words) {
			if value, ok := judgeProjectValue(v, assigns); ok {
				hits = append(hits, hit{Line: c.line, Value: value})
			}
		}
	}
	return hits
}

// --- lexing -----------------------------------------------------------------

type heredoc struct {
	delim     string
	stripTabs bool
}

type shellLexer struct {
	src          string
	i            int
	line         int
	logicalStart int

	word        strings.Builder
	wordStarted bool
	cur         simpleCmd
	cmds        []simpleCmd
	heredocs    []heredoc
}

// splitSimpleCommands splits text into simple commands on unquoted `;`, `&`,
// `|` (hence `&&`, `||`), `(`, `)` and newline. `\`-newline continuations are
// joined, a `#` starting a word ends the line, and here-doc bodies are
// skipped. Quotes, `$(…)`, `${…}` and backticks stay inside their word.
func splitSimpleCommands(text string) []simpleCmd {
	l := &shellLexer{src: text, line: 1, logicalStart: 1}
	for l.i < len(l.src) {
		c := l.src[l.i]
		rest := l.src[l.i:]
		switch {
		case strings.HasPrefix(rest, "\\\n"):
			l.i += 2
			l.line++
		case c == '\\':
			l.writeRaw(min(2, len(rest)))
		case c == '\n':
			l.endCommand()
			l.i++
			l.line++
			l.skipHeredocBodies()
			l.logicalStart = l.line
		case c == ' ' || c == '\t' || c == '\r':
			l.endWord()
			l.i++
		case c == '&' && l.wordStarted && strings.HasSuffix(l.word.String(), ">"),
			c == '&' && l.wordStarted && strings.HasSuffix(l.word.String(), "<"):
			// `2>&1` / `<&3` is a redirection, not a background separator.
			l.writeRaw(1)
		case c == ';' || c == '&' || c == '|' || c == '(' || c == ')':
			l.endCommand()
			l.i++
		case c == '#' && !l.wordStarted:
			if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
				l.i += nl
			} else {
				l.i = len(l.src)
			}
		case strings.HasPrefix(rest, "<<<"):
			l.writeRaw(3)
		case strings.HasPrefix(rest, "<<"):
			l.endWord()
			l.readHeredocMarker()
		case c == '\'':
			l.wordStarted = true
			l.consumeSingleQuoted()
		case c == '"' || c == '`' || strings.HasPrefix(rest, "$(") || strings.HasPrefix(rest, "${"):
			l.wordStarted = true
			l.consumeNested()
		default:
			l.writeRaw(1)
		}
	}
	l.endCommand()
	return l.cmds
}

func (l *shellLexer) writeRaw(n int) {
	l.word.WriteString(l.src[l.i : l.i+n])
	l.wordStarted = true
	l.i += n
}

func (l *shellLexer) endWord() {
	if !l.wordStarted {
		return
	}
	if len(l.cur.words) == 0 {
		l.cur.line = l.logicalStart
	}
	l.cur.words = append(l.cur.words, l.word.String())
	l.word.Reset()
	l.wordStarted = false
}

func (l *shellLexer) endCommand() {
	l.endWord()
	if len(l.cur.words) > 0 {
		l.cmds = append(l.cmds, l.cur)
	}
	l.cur = simpleCmd{}
}

// consumeSingleQuoted copies a '…' literal (quotes included) into the word.
// An unterminated quote runs to the end of the text.
func (l *shellLexer) consumeSingleQuoted() {
	end := strings.IndexByte(l.src[l.i+1:], '\'')
	stop := len(l.src)
	if end >= 0 {
		stop = l.i + 1 + end + 1
	}
	chunk := l.src[l.i:stop]
	l.line += strings.Count(chunk, "\n")
	l.word.WriteString(chunk)
	l.i = stop
}

// consumeNested copies one "…", `…`, $(…) or ${…} construct — with whatever
// it nests — into the current word, dropping `\`-newline continuations.
func (l *shellLexer) consumeNested() {
	var stack []byte
	open := func(kind byte, n int) {
		stack = append(stack, kind)
		l.word.WriteString(l.src[l.i : l.i+n])
		l.i += n
	}
	closeTop := func() {
		stack = stack[:len(stack)-1]
		l.word.WriteByte(l.src[l.i])
		l.i++
	}

	switch rest := l.src[l.i:]; {
	case rest[0] == '"':
		open('"', 1)
	case rest[0] == '`':
		open('`', 1)
	case strings.HasPrefix(rest, "$("):
		open('(', 2)
	default: // "${"
		open('{', 2)
	}

	for len(stack) > 0 && l.i < len(l.src) {
		c := l.src[l.i]
		rest := l.src[l.i:]
		top := stack[len(stack)-1]
		switch {
		case strings.HasPrefix(rest, "\\\n"):
			l.i += 2
			l.line++
		case c == '\\':
			n := min(2, len(rest))
			l.word.WriteString(rest[:n])
			l.i += n
		case c == '\n':
			l.line++
			l.word.WriteByte(c)
			l.i++
		case strings.HasPrefix(rest, "$("):
			open('(', 2)
		case strings.HasPrefix(rest, "${"):
			open('{', 2)
		case c == '`' && top == '`':
			closeTop()
		case c == '`':
			open('`', 1)
		case c == '"' && top == '"':
			closeTop()
		case c == '"':
			open('"', 1)
		case top == '"':
			l.word.WriteByte(c)
			l.i++
		case c == '\'':
			l.consumeSingleQuoted()
		case c == '(' && top == '(':
			open('(', 1)
		case c == ')' && top == '(':
			closeTop()
		case c == '}' && top == '{':
			closeTop()
		default:
			l.word.WriteByte(c)
			l.i++
		}
	}
}

// readHeredocMarker parses `<<[-]WORD` at l.i and queues the here-doc whose
// body starts after the current line. The delimiter's quoting is stripped.
func (l *shellLexer) readHeredocMarker() {
	l.i += 2
	h := heredoc{}
	if l.i < len(l.src) && l.src[l.i] == '-' {
		h.stripTabs = true
		l.i++
	}
	for l.i < len(l.src) && (l.src[l.i] == ' ' || l.src[l.i] == '\t') {
		l.i++
	}
	var delim strings.Builder
	for l.i < len(l.src) {
		c := l.src[l.i]
		if strings.IndexByte(" \t\r\n;&|<>()", c) >= 0 {
			break
		}
		if c != '\'' && c != '"' && c != '\\' {
			delim.WriteByte(c)
		}
		l.i++
	}
	if delim.Len() > 0 {
		h.delim = delim.String()
		l.heredocs = append(l.heredocs, h)
	}
}

// skipHeredocBodies consumes the bodies of every here-doc queued on the line
// just ended, in order, up to and including each delimiter line.
func (l *shellLexer) skipHeredocBodies() {
	for _, h := range l.heredocs {
		for l.i < len(l.src) {
			ln := l.src[l.i:]
			if nl := strings.IndexByte(ln, '\n'); nl >= 0 {
				ln = ln[:nl]
				l.i += nl + 1
				l.line++
			} else {
				l.i = len(l.src)
			}
			if h.stripTabs {
				ln = strings.TrimLeft(ln, "\t")
			}
			if strings.TrimRight(ln, "\r") == h.delim {
				break
			}
		}
	}
	l.heredocs = nil
}

// --- command analysis -------------------------------------------------------

// reservedWords may precede a command in the same simple-command slot once
// `;`/`&&`/newline splitting is done (`then docker compose …`).
var reservedWords = map[string]bool{
	"!": true, "{": true, "}": true, "if": true, "then": true, "else": true,
	"elif": true, "fi": true, "do": true, "done": true, "while": true, "until": true,
}

var declKeywords = map[string]bool{
	"export": true, "local": true, "readonly": true, "declare": true, "typeset": true,
}

// commandWrappers run their argument as a command, so the compose anchor is
// looked for after them (their options, option values and `env`'s NAME=value
// skipped).
var commandWrappers = map[string]wrapperOpts{
	"exec":    {short: "a"},
	"command": {},
	"env":     {short: "uCPS", long: []string{"--unset", "--chdir", "--split-string"}},
	"sudo": {short: "ughpCDrtUTR", long: []string{
		"--user", "--group", "--host", "--prompt", "--close-from", "--chdir",
		"--role", "--type", "--other-user", "--command-timeout", "--chroot",
	}},
	"time": {short: "fo", long: []string{"--format", "--output"}},
	"nice": {short: "n", long: []string{"--adjustment"}},
}

// wrapperOpts names a wrapper's options whose value may be the next word.
type wrapperOpts struct {
	short string
	long  []string
}

// takesNextWord reports whether option opt consumes the following word: a
// value-taking long option without `=`, or a short cluster whose first
// value-taking letter ends it (`-u root`, `-Eu root`, but not `-uroot`).
func (o wrapperOpts) takesNextWord(opt string) bool {
	if strings.HasPrefix(opt, "--") {
		return !strings.Contains(opt, "=") && slices.Contains(o.long, opt)
	}
	for k := 1; k < len(opt); k++ {
		if strings.IndexByte(o.short, opt[k]) >= 0 {
			return k == len(opt)-1
		}
	}
	return false
}

// skipWrappers returns the index of the first word after the wrappers
// starting at words[i], feeding every NAME=value word among their arguments
// to assign (which reports whether the word was one).
func skipWrappers(words []string, i int, assign func(raw string) bool) int {
	for i < len(words) {
		opts, ok := commandWrappers[words[i]]
		if !ok {
			break
		}
		i = skipWrapperArgs(words, i+1, opts, assign)
	}
	return i
}

func skipWrapperArgs(words []string, i int, opts wrapperOpts, assign func(raw string) bool) int {
	optsDone := false
	for ; i < len(words); i++ {
		w := unquoted(words[i])
		switch {
		case !optsDone && w == "--":
			optsDone = true
		case !optsDone && strings.HasPrefix(w, "-"):
			if opts.takesNextWord(w) {
				i++
			}
		case !assign(words[i]):
			return i
		}
	}
	return i
}

// composeValueFlags are compose global flags whose value is the next word.
var composeValueFlags = map[string]bool{
	"-f": true, "--file": true, "--project-directory": true, "--env-file": true,
	"--profile": true, "--ansi": true, "--progress": true, "--parallel": true,
}

const composeProjectVar = "COMPOSE_PROJECT_NAME"

type assignment struct {
	name, value string
}

// parseAssignment splits a raw `NAME=value` / `NAME+=value` word.
func parseAssignment(raw string) (assignment, bool) {
	eq := strings.IndexByte(raw, '=')
	if eq <= 0 {
		return assignment{}, false
	}
	name := strings.TrimSuffix(raw[:eq], "+")
	if !isShellName(name) {
		return assignment{}, false
	}
	return assignment{name: name, value: raw[eq+1:]}, true
}

func isShellName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '_' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func skipReserved(words []string, i int) int {
	for i < len(words) && reservedWords[words[i]] {
		i++
	}
	return i
}

// variableAssignments returns the `[export|local|…] [-opts] X=W` assignments
// of one simple command: leading assignment words, and every NAME=value
// argument of a declaration keyword.
func variableAssignments(words []string) []assignment {
	var out []assignment
	i := skipReserved(words, 0)
	for ; i < len(words); i++ {
		a, ok := parseAssignment(words[i])
		if !ok {
			break
		}
		out = append(out, a)
	}
	if i < len(words) && declKeywords[words[i]] {
		for _, w := range words[i+1:] {
			if a, ok := parseAssignment(w); ok {
				out = append(out, a)
			}
		}
	}
	return out
}

// composeProjectValues returns the raw project-name values one simple command
// hands compose: COMPOSE_PROJECT_NAME= assignments (leading, declared, or via
// `env`) and the `-p`/`--project-name` global flags of a compose invocation
// in command position.
func composeProjectValues(words []string) []string {
	var out []string
	captureAssignment := func(w string) bool {
		a, ok := parseAssignment(w)
		if ok && a.name == composeProjectVar {
			out = append(out, a.value)
		}
		return ok
	}

	i := skipReserved(words, 0)
	for i < len(words) && captureAssignment(words[i]) {
		i++
	}
	if i < len(words) && declKeywords[words[i]] {
		for _, w := range words[i+1:] {
			captureAssignment(w)
		}
		return out
	}
	i = skipWrappers(words, i, captureAssignment)

	if i >= len(words) {
		return out
	}
	anchor, _ := unquoteShell(words[i])
	switch {
	case path.Base(anchor) == "docker-compose":
		i++
	case (path.Base(anchor) == "docker" || isExpansionWord(anchor)) &&
		i+1 < len(words) && unquoted(words[i+1]) == "compose":
		i += 2
	default:
		return out
	}

	for i < len(words) {
		raw := words[i]
		flag := unquoted(raw)
		if !strings.HasPrefix(flag, "-") || flag == "-" || flag == "--" {
			break
		}
		switch {
		case flag == "-p" || flag == "--project-name":
			if i+1 < len(words) {
				out = append(out, words[i+1])
			}
			i += 2
			continue
		case strings.HasPrefix(raw, "--project-name="):
			out = append(out, strings.TrimPrefix(raw, "--project-name="))
		case strings.HasPrefix(raw, "-p="):
			out = append(out, strings.TrimPrefix(raw, "-p="))
		case strings.HasPrefix(raw, "-p"):
			out = append(out, strings.TrimPrefix(raw, "-p"))
		case composeValueFlags[flag]:
			i += 2
			continue
		}
		i++
	}
	return out
}

// isExpansionWord reports a command word that is a parameter expansion, such
// as `${DOCKER:-docker}` or `$DOCKER` — never a command substitution.
func isExpansionWord(w string) bool {
	return strings.HasPrefix(w, "$") && !strings.HasPrefix(w, "$(")
}

// --- value judgement --------------------------------------------------------

type valueClass int

const (
	valueOK valueClass = iota
	valueSkip
	valueRef
	valueHit
)

// judgeProjectValue decides whether a raw value is reported, following one
// `$X` hop through every assignment to X in the text: it is reported only
// when at least one assignment exists and all of them are hits.
func judgeProjectValue(raw string, assigns map[string][]string) (string, bool) {
	display, expand := unquoteShell(raw)
	class, ref := classifyProjectValue(expand)
	switch class {
	case valueHit:
		return display, true
	case valueRef:
		values := assigns[ref]
		if len(values) == 0 {
			return "", false
		}
		for _, w := range values {
			_, wExpand := unquoteShell(w)
			if c, _ := classifyProjectValue(wExpand); c != valueHit {
				return "", false
			}
		}
		first, _ := unquoteShell(values[0])
		return first, true
	default:
		return "", false
	}
}

// classifyProjectValue classifies an unquoted value; the first matching rule
// wins. For valueRef it also returns the referenced variable name.
func classifyProjectValue(v string) (valueClass, string) {
	if referencesComposeProjectName(v) {
		return valueOK, ""
	}
	if !strings.ContainsAny(v, "$`") || hasPositionalOrSubstitution(v) {
		return valueSkip, ""
	}
	if name, ok := exactReference(v); ok {
		return valueRef, name
	}
	return valueHit, ""
}

// referencesComposeProjectName reports a $COMPOSE_PROJECT_NAME or
// ${COMPOSE_PROJECT_NAME… reference anywhere in v, ended by a non-name
// character or the end — so ${COMPOSE_PROJECT_NAME_OLD} does not count.
func referencesComposeProjectName(v string) bool {
	for _, prefix := range []string{"$" + composeProjectVar, "${" + composeProjectVar} {
		rest := v
		for {
			idx := strings.Index(rest, prefix)
			if idx < 0 {
				break
			}
			rest = rest[idx+len(prefix):]
			if rest == "" || !isNameByte(rest[0]) {
				return true
			}
		}
	}
	return false
}

func isNameByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func hasPositionalOrSubstitution(v string) bool {
	if strings.Contains(v, "`") || strings.Contains(v, "$(") {
		return true
	}
	for i := 0; i+1 < len(v); i++ {
		if v[i] != '$' {
			continue
		}
		next := v[i+1]
		if next == '{' && i+2 < len(v) {
			next = v[i+2]
		}
		if (next >= '0' && next <= '9') || next == '@' || next == '*' {
			return true
		}
	}
	return false
}

// exactReference matches a value that is exactly `$X` or `${X}`.
func exactReference(v string) (string, bool) {
	switch {
	case strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}"):
		name := v[2 : len(v)-1]
		return name, isShellName(name)
	case strings.HasPrefix(v, "$"):
		name := v[1:]
		return name, isShellName(name)
	}
	return "", false
}

func unquoted(raw string) string {
	d, _ := unquoteShell(raw)
	return d
}

// unquoteShell removes shell quoting from a raw word. display is the text as
// the shell would see it with expansions left unexpanded; expand is the same
// text with every `$` and backtick that the quoting makes literal (inside
// '…' or escaped) neutralised, so classification never mistakes a literal
// for an expansion.
func unquoteShell(raw string) (display, expand string) {
	var d, e strings.Builder
	inDouble := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c == '\'' && !inDouble:
			lit := raw[i+1:]
			if end := strings.IndexByte(lit, '\''); end >= 0 {
				lit = lit[:end]
			}
			i += len(lit) + 1
			d.WriteString(lit)
			e.WriteString(neutraliseExpansions(lit))
		case c == '"':
			inDouble = !inDouble
		case c == '\\' && i+1 < len(raw):
			next := raw[i+1]
			if inDouble && strings.IndexByte("$`\"\\", next) < 0 {
				d.WriteByte(c)
				e.WriteByte(c)
			}
			d.WriteByte(next)
			e.WriteString(neutraliseExpansions(string(next)))
			i++
		default:
			d.WriteByte(c)
			e.WriteByte(c)
		}
	}
	return d.String(), e.String()
}

var expansionNeutraliser = strings.NewReplacer("$", "_", "`", "_")

func neutraliseExpansions(s string) string {
	return expansionNeutraliser.Replace(s)
}
