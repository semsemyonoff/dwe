package yamlstrict

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type step struct {
	Name string `yaml:"name"`
	Cmd  string `yaml:"cmd,omitempty"`
	Skip bool   `yaml:"-"`
	priv string //nolint:unused // exercises the unexported-field skip
}

type phase struct {
	Steps []step          `yaml:"steps"`
	Env   map[string]step `yaml:"env,omitempty"`
	Next  *phase          `yaml:"next,omitempty"` // cycle guard
}

type common struct {
	Log string `yaml:"log,omitempty"`
}

type pipeline struct {
	common   `yaml:",inline"`
	FailFast bool    `yaml:"fail_fast,omitempty"`
	Phases   []phase `yaml:"phases"`
	Untagged string
}

// strictStep mimics config.DeployStep: a custom UnmarshalYAML that hand-checks
// keys and raises a plain (non-*yaml.TypeError) error yaml.v3 re-raises verbatim.
type strictStep struct {
	Name string `yaml:"name"`
}

func (s *strictStep) UnmarshalYAML(value *yaml.Node) error {
	for i := 0; i < len(value.Content)-1; i += 2 {
		if key := value.Content[i].Value; key != "name" {
			return fmt.Errorf("line %d: field %s not found in type yamlstrict.strictStep", value.Content[i].Line, key)
		}
	}
	s.Name = value.Content[1].Value
	return nil
}

type strictHolder struct {
	Step strictStep `yaml:"step"`
}

func TestDecodeUnknownField(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		out      any
		file     string
		want     string
		wantLine int
		wantType string
	}{
		{
			name: "top level with line and allowed set",
			data: "fail_fast: true\ndefaults: {}\n",
			out:  &pipeline{},
			file: "workspace/deploy.yml",
			want: "workspace/deploy.yml:2: unknown field \"defaults\" — allowed here: fail_fast, log, phases, untagged\n" +
				versionHint,
			wantLine: 2,
			wantType: "yamlstrict.pipeline",
		},
		{
			name: "nested through slice of struct",
			data: "phases:\n  - steps:\n      - name: a\n        cdm: echo\n",
			out:  &pipeline{},
			file: "workspace/deploy.yml",
			want: "workspace/deploy.yml:4: unknown field \"cdm\" — allowed here: cmd, name\n" +
				versionHint,
			wantLine: 4,
			wantType: "yamlstrict.step",
		},
		{
			name: "nested through map of struct",
			data: "phases:\n  - steps: []\n    env:\n      one:\n        bogus: a\n",
			out:  &pipeline{},
			file: "workspace/deploy.yml",
			want: "workspace/deploy.yml:5: unknown field \"bogus\" — allowed here: cmd, name\n" +
				versionHint,
			wantLine: 5,
			wantType: "yamlstrict.step",
		},
		{
			name: "nested through pointer",
			data: "phases:\n  - steps: []\n    next:\n      stpes: []\n",
			out:  &pipeline{},
			file: "workspace/deploy.yml",
			want: "workspace/deploy.yml:4: unknown field \"stpes\" — allowed here: env, next, steps\n" +
				versionHint,
			wantLine: 4,
			wantType: "yamlstrict.phase",
		},
		{
			name: "empty file yields no prefix",
			data: "defaults: {}\n",
			out:  &pipeline{},
			file: "",
			want: "line 1: unknown field \"defaults\" — allowed here: fail_fast, log, phases, untagged\n" +
				versionHint,
			wantLine: 1,
			wantType: "yamlstrict.pipeline",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Decode([]byte(tt.data), tt.out, tt.file)
			if err == nil {
				t.Fatal("expected an error")
			}
			strictErr, ok := errors.AsType[*Error](err)
			if !ok {
				t.Fatalf("expected *Error, got %T: %v", err, err)
			}
			if got := err.Error(); got != tt.want {
				t.Errorf("message:\n got: %q\nwant: %q", got, tt.want)
			}
			if len(strictErr.Unknown) != 1 {
				t.Fatalf("expected 1 unknown field, got %d", len(strictErr.Unknown))
			}
			if strictErr.Unknown[0].Line != tt.wantLine {
				t.Errorf("line = %d, want %d", strictErr.Unknown[0].Line, tt.wantLine)
			}
			if strictErr.Unknown[0].Type != tt.wantType {
				t.Errorf("type = %q, want %q", strictErr.Unknown[0].Type, tt.wantType)
			}
			if strictErr.File != tt.file {
				t.Errorf("file = %q, want %q", strictErr.File, tt.file)
			}
			if _, ok := errors.AsType[*yaml.TypeError](err); !ok {
				t.Error("expected the original *yaml.TypeError to stay reachable through Unwrap")
			}
		})
	}
}

func TestDecodeInlineFieldsBelongToParent(t *testing.T) {
	// log: comes from the embedded common via ",inline" — it must decode, and it
	// must appear in the parent's allowed set (asserted above).
	var cfg pipeline
	if err := Decode([]byte("log: full\n"), &cfg, "deploy.yml"); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Log != "full" {
		t.Fatalf("Log = %q, want %q", cfg.Log, "full")
	}
}

