package docker

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

var (
	escapeDirectiveRe = regexp.MustCompile(`(?i)^#\s*escape\s*=\s*(\S)\s*$`)
	varRefRe          = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)
)

// argDecl is an ARG instruction seen before the first FROM: its optional
// default value, and whether one was given at all.
type argDecl struct {
	hasValue bool
	value    string
}

// externalBaseRefs parses a Dockerfile and returns the sorted, deduplicated
// set of external FROM base image references — stage names (declared via
// "AS") and "scratch" are excluded. buildArgs overrides ARG defaults
// declared before the first FROM instruction, mirroring how
// `docker build --build-arg` / compose build.args resolve FROM-time
// variables. A FROM ref containing a variable that cannot be resolved (not
// declared via ARG, or declared with no default/override and no
// "${VAR:-default}" fallback — e.g. buildkit builtins like $BUILDPLATFORM)
// is skipped with a trace diagnostic rather than failing the whole parse:
// this function is advisory and must never error out a build.
//
// Known limitation: a "FROM ..." line inside a RUN <<EOF heredoc body is not
// distinguished from a real stage and would be misparsed. This is rare and
// safe here — the derived ref set is only used to prepull images.
func externalBaseRefs(dockerfile []byte, buildArgs map[string]string) []string {
	escape := detectEscapeChar(dockerfile)
	logical := splitLogicalLines(string(dockerfile), escape)

	declared := map[string]argDecl{}
	firstFromSeen := false
	stages := map[string]struct{}{}
	seen := map[string]struct{}{}
	var refs []string

	for _, line := range logical {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		instr, rest := splitInstruction(line)
		switch strings.ToUpper(instr) {
		case "ARG":
			if firstFromSeen {
				continue
			}
			name, def, hasDef := parseArgDecl(rest)
			if name == "" {
				continue
			}
			declared[name] = argDecl{hasValue: hasDef, value: def}
		case "FROM":
			firstFromSeen = true
			ref, stage, ok := parseFromLine(rest)
			if !ok {
				continue
			}
			resolved, isResolved := resolveRef(ref, declared, buildArgs)
			if !isResolved {
				trace.Debugf(context.Background(), "dockerfile: skipping FROM %q: unresolved build variable", ref)
				if stage != "" {
					stages[stage] = struct{}{}
				}
				continue
			}
			_, isStageRef := stages[resolved]
			if stage != "" {
				stages[stage] = struct{}{}
			}
			if isStageRef || strings.EqualFold(resolved, "scratch") {
				continue
			}
			if _, dup := seen[resolved]; !dup {
				seen[resolved] = struct{}{}
				refs = append(refs, resolved)
			}
		}
	}

	sort.Strings(refs)
	return refs
}

// detectEscapeChar scans the leading run of blank lines and parser-directive
// comments (e.g. "# escape=`") for an escape-char override. Per the
// Dockerfile spec, directives are only recognized at the very top of the
// file; the first non-blank, non-directive line stops the scan.
func detectEscapeChar(dockerfile []byte) byte {
	escape := byte('\\')
	for raw := range strings.SplitSeq(string(dockerfile), "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" {
			continue
		}
		m := escapeDirectiveRe.FindStringSubmatch(line)
		if m == nil {
			break
		}
		if len(m[1]) == 1 {
			escape = m[1][0]
		}
	}
	return escape
}

// splitLogicalLines joins escape-char line continuations into logical
// instruction lines. Comment lines are never treated as continued.
func splitLogicalLines(src string, escape byte) []string {
	var logical []string
	var buf strings.Builder
	for raw := range strings.SplitSeq(src, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmedRight := strings.TrimRight(line, " \t")
		if strings.HasSuffix(trimmedRight, string(escape)) && !isCommentLine(line) {
			buf.WriteString(strings.TrimSuffix(trimmedRight, string(escape)))
			continue
		}
		buf.WriteString(line)
		logical = append(logical, buf.String())
		buf.Reset()
	}
	if buf.Len() > 0 {
		logical = append(logical, buf.String())
	}
	return logical
}

func isCommentLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "#")
}

// splitInstruction splits a logical line into its leading instruction
// keyword and the (trimmed) remainder.
func splitInstruction(line string) (instr, rest string) {
	idx := strings.IndexAny(line, " \t")
	if idx < 0 {
		return line, ""
	}
	return line[:idx], strings.TrimSpace(line[idx:])
}

// parseArgDecl parses the remainder of an "ARG" instruction: "NAME=default"
// or bare "NAME".
func parseArgDecl(rest string) (name, def string, hasDef bool) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", "", false
	}
	if before, after, found := strings.Cut(rest, "="); found {
		name = strings.TrimSpace(before)
		def = unquote(strings.TrimSpace(after))
		return name, def, true
	}
	return rest, "", false
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// parseFromLine parses the remainder of a "FROM" instruction: an optional
// leading run of "--flag" tokens (e.g. "--platform=..."), the base ref, and
// an optional trailing "AS <stage>".
func parseFromLine(rest string) (ref, stage string, ok bool) {
	fields := strings.Fields(rest)
	for len(fields) > 0 && strings.HasPrefix(fields[0], "--") {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return "", "", false
	}
	ref = fields[0]
	if len(fields) >= 3 && strings.EqualFold(fields[len(fields)-2], "AS") {
		stage = fields[len(fields)-1]
	}
	return ref, stage, true
}

// resolveRef substitutes "${VAR}", "${VAR:-default}", and "$VAR" references
// in ref using declared (ARG defaults, overridden by buildArgs). ok is false
// if any referenced variable cannot be resolved.
func resolveRef(ref string, declared map[string]argDecl, buildArgs map[string]string) (resolved string, ok bool) {
	matches := varRefRe.FindAllStringSubmatchIndex(ref, -1)
	if matches == nil {
		return ref, true
	}
	var buf strings.Builder
	last := 0
	for _, m := range matches {
		buf.WriteString(ref[last:m[0]])
		last = m[1]

		var name, def string
		hasDefault := false
		if m[2] >= 0 {
			name = ref[m[2]:m[3]]
			if m[6] >= 0 {
				hasDefault = true
				def = ref[m[6]:m[7]]
			}
		} else {
			name = ref[m[8]:m[9]]
		}

		val, resolvedVar := resolveVar(name, declared, buildArgs)
		switch {
		case resolvedVar:
			buf.WriteString(val)
		case hasDefault:
			buf.WriteString(def)
		default:
			return "", false
		}
	}
	buf.WriteString(ref[last:])
	return buf.String(), true
}

// resolveVar looks up a build variable's value: buildArgs overrides the ARG
// default, but only for names actually declared via ARG before the first
// FROM — an undeclared buildArgs entry is not consumed here, matching
// Docker's own "build-arg not consumed" behavior.
func resolveVar(name string, declared map[string]argDecl, buildArgs map[string]string) (string, bool) {
	d, ok := declared[name]
	if !ok {
		return "", false
	}
	if override, hasOverride := buildArgs[name]; hasOverride {
		return override, true
	}
	if d.hasValue {
		return d.value, true
	}
	return "", false
}
