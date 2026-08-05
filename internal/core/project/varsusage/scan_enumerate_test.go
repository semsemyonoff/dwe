package varsusage

import (
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
// is the validator's job, not the scanner's), and that a nested with: value
// (map-of-map, and inside a sequence) is reached by recursion.
func TestEnumerateAllUsages(t *testing.T) {
	usages, err := EnumerateAllUsages(fixtureEnumProj)
	if err != nil {
		t.Fatalf("EnumerateAllUsages: %v", err)
	}
	got := enumLocsOf(usages)
	want := []enumLoc{
		{"workspace/deploy.yml", 6, "vars.source.repo"},
		{"workspace/deploy.yml", 9, "vars.source.branch"},
		{"workspace/deploy.yml", 11, "project.name"},
		{"workspace/deploy.yml", 17, "HOME"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EnumerateAllUsages() locs =\n  %v\nwant\n  %v", got, want)
	}
}

// TestWithRecursion pins that templatedMapKeys (with:/env:) scanning recurses
// through nested maps and sequences — not only direct scalar children —
// matching what Task 2's pipeline render helper actually renders at any
// depth. Exercised here through the query-driven ScanUsages path, which
// shares the same recursive walk as EnumerateAllUsages.
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
