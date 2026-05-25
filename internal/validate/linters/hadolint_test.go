package linters

import (
	"testing"

	"devbox-cli/internal/validate"
)

func TestHadolintBuildArgs(t *testing.T) {
	t.Parallel()
	a := NewHadolint()

	t.Run("forced format first, files after --", func(t *testing.T) {
		t.Parallel()
		got := a.BuildArgs([]string{"Dockerfile", "service.dockerfile"}, []string{"--no-color"})
		want := []string{"-f", "json", "--no-color", "--", "Dockerfile", "service.dockerfile"}
		if !equal(got, want) {
			t.Errorf("argv: want %v, got %v", want, got)
		}
	})

	t.Run("no files omits the -- separator", func(t *testing.T) {
		t.Parallel()
		got := a.BuildArgs(nil, []string{"--ignore=DL3008"})
		want := []string{"-f", "json", "--ignore=DL3008"}
		if !equal(got, want) {
			t.Errorf("argv: want %v, got %v", want, got)
		}
	})
}

func TestHadolintReservedFlagsContract(t *testing.T) {
	t.Parallel()
	a := NewHadolint()

	cases := []struct {
		name    string
		flags   []string
		wantErr bool
	}{
		{"long flag bare", []string{"--format"}, true},
		{"long flag with equals", []string{"--format=gcc"}, true},
		{"long flag value-as-next-arg", []string{"--format", "gcc"}, true},
		{"short flag bare", []string{"-f"}, true},
		{"short flag value-as-next-arg", []string{"-f", "tty"}, true},
		{"short flag attached value tty", []string{"-ftty"}, true},
		{"short flag attached value json", []string{"-fjson"}, true},
		{"ignore flag ok", []string{"--ignore=DL3008"}, false},
		{"no-color flag ok", []string{"--no-color"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateUserFlags(a, tc.flags)
			if (err != nil) != tc.wantErr {
				t.Fatalf("flags=%v err=%v wantErr=%v", tc.flags, err, tc.wantErr)
			}
		})
	}
}

func TestHadolintParseOutput(t *testing.T) {
	t.Parallel()
	a := NewHadolint()

	t.Run("clean JSON empty array, exit 0", func(t *testing.T) {
		t.Parallel()
		stdout := readFixture(t, "hadolint/clean.json")
		diags, err := a.ParseOutput(stdout, nil, 0)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(diags) != 0 {
			t.Fatalf("want 0 diagnostics, got %d", len(diags))
		}
	})

	t.Run("findings with non-zero exit map to typed diagnostics", func(t *testing.T) {
		t.Parallel()
		stdout := readFixture(t, "hadolint/findings.json")
		diags, err := a.ParseOutput(stdout, nil, 1)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(diags) != 4 {
			t.Fatalf("want 4 diagnostics, got %d", len(diags))
		}
		wantSev := []validate.Severity{
			validate.SeverityWarning,
			validate.SeverityError,
			validate.SeverityInfo,
			validate.SeverityInfo, // "style" → info
		}
		for i, d := range diags {
			if d.Severity != wantSev[i] {
				t.Errorf("diag[%d] severity: want %v, got %v", i, wantSev[i], d.Severity)
			}
			if d.Domain != "linters" {
				t.Errorf("diag[%d] domain: want linters, got %q", i, d.Domain)
			}
			if d.Target != "hadolint" {
				t.Errorf("diag[%d] target: want hadolint, got %q", i, d.Target)
			}
			if d.File == "" || d.Line == 0 {
				t.Errorf("diag[%d] missing file/line: %#v", i, d)
			}
		}
		if !contains(diags[0].Message, "DL3008") {
			t.Errorf("diag[0] message missing code: %q", diags[0].Message)
		}
	})

	t.Run("empty stdout + non-zero exit + stderr → internal failure", func(t *testing.T) {
		t.Parallel()
		diags, err := a.ParseOutput(nil, []byte("hadolint: failed to read file\n"), 2)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("want 1 diagnostic, got %d", len(diags))
		}
		if diags[0].Severity != validate.SeverityError {
			t.Errorf("want Error, got %v", diags[0].Severity)
		}
		if !contains(diags[0].Message, "failed to read file") {
			t.Errorf("message should embed stderr: %q", diags[0].Message)
		}
	})

	t.Run("empty stdout + non-zero exit + empty stderr → placeholder", func(t *testing.T) {
		t.Parallel()
		diags, err := a.ParseOutput(nil, nil, 2)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(diags) != 1 || diags[0].Message == "" {
			t.Fatalf("want one diag with placeholder, got %#v", diags)
		}
	})

	t.Run("invalid JSON + non-zero exit treated as internal failure", func(t *testing.T) {
		t.Parallel()
		diags, err := a.ParseOutput([]byte("not json"), []byte("boom"), 2)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(diags) != 1 || diags[0].Severity != validate.SeverityError {
			t.Fatalf("want one Error diag, got %#v", diags)
		}
	})

	t.Run("invalid JSON + exit 0 propagates parse error", func(t *testing.T) {
		t.Parallel()
		_, err := a.ParseOutput([]byte("not json"), nil, 0)
		if err == nil {
			t.Fatal("want parse error, got nil")
		}
	})
}
