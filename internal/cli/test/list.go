package test

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/workflow/envtest"

	"github.com/spf13/cobra"
)

func newTestListCmd(flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available integration test scenarios",
		Long: `List every scenario file under workspace/tests/*.yml with its description.

An absent workspace/tests/ directory is not an error — it simply lists nothing.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTestList(cmd, flags)
		},
	}
	return cmd
}

// testListEntryJSON is one scenario row in `dwe test list --output json`.
type testListEntryJSON struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// testListJSON is the JSON payload for `dwe test list --output json`.
type testListJSON struct {
	Scenarios []testListEntryJSON `json:"scenarios"`
}

func runTestList(cmd *cobra.Command, flags *cmdctx.RootFlags) error {
	baseDir := flags.ProjectRoot()
	names, err := envtest.ListScenarios(baseDir)
	if err != nil {
		return cmdctx.ErrWrap("scenario_list_failed", err)
	}

	entries := make([]testListEntryJSON, 0, len(names))
	for _, name := range names {
		scn, err := envtest.LoadScenario(filepath.Join(envtest.TestsDir(baseDir), name+".yml"))
		if err != nil {
			return cmdctx.ErrWrap("scenario_load_failed", err).WithDetail("scenario", name)
		}
		entries = append(entries, testListEntryJSON{Name: name, Description: scn.Description})
	}

	return cmdctx.WriteData(flags, cmd, testListJSON{Scenarios: entries}, renderTestListText)
}

// renderTestListText renders a two-column name/description listing. An empty
// scenario set renders nothing (no stray output for an absent tests dir).
func renderTestListText(data testListJSON) string {
	if len(data.Scenarios) == 0 {
		return ""
	}
	lines := make([]string, 0, len(data.Scenarios))
	for _, e := range data.Scenarios {
		if e.Description == "" {
			lines = append(lines, e.Name)
			continue
		}
		lines = append(lines, fmt.Sprintf("%-24s %s", e.Name, e.Description))
	}
	return strings.Join(lines, "\n")
}
