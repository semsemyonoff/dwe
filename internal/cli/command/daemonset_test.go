package command

import (
	"reflect"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/shared/daemon"
)

func TestBuildDaemonSetPSArgs_endToEnd(t *testing.T) {
	got := buildDaemonSetPSArgs("my-proj", "services.main.queue")
	want := []string{
		"ps",
		"--filter", "label=dwe.project=my-proj",
		"--filter", "label=dwe.daemon.id=services.main.queue",
		"--format=json",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestParseDaemonParamValuesForKey_modernLabels(t *testing.T) {
	// docker ps --format=json output: Labels is an object map.
	nd := `{"Names":"my-proj-queue_emails","Labels":{"dwe.project":"my-proj","dwe.daemon.id":"queue","dwe.daemon.params":"{\"name\":\"emails\"}"}}
{"Names":"my-proj-queue_default","Labels":{"dwe.project":"my-proj","dwe.daemon.id":"queue","dwe.daemon.params":"{\"name\":\"default\"}"}}
{"Names":"my-proj-queue_emails2","Labels":{"dwe.project":"my-proj","dwe.daemon.id":"queue","dwe.daemon.params":"{\"name\":\"emails\"}"}}
`
	got := parseDaemonParamValuesForKey(strings.NewReader(nd), "name")
	want := []string{"default", "emails"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("values mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestParseDaemonParamValuesForKey_legacyStringLabels(t *testing.T) {
	// Legacy: Labels is a comma-separated string.
	nd := `{"Names":"my-proj-queue_a","Labels":"dwe.project=my-proj,dwe.daemon.id=queue,dwe.daemon.params={\"name\":\"a\"}"}
{"Names":"my-proj-queue_b","Labels":"dwe.project=my-proj,dwe.daemon.id=queue,dwe.daemon.params={\"name\":\"b\"}"}
`
	got := parseDaemonParamValuesForKey(strings.NewReader(nd), "name")
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy values mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestParseDaemonParamValuesForKey_legacyMultiParamJSON(t *testing.T) {
	// Legacy string where dwe.daemon.params contains a multi-key JSON
	// object. Commas inside the JSON must not split the entry.
	nd := `{"Names":"my-proj-queue_a","Labels":"dwe.project=my-proj,dwe.daemon.id=queue,dwe.daemon.params={\"name\":\"a\",\"queue\":\"emails\"}"}
`
	got := parseDaemonParamValuesForKey(strings.NewReader(nd), "name")
	want := []string{"a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("multi-param legacy values mismatch\n got: %v\nwant: %v", got, want)
	}
	got2 := parseDaemonParamValuesForKey(strings.NewReader(nd), "queue")
	want2 := []string{"emails"}
	if !reflect.DeepEqual(got2, want2) {
		t.Fatalf("multi-param legacy values (queue) mismatch\n got: %v\nwant: %v", got2, want2)
	}
}

func TestParseDaemonParamValuesForKey_legacySpecialCharsInValue(t *testing.T) {
	// A JSON string value containing '}' and ',' must not corrupt parsing.
	nd := `{"Names":"my-proj-queue_a","Labels":"dwe.project=my-proj,dwe.daemon.id=queue,dwe.daemon.params={\"name\":\"foo},bar\",\"queue\":\"emails\"}"}
`
	got := parseDaemonParamValuesForKey(strings.NewReader(nd), "name")
	want := []string{"foo},bar"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("special-chars name mismatch\n got: %v\nwant: %v", got, want)
	}
	got2 := parseDaemonParamValuesForKey(strings.NewReader(nd), "queue")
	want2 := []string{"emails"}
	if !reflect.DeepEqual(got2, want2) {
		t.Fatalf("special-chars queue mismatch\n got: %v\nwant: %v", got2, want2)
	}
}

func TestParseDaemonParamValuesForKey_missingKey(t *testing.T) {
	nd := `{"Names":"x","Labels":{"dwe.daemon.params":"{\"queue\":\"emails\"}"}}
`
	got := parseDaemonParamValuesForKey(strings.NewReader(nd), "name")
	if len(got) != 0 {
		t.Fatalf("expected no values for missing key, got %v", got)
	}
}

func TestParseDaemonParamValuesForKey_invalidLines(t *testing.T) {
	nd := `not-json
{"Names":"x","Labels":{"dwe.daemon.params":"{\"name\":\"good\"}"}}

{"Names":"y","Labels":{"dwe.daemon.params":"not-json"}}
`
	got := parseDaemonParamValuesForKey(strings.NewReader(nd), "name")
	want := []string{"good"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected to skip invalid lines, got %v", got)
	}
}

func TestParseDaemonParamValuesForKey_emptyInput(t *testing.T) {
	got := parseDaemonParamValuesForKey(strings.NewReader(""), "name")
	if len(got) != 0 {
		t.Fatalf("expected no values for empty input, got %v", got)
	}
}

func TestParseDaemonParamValuesForKey_nonStringValueCoerced(t *testing.T) {
	// Param values can be non-strings in the JSON (e.g. numbers); coerce.
	nd := `{"Labels":{"dwe.daemon.params":"{\"workers\":3}"}}
{"Labels":{"dwe.daemon.params":"{\"workers\":5}"}}
`
	got := parseDaemonParamValuesForKey(strings.NewReader(nd), "workers")
	want := []string{"3", "5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("coerced values mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestDecodeLabelsForCompletion_emptyRawReturnsNil(t *testing.T) {
	if got := daemon.DecodeLabels(nil); got != nil {
		t.Errorf("expected nil for empty raw, got %v", got)
	}
}

func TestDecodeLabelsForCompletion_neitherShapeReturnsNil(t *testing.T) {
	// A number is neither a map nor a string.
	got := daemon.DecodeLabels([]byte(`42`))
	if got != nil {
		t.Errorf("expected nil for non-map non-string Labels, got %v", got)
	}
}
