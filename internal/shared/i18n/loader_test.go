package i18n

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseBundle(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		want    *Bundle
	}{
		{
			name: "valid empty bundle",
			input: `
ui: {}
commands: {}
groups: {}
`,
			wantErr: false,
			want:    &Bundle{UI: map[string]string{}, Commands: map[string]CommandStrings{}, Groups: map[string]GroupStrings{}},
		},
		{
			name: "valid with content",
			input: `
ui:
  test.key: "value"
commands:
  build.docker:
    description: "Build"
    confirmation_text: "Sure?"
    params:
      tag:
        description: "Tag"
groups:
  build:
    title: "Build"
    description: "Build group"
`,
			wantErr: false,
			want: &Bundle{
				UI: map[string]string{"test.key": "value"},
				Commands: map[string]CommandStrings{
					"build.docker": {
						Description:      "Build",
						ConfirmationText: "Sure?",
						Params: map[string]ParamStrings{
							"tag": {Description: "Tag"},
						},
					},
				},
				Groups: map[string]GroupStrings{
					"build": {Title: "Build", Description: "Build group"},
				},
			},
		},
		{
			name: "strict mode rejects unknown field",
			input: `
ui: {}
commands: {}
groups: {}
unknown_field: "value"
`,
			wantErr: true,
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: false,
			want:    &Bundle{},
		},
		{
			name: "invalid yaml",
			input: `
invalid: [
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseBundle(strings.NewReader(tt.input))
			if (err != nil) != tt.wantErr {
				t.Errorf("parseBundle() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got == nil {
				t.Errorf("parseBundle() returned nil bundle but wanted one")
			}
		})
	}
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func(t *testing.T) string // returns projectRoot
		wantErr   bool
		wantEn    bool
		check     func(t *testing.T, s *Store) // optional per-case assertions
	}{
		{
			name: "built-in only, no project dir",
			setupFunc: func(t *testing.T) string {
				// Create a temp dir with no workspace/i18n/ subdirectory
				dir := t.TempDir()
				return dir
			},
			wantErr: false,
			wantEn:  true,
		},
		{
			name: "built-in + project overlay",
			setupFunc: func(t *testing.T) string {
				dir := t.TempDir()
				i18nDir := filepath.Join(dir, "workspace", "i18n")
				if err := os.MkdirAll(i18nDir, 0755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				ruFile := filepath.Join(i18nDir, "ru.yml")
				if err := os.WriteFile(ruFile, []byte(`
ui:
  test.key: "Russian"
commands:
  build.docker:
    description: "Собрать"
`), 0644); err != nil {
					t.Fatalf("write: %v", err)
				}
				return dir
			},
			wantErr: false,
			wantEn:  true,
		},
		{
			name: "project overlay wins over built-in",
			setupFunc: func(t *testing.T) string {
				dir := t.TempDir()
				i18nDir := filepath.Join(dir, "workspace", "i18n")
				if err := os.MkdirAll(i18nDir, 0755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				enFile := filepath.Join(i18nDir, "en.yml")
				if err := os.WriteFile(enFile, []byte(`
ui:
  docs.section.properties: "Свойства"
`), 0644); err != nil {
					t.Fatalf("write: %v", err)
				}
				return dir
			},
			wantErr: false,
			wantEn:  true,
			check: func(t *testing.T, s *Store) {
				got := s.T("en", "docs.section.properties", "fallback")
				if got != "Свойства" {
					t.Errorf("project overlay not applied: T(\"en\", \"docs.section.properties\") = %q, want \"Свойства\"", got)
				}
			},
		},
		{
			name: "malformed project file is non-fatal",
			setupFunc: func(t *testing.T) string {
				dir := t.TempDir()
				i18nDir := filepath.Join(dir, "workspace", "i18n")
				if err := os.MkdirAll(i18nDir, 0755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				badFile := filepath.Join(i18nDir, "bad.yml")
				if err := os.WriteFile(badFile, []byte(`
ui: {}
commands: {}
groups: {}
invalid_field: "this will fail strict decode"
`), 0644); err != nil {
					t.Fatalf("write: %v", err)
				}
				// Also write a good file to prove others load
				goodFile := filepath.Join(i18nDir, "ru.yml")
				if err := os.WriteFile(goodFile, []byte(`
ui:
  test: "OK"
commands: {}
groups: {}
`), 0644); err != nil {
					t.Fatalf("write: %v", err)
				}
				return dir
			},
			wantErr: false,
			wantEn:  true,
			check: func(t *testing.T, s *Store) {
				locales := s.AvailableLocales()
				if !slices.Contains(locales, "ru") {
					t.Errorf("good file not loaded: locales = %v, want \"ru\" present", locales)
				}
				got := s.T("ru", "test", "")
				if got != "OK" {
					t.Errorf("good file content not accessible: T(\"ru\", \"test\") = %q, want \"OK\"", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectRoot := tt.setupFunc(t)
			got, err := Load(projectRoot)
			if (err != nil) != tt.wantErr {
				t.Errorf("Load() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}
			if got == nil {
				t.Errorf("Load() returned nil store")
				return
			}
			if tt.wantEn {
				locales := got.AvailableLocales()
				if !slices.Contains(locales, "en") {
					t.Errorf("Load() missing 'en' locale")
				}
			}
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

func TestLoadProjectBundles(t *testing.T) {
	tests := []struct {
		name         string
		setupFunc    func(t *testing.T) string // returns projectRoot
		wantNil      bool
		wantLen      int
		wantParseErr bool
		wantDirError bool
	}{
		{
			name: "absent directory returns nil",
			setupFunc: func(t *testing.T) string {
				return t.TempDir()
			},
			wantNil:      true,
			wantLen:      0,
			wantParseErr: false,
			wantDirError: false,
		},
		{
			name: "single valid file",
			setupFunc: func(t *testing.T) string {
				dir := t.TempDir()
				i18nDir := filepath.Join(dir, "workspace", "i18n")
				if err := os.MkdirAll(i18nDir, 0755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				ruFile := filepath.Join(i18nDir, "ru.yml")
				if err := os.WriteFile(ruFile, []byte(`
ui:
  test: "value"
commands: {}
groups: {}
`), 0644); err != nil {
					t.Fatalf("write: %v", err)
				}
				return dir
			},
			wantNil:      false,
			wantLen:      1,
			wantParseErr: false,
			wantDirError: false,
		},
		{
			name: "multiple valid files",
			setupFunc: func(t *testing.T) string {
				dir := t.TempDir()
				i18nDir := filepath.Join(dir, "workspace", "i18n")
				if err := os.MkdirAll(i18nDir, 0755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				for _, locale := range []string{"ru", "de", "fr"} {
					file := filepath.Join(i18nDir, locale+".yml")
					if err := os.WriteFile(file, []byte(`
ui: {}
commands: {}
groups: {}
`), 0644); err != nil {
						t.Fatalf("write: %v", err)
					}
				}
				return dir
			},
			wantNil:      false,
			wantLen:      3,
			wantParseErr: false,
			wantDirError: false,
		},
		{
			name: "parse error in one file, others load",
			setupFunc: func(t *testing.T) string {
				dir := t.TempDir()
				i18nDir := filepath.Join(dir, "workspace", "i18n")
				if err := os.MkdirAll(i18nDir, 0755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				badFile := filepath.Join(i18nDir, "bad.yml")
				if err := os.WriteFile(badFile, []byte(`
ui: {}
commands: {}
groups: {}
invalid: "field"
`), 0644); err != nil {
					t.Fatalf("write: %v", err)
				}
				goodFile := filepath.Join(i18nDir, "ru.yml")
				if err := os.WriteFile(goodFile, []byte(`
ui: {}
commands: {}
groups: {}
`), 0644); err != nil {
					t.Fatalf("write: %v", err)
				}
				return dir
			},
			wantNil:      false,
			wantLen:      2,
			wantParseErr: true,
			wantDirError: false,
		},
		{
			name: "only yml files loaded, others ignored",
			setupFunc: func(t *testing.T) string {
				dir := t.TempDir()
				i18nDir := filepath.Join(dir, "workspace", "i18n")
				if err := os.MkdirAll(i18nDir, 0755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				ymlFile := filepath.Join(i18nDir, "ru.yml")
				if err := os.WriteFile(ymlFile, []byte(`
ui: {}
commands: {}
groups: {}
`), 0644); err != nil {
					t.Fatalf("write: %v", err)
				}
				txtFile := filepath.Join(i18nDir, "readme.txt")
				if err := os.WriteFile(txtFile, []byte("ignore me"), 0644); err != nil {
					t.Fatalf("write: %v", err)
				}
				return dir
			},
			wantNil:      false,
			wantLen:      1,
			wantParseErr: false,
			wantDirError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectRoot := tt.setupFunc(t)
			got, err := LoadProjectBundles(projectRoot)
			if err != nil {
				t.Errorf("LoadProjectBundles() error = %v", err)
				return
			}
			if tt.wantNil {
				if got != nil {
					t.Errorf("LoadProjectBundles() = %v, want nil", got)
				}
				return
			}
			if len(got) != tt.wantLen {
				t.Errorf("LoadProjectBundles() len = %d, want %d", len(got), tt.wantLen)
			}
			if tt.wantParseErr {
				found := false
				for _, pf := range got {
					if pf.ParseErr != nil {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("LoadProjectBundles() expected a parse error, found none")
				}
			}
			if tt.wantDirError {
				if len(got) == 0 || got[0].Locale != "" || got[0].ParseErr == nil {
					t.Errorf("LoadProjectBundles() expected a sentinel ProjectFile with Locale == \"\" and ParseErr != nil")
				}
			}
		})
	}
}

func TestLoadProjectBundlesDirectoryErrors(t *testing.T) {
	t.Run("unreadable directory returns sentinel", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("cannot test permission errors as root")
		}

		dir := t.TempDir()
		i18nDir := filepath.Join(dir, "workspace", "i18n")
		if err := os.MkdirAll(i18nDir, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		// Write a valid file
		validFile := filepath.Join(i18nDir, "ru.yml")
		if err := os.WriteFile(validFile, []byte("ui: {}\ncommands: {}\ngroups: {}"), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}

		// Remove read permission
		if err := os.Chmod(i18nDir, 0000); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		defer func() {
			_ = os.Chmod(i18nDir, 0755)
		}()

		got, err := LoadProjectBundles(dir)
		if err != nil {
			t.Errorf("LoadProjectBundles() error = %v", err)
			return
		}

		// Should return a sentinel ProjectFile with dir path and error
		if len(got) != 1 || got[0].Locale != "" || got[0].ParseErr == nil {
			t.Errorf("LoadProjectBundles() expected sentinel ProjectFile with Locale='' and error")
		}
	})

	t.Run("project dir is file returns sentinel", func(t *testing.T) {
		dir := t.TempDir()
		i18nFile := filepath.Join(dir, "workspace", "i18n")
		devboxDir := filepath.Join(dir, "workspace")
		if err := os.MkdirAll(devboxDir, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(i18nFile, []byte("not a dir"), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}

		got, err := LoadProjectBundles(dir)
		if err != nil {
			t.Errorf("LoadProjectBundles() error = %v", err)
			return
		}

		if len(got) != 1 || got[0].Locale != "" || got[0].ParseErr == nil {
			t.Errorf("LoadProjectBundles() expected sentinel with error for non-directory")
		}
	})
}

func TestMergeBundle(t *testing.T) {
	tests := []struct {
		name string
		dst  *Bundle
		src  *Bundle
		want *Bundle
	}{
		{
			name: "merge into empty",
			dst:  &Bundle{UI: make(map[string]string), Commands: make(map[string]CommandStrings), Groups: make(map[string]GroupStrings)},
			src: &Bundle{
				UI: map[string]string{"key": "value"},
				Commands: map[string]CommandStrings{
					"cmd": {Description: "desc"},
				},
			},
			want: &Bundle{
				UI: map[string]string{"key": "value"},
				Commands: map[string]CommandStrings{
					"cmd": {Description: "desc"},
				},
			},
		},
		{
			name: "src wins on conflict",
			dst: &Bundle{
				UI:       map[string]string{"key": "dst_value"},
				Commands: make(map[string]CommandStrings),
				Groups:   make(map[string]GroupStrings),
			},
			src: &Bundle{
				UI: map[string]string{"key": "src_value"},
			},
			want: &Bundle{
				UI: map[string]string{"key": "src_value"},
			},
		},
		{
			name: "empty values from src are skipped",
			dst: &Bundle{
				UI:       map[string]string{"key": "value"},
				Commands: make(map[string]CommandStrings),
				Groups:   make(map[string]GroupStrings),
			},
			src: &Bundle{
				UI: map[string]string{"key": ""},
			},
			want: &Bundle{
				UI: map[string]string{"key": "value"},
			},
		},
		{
			name: "merge commands params",
			dst: &Bundle{
				Commands: map[string]CommandStrings{
					"cmd": {
						Description: "dst_desc",
						Params: map[string]ParamStrings{
							"p1": {Description: "p1_dst"},
						},
					},
				},
			},
			src: &Bundle{
				Commands: map[string]CommandStrings{
					"cmd": {
						Params: map[string]ParamStrings{
							"p1": {Description: "p1_src"},
							"p2": {Description: "p2_src"},
						},
					},
				},
			},
			want: &Bundle{
				Commands: map[string]CommandStrings{
					"cmd": {
						Description: "dst_desc",
						Params: map[string]ParamStrings{
							"p1": {Description: "p1_src"},
							"p2": {Description: "p2_src"},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mergeBundle(tt.dst, tt.src)
			// Compare UI fields
			if len(tt.dst.UI) != len(tt.want.UI) {
				t.Errorf("mergeBundle() UI len = %d, want %d", len(tt.dst.UI), len(tt.want.UI))
			}
			for k, v := range tt.want.UI {
				if tt.dst.UI[k] != v {
					t.Errorf("mergeBundle() UI[%q] = %q, want %q", k, tt.dst.UI[k], v)
				}
			}
			// Compare Commands fields
			for cmdID, wantCS := range tt.want.Commands {
				gotCS, ok := tt.dst.Commands[cmdID]
				if !ok {
					t.Errorf("mergeBundle() Commands[%q] missing", cmdID)
					continue
				}
				if gotCS.Description != wantCS.Description {
					t.Errorf("mergeBundle() Commands[%q].Description = %q, want %q", cmdID, gotCS.Description, wantCS.Description)
				}
				for paramName, wantPS := range wantCS.Params {
					gotPS, ok := gotCS.Params[paramName]
					if !ok {
						t.Errorf("mergeBundle() Commands[%q].Params[%q] missing", cmdID, paramName)
						continue
					}
					if gotPS.Description != wantPS.Description {
						t.Errorf("mergeBundle() Commands[%q].Params[%q].Description = %q, want %q", cmdID, paramName, gotPS.Description, wantPS.Description)
					}
				}
			}
		})
	}
}

func TestLoadBuiltinFallback(t *testing.T) {
	// Verify Load() returns a valid store even with an empty project root
	store, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\") error = %v", err)
	}
	if store == nil {
		t.Fatal("Load(\"\") returned nil store")
	}

	// Should have en locale
	locales := store.AvailableLocales()
	if !slices.Contains(locales, "en") {
		t.Errorf("Load(\"\") missing 'en' locale")
	}

	// Test that embedded ui keys are accessible
	val := store.T("en", "docs.section.properties", "fallback")
	if val == "fallback" {
		t.Errorf("T(\"en\", \"docs.section.properties\", ...) = fallback, expected embedded value")
	}
}
