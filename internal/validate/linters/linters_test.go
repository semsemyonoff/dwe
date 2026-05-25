package linters

import (
	"testing"

	"devbox-cli/internal/validate"
)

func TestGenericAdapterParseOutput(t *testing.T) {
	t.Parallel()

	g := NewGeneric("yamllint", "yamllint")

	t.Run("exit zero is clean", func(t *testing.T) {
		t.Parallel()
		diags, err := g.ParseOutput([]byte("ignored stdout"), nil, 0)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(diags) != 0 {
			t.Fatalf("want no diagnostics, got %d", len(diags))
		}
	})

	t.Run("exit non-zero emits single error", func(t *testing.T) {
		t.Parallel()
		diags, err := g.ParseOutput([]byte("warning: bad indent\n"), []byte("stderr stuff"), 2)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("want 1 diagnostic, got %d", len(diags))
		}
		d := diags[0]
		if d.Severity != validate.SeverityError {
			t.Errorf("severity: want Error, got %v", d.Severity)
		}
		if d.Domain != "linters" {
			t.Errorf("domain: want linters, got %q", d.Domain)
		}
		if d.Target != "yamllint" {
			t.Errorf("target: want yamllint, got %q", d.Target)
		}
		if d.Message == "" {
			t.Error("message must not be empty")
		}
	})

	t.Run("non-zero with empty output gets placeholder message", func(t *testing.T) {
		t.Parallel()
		diags, err := g.ParseOutput(nil, nil, 1)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(diags) != 1 || diags[0].Message == "" {
			t.Fatalf("want one diag with placeholder message, got %#v", diags)
		}
	})

	t.Run("message truncated past cap", func(t *testing.T) {
		t.Parallel()
		big := make([]byte, genericMessageCap*2)
		for i := range big {
			big[i] = 'x'
		}
		diags, _ := g.ParseOutput(big, nil, 1)
		if len(diags) != 1 {
			t.Fatalf("want one diag, got %d", len(diags))
		}
		if len(diags[0].Message) >= len(big) {
			t.Errorf("message not truncated: %d bytes (want < %d)", len(diags[0].Message), len(big))
		}
	})
}

func TestGenericAdapterBuildArgs(t *testing.T) {
	t.Parallel()
	g := NewGeneric("yamllint", "yamllint")

	args := g.BuildArgs([]string{"a.yml", "b.yml"}, []string{"-s"})
	want := []string{"-s", "--", "a.yml", "b.yml"}
	if !equal(args, want) {
		t.Errorf("argv: want %v, got %v", want, args)
	}

	args = g.BuildArgs(nil, []string{"-s"})
	if !equal(args, []string{"-s"}) {
		t.Errorf("argv with no files: want [-s], got %v", args)
	}
}

func TestValidateUserFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		reserved []string
		flags    []string
		wantErr  bool
	}{
		{"no reserved flags allows anything", nil, []string{"--format=gcc"}, false},
		{"exact long flag rejected", []string{"--format", "-f"}, []string{"--format"}, true},
		{"long flag with equals rejected", []string{"--format", "-f"}, []string{"--format=gcc"}, true},
		{"short flag exact rejected", []string{"--format", "-f"}, []string{"-f"}, true},
		{"short flag attached value rejected", []string{"--format", "-f"}, []string{"-fgcc"}, true},
		{"short flag json variant rejected", []string{"--format", "-f"}, []string{"-fjson"}, true},
		{"value-as-next-arg rejected via the flag itself", []string{"--format", "-f"}, []string{"-f", "gcc"}, true},
		{"benign flags pass", []string{"--format", "-f"}, []string{"--severity=warning", "--shell=bash", "-x"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := &fakeReservedAdapter{reserved: tc.reserved}
			err := validateUserFlags(a, tc.flags)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

// fakeReservedAdapter is the minimal Adapter the validateUserFlags helper
// needs to exercise its policy (only ReservedFlags is called).
type fakeReservedAdapter struct {
	reserved []string
}

func (f *fakeReservedAdapter) ID() string             { return "fake" }
func (f *fakeReservedAdapter) DefaultBin() string     { return "fake" }
func (f *fakeReservedAdapter) DefaultPaths() []string { return nil }
func (f *fakeReservedAdapter) DefaultExtensions() []string {
	return nil
}
func (f *fakeReservedAdapter) DefaultFilenames() []string { return nil }
func (f *fakeReservedAdapter) ReservedFlags() []string    { return f.reserved }
func (f *fakeReservedAdapter) BuildArgs(files, userFlags []string) []string {
	return nil
}
func (f *fakeReservedAdapter) ParseOutput(stdout, stderr []byte, exitCode int) ([]validate.Diagnostic, error) {
	return nil, nil
}

func TestDiagnosticHelpers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		got     validate.Diagnostic
		wantSev validate.Severity
	}{
		{"fail", fail("sc", "boom", "fix it"), validate.SeverityError},
		{"warn", warn("sc", "soft", ""), validate.SeverityWarning},
		{"info", info("sc", "note", ""), validate.SeverityInfo},
		{"finding", finding("sc", validate.SeverityWarning, "f.sh", 7, "msg", ""), validate.SeverityWarning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got.Severity != tc.wantSev {
				t.Errorf("severity: want %v, got %v", tc.wantSev, tc.got.Severity)
			}
			if tc.got.Domain != "linters" {
				t.Errorf("domain: want linters, got %q", tc.got.Domain)
			}
			if tc.got.Target != "sc" {
				t.Errorf("target: want sc, got %q", tc.got.Target)
			}
		})
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
