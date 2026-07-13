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
	// parserDirectiveRe matches any Dockerfile parser directive line
	// ("# key=value", e.g. "# syntax=docker/dockerfile:1"). Such directives may
	// precede "# escape=" and must not stop the escape-directive scan.
	parserDirectiveRe = regexp.MustCompile(`(?i)^#\s*[a-z][a-z0-9]*\s*=\s*\S+\s*$`)
	varRefRe          = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)
	// soleVarRe matches a string that is EXACTLY a single "$VAR" / "${VAR}"
	// reference with nothing else around it — used to recognize a FROM
	// "--platform=$TARGETPLATFORM"/"$BUILDPLATFORM" buildkit builtin.
	soleVarRe = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$|^\$([A-Za-z_][A-Za-z0-9_]*)$`)
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
// targetPlatform is the platform Compose already resolved for this service's
// build (from `services.<name>.platform` or `build.platforms`, or "" for the
// daemon default). It is the effective platform of any base whose FROM pins no
// "--platform" of its own, and of a "--platform=$TARGETPLATFORM" builtin — in
// both cases buildkit fetches the base for the build's target platform, so
// prepull must probe/pull that same variant. "--platform=$BUILDPLATFORM" (the
// native build host) instead maps to "" (daemon default), matching how a
// cross-compilation base is fetched.
//
// Each returned BaseRef carries the effective "--platform" for that FROM: a
// statically resolvable value (literal, or an ARG-substituted value), the
// targetPlatform (bare FROM or "$TARGETPLATFORM"), or "" when the FROM pins a
// $BUILDPLATFORM/unresolvable platform and no service target platform applies.
// Carrying the platform is load-bearing: on a host whose default platform
// differs from the pinned one, prepulling (and existence-probing) the default
// variant would leave buildkit to fetch the pinned variant from the LAN
// registry — the exact fetch this feature exists to avoid.
//
// Known limitation: a "FROM ..." line inside a RUN <<EOF heredoc body is not
// distinguished from a real stage and would be misparsed. This is rare and
// safe here — the derived ref set is only used to prepull images.
func externalBaseRefs(dockerfile []byte, buildArgs map[string]string, targetPlatform string) []BaseRef {
	escape := detectEscapeChar(dockerfile)
	logical := splitLogicalLines(string(dockerfile), escape)

	declared := map[string]argDecl{}
	firstFromSeen := false
	stages := map[string]struct{}{}
	seen := map[string]struct{}{}
	var refs []BaseRef

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
			ref, platform, stage, ok := parseFromLine(rest)
			if !ok {
				continue
			}
			resolved, isResolved := resolveRef(ref, declared, buildArgs)
			if !isResolved {
				trace.Debugf(context.Background(), "dockerfile: skipping FROM %q: unresolved build variable", ref)
				if stage != "" {
					stages[strings.ToLower(stage)] = struct{}{}
				}
				continue
			}
			resolvedPlatform := resolvePlatform(platform, declared, buildArgs, targetPlatform)
			// Buildkit normalizes build-stage names case-insensitively, so
			// "FROM alpine AS Build" then "FROM build" reuses the stage. Key
			// the stage set by lowercase name to match, avoiding a spurious
			// external-ref classification (and pull) for a case-mismatched
			// stage reference.
			_, isStageRef := stages[strings.ToLower(resolved)]
			if stage != "" {
				stages[strings.ToLower(stage)] = struct{}{}
			}
			if isStageRef || strings.EqualFold(resolved, "scratch") {
				continue
			}
			key := resolved + "\x00" + resolvedPlatform
			if _, dup := seen[key]; !dup {
				seen[key] = struct{}{}
				refs = append(refs, BaseRef{Ref: resolved, Platform: resolvedPlatform})
			}
		}
	}

	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Ref != refs[j].Ref {
			return refs[i].Ref < refs[j].Ref
		}
		return refs[i].Platform < refs[j].Platform
	})
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
		if m := escapeDirectiveRe.FindStringSubmatch(line); m != nil {
			if len(m[1]) == 1 {
				escape = m[1][0]
			}
			continue
		}
		// A non-escape parser directive (e.g. "# syntax=...") is allowed to
		// precede "# escape=" per the spec — keep scanning. Only a genuine
		// non-directive line (regular comment or instruction) stops the scan.
		if parserDirectiveRe.MatchString(line) {
			continue
		}
		break
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
// an optional trailing "AS <stage>". The "--platform=<value>" flag's raw
// value (which may still carry a build variable) is returned separately;
// other flags are ignored. Only the "=" form is recognized — buildkit's FROM
// flag parser does not accept a space-separated "--platform <value>".
func parseFromLine(rest string) (ref, platform, stage string, ok bool) {
	fields := strings.Fields(rest)
	for len(fields) > 0 && strings.HasPrefix(fields[0], "--") {
		if v, found := strings.CutPrefix(fields[0], "--platform="); found {
			platform = v
		}
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return "", "", "", false
	}
	ref = fields[0]
	if len(fields) >= 3 && strings.EqualFold(fields[len(fields)-2], "AS") {
		stage = fields[len(fields)-1]
	}
	return ref, platform, stage, true
}

// resolvePlatform computes the effective platform for a FROM instruction:
//   - no "--platform" (raw == "") → the service's target platform (buildkit
//     builds a bare FROM for the build's target platform).
//   - "--platform=$TARGETPLATFORM" (a buildkit builtin) → the target platform.
//   - "--platform=$BUILDPLATFORM" (the native build host) → "" (daemon default).
//   - a literal or ARG-resolvable value → that value verbatim.
//   - any other unresolvable variable → "" (prepull at the default platform).
func resolvePlatform(raw string, declared map[string]argDecl, buildArgs map[string]string, targetPlatform string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return targetPlatform
	}
	if name, ok := soleVarName(raw); ok {
		switch name {
		case "TARGETPLATFORM":
			return targetPlatform
		case "BUILDPLATFORM":
			return ""
		}
	}
	resolved, ok := resolveRef(raw, declared, buildArgs)
	if !ok {
		return ""
	}
	return resolved
}

// soleVarName returns the variable name when s is EXACTLY a single "$VAR" or
// "${VAR}" reference (nothing else around it), else ok is false.
func soleVarName(s string) (name string, ok bool) {
	m := soleVarRe.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	if m[1] != "" {
		return m[1], true
	}
	return m[2], true
}

// resolveRef substitutes "${VAR}", "${VAR:-default}", and "$VAR" references
// in ref using declared (ARG defaults, overridden by buildArgs). ok is false
// if any referenced variable cannot be resolved.
func resolveRef(ref string, declared map[string]argDecl, buildArgs map[string]string) (resolved string, ok bool) {
	matches := varRefRe.FindAllStringSubmatchIndex(ref, -1)
	out := ref
	if matches != nil {
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
			case resolvedVar && val != "":
				buf.WriteString(val)
			case hasDefault:
				// "${VAR:-default}" applies default when VAR is unset OR
				// resolves empty — matching bash/buildkit ":-" semantics, so a
				// declared-but-empty ARG (or an empty --build-arg override) does
				// not blank out the FROM ref.
				buf.WriteString(def)
			case resolvedVar:
				// Resolved to empty with no default (e.g. "${VAR}", VAR empty) —
				// expands to empty, matching buildkit.
				buf.WriteString(val)
			default:
				return "", false
			}
		}
		buf.WriteString(ref[last:])
		out = buf.String()
	}
	// Unsupported expansion forms (e.g. "${VAR:+alt}", "${VAR:?err}") are not
	// matched by varRefRe and survive substitution verbatim. A ref still
	// carrying an unexpanded "${" cannot be reliably prepulled — treat it as
	// unresolved (skip it) rather than emitting a misleading
	// "build will likely fail" warning for a ref the real build resolves fine.
	if strings.Contains(out, "${") {
		return "", false
	}
	return out, true
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
