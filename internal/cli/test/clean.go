package test

import (
	"fmt"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/workflow/envtest"

	"github.com/spf13/cobra"
)

// cleanFn is the seam `dwe test clean` drives; envtest.Clean satisfies it in
// production, tests inject a stub — mirrors newRunner in run.go.
var cleanFn = envtest.Clean

func newTestCleanCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "clean [scenario...]",
		Short: "Remove orphaned or kept integration-test environments",
		Long: `Sweep test environments left behind by 'dwe test run --keep' or an
interrupted/crashed run, driven strictly by the on-disk manifests under
.dwe/tests/manifests/.

With no arguments, every manifested scenario is swept. Passing scenario names
restricts the sweep to those. A scenario whose flock is currently held by a
live run is skipped, never torn down. A best-effort, report-only scan also
lists compose projects matching this project's test-name prefix that have no
manifest at all — those are never destroyed automatically.

Exit codes: 0 = sweep completed (including nothing to sweep), 1 = at least
one manifest's teardown did not complete cleanly.`,
		Example: `  dwe test clean
  dwe test clean smoke
  dwe test clean --dry-run`,
		Args:         cobra.ArbitraryArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTestClean(cmd, flags, args, dryRun)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"report what would be swept without tearing anything down")
	return cmd
}

// cleanEntryJSON is one swept/skipped/failed manifest row.
type cleanEntryJSON struct {
	Scenario       string `json:"scenario"`
	ComposeProject string `json:"compose_project"`
	CopyPath       string `json:"copy_path"`
	Reason         string `json:"reason,omitempty"`
	Error          string `json:"error,omitempty"`
}

// cleanOrphanJSON is one report-only orphan row.
type cleanOrphanJSON struct {
	ComposeProject string `json:"compose_project"`
	Note           string `json:"note"`
}

// testCleanJSON is the JSON payload for `dwe test clean --output json`.
type testCleanJSON struct {
	DryRun  bool              `json:"dry_run"`
	Swept   []cleanEntryJSON  `json:"swept"`
	Skipped []cleanEntryJSON  `json:"skipped"`
	Failed  []cleanEntryJSON  `json:"failed"`
	Orphans []cleanOrphanJSON `json:"orphans"`
}

func runTestClean(cmd *cobra.Command, flags *cmdctx.RootFlags, args []string, dryRun bool) error {
	// Must run before Clean, which tears down via `compose down` (spec §3).
	envtest.ScrubComposeEnv()

	warnColor := writerIsTTY(cmd.ErrOrStderr())
	warn := func(msg string) {
		if flags.Output == "json" {
			return
		}
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), styledWarning("", msg, warnColor))
	}

	req := envtest.CleanRequest{
		BaseDir:   flags.ProjectRoot(),
		Scenarios: args,
		DryRun:    dryRun,
		Warn:      warn,
	}
	result, err := cleanFn(cmd.Context(), req)
	if err != nil {
		return cmdctx.ErrWrap("test_clean_failed", err)
	}

	payload := testCleanJSONFromResult(result)
	color := writerIsTTY(cmd.OutOrStdout())
	if err := cmdctx.WriteData(flags, cmd, payload, func(data testCleanJSON) string {
		return renderTestCleanText(data, color)
	}); err != nil {
		return err
	}

	if len(result.Failed) > 0 {
		return &testRunOutcomeError{code: 1}
	}
	return nil
}

func testCleanJSONFromResult(result *envtest.CleanResult) testCleanJSON {
	payload := testCleanJSON{
		DryRun:  result.DryRun,
		Swept:   make([]cleanEntryJSON, 0, len(result.Swept)),
		Skipped: make([]cleanEntryJSON, 0, len(result.Skipped)),
		Failed:  make([]cleanEntryJSON, 0, len(result.Failed)),
		Orphans: make([]cleanOrphanJSON, 0, len(result.Orphans)),
	}
	for _, e := range result.Swept {
		payload.Swept = append(payload.Swept, cleanEntryJSON{
			Scenario: e.Scenario, ComposeProject: e.ComposeProject, CopyPath: e.CopyPath,
		})
	}
	for _, e := range result.Skipped {
		payload.Skipped = append(payload.Skipped, cleanEntryJSON{
			Scenario: e.Scenario, ComposeProject: e.ComposeProject, CopyPath: e.CopyPath, Reason: e.Reason,
		})
	}
	for _, e := range result.Failed {
		payload.Failed = append(payload.Failed, cleanEntryJSON{
			Scenario: e.Scenario, ComposeProject: e.ComposeProject, CopyPath: e.CopyPath, Error: e.Error,
		})
	}
	for _, o := range result.Orphans {
		payload.Orphans = append(payload.Orphans, cleanOrphanJSON{ComposeProject: o.ComposeProject, Note: o.Note})
	}
	return payload
}

// renderTestCleanText renders the per-entry lines followed by a summary line.
// With color off it is byte-identical to the historical plain form; with color
// on each entry gains a leading ✓/✗/• glyph, an accent scenario name, a
// severity-colored verb (success for swept, danger for failed, warning for
// skipped/orphan), and muted secondary text.
func renderTestCleanText(data testCleanJSON, color bool) string {
	sweptVerb := "swept"
	if data.DryRun {
		sweptVerb = "would sweep"
	}

	var lines []string
	for _, e := range data.Swept {
		lines = append(lines, cleanEntryLine(sevSuccess, e.Scenario, sweptVerb, e.ComposeProject, color))
	}
	for _, e := range data.Skipped {
		lines = append(lines, cleanEntryLine(sevWarning, e.Scenario, "skipped", e.Reason, color))
	}
	for _, e := range data.Failed {
		lines = append(lines, cleanEntryLine(sevDanger, e.Scenario, "failed", e.Error, color))
	}
	for _, o := range data.Orphans {
		// Orphans have no scenario name; the "orphan" tag carries the warning glyph.
		line := statusWord("orphan", sevWarning, color) + ": " + cMuted(o.ComposeProject, color) + " (" + cMuted(o.Note, color) + ")"
		if g := statusGlyph(sevWarning, color); g != "" {
			line = g + " " + line
		}
		lines = append(lines, line)
	}

	summary := fmt.Sprintf("%d %s, %d skipped, %d failed, %d orphan(s)",
		len(data.Swept), sweptVerb, len(data.Skipped), len(data.Failed), len(data.Orphans))
	if len(lines) == 0 {
		return summary
	}
	lines = append(lines, "", summary)
	return strings.Join(lines, "\n")
}

// cleanEntryLine formats one `<name>: <verb> (<detail>)` clean entry, byte-identical
// to the historical form when color is off, with a leading glyph + palette colors
// when on.
func cleanEntryLine(sev severity, scenario, verb, detail string, color bool) string {
	line := cName(scenario, color) + ": " + statusWord(verb, sev, color) + " (" + cMuted(detail, color) + ")"
	if g := statusGlyph(sev, color); g != "" {
		line = g + " " + line
	}
	return line
}
