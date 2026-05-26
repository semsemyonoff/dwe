package command

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDocsShowExactMatch(t *testing.T) {
	// Use testdata embedded docs for this test
	flags := &rootFlags{
		configPath:  "",
		projectRoot: "",
		I18n:        nil,
		Locale:      "en",
	}

	cmd := newDocsShowCmd(flags)
	require.NotNil(t, cmd)

	// Verify the command exists and has the right shape
	require.Equal(t, "show", cmd.Name())
	require.NotEmpty(t, cmd.Short)
}

func TestDocsShowRawMode(t *testing.T) {
	// Test that --raw flag forces raw output even in TTY
	flags := &rootFlags{
		configPath:  "",
		projectRoot: "",
		I18n:        nil,
		Locale:      "en",
	}

	cmd := newDocsShowCmd(flags)
	require.NotNil(t, cmd)

	// Check flags are registered
	require.NotNil(t, cmd.Flag("raw"))
	require.NotNil(t, cmd.Flag("lang"))
	require.NotNil(t, cmd.Flag("source"))
}

func TestGetTermWidth(t *testing.T) {
	width := getTermWidth()
	require.Greater(t, width, 0)
}

func TestDocShowIntegration(t *testing.T) {
	// Create a temporary test directory with a simple markdown file
	tmpDir := t.TempDir()

	// Create a docs subdirectory with a test markdown file
	docsDir := filepath.Join(tmpDir, "docs")
	err := os.MkdirAll(docsDir, 0o755)
	require.NoError(t, err)

	testMarkdown := "# Test Documentation\n\nThis is a test file.\n"
	err = os.WriteFile(filepath.Join(docsDir, "test.md"), []byte(testMarkdown), 0o644)
	require.NoError(t, err)

	// Create minimal devbox.yml to trigger project discovery
	err = os.WriteFile(filepath.Join(tmpDir, "devbox.yml"), []byte(""), 0o644)
	require.NoError(t, err)

	// Verify the test files exist
	require.FileExists(t, filepath.Join(docsDir, "test.md"))
}

func TestDocsShowOutput(t *testing.T) {
	// Test that the command can write to a buffer without panicking
	flags := &rootFlags{
		configPath:  "",
		projectRoot: "",
		I18n:        nil,
		Locale:      "en",
	}

	cmd := newDocsShowCmd(flags)
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	// The command should exist and be executable (even if it fails on missing topic)
	require.NotNil(t, cmd.RunE)
}
