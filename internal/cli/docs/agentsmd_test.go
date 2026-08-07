package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// The repo-root AGENTS.md (symlinked as CLAUDE.md) is loaded into every agent
// session in this repo, so its size is a per-session context tax exactly like
// `dwe docs llms-txt --no-project` — which is why its budget lives next to
// llmsTxtNoProjectBudget rather than in a package of its own.
//
// The file grew to 98 KB / ~25k tokens because nothing pinned it: `## Critical
// Patterns` accumulated one full write-up per feature until it duplicated
// docs/internals/packages.md. The contract is that a bullet is a trap plus a
// `§` pointer; the write-up belongs in packages.md, which is NOT loaded into
// context. These tests enforce the mechanical half of that contract.
const (
	// Ceiling, not a target: the file is well under this today. A change that
	// pushes past it is a signal to move the write-up into packages.md, not to
	// raise the number.
	agentsMdBudget = 40 * 1024

	// A single bullet used to be a single 17.8 KB line, which made every edit
	// a whole-line diff and every concurrent branch a whole-bullet conflict.
	// One sentence per line keeps diffs and conflicts surgical.
	agentsMdMaxLineLen = 600
)

// repoRoot walks up from the test's working directory (the package dir under
// `go test`) to the module root, identified by the go.mod that declares this
// module. Walking beats a fixed `../../..` so the test survives a package move.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(data), "module github.com/semsemyonoff/dwe") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("module root not found walking up from the package directory")
		}
		dir = parent
	}
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	require.NoError(t, err, "reading %s", rel)
	return string(data)
}

func TestAgentsMdBudget(t *testing.T) {
	got := readRepoFile(t, "AGENTS.md")
	require.LessOrEqual(t, len(got), agentsMdBudget,
		"AGENTS.md is %d B, over the %d B budget; move the write-up into docs/internals/packages.md and leave a trap + § pointer here",
		len(got), agentsMdBudget)
}

// criticalPatternsSection returns the lines of `## Critical Patterns`, which is
// the section the budget exists for. The surrounding sections are prose and are
// deliberately left alone.
func criticalPatternsSection(t *testing.T, content string) []string {
	t.Helper()
	lines := strings.Split(content, "\n")
	start, end := -1, len(lines)
	for i, line := range lines {
		switch {
		case line == "## Critical Patterns":
			start = i
		case start >= 0 && strings.HasPrefix(line, "## "):
			end = i
		}
		if start >= 0 && end < len(lines) {
			break
		}
	}
	require.GreaterOrEqual(t, start, 0, "AGENTS.md has no `## Critical Patterns` section")
	return lines[start:end]
}

func TestAgentsMdCriticalPatternsLineLength(t *testing.T) {
	section := criticalPatternsSection(t, readRepoFile(t, "AGENTS.md"))
	for i, line := range section {
		// Runes, not bytes: the section is full of `—`, `§` and `…`, so a byte
		// count would enforce a stricter, prose-dependent limit than documented.
		n := utf8.RuneCountInString(line)
		require.LessOrEqualf(t, n, agentsMdMaxLineLen,
			"AGENTS.md `## Critical Patterns` line %d is %d chars; keep one sentence per line so diffs and merge conflicts stay surgical",
			i+1, n)
	}
}

// pointerRe matches the `§ <target>` references that every Critical Patterns
// bullet ends with. A target is either a backticked path or a packages.md
// heading title (optionally followed by a parenthetical hint).
//
// `(?m)` is load-bearing: the unbackticked branch is `[^,;.]`, which admits a
// newline, and without multiline mode `$` only anchors the end of the whole
// file — so a pointer that is last on its line would swallow the newline and
// keep eating until the next `.` or `,` several lines down.
var pointerRe = regexp.MustCompile("(?m)§ (`[^`]+`|[A-Za-z][^,;.]*?)(?:[,.]| and | or |$)")

func TestAgentsMdPointerRegexIsLineAware(t *testing.T) {
	targets := func(s string) []string {
		var got []string
		for _, m := range pointerRe.FindAllStringSubmatch(s, -1) {
			got = append(got, strings.TrimSpace(m[1]))
		}
		return got
	}

	require.Equal(t,
		[]string{"`internal/shared/tpl/`", "Core — Execution (`pipeline/`)", "Core — Validation"},
		targets("  See § `internal/shared/tpl/`, § Core — Execution (`pipeline/`) and § Core — Validation.\n"),
		"a multi-target pointer line must split on the separators")

	// A pointer that ends its line with no trailing punctuation must stop at the
	// newline instead of consuming the following line.
	require.Equal(t,
		[]string{"Core — Workflow", "`internal/cli/test/`"},
		targets("  See § Core — Workflow\n  See § `internal/cli/test/`.\n"),
		"an unterminated pointer must not swallow the next line")
}

// TestAgentsMdPointersResolve keeps the `§` pointers honest: a bullet that
// delegates its write-up to packages.md is only useful if the named section
// actually exists there. A renamed package or heading breaks this, not a reader.
func TestAgentsMdPointersResolve(t *testing.T) {
	agents := readRepoFile(t, "AGENTS.md")
	packages := readRepoFile(t, "docs/internals/packages.md")

	bulletRe := regexp.MustCompile("^\\s*- `([^`]+)`")
	paths := map[string]bool{}
	headings := []string{}
	for line := range strings.SplitSeq(packages, "\n") {
		if m := bulletRe.FindStringSubmatch(line); m != nil {
			paths[m[1]] = true
		}
		if title, ok := strings.CutPrefix(line, "## "); ok {
			headings = append(headings, strings.TrimSpace(title))
		}
	}
	require.NotEmpty(t, headings, "packages.md has no `## ` headings")

	matches := pointerRe.FindAllStringSubmatch(agents, -1)
	require.NotEmpty(t, matches, "AGENTS.md has no `§` pointers")

	for _, m := range matches {
		target := strings.TrimSpace(m[1])
		if path, ok := strings.CutPrefix(target, "`"); ok {
			path = strings.TrimSuffix(path, "`")
			if strings.HasSuffix(path, ".md") {
				_, err := os.Stat(filepath.Join(repoRoot(t), path))
				require.NoErrorf(t, err, "AGENTS.md points at `§ %s`, which does not exist", path)
				continue
			}
			require.Truef(t, paths[path],
				"AGENTS.md points at `§ %s`, which is not a package bullet in docs/internals/packages.md", path)
			continue
		}
		title := strings.TrimSpace(strings.SplitN(target, " (", 2)[0])
		found := false
		for _, h := range headings {
			if h == title || strings.HasPrefix(h, title+" (") {
				found = true
				break
			}
		}
		require.Truef(t, found,
			"AGENTS.md points at `§ %s`, which is not a `## ` heading in docs/internals/packages.md", title)
	}
}
