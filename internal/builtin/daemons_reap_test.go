package builtin

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseDaemonsPSOutput_NDJSON(t *testing.T) {
	in := strings.NewReader(`{"Names":"proj-php_queue_default","Image":"foo"}
{"Names":"proj-php_queue_emails","Image":"foo"}
`)
	got, err := parseDaemonsPSOutput(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"proj-php_queue_default", "proj-php_queue_emails"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseDaemonsPSOutput_EmptyAndJunkLines(t *testing.T) {
	in := strings.NewReader("\n   \n{not valid json}\n{\"Names\":\"only\"}\n")
	got, err := parseDaemonsPSOutput(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"only"}) {
		t.Fatalf("got %v, want [only]", got)
	}
}

func TestParseDaemonsPSOutput_CommaSeparatedNames(t *testing.T) {
	// Older docker may return multiple comma-joined aliases; first wins.
	in := strings.NewReader(`{"Names":"primary,alias1,alias2"}`)
	got, err := parseDaemonsPSOutput(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"primary"}) {
		t.Fatalf("got %v, want [primary]", got)
	}
}

func TestParseDaemonsPSOutput_DedupAndSort(t *testing.T) {
	in := strings.NewReader(`{"Names":"b"}
{"Names":"a"}
{"Names":"b"}
`)
	got, err := parseDaemonsPSOutput(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("got %v, want [a b]", got)
	}
}

func TestDaemonsReap_Registered(t *testing.T) {
	if _, ok := Get("daemons_reap"); !ok {
		t.Fatal("daemons_reap builtin not registered")
	}
}

func TestDaemonsReap_Validate_RejectsUnknownKeys(t *testing.T) {
	err := Validate("daemons_reap", map[string]any{"foo": "bar"})
	if err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("expected unknown key error, got: %v", err)
	}
}

func TestDaemonsReap_Validate_EmptyOK(t *testing.T) {
	if err := Validate("daemons_reap", nil); err != nil {
		t.Fatalf("nil with: should validate, got: %v", err)
	}
	if err := Validate("daemons_reap", map[string]any{}); err != nil {
		t.Fatalf("empty with: should validate, got: %v", err)
	}
}
