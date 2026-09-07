package docs

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/execution/builtin"
	"github.com/semsemyonoff/dwe/internal/core/execution/condition"

	"github.com/stretchr/testify/require"
)

func newTestLlmsTxtFlags() *cmdctx.RootFlags {
	return &cmdctx.RootFlags{
		ConfigPath: "",
		Root:       "",
		I18n:       nil,
		Locale:     "en",
	}
}

func TestDocsLlmsTxtCommand_Structure(t *testing.T) {
	cmd := newDocsLlmsTxtCmd(newTestLlmsTxtFlags())
	require.NotNil(t, cmd)
	require.Equal(t, "llms-txt", cmd.Name())
	require.NotEmpty(t, cmd.Short)
}

func TestDocsLlmsTxtCommand_Flags(t *testing.T) {
	cmd := newDocsLlmsTxtCmd(newTestLlmsTxtFlags())

	require.NotNil(t, cmd.Flag("out"))
	require.NotNil(t, cmd.Flag("lang"))
	require.NotNil(t, cmd.Flag("include-internals"))
	require.NotNil(t, cmd.Flag("no-project"))
	// The point of the rename: a local `--output` here would shadow the root's
	// and make `-o` unresolvable, so its absence is the assertion that matters.
	require.Nil(t, cmd.Flags().Lookup("output"))
}

func TestDocsLlmsTxtCommand_NoProject_Stdout(t *testing.T) {
	flags := newTestLlmsTxtFlags()
	cmd := newDocsLlmsTxtCmd(flags)
	cmd.SetArgs([]string{})

	out := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	output := out.String()
	require.NotEmpty(t, output, "output should not be empty")
	require.True(t, strings.HasPrefix(output, "# "), "output should start with H1 title")
}

func TestDocsLlmsTxtCommand_OutputFile(t *testing.T) {
	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "llms.txt")

	flags := newTestLlmsTxtFlags()
	cmd := newDocsLlmsTxtCmd(flags)
	cmd.SetArgs([]string{"--out", outPath})

	out := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	// stdout should be empty when writing to file
	require.Empty(t, out.String(), "stdout should be empty when --out is set")

	// file should exist and contain the llms.txt document
	content, err := os.ReadFile(outPath)
	require.NoError(t, err)
	require.NotEmpty(t, content)
	require.True(t, strings.HasPrefix(string(content), "# "), "file should start with H1 title")
}

func TestDocsLlmsTxtCommand_OutputFile_CreatesParentDirs(t *testing.T) {
	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "nested", "dir", "llms.txt")

	flags := newTestLlmsTxtFlags()
	cmd := newDocsLlmsTxtCmd(flags)
	cmd.SetArgs([]string{"--out", outPath})

	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.NoError(t, err)

	require.FileExists(t, outPath)
	content, err := os.ReadFile(outPath)
	require.NoError(t, err)
	require.NotEmpty(t, content)
}

func TestDocsLlmsTxtCommand_OutputFile_WriteError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping write-error test when running as root")
	}

	tmpDir := t.TempDir()
	// Make directory read-only so writing inside it fails.
	require.NoError(t, os.Chmod(tmpDir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(tmpDir, 0o755) })

	outPath := filepath.Join(tmpDir, "llms.txt")

	flags := newTestLlmsTxtFlags()
	cmd := newDocsLlmsTxtCmd(flags)
	cmd.SetArgs([]string{"--out", outPath})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.Error(t, err, "expected error when writing to read-only directory")
}

func TestDocsLlmsTxtCommand_IncludeInternals_Flag(t *testing.T) {
	runOnce := func(args []string) string {
		flags := newTestLlmsTxtFlags()
		cmd := newDocsLlmsTxtCmd(flags)
		cmd.SetArgs(args)
		out := &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(&bytes.Buffer{})
		require.NoError(t, cmd.Execute())
		return out.String()
	}

	without := runOnce([]string{})
	with := runOnce([]string{"--include-internals"})

	require.NotEmpty(t, without)
	require.NotEmpty(t, with)
	// The flag must actually change the output. Embedded internals docs add
	// internals/* topics under the Documentation section when the flag is on.
	require.NotEqual(t, without, with, "--include-internals must change output (embedded internals topics)")
	require.Contains(t, with, "internals/", "expected internals/ topic when --include-internals is set")
	require.NotContains(t, without, "internals/", "internals/ topics must be absent without --include-internals")
}

