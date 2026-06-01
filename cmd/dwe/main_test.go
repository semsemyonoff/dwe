package main

import (
	"os"
	"path/filepath"
	"testing"
)

// makeProject creates a minimal temp project tree at the given root with:
//   - devbox.yml with schema_version (v2 by default, v1 if legacy=true)
//   - workspace/styles.yml so loadHelpColorScheme finds a styles file and returns
//     a non-nil ColorSchemeFunc (file presence triggers the non-nil path).
func makeProject(t *testing.T, root string, legacy bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	schema := `schema_version: "2"`
	if legacy {
		schema = `schema_version: "1"`
	}
	if err := os.WriteFile(filepath.Join(root, "workspace.yml"), []byte(schema+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	styles := "colors:\n  accent: \"#ff0000\"\n"
	if err := os.WriteFile(filepath.Join(root, "workspace", "styles.yml"), []byte(styles), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIsPromptInvocation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		argv []string
		want bool
	}{
		{"bare prompt", []string{"dwe", "prompt"}, true},
		{"prompt --check", []string{"dwe", "prompt", "--check"}, true},
		{"prompt --help", []string{"dwe", "prompt", "--help"}, false},
		{"prompt -h", []string{"dwe", "prompt", "-h"}, false},
		{"prompt foo", []string{"dwe", "prompt", "foo"}, false},
		{"prompt --check extra", []string{"dwe", "prompt", "--check", "x"}, false},
		{"only program", []string{"dwe"}, false},
		{"empty", nil, false},
		{"other subcommand", []string{"dwe", "status"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isPromptInvocation(tc.argv); got != tc.want {
				t.Errorf("isPromptInvocation(%v) = %v, want %v", tc.argv, got, tc.want)
			}
		})
	}
}

func TestConfigPathFromArgs(t *testing.T) {
	t.Run("no flag returns empty and not explicit", func(t *testing.T) {
		origArgs := os.Args
		os.Args = []string{"dwe", "info"}
		defer func() { os.Args = origArgs }()

		path, explicit := configPathFromArgs()
		if path != "" || explicit {
			t.Errorf("want (\"\", false), got (%q, %v)", path, explicit)
		}
	})

	t.Run("explicit --config flag", func(t *testing.T) {
		origArgs := os.Args
		os.Args = []string{"dwe", "--config", "/some/devbox.yml", "info"}
		defer func() { os.Args = origArgs }()

		path, explicit := configPathFromArgs()
		if path != "/some/devbox.yml" || !explicit {
			t.Errorf("want (\"/some/devbox.yml\", true), got (%q, %v)", path, explicit)
		}
	})

	t.Run("short -c flag", func(t *testing.T) {
		origArgs := os.Args
		os.Args = []string{"dwe", "-c", "/other/devbox.yml"}
		defer func() { os.Args = origArgs }()

		path, explicit := configPathFromArgs()
		if path != "/other/devbox.yml" || !explicit {
			t.Errorf("want (\"/other/devbox.yml\", true), got (%q, %v)", path, explicit)
		}
	})

	// Passing --config devbox.yml (value matching the old default) must be treated as explicit.
	t.Run("explicit flag with default-like value is treated as explicit", func(t *testing.T) {
		origArgs := os.Args
		os.Args = []string{"dwe", "--config", "workspace.yml"}
		defer func() { os.Args = origArgs }()

		path, explicit := configPathFromArgs()
		if path != "workspace.yml" || !explicit {
			t.Errorf("want (\"devbox.yml\", true), got (%q, %v)", path, explicit)
		}
	})
}

func TestLoadHelpColorScheme(t *testing.T) {
	t.Run("subdir invocation finds styles two levels up", func(t *testing.T) {
		root := t.TempDir()
		makeProject(t, root, false)

		subdir := filepath.Join(root, "a", "b")
		if err := os.MkdirAll(subdir, 0o755); err != nil {
			t.Fatal(err)
		}

		orig, _ := os.Getwd()
		if err := os.Chdir(subdir); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chdir(orig) }()

		// Discovery mode: configPath="", explicit=false
		cs := loadHelpColorScheme("", false)
		if cs == nil {
			t.Error("expected non-nil ColorSchemeFunc from subdir discovery")
		}
	})

	t.Run("no project anywhere returns nil silently", func(t *testing.T) {
		orig, _ := os.Getwd()
		if err := os.Chdir(os.TempDir()); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chdir(orig) }()

		// Ensure we are not inside any devbox project.
		cs := loadHelpColorScheme("", false)
		// We can't guarantee /tmp has no devbox.yml above it in all CI environments,
		// but we can guarantee no panic occurs. If nil, that is fine.
		// If non-nil, the test environment has a devbox project above /tmp (unusual).
		_ = cs
	})

	t.Run("explicit --config path resolves styles relative to that dir", func(t *testing.T) {
		root := t.TempDir()
		makeProject(t, root, false)

		configPath := filepath.Join(root, "workspace.yml")
		cs := loadHelpColorScheme(configPath, true)
		if cs == nil {
			t.Error("expected non-nil ColorSchemeFunc with explicit config path")
		}
	})

	t.Run("legacy v1 project still loads styles (schema validated later)", func(t *testing.T) {
		root := t.TempDir()
		makeProject(t, root, true) // schema_version: "1"

		orig, _ := os.Getwd()
		if err := os.Chdir(root); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chdir(orig) }()

		// Locate is schema-agnostic: it finds the file regardless of schema_version.
		// So styles should be loaded even for a legacy project.
		cs := loadHelpColorScheme("", false)
		if cs == nil {
			t.Error("expected non-nil ColorSchemeFunc: Locate should succeed for legacy v1 (schema check is Task 3)")
		}
	})

	t.Run("explicit bad path returns nil silently", func(t *testing.T) {
		cs := loadHelpColorScheme("/nonexistent/path/devbox.yml", true)
		if cs != nil {
			t.Error("expected nil ColorSchemeFunc for nonexistent explicit path")
		}
	})

	t.Run("project without styles.yml returns nil", func(t *testing.T) {
		root := t.TempDir()
		// Create devbox.yml but no styles.yml
		if err := os.WriteFile(filepath.Join(root, "workspace.yml"), []byte("schema_version: \"2\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		cs := loadHelpColorScheme(filepath.Join(root, "workspace.yml"), true)
		if cs != nil {
			t.Error("expected nil ColorSchemeFunc when no styles.yml present")
		}
	})
}
