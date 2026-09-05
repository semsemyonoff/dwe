package config

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/shared/yamlstrict"
)

// TestHandMaintainedKnownFieldsMatchTags guards the one place where an
// allow-list is maintained by hand: DeployStep and ParallelGroup have custom
// UnmarshalYAML implementations, so yaml.v3 bypasses KnownFields(true) and
// checkKnownFields rejects unknown keys against these maps — while yamlstrict
// prints the *reflected* tag set. Drift would advertise a field the loader
// rejects (or hide one it accepts).
func TestHandMaintainedKnownFieldsMatchTags(t *testing.T) {
	cases := []struct {
		name string
		hand map[string]bool
		typ  reflect.Type
	}{
		{"DeployStep", deployStepKnownFields, reflect.TypeFor[DeployStep]()},
		{"ParallelGroup", parallelGroupKnownFields, reflect.TypeFor[ParallelGroup]()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make([]string, 0, len(tc.hand))
			for k := range tc.hand {
				got = append(got, k)
			}
			slices.Sort(got)
			want := yamlstrict.AllowedFields(tc.typ)
			if !slices.Equal(got, want) {
				t.Fatalf("hand-maintained allow-list drifted from %s tags:\n hand: %v\n tags: %v", tc.name, got, want)
			}
		})
	}
}

// writeTempYAML writes body to <tmp>/<name> and returns the path.
func writeTempYAML(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// TestStrictLoadersReportUnknownField pins the migrated loaders on the
// yamlstrict message shape: file, line, the rejected field and the allowed set.
func TestStrictLoadersReportUnknownField(t *testing.T) {
	cases := []struct {
		name        string
		file        string
		body        string
		load        func(path string) error
		wantLine    int
		wantField   string
		wantAllowed string
	}{
		{
			name: "project deploy.yml",
			file: "deploy.yml",
			body: "defaults: {}\nphases: []\n",
			load: func(p string) error { _, err := LoadProjectDeployConfig(p); return err },

			wantLine:    1,
			wantField:   `unknown field "defaults"`,
			wantAllowed: "allowed here: log, phases",
		},
		{
			name: "reset.yml",
			file: "reset.yml",
			body: "bogus: 1\n",
			load: func(p string) error { _, err := LoadResetConfig(p); return err },

			wantLine:    1,
			wantField:   `unknown field "bogus"`,
			wantAllowed: "allowed here: log, phases",
		},
		{
			name: "service deploy.yml",
			file: "deploy.yml",
			body: "bogus: 1\n",
			load: func(p string) error { _, err := LoadServiceDeployConfig(p); return err },

			wantLine:    1,
			wantField:   `unknown field "bogus"`,
			wantAllowed: "allowed here: after, log, phases",
		},
		{
			name: "lifecycle.yml",
			file: "lifecycle.yml",
			body: "bogus: 1\n",
			load: func(p string) error { _, err := LoadLifecycleConfig(p); return err },

			wantLine:    1,
			wantField:   `unknown field "bogus"`,
			wantAllowed: "allowed here: run, stop",
		},
		{
			name: "deploy step (custom unmarshaler)",
			file: "deploy.yml",
			body: "phases:\n  - name: p\n    steps:\n      - name: s\n        cmdd: echo hi\n",
			load: func(p string) error { _, err := LoadProjectDeployConfig(p); return err },

			wantLine:    5,
			wantField:   `unknown field "cmdd"`,
			wantAllowed: "allowed here: check, cmd, continue_on_error, description, files_gate, name, parallel, skip_confirm, sub_step_overrides, timeout, type, untracked, when",
		},
		{
			name: "snapshot.yml",
			file: "snapshot.yml",
			body: "dir: ./snap\nmystery: 1\n",
			load: func(p string) error { _, err := LoadSnapshotConfig(p); return err },

			wantLine:    2,
			wantField:   `unknown field "mystery"`,
			wantAllowed: "allowed here:",
		},
		{
			name: "validate.yml",
			file: "validate.yml",
			body: "checks:\n  - id: c\n    bogus: 1\n",
			load: func(p string) error { _, _, err := LoadValidateConfig(p); return err },

			wantLine:    3,
			wantField:   `unknown field "bogus"`,
			wantAllowed: "allowed here: cmd, description, hint, id, services, severity, stages, type, with",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeTempYAML(t, tc.file, tc.body)
			err := tc.load(p)
			if err == nil {
				t.Fatal("expected an unknown-field error, got nil")
			}
			msg := err.Error()
			for _, want := range []string{
				p + ":" + strconv.Itoa(tc.wantLine) + ":",
				tc.wantField,
				tc.wantAllowed,
				"check `dwe version`",
			} {
				if !strings.Contains(msg, want) {
					t.Errorf("error %q does not contain %q", msg, want)
				}
			}
		})
	}
}

// TestLoadServiceFolderUnknownFieldMessage covers the one migrated loader whose
// path is derived rather than passed in: service.yml keeps the outer
// `loading service %q definition:` context and gains the project-relative file.
func TestLoadServiceFolderUnknownFieldMessage(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "workspace", "services", "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// The lenient first pass (allowedFieldsFor) rejects top-level unknowns with
	// its own message, so only a nested unknown reaches the strict decode.
	body := "type: app\ninfo:\n  schemee: http\n"
	if err := os.WriteFile(filepath.Join(dir, "service.yml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write service.yml: %v", err)
	}
	_, err := LoadServiceFolder(base, "app")
	if err == nil {
		t.Fatal("expected an unknown-field error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		`loading service "app" definition:`,
		"workspace/services/app/service.yml:3:",
		`unknown field "schemee"`,
		"allowed here:",
		"check `dwe version`",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not contain %q", msg, want)
		}
	}
}

// TestNonTolerantLoadersNameTheFileOnEOF pins the io.EOF branch of the two
// migrated loaders that do NOT read an all-comment file as "absent". yamlstrict
// passes io.EOF through untouched so the four pipeline loaders can fall back to
// the built-in default; these two must re-wrap it, or `dwe snapshot list` and
// `dwe validate` degrade to the bare three-letter message "EOF".
func TestNonTolerantLoadersNameTheFileOnEOF(t *testing.T) {
	cases := []struct {
		name string
		file string
		body string
		load func(string) error
	}{
		{
			name: "snapshot.yml all comments",
			file: "snapshot.yml",
			body: "# nothing here yet\n# dir: ./snapshots\n",
			load: func(p string) error { _, err := LoadSnapshotConfig(p); return err },
		},
		{
			name: "validate.yml all comments",
			file: "validate.yml",
			body: "# nothing here yet\n# checks:\n",
			load: func(p string) error { _, _, err := LoadValidateConfig(p); return err },
		},
		{
			name: "validate.yml empty",
			file: "validate.yml",
			body: "",
			load: func(p string) error { _, _, err := LoadValidateConfig(p); return err },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeTempYAML(t, tc.file, tc.body)
			err := tc.load(p)
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !errors.Is(err, io.EOF) {
				t.Fatalf("expected io.EOF, got %v", err)
			}
			if !strings.Contains(err.Error(), p) {
				t.Fatalf("error %q does not name the file %q", err.Error(), p)
			}
		})
	}
}
