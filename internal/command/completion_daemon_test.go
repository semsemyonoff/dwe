package command

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildDaemonSetPSArgs_endToEnd(t *testing.T) {
	got := buildDaemonSetPSArgs("my-proj", "services.main.queue")
	want := []string{
		"ps",
		"--filter", "label=devbox.project=my-proj",
		"--filter", "label=devbox.daemon.id=services.main.queue",
		"--format=json",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestParseDaemonParamValuesForKey_modernLabels(t *testing.T) {
	// docker ps --format=json output: Labels is an object map.
	nd := `{"Names":"my-proj-queue_emails","Labels":{"devbox.project":"my-proj","devbox.daemon.id":"queue","devbox.daemon.params":"{\"name\":\"emails\"}"}}
{"Names":"my-proj-queue_default","Labels":{"devbox.project":"my-proj","devbox.daemon.id":"queue","devbox.daemon.params":"{\"name\":\"default\"}"}}
{"Names":"my-proj-queue_emails2","Labels":{"devbox.project":"my-proj","devbox.daemon.id":"queue","devbox.daemon.params":"{\"name\":\"emails\"}"}}
`
	got := parseDaemonParamValuesForKey(strings.NewReader(nd), "name")
	want := []string{"default", "emails"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("values mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestParseDaemonParamValuesForKey_legacyStringLabels(t *testing.T) {
	// Legacy: Labels is a comma-separated string.
	nd := `{"Names":"my-proj-queue_a","Labels":"devbox.project=my-proj,devbox.daemon.id=queue,devbox.daemon.params={\"name\":\"a\"}"}
{"Names":"my-proj-queue_b","Labels":"devbox.project=my-proj,devbox.daemon.id=queue,devbox.daemon.params={\"name\":\"b\"}"}
`
	got := parseDaemonParamValuesForKey(strings.NewReader(nd), "name")
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy values mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestParseDaemonParamValuesForKey_missingKey(t *testing.T) {
	nd := `{"Names":"x","Labels":{"devbox.daemon.params":"{\"queue\":\"emails\"}"}}
`
	got := parseDaemonParamValuesForKey(strings.NewReader(nd), "name")
	if len(got) != 0 {
		t.Fatalf("expected no values for missing key, got %v", got)
	}
}

func TestParseDaemonParamValuesForKey_invalidLines(t *testing.T) {
	nd := `not-json
{"Names":"x","Labels":{"devbox.daemon.params":"{\"name\":\"good\"}"}}

{"Names":"y","Labels":{"devbox.daemon.params":"not-json"}}
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
	nd := `{"Labels":{"devbox.daemon.params":"{\"workers\":3}"}}
{"Labels":{"devbox.daemon.params":"{\"workers\":5}"}}
`
	got := parseDaemonParamValuesForKey(strings.NewReader(nd), "workers")
	want := []string{"3", "5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("coerced values mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestDecodeLabelsForCompletion_emptyRawReturnsNil(t *testing.T) {
	if got := decodeLabelsForCompletion(nil); got != nil {
		t.Errorf("expected nil for empty raw, got %v", got)
	}
}

func TestDecodeLabelsForCompletion_neitherShapeReturnsNil(t *testing.T) {
	// A number is neither a map nor a string.
	got := decodeLabelsForCompletion([]byte(`42`))
	if got != nil {
		t.Errorf("expected nil for non-map non-string Labels, got %v", got)
	}
}
