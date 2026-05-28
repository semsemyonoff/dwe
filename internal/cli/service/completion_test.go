package service

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"devbox-cli/internal/cli/cmdctx"

	"github.com/spf13/cobra"
)

// makeServiceProject writes a v2 project with three services: 'main'
// (required app), 'api' (enabled optional app), 'worker' (disabled optional
// app). Returns the project root.
func makeServiceProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"), []byte("schema_version: \"2\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defaults := "project:\n  name: test\n  prefix: test\nservices:\n  api:\n    enabled: true\n  worker:\n    enabled: false\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "defaults.yml"), []byte(defaults), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"main":   "type: app\ncontainer: app-main\nrequired: true\n",
		"api":    "type: app\ncontainer: app-api\n",
		"worker": "type: app\ncontainer: app-worker\n",
	} {
		svcDir := filepath.Join(devboxDir, "services", name)
		if err := os.MkdirAll(svcDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestServiceCompletion_Filters verifies that serviceCompletion returns
// only optional disabled (resp. enabled) services and never the required
// service 'main'. Guards the `services enable` / `services disable`
// completion contract.
func TestServiceCompletion_Filters(t *testing.T) {
	dir := makeServiceProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "devbox.yml"), Root: dir}
	cmd := &cobra.Command{}

	t.Run("disabled optional", func(t *testing.T) {
		names, directive := serviceCompletion(flags, completeDisabledOptional)(cmd, nil, "")
		slices.Sort(names)
		if !slices.Equal(names, []string{"worker"}) {
			t.Errorf("got %v, want [worker]", names)
		}
		if directive != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("directive = %v, want NoFileComp", directive)
		}
	})

	t.Run("enabled optional", func(t *testing.T) {
		names, _ := serviceCompletion(flags, completeEnabledOptional)(cmd, nil, "")
		slices.Sort(names)
		if !slices.Equal(names, []string{"api"}) {
			t.Errorf("got %v, want [api]", names)
		}
	})
}

// TestServiceCompletion_NoSecondArg: with a positional arg already given,
// no completions are returned (suppresses double-suggesting).
func TestServiceCompletion_NoSecondArg(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	names, directive := serviceCompletion(flags, completeDisabledOptional)(nil, []string{"already"}, "")
	if len(names) != 0 {
		t.Errorf("got %d completions, want 0", len(names))
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
}