// llmsTxtNoProjectBudget caps the project-agnostic document. The command is a
// mandatory first step of every agent session, so growth is a permanent token
// tax; the cap is enforced on --no-project only, since a project-aware document
// grows with someone else's workspace.
const llmsTxtNoProjectBudget = 12 * 1024

func runLlmsTxt(t *testing.T, args ...string) string {
	t.Helper()
	cmd := newDocsLlmsTxtCmd(newTestLlmsTxtFlags())
	cmd.SetArgs(args)
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	require.NoError(t, cmd.Execute())
	return out.String()
}

func TestDocsLlmsTxtCommand_SizeBudget(t *testing.T) {
	got := runLlmsTxt(t, "--no-project")
	require.LessOrEqual(t, len(got), llmsTxtNoProjectBudget,
		"project-agnostic llms.txt is %d B, over the %d B budget advertised in --help and docs/reference/docs/commands.md",
		len(got), llmsTxtNoProjectBudget)
}

func TestDocsLlmsTxtCommand_BriefingSections(t *testing.T) {
	got := runLlmsTxt(t, "--no-project")

	for _, want := range []string{
		"## Builtins",
		"## Template syntax by site",
		"## Diagnostics and machine-readable output",
		"## Reserved env names",
	} {
		require.Contains(t, got, want)
	}

	// Inventory collected from the real registries (not a stub), one line each.
	require.Contains(t, got, "`source_clone` — action —", "expected a step builtin with its kind and summary")
	require.Contains(t, got, "`http_check` — predicate —", "expected a predicate builtin")
	require.Contains(t, got, "`daemons_reap` — internal —", "expected internal builtins to be listed too")
	require.Contains(t, got, "`dir-not-empty <path>` —", "expected the disjoint when: predicate registry")

	// Reserved names come from config.ReservedExportNames.
	require.Contains(t, got, "`PROJECT`, `UID`, `GID`")
}

func TestDocsLlmsTxtCommand_InventoryMatchesRegistries(t *testing.T) {
	got := runLlmsTxt(t, "--no-project")

	for _, e := range builtin.Inventory() {
		require.Contains(t, got, "`"+e.Name+"` — "+e.Kind.String()+" — "+e.Summary,
			"builtin %q missing from the llms.txt inventory", e.Name)
	}
	for _, p := range condition.Predicates() {
		require.Contains(t, got, "`"+p.Name+" "+p.Args+"` — "+p.Summary,
			"when: predicate %q missing from the llms.txt inventory", p.Name)
	}
}

func TestDocsLlmsTxtCommand_NoProjectFlag_InsideProject(t *testing.T) {
	// Create a minimal fake dwe project.
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "workspace.yml")
	err := os.WriteFile(cfgPath, []byte("project:\n  name: test-project\n"), 0o644)
	require.NoError(t, err)

	flags := &cmdctx.RootFlags{
		ConfigPath: cfgPath,
		Root:       tmpDir,
		I18n:       nil,
		Locale:     "en",
	}

	// --no-project should produce the generic (no-project) output shape.
	cmd := newDocsLlmsTxtCmd(flags)
	cmd.SetArgs([]string{"--no-project"})

	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})

	err = cmd.Execute()
	require.NoError(t, err)

	output := out.String()
	require.NotEmpty(t, output)
	// Generic output starts with "# DWE", not "# test-project".
	require.True(t, strings.HasPrefix(output, "# DWE"), "no-project output should use generic title, got: %q", output[:min(len(output), 40)])
}

func TestDocsLlmsTxtCommand_ProjectAware(t *testing.T) {
	// Create a minimal fake dwe project.
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "workspace.yml")
	err := os.WriteFile(cfgPath, []byte("project:\n  name: myapp\n"), 0o644)
	require.NoError(t, err)

	flags := &cmdctx.RootFlags{
		ConfigPath: cfgPath,
		Root:       tmpDir,
		I18n:       nil,
		Locale:     "en",
	}

	cmd := newDocsLlmsTxtCmd(flags)
	cmd.SetArgs([]string{})

	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})

	err = cmd.Execute()
	require.NoError(t, err)

	output := out.String()
	require.NotEmpty(t, output)
	// Project-aware output includes the project name.
	require.Contains(t, output, "myapp", "project-aware output should contain project name")
}