func TestDecodeTwoUnknownFieldsOneHint(t *testing.T) {
	err := Decode([]byte("defaults: {}\nextras: {}\n"), &pipeline{}, "workspace/deploy.yml")
	if err == nil {
		t.Fatal("expected an error")
	}
	strictErr, ok := errors.AsType[*Error](err)
	if !ok {
		t.Fatalf("expected *Error, got %T", err)
	}
	if len(strictErr.Unknown) != 2 {
		t.Fatalf("expected 2 unknown fields, got %d", len(strictErr.Unknown))
	}
	msg := err.Error()
	if n := strings.Count(msg, versionHint); n != 1 {
		t.Errorf("hint appears %d times, want 1:\n%s", n, msg)
	}
	if n := strings.Count(msg, "unknown field"); n != 2 {
		t.Errorf("unknown-field lines = %d, want 2:\n%s", n, msg)
	}
	if !strings.HasSuffix(msg, versionHint) {
		t.Errorf("hint must come last:\n%s", msg)
	}
}

func TestDecodeUnreachableTypeHasNoAllowedClause(t *testing.T) {
	// The unknown field is reported against a type the decoder cannot reach from
	// out (an anonymous inline map target inside yaml itself is not modelled), so
	// simulate it by decoding into a type whose named struct is only reachable
	// behind an interface field.
	type opaque struct {
		Data any `yaml:"data"`
	}
	var target opaque
	if err := Decode([]byte("data:\n  a: 1\n"), &target, "f.yml"); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Direct construction covers the "type not in the index" branch of Error().
	e := &Error{File: "f.yml", Unknown: []UnknownField{{Line: 3, Field: "x", Type: "pkg.Hidden"}}}
	want := "f.yml:3: unknown field \"x\"\n" + versionHint
	if got := e.Error(); got != want {
		t.Errorf("message:\n got: %q\nwant: %q", got, want)
	}
}

