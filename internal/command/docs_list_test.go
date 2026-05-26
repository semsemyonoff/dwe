package command

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDocsListCommand(t *testing.T) {
	flags := &rootFlags{
		configPath:  "",
		projectRoot: "",
		I18n:        nil,
		Locale:      "en",
	}

	cmd := newDocsListCmd(flags)
	require.NotNil(t, cmd)
	require.Equal(t, "list", cmd.Name())
	require.NotEmpty(t, cmd.Short)
}

func TestDocsListFlags(t *testing.T) {
	flags := &rootFlags{
		configPath:  "",
		projectRoot: "",
		I18n:        nil,
		Locale:      "en",
	}

	cmd := newDocsListCmd(flags)
	require.NotNil(t, cmd.Flag("lang"))
	require.NotNil(t, cmd.Flag("source"))
}

// TestDocsListOutput executes the list command and verifies tab-separated output format.
func TestDocsListOutput(t *testing.T) {
	flags := &rootFlags{
		configPath:  "",
		projectRoot: "",
		I18n:        nil,
		Locale:      "en",
	}

	cmd := newDocsListCmd(flags)
	var outBuf strings.Builder
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	require.NoError(t, err, "list should succeed with built-in docs")

	output := outBuf.String()
	require.NotEmpty(t, output, "list should produce output for built-in docs")

	// Each line must be tab-separated: source\tpath\tlang
	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		require.Len(t, parts, 3, "each output line must have exactly 3 tab-separated fields: %q", line)
	}
}
