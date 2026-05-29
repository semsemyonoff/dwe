package docs

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/cli/cmdctx"

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

	require.NotNil(t, cmd.Flag("output"))
	require.NotNil(t, cmd.Flag("lang"))
	require.NotNil(t, cmd.Flag("include-internals"))
	require.NotNil(t, cmd.Flag("no-project"))
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
	cmd.SetArgs([]string{"--output", outPath})

	out := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	// stdout should be empty when writing to file
	require.Empty(t, out.String(), "stdout should be empty when --output is set")

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
	cmd.SetArgs([]string{"--output", outPath})

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

func TestDocsLlmsTxtCommand_IncludeInternals_Flag(t *testing.T) {
	flags := newTestLlmsTxtFlags()
	cmd := newDocsLlmsTxtCmd(flags)
	cmd.SetArgs([]string{"--include-internals"})

	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotEmpty(t, out.String())
}

func TestDocsLlmsTxtCommand_NoProjectFlag_InsideProject(t *testing.T) {
	// Create a minimal fake devbox project.
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "devbox.yml")
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
	// Generic output starts with "# devbox", not "# test-project".
	require.True(t, strings.HasPrefix(output, "# devbox"), "no-project output should use generic title, got: %q", output[:min(len(output), 40)])
}

func TestDocsLlmsTxtCommand_ProjectAware(t *testing.T) {
	// Create a minimal fake devbox project.
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "devbox.yml")
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