func TestErrorPrefixForms(t *testing.T) {
	tests := []struct {
		name string
		e    *Error
		want string
	}{
		{
			name: "file without line",
			e:    &Error{File: "a.yml", Unknown: []UnknownField{{Field: "x"}}},
			want: "a.yml: unknown field \"x\"\n" + versionHint,
		},
		{
			name: "neither file nor line",
			e:    &Error{Unknown: []UnknownField{{Field: "x"}}},
			want: "unknown field \"x\"\n" + versionHint,
		},
		{
			name: "other lines carry the file and no hint",
			e:    &Error{File: "a.yml", Other: []string{"line 3: cannot unmarshal !!str `x` into int"}},
			want: "a.yml: line 3: cannot unmarshal !!str `x` into int",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.e.Error(); got != tt.want {
				t.Errorf("message:\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

func TestDecodeMixedUnknownAndTypeMismatch(t *testing.T) {
	err := Decode([]byte("fail_fast: nope\ndefaults: {}\n"), &pipeline{}, "d.yml")
	if err == nil {
		t.Fatal("expected an error")
	}
	strictErr, ok := errors.AsType[*Error](err)
	if !ok {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if len(strictErr.Unknown) != 1 || len(strictErr.Other) != 1 {
		t.Fatalf("unknown=%v other=%v", strictErr.Unknown, strictErr.Other)
	}
	if !strings.Contains(err.Error(), "d.yml: line 1: cannot unmarshal") {
		t.Errorf("other line must keep the file prefix and its text:\n%s", err.Error())
	}
}

func TestDecodePlainUnmarshalerError(t *testing.T) {
	err := Decode([]byte("step:\n  bogus: a\n"), &strictHolder{}, "workspace/deploy.yml")
	if err == nil {
		t.Fatal("expected an error")
	}
	strictErr, ok := errors.AsType[*Error](err)
	if !ok {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if len(strictErr.Unknown) != 1 {
		t.Fatalf("expected 1 unknown field, got %d", len(strictErr.Unknown))
	}
	want := "workspace/deploy.yml:2: unknown field \"bogus\" — allowed here: name\n" + versionHint
	if got := err.Error(); got != want {
		t.Errorf("message:\n got: %q\nwant: %q", got, want)
	}
	if _, ok := errors.AsType[*yaml.TypeError](err); ok {
		t.Error("the plain-error path must not produce a *yaml.TypeError")
	}
	if !strings.Contains(errors.Unwrap(err).Error(), "field bogus not found in type") {
		t.Errorf("Unwrap must return the original plain error, got %v", errors.Unwrap(err))
	}
}

func TestDecodePassthrough(t *testing.T) {
	t.Run("io.EOF untouched", func(t *testing.T) {
		var cfg pipeline
		err := Decode([]byte("# only comments\n\n"), &cfg, "workspace/deploy.yml")
		if !errors.Is(err, io.EOF) {
			t.Fatalf("expected io.EOF, got %v", err)
		}
		if err.Error() != io.EOF.Error() {
			t.Errorf("io.EOF must not be wrapped, got %q", err.Error())
		}
	})

	t.Run("syntax error keeps the file prefix", func(t *testing.T) {
		err := Decode([]byte("phases: [\n"), &pipeline{}, "workspace/deploy.yml")
		if err == nil {
			t.Fatal("expected an error")
		}
		if _, ok := errors.AsType[*Error](err); ok {
			t.Fatalf("a syntax error must not become *Error: %v", err)
		}
		if !strings.HasPrefix(err.Error(), "workspace/deploy.yml: ") {
			t.Errorf("expected the file prefix, got %q", err.Error())
		}
	})

	t.Run("syntax error without a file is verbatim", func(t *testing.T) {
		err := Decode([]byte("phases: [\n"), &pipeline{}, "")
		if err == nil {
			t.Fatal("expected an error")
		}
		if strings.HasPrefix(err.Error(), ": ") {
			t.Errorf("no file means no prefix, got %q", err.Error())
		}
	})

	t.Run("valid document decodes", func(t *testing.T) {
		var cfg pipeline
		if err := Decode([]byte("fail_fast: true\nphases:\n  - steps:\n      - name: a\n"), &cfg, "d.yml"); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !cfg.FailFast || len(cfg.Phases) != 1 || cfg.Phases[0].Steps[0].Name != "a" {
			t.Fatalf("unexpected cfg: %+v", cfg)
		}
	})
}

func TestAllowedFields(t *testing.T) {
	tests := []struct {
		name string
		typ  reflect.Type
		want []string
	}{
		{
			name: "inline flattens into the parent",
			typ:  reflect.TypeFor[pipeline](),
			want: []string{"fail_fast", "log", "phases", "untagged"},
		},
		{
			name: "yaml:\"-\" and unexported are skipped",
			typ:  reflect.TypeFor[step](),
			want: []string{"cmd", "name"},
		},
		{
			name: "pointer is followed",
			typ:  reflect.TypeFor[*phase](),
			want: []string{"env", "next", "steps"},
		},
		{
			name: "non-struct has no fields",
			typ:  reflect.TypeFor[[]string](),
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AllowedFields(tt.typ)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("AllowedFields = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestErrorWithoutAllowed pins the narrowing a validator needs when its decode
// target is deliberately wider than the runtime loader's shape for the same
// file: the reported allowed set drops the extra fields, the rest of the error
// is untouched, and the receiver is not mutated.
func TestErrorWithoutAllowed(t *testing.T) {
	type union struct {
		After  []string `yaml:"after"`
		Log    *bool    `yaml:"log"`
		Phases []string `yaml:"phases"`
	}

	var out union
	err := Decode([]byte("bogus: 1\n"), &out, "workspace/deploy.yml")

	strictErr, ok := errors.AsType[*Error](err)
	if !ok {
		t.Fatalf("Decode error = %v, want *Error", err)
	}
	if got := strictErr.Error(); !strings.Contains(got, "allowed here: after, log, phases") {
		t.Fatalf("unnarrowed error = %q", got)
	}

	narrowed := strictErr.WithoutAllowed("after")
	got := narrowed.Error()
	if !strings.Contains(got, "allowed here: log, phases") {
		t.Errorf("narrowed error = %q, want the after-less allowed set", got)
	}
	if strings.Contains(got, "after") {
		t.Errorf("narrowed error = %q, still advertises after", got)
	}
	// Everything else survives: file, line, field and the version hint.
	if !strings.Contains(got, `workspace/deploy.yml:1: unknown field "bogus"`) {
		t.Errorf("narrowed error = %q, lost its location", got)
	}
	if !strings.Contains(got, versionHint) {
		t.Errorf("narrowed error = %q, lost the version hint", got)
	}
	if !errors.Is(narrowed, strictErr.Unwrap()) {
		t.Error("narrowed error lost its wrapped cause")
	}

	// The copy must not have written through to the original.
	if orig := strictErr.Error(); !strings.Contains(orig, "allowed here: after, log, phases") {
		t.Errorf("receiver mutated: %q", orig)
	}
}

// A no-op call and a nil receiver are both safe, so a caller can narrow
// unconditionally.
func TestErrorWithoutAllowedEdgeCases(t *testing.T) {
	var nilErr *Error
	if got := nilErr.WithoutAllowed("after"); got != nil {
		t.Errorf("nil receiver = %v, want nil", got)
	}

	var out struct {
		Log *bool `yaml:"log"`
	}
	strictErr, ok := errors.AsType[*Error](Decode([]byte("bogus: 1\n"), &out, "f.yml"))
	if !ok {
		t.Fatal("want *Error")
	}
	if got := strictErr.WithoutAllowed().Error(); got != strictErr.Error() {
		t.Errorf("no fields = %q, want unchanged", got)
	}
	// Removing every allowed field drops the clause rather than printing an
	// empty list.
	if got := strictErr.WithoutAllowed("log").Error(); strings.Contains(got, "allowed here") {
		t.Errorf("emptied set = %q, want no allowed-here clause", got)
	}
}
