package varsusage

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

const fixtureEnumProj = "testdata/enumproj"

// enumLoc is a compact (file, line, ref) tuple for asserting enumeration
// results without pinning the exact source text.
type enumLoc struct {
	File string
	Line int
	Ref  string
}

func enumLocsOf(usages []Usage) []enumLoc {
	out := make([]enumLoc, 0, len(usages))
	for _, u := range usages {
		out = append(out, enumLoc{u.File, u.Line, u.Ref})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Ref < out[j].Ref
	})
	return out
}

// TestEnumerateAllUsages pins that every ${...} shorthand reference is
// enumerated regardless of head namespace (vars, project, and even an
// unknown head like HOME all surface — filtering to known/resolvable heads
// is the validator's job, not the scanner's), that a nested with: value
// (map-of-map, and inside a sequence) is reached by recursion, that a config
// render template under workspace/templates/config/** is raw-text scanned
// (and its Go-template `{{ resolve .Raw "vars.x" }}` form is NOT enumerated —
// enumeration covers the ${...} shorthand only), and that two references on
// one line both surface as distinct usages.
func TestEnumerateAllUsages(t *testing.T) {
	usages, err := EnumerateAllUsages(fixtureEnumProj)
	if err != nil {
		t.Fatalf("EnumerateAllUsages: %v", err)
	}
	got := enumLocsOf(usages)
	want := []enumLoc{
		// argv: and compose_args: are sequences whose elements each render
		// through tpl.RenderCommand (runio.RenderArgvWithArgs /
		// service.buildRenderedComposeArgs), and argv_append_from is a scalar on
		// the same substrate as cmd:. A typo in any of them renders to "" —
		// dropping an argument, a compose flag, or changing which items the
		// expression computes — so all three have to be enumerated.
		{"workspace/commands/quality.yml", 5, "vars.lint.config"},
		{"workspace/commands/quality.yml", 6, "vars.source.dir"},
		{"workspace/commands/quality.yml", 7, "vars.lint.profile"},
		{"workspace/deploy.yml", 6, "vars.source.repo"},
		{"workspace/deploy.yml", 9, "vars.source.branch"},
		{"workspace/deploy.yml", 11, "project.name"},
		{"workspace/deploy.yml", 17, "HOME"},
		{"workspace/deploy.yml", 20, "vars.db.host"},
		{"workspace/deploy.yml", 20, "vars.db.port"},
		// timeout: and files_gate.command: are rendered by the pipeline resolver
		// alongside cmd:, so the scanner must see them too — otherwise a typo
		// there renders to "" (an unbounded step, a gate falling back to the
		// step's own cmd) with no validator reporting it.
		{"workspace/deploy.yml", 24, "vars.timeouts.migrate"},
		{"workspace/deploy.yml", 26, "vars.gate.command"},
		{"workspace/templates/config/app/.env.tmpl", 1, "vars.db.host"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EnumerateAllUsages() locs =\n  %v\nwant\n  %v", got, want)
	}
}

// TestEnumerateAllUsages_MultipleRefsOnOneLineAreOrdered guards the dedupe key
// and the sort comparator against each other: two references on one line are
// identical in File/Line/Kind/Text and differ only in Ref, so Ref must be both
// part of the dedupe key (else one is dropped) and the final sort tiebreaker
// (else sort.Slice, which is not stable, orders them arbitrarily and the
// resulting config.template_refs diagnostics flap between runs).
func TestEnumerateAllUsages_MultipleRefsOnOneLineAreOrdered(t *testing.T) {
	var first []Usage
	for i := range 5 {
		usages, err := EnumerateAllUsages(fixtureEnumProj)
		if err != nil {
			t.Fatalf("EnumerateAllUsages: %v", err)
		}
		var line20 []string
		for _, u := range usages {
			if u.File == "workspace/deploy.yml" && u.Line == 20 {
				line20 = append(line20, u.Ref)
			}
		}
		if want := []string{"vars.db.host", "vars.db.port"}; !reflect.DeepEqual(line20, want) {
			t.Fatalf("run %d: refs on deploy.yml:20 = %v, want %v (both kept, in Ref order)", i, line20, want)
		}
		if i == 0 {
			first = usages
			continue
		}
		if !reflect.DeepEqual(usages, first) {
			t.Fatalf("run %d: enumeration order differs from run 0", i)
		}
	}
}

// TestScanUsages_MultipleMatchingRefsOnOneLineDedupe pins that the query path
// reports ONE usage for a line carrying two references that both match the
// query. Usage.Ref is deliberately empty on this path so it stays out of the
// dedupe key: it is not rendered anywhere, so populating it would show the
// user the same source line twice under an inflated "Usages (N)" count.
func TestScanUsages_MultipleMatchingRefsOnOneLineDedupe(t *testing.T) {
	res, err := ScanUsages(fixtureEnumProj, "vars.db")
	if err != nil {
		t.Fatalf("ScanUsages: %v", err)
	}
	var yamlHits []Usage
	for _, u := range res.Usages {
		if u.File == "workspace/deploy.yml" {
			yamlHits = append(yamlHits, u)
		}
	}
	if len(yamlHits) != 1 {
		t.Fatalf("ScanUsages(vars.db) = %d usages in deploy.yml, want 1: %+v", len(yamlHits), yamlHits)
	}
	if yamlHits[0].Line != 20 {
		t.Errorf("usage line = %d, want 20", yamlHits[0].Line)
	}
	if yamlHits[0].Ref != "" {
		t.Errorf("Ref = %q on the query path, want empty (see Usage.Ref)", yamlHits[0].Ref)
	}
}

// TestWithRecursion pins that templatedMapKeys (with:/env:) scanning recurses
// through nested maps and sequences — not only direct scalar children —
// matching what the pipeline render helper actually renders at any depth.
// Exercised here through the query-driven ScanUsages path, which shares the
// same recursive walk as EnumerateAllUsages.
func TestWithRecursion(t *testing.T) {
	res, err := ScanUsages(fixtureEnumProj, "vars.source.branch")
	if err != nil {
		t.Fatalf("ScanUsages: %v", err)
	}
	if len(res.Usages) != 1 {
		t.Fatalf("ScanUsages(vars.source.branch) = %d usages, want 1: %+v", len(res.Usages), res.Usages)
	}
	if res.Usages[0].Line != 9 {
		t.Errorf("ScanUsages(vars.source.branch) line = %d, want 9", res.Usages[0].Line)
	}
}

// TestEnumerateAllUsages_SkipsServiceSrc pins that a service's src/ hub — the
// application's own checkout, which dwe never authors templates into — is not
// walked. config.template_refs runs this scan on every `dwe validate`, so a
// hit here would both cost a node_modules-sized traversal and warn about a
// file dwe does not own.
func TestEnumerateAllUsages_SkipsServiceSrc(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "workspace", "services", "app", "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	// A file dwe does not own, shaped so the scanner would hit it if walked.
	if err := os.WriteFile(filepath.Join(src, "ci.yml"), []byte("cmd: echo ${vars.db.host}\n"), 0o644); err != nil {
		t.Fatalf("write src yaml: %v", err)
	}
	// A sibling dwe-owned file, to prove the walk still happens at all.
	svc := filepath.Join(root, "workspace", "services", "app")
	if err := os.WriteFile(filepath.Join(svc, "deploy.yml"), []byte("cmd: echo ${vars.db.port}\n"), 0o644); err != nil {
		t.Fatalf("write service yaml: %v", err)
	}

	usages, err := EnumerateAllUsages(root)
	if err != nil {
		t.Fatalf("EnumerateAllUsages: %v", err)
	}
	got := enumLocsOf(usages)
	want := []enumLoc{{"workspace/services/app/deploy.yml", 1, "vars.db.port"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EnumerateAllUsages() locs = %v, want %v", got, want)
	}
}
