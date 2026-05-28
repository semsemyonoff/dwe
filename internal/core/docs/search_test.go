package docs

import (
	"testing"
	"testing/fstest"
)

func TestSearch_AttributesToNearestSection(t *testing.T) {
	doc := []byte("" +
		"# Title\n\n" +
		"Lead paragraph mentions depends_on once.\n\n" +
		"## Alpha\n\n" +
		"Alpha body has depends_on twice: depends_on.\n\n" +
		"```yaml\n" +
		"services:\n" +
		"  depends_on:\n" +
		"    - redis\n" +
		"```\n\n" +
		"## Beta\n\n" +
		"Beta does not mention the keyword.\n",
	)
	roots := []DocRoot{{
		Name: "devbox",
		FS:   fstest.MapFS{"config/services.md": &fstest.MapFile{Data: doc}},
	}}

	hits := Search(roots, "depends_on", "en")
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits (lead + alpha), got %d: %+v", len(hits), hits)
	}

	// Highest count first: alpha has 3 (body twice + fenced YAML once).
	if hits[0].Section != "alpha" || hits[0].Count != 3 {
		t.Errorf("hit[0] = {Section: %q, Count: %d}, want {alpha, 3}", hits[0].Section, hits[0].Count)
	}
	// Lead paragraph has no section attribution.
	if hits[1].Section != "" || hits[1].Count != 1 {
		t.Errorf("hit[1] = {Section: %q, Count: %d}, want {<empty>, 1}", hits[1].Section, hits[1].Count)
	}
}

func TestSearch_CaseInsensitive(t *testing.T) {
	doc := []byte("# T\n\n## Section\n\nFooBar foobar FOOBAR\n")
	roots := []DocRoot{{
		Name: "devbox",
		FS:   fstest.MapFS{"x.md": &fstest.MapFile{Data: doc}},
	}}
	hits := Search(roots, "foobar", "en")
	if len(hits) != 1 || hits[0].Count != 3 {
		t.Fatalf("expected 1 hit with Count=3, got %+v", hits)
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	roots := []DocRoot{{
		Name: "devbox",
		FS:   fstest.MapFS{"x.md": &fstest.MapFile{Data: []byte("# T\n\nbody\n")}},
	}}
	if hits := Search(roots, "", "en"); hits != nil {
		t.Errorf("empty query should return nil, got %+v", hits)
	}
}

func TestSearch_SortByCountThenPath(t *testing.T) {
	roots := []DocRoot{{
		Name: "devbox",
		FS: fstest.MapFS{
			"b.md": &fstest.MapFile{Data: []byte("# T\n\n## A\n\nneedle needle needle\n")},
			"a.md": &fstest.MapFile{Data: []byte("# T\n\n## A\n\nneedle needle needle\n")},
			"c.md": &fstest.MapFile{Data: []byte("# T\n\n## A\n\nneedle\n")},
		},
	}}
	hits := Search(roots, "needle", "en")
	if len(hits) != 3 {
		t.Fatalf("expected 3 hits, got %d", len(hits))
	}
	// Ties broken by path ascending.
	if hits[0].Path != "a" || hits[1].Path != "b" || hits[2].Path != "c" {
		t.Errorf("unexpected order: %+v", hits)
	}
}
