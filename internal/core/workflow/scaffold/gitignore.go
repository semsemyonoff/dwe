package scaffold

import (
	"bytes"
	"fmt"
	"os"
	"strings"
)

// gitignoreMarker introduces the DWE-managed section appended to a pre-existing
// .gitignore. The missing DWE patterns are listed beneath it so the user can see
// (and, if they wish, relocate) the lines the CLI added.
const gitignoreMarker = "# dwe (managed by the CLI)"

// dweGitignoreBlock is the canonical .gitignore content for a fresh DWE project.
// It is written verbatim when no .gitignore exists; when one already exists, only
// the pattern lines missing from it are appended under gitignoreMarker (see
// mergeGitignore).
//
// We ignore the .dwe/ runtime subdirs/files individually (not .dwe/ wholesale) so
// the committed .dwe/config template stays tracked. The real lock files
// (.dwe/deploy/deploy.lock, .dwe/snapshots/snapshot.lock) are already covered by
// the subdir entries, so there is no separate *.lock line.
const dweGitignoreBlock = `# dwe — runtime data (managed by the CLI)
.dwe/deploy/
.dwe/snapshots/
.dwe/logs/
.dwe/prompt-cache.yml
# dwe — per-developer overrides
workspace/local.yml
workspace/docker.local.yml
# dwe — container data
volumes/
snapshots/
`

// dweGitignorePatterns returns the ignore-pattern lines from the canonical block,
// in order, skipping comment and blank lines.
func dweGitignorePatterns() []string {
	var patterns []string
	for line := range strings.SplitSeq(dweGitignoreBlock, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		patterns = append(patterns, trimmed)
	}
	return patterns
}

// mergeGitignore computes the desired .gitignore content given the existing
// content (nil/empty when the file is absent). When the file is absent it returns
// the full canonical block; otherwise it appends — under a single marker comment —
// only the DWE patterns not already present, preserving the user's content (and
// trailing-newline style) exactly. changed is false when nothing needs writing
// (the file already contains every DWE pattern), making re-runs idempotent.
func mergeGitignore(existing []byte) (merged []byte, changed bool) {
	if len(bytes.TrimSpace(existing)) == 0 {
		return []byte(dweGitignoreBlock), true
	}

	present := make(map[string]bool)
	for line := range strings.SplitSeq(string(existing), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		present[trimmed] = true
	}

	var missing []string
	for _, p := range dweGitignorePatterns() {
		if !present[p] {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return existing, false
	}

	var buf bytes.Buffer
	buf.Write(existing)
	// Separate our block from the user's content without rewriting their lines:
	// ensure the existing content ends with a newline, then add one blank line.
	if !bytes.HasSuffix(existing, []byte("\n")) {
		buf.WriteByte('\n')
	}
	buf.WriteByte('\n')
	buf.WriteString(gitignoreMarker)
	buf.WriteByte('\n')
	for _, p := range missing {
		buf.WriteString(p)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), true
}

// applyGitignore reads the .gitignore at path (if any), merges in the DWE block,
// and writes the result back atomically. It returns written=true when the file was
// created or modified, and false when it already contained every DWE pattern.
//
// Unlike writeFile's fill-gaps semantics, this always rewrites the file when the
// merge produces new content — the merge itself is the idempotency guard.
func applyGitignore(path string) (written bool, err error) {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("scaffold: read %s: %w", path, err)
	}
	merged, changed := mergeGitignore(existing)
	if !changed {
		return false, nil
	}
	return writeFile(path, merged, true)
}
