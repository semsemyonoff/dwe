package docs

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"devbox-cli/internal/cli/cmdctx"

	"github.com/stretchr/testify/require"
)

func TestDocsExportCommand(t *testing.T) {
	// Verify the command structure
	flags := &cmdctx.RootFlags{
		ConfigPath: "",
		Root:       "",
		I18n:       nil,
		Locale:     "en",
	}

	cmd := newDocsExportCmd(flags)
	require.NotNil(t, cmd)
	require.Equal(t, "export", cmd.Name())
	require.NotEmpty(t, cmd.Short)
}

func TestDocsExportFlags(t *testing.T) {
	flags := &cmdctx.RootFlags{
		ConfigPath: "",
		Root:       "",
		I18n:       nil,
		Locale:     "en",
	}

	cmd := newDocsExportCmd(flags)

	// Check flags are registered
	require.NotNil(t, cmd.Flag("lang"))
	require.NotNil(t, cmd.Flag("include-project"))
	require.NotNil(t, cmd.Flag("include-internals"))
	require.NotNil(t, cmd.Flag("force"))
}

func TestDocsExportBasic(t *testing.T) {
	// Create a temporary directory
	tmpDir := t.TempDir()
	targetDir := filepath.Join(tmpDir, "export")

	flags := &cmdctx.RootFlags{
		ConfigPath: "",
		Root:       "",
		I18n:       nil,
		Locale:     "en",
	}

	cmd := newDocsExportCmd(flags)
	cmd.SetArgs([]string{targetDir})

	// Set up output buffer
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	cmd.SetOut(outBuf)
	cmd.SetErr(errBuf)

	// Run the command
	err := cmd.Execute()
	require.NoError(t, err)

	// Verify the target directory was created
	require.DirExists(t, targetDir)
}

func TestDocsExportWithForce(t *testing.T) {
	// Create a temporary directory with existing content
	tmpDir := t.TempDir()
	targetDir := filepath.Join(tmpDir, "export")
	err := os.MkdirAll(targetDir, 0o755)
	require.NoError(t, err)

	// Create existing file
	err = os.WriteFile(filepath.Join(targetDir, "existing.txt"), []byte("existing"), 0o644)
	require.NoError(t, err)

	flags := &cmdctx.RootFlags{
		ConfigPath: "",
		Root:       "",
		I18n:       nil,
		Locale:     "en",
	}

	cmd := newDocsExportCmd(flags)
	cmd.SetArgs([]string{targetDir, "--force"})

	// Set up output buffer
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	cmd.SetOut(outBuf)
	cmd.SetErr(errBuf)

	// Run the command
	err = cmd.Execute()
	require.NoError(t, err)
}

func TestDocsExportOutput(t *testing.T) {
	// Create a temporary directory
	tmpDir := t.TempDir()
	targetDir := filepath.Join(tmpDir, "export")

	flags := &cmdctx.RootFlags{
		ConfigPath: "",
		Root:       "",
		I18n:       nil,
		Locale:     "en",
	}

	cmd := newDocsExportCmd(flags)
	cmd.SetArgs([]string{targetDir})

	// Set up output buffer
	outBuf := &bytes.Buffer{}
	cmd.SetOut(outBuf)
	cmd.SetErr(&bytes.Buffer{})

	// Run the command
	err := cmd.Execute()
	require.NoError(t, err)

	// Check output contains success message
	output := outBuf.String()
	require.Contains(t, output, "exported")
	require.Contains(t, output, targetDir)
}
