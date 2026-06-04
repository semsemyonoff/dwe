package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// countLines counts how many lines of s equal want exactly (after trimming
// surrounding whitespace), so substring matches like ".dwe/snapshots/" do not
// inflate the count for the bare "snapshots/" pattern.
func countLines(s, want string) int {
	n := 0
	for line := range strings.SplitSeq(s, "\n") {
		if strings.TrimSpace(line) == want {
			n++
		}
	}
	return n
}

func TestMergeGitignore_Absent(t *testing.T) {
	merged, changed := mergeGitignore(nil)
	if !changed {
		t.Fatal("expected changed=true for an absent .gitignore")
	}
	if string(merged) != dweGitignoreBlock {
		t.Fatalf("absent merge should equal the full block, got:\n%s", merged)
	}
	// Sanity: the canonical block carries every pattern.
	for _, p := range dweGitignorePatterns() {
		if !strings.Contains(string(merged), p) {
			t.Fatalf("merged block missing pattern %q", p)
		}
	}
}

func TestMergeGitignore_EmptyTreatedAsAbsent(t *testing.T) {
	merged, changed := mergeGitignore([]byte("   \n\n\t\n"))
	if !changed {
		t.Fatal("expected changed=true for a whitespace-only .gitignore")
	}
	if string(merged) != dweGitignoreBlock {
		t.Fatalf("whitespace-only merge should equal the full block, got:\n%s", merged)
	}
}

func TestMergeGitignore_PresentWithoutBlock(t *testing.T) {
	existing := []byte("node_modules/\n*.log\n")
	merged, changed := mergeGitignore(existing)
	if !changed {
		t.Fatal("expected changed=true when no DWE patterns are present")
	}
	got := string(merged)

	// The user's content is preserved verbatim at the head.
	if !strings.HasPrefix(got, "node_modules/\n*.log\n") {
		t.Fatalf("user content not preserved at head:\n%s", got)
	}
	// A marker comment introduces the appended section.
	if !strings.Contains(got, gitignoreMarker) {
		t.Fatalf("missing marker comment:\n%s", got)
	}
	// Every DWE pattern is appended.
	for _, p := range dweGitignorePatterns() {
		if !strings.Contains(got, "\n"+p+"\n") {
			t.Fatalf("missing appended pattern %q:\n%s", p, got)
		}
	}
}

func TestMergeGitignore_PresentWithSomeLines(t *testing.T) {
	// The user already ignores two of the DWE patterns.
	existing := []byte("volumes/\nsnapshots/\nbuild/\n")
	merged, changed := mergeGitignore(existing)
	if !changed {
		t.Fatal("expected changed=true when some DWE patterns are missing")
	}
	got := string(merged)

	// Already-present patterns are NOT duplicated (count exact lines, so that
	// e.g. ".dwe/snapshots/" does not match the bare "snapshots/" pattern).
	if n := countLines(got, "volumes/"); n != 1 {
		t.Fatalf("volumes/ appears %d times, want 1:\n%s", n, got)
	}
	if n := countLines(got, "snapshots/"); n != 1 {
		t.Fatalf("snapshots/ appears %d times, want 1:\n%s", n, got)
	}
	// Missing patterns are appended.
	if !strings.Contains(got, ".dwe/deploy/") {
		t.Fatalf("missing pattern .dwe/deploy/ not appended:\n%s", got)
	}
}

func TestMergeGitignore_Idempotent(t *testing.T) {
	first, changed := mergeGitignore([]byte("custom/\n"))
	if !changed {
		t.Fatal("expected first merge to change")
	}
	second, changed := mergeGitignore(first)
	if changed {
		t.Fatalf("expected second merge to be a no-op, got changes:\n%s", second)
	}
	if string(second) != string(first) {
		t.Fatalf("idempotent merge altered content:\n%s", second)
	}
}

func TestMergeGitignore_NoTrailingNewline(t *testing.T) {
	// User content without a trailing newline must not have its last line fused
	// with the appended block.
	existing := []byte("dist/")
	merged, changed := mergeGitignore(existing)
	if !changed {
		t.Fatal("expected changed=true")
	}
	got := string(merged)
	if !strings.HasPrefix(got, "dist/\n") {
		t.Fatalf("expected a newline inserted after user content, got:\n%q", got)
	}
}

func TestApplyGitignore_CreatesWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")

	written, err := applyGitignore(path)
	if err != nil {
		t.Fatalf("applyGitignore: %v", err)
	}
	if !written {
		t.Fatal("expected written=true for an absent file")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != dweGitignoreBlock {
		t.Fatalf("created file should equal the full block, got:\n%s", got)
	}
}

func TestApplyGitignore_AppendsAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(path, []byte("node_modules/\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	written, err := applyGitignore(path)
	if err != nil {
		t.Fatalf("applyGitignore (first): %v", err)
	}
	if !written {
		t.Fatal("expected written=true on first apply")
	}
	afterFirst, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.HasPrefix(string(afterFirst), "node_modules/\n") {
		t.Fatalf("user content not preserved:\n%s", afterFirst)
	}

	// Second apply must be a no-op.
	written, err = applyGitignore(path)
	if err != nil {
		t.Fatalf("applyGitignore (second): %v", err)
	}
	if written {
		t.Fatal("expected written=false on the idempotent second apply")
	}
	afterSecond, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(afterSecond) != string(afterFirst) {
		t.Fatalf("second apply changed the file:\n%s", afterSecond)
	}
}
