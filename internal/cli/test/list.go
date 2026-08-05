package test

import (
	"fmt"
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

An absent workspace/tests/ directory is not an error — it simply lists nothing.

With --output json every scenario also carries a cost_profile: enabled service
count (after the scenario's env.services overlay), compose services that build,
external images to pull, the largest healthcheck start_period, shared volumes,
non-blocking compose isolation findings, and the number of type: shell steps the
scenario would run. Facts only — no cheap/expensive verdict; and it reports
whether there IS a build, not what the build costs (layer-cache warmth is not
modelled). The profile is omitted when the project config does not load.`,
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
	// CostProfile is omitted when the project state it needs is unavailable
	// (config, docker.yml or a deploy pipeline that does not load). `list`
	// must keep working on a broken config, so an absent profile is never an
	// error — see newCostProfiler.
	CostProfile *testCostProfileJSON `json:"cost_profile,omitempty"`
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

	// The cost profile is a JSON-only payload, so the text path keeps `list`'s
	// original shape exactly: no config load, no docker.yml, no pipeline load.
	var profiler *costProfiler
	if flags.Output == "json" && len(names) > 0 {
		profiler = newCostProfiler(baseDir, flags.ConfigPath)
	}

	entries := make([]testListEntryJSON, 0, len(names))
	for _, name := range names {
		path, err := envtest.ScenarioPath(baseDir, name)
		if err != nil {
			return cmdctx.ErrWrap("scenario_load_failed", err).WithDetail("scenario", name)
		}
		scn, err := envtest.LoadScenario(path)
		if err != nil {
			return cmdctx.ErrWrap("scenario_load_failed", err).WithDetail("scenario", name)
		}
		entries = append(entries, testListEntryJSON{
			Name:        name,
			Description: scn.Description,
			CostProfile: profiler.profile(scn),
		})
	}

	color := writerIsTTY(cmd.OutOrStdout())
	return cmdctx.WriteData(flags, cmd, testListJSON{Scenarios: entries}, func(data testListJSON) string {
		return renderTestListText(data, color)
	})
}

// renderTestListText renders a two-column name/description listing. An empty
// scenario set renders nothing (no stray output for an absent tests dir). With
// color off it is byte-identical to the historical `%-24s %s` form; with color
// on the name carries the accent token and the description the muted token.
// Padding is applied to the RAW name before styling so the column alignment is
// not thrown off by ANSI escapes.
func renderTestListText(data testListJSON, color bool) string {
	if len(data.Scenarios) == 0 {
		return ""
	}
	lines := make([]string, 0, len(data.Scenarios))
	for _, e := range data.Scenarios {
		if e.Description == "" {
			lines = append(lines, cName(e.Name, color))
			continue
		}
		lines = append(lines, cName(fmt.Sprintf("%-24s", e.Name), color)+" "+cMuted(e.Description, color))
	}
	return strings.Join(lines, "\n")
}
