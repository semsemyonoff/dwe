package docs

import (
	"io/fs"
	"testing"
)

func TestBuiltinFS(t *testing.T) {
	// Smoke test: BuiltinFS should contain reference/config/workspace.md
	// (or be empty if the sync hasn't run, which is tolerated in tests).
	if BuiltinFS == nil {
		t.Fatal("BuiltinFS is nil")
	}

	_, err := fs.Stat(BuiltinFS, "reference/config/workspace.md")
	if err != nil && err != fs.ErrNotExist {
		t.Fatalf("unexpected error checking for reference/config/workspace.md: %v", err)
	}
	// If file doesn't exist, that's fine for tests (sync may not have run yet).
	// This test just ensures BuiltinFS is initialized and accessible.
}

func TestBuiltinFS_WorkspacePackTopics(t *testing.T) {
	roots := Sources("")
	for _, topic := range []string{"render/workspace", "guides/run-ralphex-in-a-workspace"} {
		for _, locale := range []string{"en", "ru"} {
			t.Run(topic+"/"+locale, func(t *testing.T) {
				resolved, err := Resolve(roots, topic, locale)
				if err != nil {
					t.Fatalf("resolve embedded topic: %v", err)
				}
				content, lang, stale, err := ResolveContent(roots[0], resolved.Path+".md", locale)
				if err != nil {
					t.Fatalf("read embedded topic: %v", err)
				}
				if lang != locale || stale {
					t.Fatalf("translation: lang=%q, stale=%v; want lang=%q, stale=false", lang, stale, locale)
				}
				title, headings := ParseDoc(content)
				if title == "" || len(headings) == 0 {
					t.Fatal("topic must have a title and navigable sections")
				}
			})
		}
	}
}
