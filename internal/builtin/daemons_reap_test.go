package builtin

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/docker"
	"devbox-cli/internal/render"
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

func TestParseDaemonsPSOutput_NameFallback(t *testing.T) {
	// Older Docker versions may emit "Name" instead of "Names".
	in := strings.NewReader(`{"Name":"proj-foo","Image":"bar"}`)
	got, err := parseDaemonsPSOutput(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "proj-foo" {
		t.Fatalf("got %v, want [proj-foo]", got)
	}
}

func TestDaemonsReap_Registered(t *testing.T) {
	if _, ok := Get("daemons_reap", CtxInternal); !ok {
		t.Fatal("daemons_reap builtin not registered")
	}
}

func TestDaemonsReap_Validate_RejectsUnknownKeys(t *testing.T) {
	err := Validate("daemons_reap", map[string]any{"foo": "bar"}, CtxInternal)
	if err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("expected unknown key error, got: %v", err)
	}
}

func TestDaemonsReap_Validate_EmptyOK(t *testing.T) {
	if err := Validate("daemons_reap", nil, CtxInternal); err != nil {
		t.Fatalf("nil with: should validate, got: %v", err)
	}
	if err := Validate("daemons_reap", map[string]any{}, CtxInternal); err != nil {
		t.Fatalf("empty with: should validate, got: %v", err)
	}
}

func newReapExecContext(buf *bytes.Buffer) ExecContext {
	cfg := &config.DevboxConfig{}
	cfg.Project.Name = "testproj"
	return ExecContext{
		Config:       cfg,
		DockerConfig: &config.DockerConfig{},
		ProjectRoot:  "/tmp",
		Output:       render.NewWriter(buf),
	}
}

func TestDaemonsReap_Run_NilConfig(t *testing.T) {
	err := daemonsReapBuiltin{}.Run(context.Background(), nil, ExecContext{})
	if err == nil || !strings.Contains(err.Error(), "config not available") {
		t.Fatalf("expected config-not-available error, got: %v", err)
	}
}

func TestDaemonsReap_Run_ListError(t *testing.T) {
	orig := listDaemonsFn
	defer func() { listDaemonsFn = orig }()
	listDaemonsFn = func(_ context.Context, _ *docker.Compose, _ string) ([]string, error) {
		return nil, errors.New("permission denied")
	}

	var buf bytes.Buffer
	ectx := newReapExecContext(&buf)
	err := daemonsReapBuiltin{}.Run(context.Background(), nil, ectx)
	if err != nil {
		t.Fatalf("list error should be best-effort (nil return), got: %v", err)
	}
	if !strings.Contains(buf.String(), "warning:") {
		t.Errorf("expected warning in output, got: %q", buf.String())
	}
}

func TestDaemonsReap_Run_NoDaemons(t *testing.T) {
	orig := listDaemonsFn
	defer func() { listDaemonsFn = orig }()
	listDaemonsFn = func(_ context.Context, _ *docker.Compose, _ string) ([]string, error) {
		return nil, nil
	}

	var buf bytes.Buffer
	ectx := newReapExecContext(&buf)
	err := daemonsReapBuiltin{}.Run(context.Background(), nil, ectx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "no daemons running") {
		t.Errorf("expected 'no daemons running' in output, got: %q", buf.String())
	}
}
