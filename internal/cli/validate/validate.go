// Package validate hosts the dwe validate command tree.
package validate

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/project/project"
	userpkg "github.com/semsemyonoff/dwe/internal/core/project/user"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/validate"
	valbridge "github.com/semsemyonoff/dwe/internal/core/validate/bridge"
	valchecks "github.com/semsemyonoff/dwe/internal/core/validate/checks"
	valcmds "github.com/semsemyonoff/dwe/internal/core/validate/commands"
	valconfig "github.com/semsemyonoff/dwe/internal/core/validate/config"
	valenv "github.com/semsemyonoff/dwe/internal/core/validate/env"
	vali18n "github.com/semsemyonoff/dwe/internal/core/validate/i18n"
	vallinters "github.com/semsemyonoff/dwe/internal/core/validate/linters"
	valsetup "github.com/semsemyonoff/dwe/internal/core/validate/setup"
	valsnap "github.com/semsemyonoff/dwe/internal/core/validate/snapshot"
	valtmpl "github.com/semsemyonoff/dwe/internal/core/validate/templates"
	valtests "github.com/semsemyonoff/dwe/internal/core/validate/tests"
	"github.com/semsemyonoff/dwe/internal/core/workflow/setup"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
	sharedrender "github.com/semsemyonoff/dwe/internal/shared/render"

	"github.com/spf13/cobra"
)

// validateJSON is the JSON output shape for `dwe validate --output json`.
type validateJSON struct {
	Summary     validateSummaryJSON `json:"summary"`
	Diagnostics []diagnosticJSON    `json:"diagnostics"`
}

type validateSummaryJSON struct {
	Scope   string `json:"scope"`
	Ok      int    `json:"ok"`
	Info    int    `json:"info"`
	Warning int    `json:"warning"`
	Error   int    `json:"error"`
}

type diagnosticJSON struct {
	Severity string `json:"severity"`
	Scope    string `json:"scope"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Message  string `json:"message"`
	Hint     string `json:"hint,omitempty"`
}

// severityNames maps the lowercase level tokens accepted by --level (and emitted
// by severityString) to their validate.Severity. Kept as the single source of
// truth for both parsing and shell completion.
var severityNames = map[string]validate.Severity{
	"ok":      validate.SeverityOK,
	"info":    validate.SeverityInfo,
	"warning": validate.SeverityWarning,
	"error":   validate.SeverityError,
}

// parseSeverityLevels turns the comma-separated --level value into a set of
// severities to display. An empty value yields a nil set, meaning "show all".
// An unknown token is a typed user error so JSON mode reports it cleanly.
func parseSeverityLevels(raw string) (map[validate.Severity]struct{}, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	set := make(map[validate.Severity]struct{})
	for tok := range strings.SplitSeq(raw, ",") {
		tok = strings.ToLower(strings.TrimSpace(tok))
		if tok == "" {
			continue
		}
		sev, ok := severityNames[tok]
		if !ok {
			return nil, cmdctx.Err("validate_invalid_level",
				fmt.Sprintf("unknown severity level %q (valid: ok, info, warning, error)", tok)).
				WithHint("pass a comma-separated list, e.g. --level error,warning")
		}
		set[sev] = struct{}{}
	}
	if len(set) == 0 {
		return nil, nil
	}
	return set, nil
}

// filterByLevels keeps only diagnostics whose severity is in set. A nil/empty
// set is a pass-through (no --level given → show everything).
func filterByLevels(diags []validate.Diagnostic, set map[validate.Severity]struct{}) []validate.Diagnostic {
	if len(set) == 0 {
		return diags
	}
	out := make([]validate.Diagnostic, 0, len(diags))
	for _, d := range diags {
		if _, ok := set[d.Severity]; ok {
			out = append(out, d)
		}
	}
	return out
}

// completeLevels offers severity names for `--level` shell completion, honoring
// the comma-separated form by completing the final token in place.
func completeLevels(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	prefix := ""
	last := toComplete
	if i := strings.LastIndex(toComplete, ","); i >= 0 {
		prefix = toComplete[:i+1]
		last = toComplete[i+1:]
	}
	var out []string
	for _, name := range []string{"ok", "info", "warning", "error"} {
		if strings.HasPrefix(name, last) {
			out = append(out, prefix+name)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
}

// filterHintThreshold is the number of rendered diagnostic rows above which a
// run counts as "long" and earns the trailing --level/--quiet hint. Roughly one
// screenful: below it the table is readable as-is and the hint is pure noise.
const filterHintThreshold = 20

// shouldEmitFilterHint decides whether a human-mode run is long enough — and
// narrowable enough — to warrant naming the filter flags.
//
// Three suppressions beyond the threshold itself, all guarding the same thing:
// never name a flag that would not actually improve the output.
//   - the user is already filtering (--quiet or --level), so they know the flags;
//   - every displayed row is already an error, in which case both flags would
//     remove nothing;
//   - nothing worth keeping survives either flag. A clean project is long
//     precisely because it renders one ok row per check, and both suggested
//     flags then empty the table completely — which is what a freshly
//     scaffolded project does (21 rows, 0 errors, 0 warnings), i.e. the single
//     most common way to meet the threshold.
func shouldEmitFilterHint(rows, errors, warnings int, quiet bool, levelRaw string) bool {
	if quiet || strings.TrimSpace(levelRaw) != "" {
		return false
	}
	if rows <= filterHintThreshold {
		return false
	}
	if errors == 0 && warnings == 0 {
		return false
	}
	return rows > errors
}

// emitFilterHint writes a single info line to stderr after a long diagnostics
// table, naming the two flags that shrink it. The output-narrowing flags have
// existed since May and were used zero times across the sessions this hint was
// added for — a table that scrolls past a screen is exactly the point of need.
//
// Same shape and constraints as cmdctx.EmitDefaultNotice: stderr only (stdout
// stays the parseable surface), no-op in JSON mode where the consumer filters
// the array itself.
func emitFilterHint(cmd *cobra.Command, flags *cmdctx.RootFlags, rows int, summary validate.Summary, quiet bool, levelRaw string) {
	if flags.Output == "json" {
		return
	}
	if !shouldEmitFilterHint(rows, summary.Errors, summary.Warnings, quiet, levelRaw) {
		return
	}
	sharedrender.NewWriter(cmd.ErrOrStderr()).Info(fmt.Sprintf(
		"Showing %d diagnostics. Narrow the output with --level error (or --level error,warning), or --quiet to drop the ok/info rows.",
		rows,
	))
}

// severityString converts a validate.Severity to its JSON string representation.
func severityString(s validate.Severity) string {
	switch s {
	case validate.SeverityOK:
		return "ok"
	case validate.SeverityInfo:
		return "info"
	case validate.SeverityWarning:
		return "warning"
	case validate.SeverityError:
		return "error"
	default:
		return "unknown"
	}
}

// canonicalScope renders scope as the machine-identifiable "domain" or
// "domain/id" form (or "all" for an unscoped run), so a narrowed run like
// `dwe validate config services` is distinguishable from `dwe validate
// config` by more than the raw diagnostic count. This is deliberately
// separate from validateScopeLabel, which produces prose for the header.
func canonicalScope(scope []string) string {
	if len(scope) == 0 {
		return "all"
	}
	if len(scope) == 1 {
		return scope[0]
	}
	return scope[0] + "/" + scope[1]
}

// buildValidateData converts diagnostics and summary into the JSON DTO.
func buildValidateData(diags []validate.Diagnostic, summary validate.Summary, scope []string) validateJSON {
	diagnostics := make([]diagnosticJSON, 0, len(diags))
	for _, d := range diags {
		// d.Target is a display-oriented label and may carry multi-line
		// decoration (e.g. `id\n(stages)\n[services]` for checks-domain
		// entries). The JSON scope is a machine identifier — take only the
		// first line so consumers see a stable `<domain>/<id>` regardless
		// of presentation-layer metadata.
		scope := d.Domain
		if d.Target != "" {
			target := d.Target
			if i := strings.IndexByte(target, '\n'); i >= 0 {
				target = target[:i]
			}
			scope = d.Domain + "/" + target
		}
		diagnostics = append(diagnostics, diagnosticJSON{
			Severity: severityString(d.Severity),
			Scope:    scope,
			File:     d.File,
			Line:     d.Line,
			Message:  d.Message,
			Hint:     d.Hint,
		})
	}
	return validateJSON{
		Summary: validateSummaryJSON{
			Scope:   canonicalScope(scope),
			Ok:      summary.OKs,
			Info:    summary.Infos,
			Warning: summary.Warnings,
			Error:   summary.Errors,
		},
		Diagnostics: diagnostics,
	}
}

// validationFailedError carries validation diagnostic summary and implements
// ExitCode() int so main.go can translate it to the appropriate exit code
// without printing fang's "Error: ..." line.
type validationFailedError struct {
	summary validate.Summary
	strict  bool
}

func (e *validationFailedError) Error() string {
	return "validation failed"
}

func (e *validationFailedError) ExitCode() int {
	return validate.ExitCode(e.summary, e.strict)
}

// NewCmd builds the root validate command with all subcommands.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	var strict, quiet bool
	var stage string
	var level string
	var verifyChecksums bool

	cmd := &cobra.Command{
		GroupID: groupID,
		Use:     "validate",
		Short:   "Validate project configuration and files",
		Long: `Check project configuration files, template packs, command definitions, environment readiness, and project checks for errors and warnings.

Validation runs statically without executing any commands or starting services. Results are
reported as one table per domain with severity levels (ok, info, warning, error). Use --strict
to treat warnings as errors, --quiet to hide ok/info rows, and --level to show only specific
severities (comma-separated, e.g. --level error,warning).

Severity levels:
  ✓ ok      - validation passed
  ⓘ info    - informational message (not an error)
  ⚠ warning - potential issue, may cause problems
  ✗ error   - configuration is invalid and must be fixed

Exit code:
  0 - all checks passed (or only warnings without --strict)
  1 - one or more errors, or warnings with --strict

Scope targets:
  dwe validate                                   - all (config + templates + commands + env + checks + linters + translations + snapshot + bridge + tests)
  dwe validate config                            - all config validators
  dwe validate config <workspace|services|...>   - specific config validator
  dwe validate templates                         - all template validators (ide, ai, git)
  dwe validate templates <ide|ai|git>            - specific template validator
  dwe validate commands                          - commands validator
  dwe validate env                               - environment readiness probes
  dwe validate checks [id]                       - project checks from workspace/validate.yml
  dwe validate linters [id]                      - external linters from workspace/validate.yml + autodetected built-ins
  dwe validate translations                      - translation files in workspace/i18n/
  dwe validate snapshot [<name>]                 - snapshot config + on-disk integrity
  dwe validate bridge                            - host-bridge service settings (bridge: blocks)
  dwe validate tests                             - workspace/tests/ integration-test scenarios
`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, flags, strict, quiet, stage, false, nil)
		},
	}

	cmd.PersistentFlags().BoolVar(&strict, "strict", false, "treat warnings as errors (exit code 1)")
	cmd.PersistentFlags().BoolVar(&quiet, "quiet", false, "hide ok/info rows")
	cmd.PersistentFlags().StringVar(&stage, "stage", "", "filter checks by stage (deploy, run, stop, command, post-setup)")
	cmd.PersistentFlags().StringVar(&level, "level", "", "show only these severity levels (comma-separated: ok, info, warning, error)")
	_ = cmd.RegisterFlagCompletionFunc("level", completeLevels)

	// Config validators subtree.
	configCmd := &cobra.Command{
		Use:          "config",
		Short:        "Validate configuration files",
		Long:         `Check workspace.yml, workspace/services/<name>/service.yml, deploy.yml, reset.yml, and related config files for errors.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, flags, strict, quiet, stage, false, []string{"config"})
		},
	}
	configCmd.AddCommand(
		newValidateConfigSubCmd(flags, &strict, &quiet, &stage, "workspace", "Validate main workspace.yml"),
		newValidateConfigSubCmd(flags, &strict, &quiet, &stage, "services", "Validate workspace/services/<name>/service.yml"),
		newValidateConfigSubCmd(flags, &strict, &quiet, &stage, "docker", "Validate workspace/docker.yml"),
		newValidateConfigSubCmd(flags, &strict, &quiet, &stage, "info", "Validate workspace/info.yml"),
		newValidateConfigSubCmd(flags, &strict, &quiet, &stage, "styles", "Validate workspace/styles.yml"),
		newValidateConfigSubCmd(flags, &strict, &quiet, &stage, "lifecycle", "Validate workspace/lifecycle.yml"),
		newValidateConfigSubCmd(flags, &strict, &quiet, &stage, "deploy", "Validate workspace/deploy.yml"),
		newValidateConfigSubCmd(flags, &strict, &quiet, &stage, "reset", "Validate workspace/reset.yml (replaces 'dwe reset config check')"),
		newValidateConfigSubCmd(flags, &strict, &quiet, &stage, "service-deploy", "Validate service deploy configs"),
	)
	cmd.AddCommand(configCmd)

	// Templates validators subtree.
	templatesCmd := &cobra.Command{
		Use:          "templates",
		Short:        "Validate template packs",
		Long:         `Check IDE, AI, and git template packs for validity and integrity.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, flags, strict, quiet, stage, false, []string{"templates"})
		},
	}
	templatesCmd.AddCommand(
		newValidateTemplateSubCmd(flags, &strict, &quiet, &stage, "ide", "Validate IDE template pack"),
		newValidateTemplateSubCmd(flags, &strict, &quiet, &stage, "ai", "Validate AI template pack"),
		newValidateTemplateSubCmd(flags, &strict, &quiet, &stage, "git", "Validate git hooks template pack"),
	)
	cmd.AddCommand(templatesCmd)

	// Commands validator.
	cmd.AddCommand(newValidateLeafCmd(flags, &strict, &quiet, &stage, "commands",
		"Validate command definitions",
		`Check workspace/commands for syntax errors, missing references, and other issues.`,
		"commands"))

	// Env probes.
	cmd.AddCommand(newValidateLeafCmd(flags, &strict, &quiet, &stage, "env",
		"Validate environment readiness",
		`Run built-in environment probes (docker binary, docker daemon, compose plugin, git/shell binaries, .dwe writable).`,
		"env"))

	// Checks (project-defined in workspace/validate.yml).
	cmd.AddCommand(&cobra.Command{
		Use:          "checks [id]",
		Short:        "Validate project checks from workspace/validate.yml",
		Long:         `Run project-defined checks from workspace/validate.yml. With an id, runs only that check.`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			scope := []string{"checks"}
			if len(args) > 0 {
				scope = append(scope, args[0])
			}
			return runValidate(cmd, flags, strict, quiet, stage, false, scope)
		},
	})

	// Setup validator.
	cmd.AddCommand(newValidateLeafCmd(flags, &strict, &quiet, &stage, "setup",
		"Validate workspace/setup.yml schema and writes: paths",
		`Check workspace/setup.yml for valid question definitions, identifier rules, and target scope constraints.`,
		"setup"))

	// Translation file validators (i18n domain).
	cmd.AddCommand(newValidateLeafCmd(flags, &strict, &quiet, &stage, "translations",
		"Validate translation files in workspace/i18n/",
		`Check workspace/i18n/*.yml files for parse errors, orphan command/group IDs, and unknown render.* keys.`,
		"i18n"))

	// External linters (shellcheck, hadolint, generic).
	cmd.AddCommand(&cobra.Command{
		Use:          "linters [id]",
		Short:        "Run external linters (shellcheck, hadolint, generic)",
		Long:         `Run external linters configured in workspace/validate.yml and autodetected built-ins (shellcheck, hadolint). With an id, runs only that linter.`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			scope := []string{"linters"}
			if len(args) > 0 {
				scope = append(scope, args[0])
			}
			return runValidate(cmd, flags, strict, quiet, stage, false, scope)
		},
	})

	// Snapshot validators (config + per-snapshot integrity).
	snapshotCmd := &cobra.Command{
		Use:          "snapshot [<name>]",
		Short:        "Validate snapshot config and on-disk snapshot integrity",
		Long:         `Validate workspace/snapshot.yml and (optionally with --verify) the on-disk integrity of every snapshot under ./snapshots/. Pass a name to scope checks to a single snapshot.`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			scope := []string{"snapshot"}
			if len(args) > 0 {
				scope = append(scope, args[0])
			}
			return runValidate(cmd, flags, strict, quiet, stage, verifyChecksums, scope)
		},
	}
	snapshotCmd.Flags().BoolVar(&verifyChecksums, "verify", false, "recompute artifact sha256 and compare against the manifest")
	cmd.AddCommand(snapshotCmd)

	// Host-bridge service settings (bridge domain).
	cmd.AddCommand(newValidateLeafCmd(flags, &strict, &quiet, &stage, "bridge",
		"Validate host-bridge service settings",
		`Check per-service bridge: blocks in workspace/services/<name>/service.yml — the on_unreachable policy, shim_path, and the workspace mapping bridged services need for working-directory translation.`,
		"bridge"))

	// Integration-test scenarios (tests domain).
	cmd.AddCommand(newValidateLeafCmd(flags, &strict, &quiet, &stage, "tests",
		"Validate workspace/tests/ scenario files",
		`Check workspace/tests/*.yml scenario files for schema errors, timeout parse errors, unknown service/command references, step resolution failures, and compose isolation warnings.`,
		"tests"))

	return cmd
}

// newValidateLeafCmd creates a single-scope validate leaf command (commands,
// env, setup, translations) — each runs the validators for exactly one scope
// with no positional args. scope is the validator-domain key (note that the
// `translations` command maps to the "i18n" scope).
func newValidateLeafCmd(flags *cmdctx.RootFlags, strict, quiet *bool, stage *string, use, short, long, scope string) *cobra.Command {
	return &cobra.Command{
		Use:          use,
		Short:        short,
		Long:         long,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, flags, *strict, *quiet, *stage, false, []string{scope})
		},
	}
}

// newValidateConfigSubCmd creates a leaf command for a single config validator.
func newValidateConfigSubCmd(flags *cmdctx.RootFlags, strict, quiet *bool, stage *string, id, short string) *cobra.Command {
	return &cobra.Command{
		Use:          id,
		Short:        short,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, flags, *strict, *quiet, *stage, false, []string{"config", id})
		},
	}
}

// newValidateTemplateSubCmd creates a leaf command for a single template validator.
func newValidateTemplateSubCmd(flags *cmdctx.RootFlags, strict, quiet *bool, stage *string, id, short string) *cobra.Command {
	return &cobra.Command{
		Use:          id,
		Short:        short,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, flags, *strict, *quiet, *stage, false, []string{"templates", id})
		},
	}
}

// runValidate executes validators matching the given scope and renders diagnostics.
// scope may be nil (run all), or a list like ["config"], ["config", "deploy"], etc.
func runValidate(cmd *cobra.Command, flags *cmdctx.RootFlags, strict, quiet bool, stage string, verifyChecksums bool, scope []string) error {
	cfg, configPath, projectRoot, err := loadForValidate(flags)
	// cfg may be nil if load failed, but that's OK — validators will report the error.
	// Only abort for infrastructure errors (cwd unreadable, etc.)
	var partialLoadErr error
	if err != nil && !errors.Is(err, errPartialLoad) {
		return err
	}
	if errors.Is(err, errPartialLoad) {
		partialLoadErr = err
	}

	// Load the command registry diagnostically (nil is OK if it fails or project isn't found).
	var cmdReg *usercommands.Registry
	if projectRoot != "" {
		configPathForReg := configPath
		if configPathForReg == "" {
			configPathForReg = filepath.Join(projectRoot, "workspace.yml")
		}
		if reg, err := usercommands.LoadRegistryFromConfigPath(configPathForReg); err == nil {
			cmdReg = reg
		}
		// Ignore registry load errors — validators will self-skip on nil.
	}

	// Single-parse point for workspace/validate.yml. The load result (including
	// any error) is threaded via validate.Context so the config.validate
	// validator and the checks roster can read it without re-parsing.
	var (
		validateCfg      *config.ValidateConfig
		validateWarnings []validate.Diagnostic
		validateLoadErr  error
	)
	if projectRoot != "" {
		validateCfg, validateWarnings, validateLoadErr = config.LoadValidateConfig(config.ValidateConfigPath(projectRoot))
	}

	// Load user-config diagnostically (nil is OK if it fails or is absent).
	// Per the pattern in command/root.go:156-160, userconfig load failures
	// are logged as warnings and do not break project-level validation.
	var userCfg *userpkg.Config
	if projectRoot != "" {
		userCfg, err = userpkg.Load(projectRoot)
		if err != nil && flags.Output != "json" {
			// Log warning but continue with nil userConfig
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: user config load failed: %v\n", err)
		}
	}

	ctx := validate.Context{
		Ctx:                 cmd.Context(),
		ProjectRoot:         projectRoot,
		ConfigPath:          configPath,
		Cfg:                 cfg,
		CommandRegistry:     cmdReg,
		ValidateCfg:         validateCfg,
		ValidateCfgWarnings: validateWarnings,
		ValidateCfgLoadErr:  validateLoadErr,
	}

	// Load snapshot.yml once, threading the result + any load error into
	// buildRegistry so the snapshot validators can self-skip on parse failures
	// without re-reading the file from disk.
	var (
		snapCfg    *config.SnapshotConfig
		snapCfgErr error
	)
	if projectRoot != "" {
		snapCfg, snapCfgErr = config.LoadSnapshotConfig(config.SnapshotConfigPath(projectRoot))
	}

	// Load setup.yml once, threading the result + any load error into
	// buildRegistry so the setup validators can self-skip on parse failures
	// without re-reading the file from disk.
	var (
		setupCfg    *setup.Config
		setupCfgErr error
		setupPath   string
	)
	if projectRoot != "" {
		setupPath = filepath.Join(projectRoot, "workspace", "setup.yml")
		setupCfg, setupCfgErr = setup.LoadSetupYAML(setupPath)
	}

	// Build the registry and run validators. Stage filtering happens at
	// assembly time for checks; env probes always run (they have no stages).
	registry := buildRegistry(cfg, validateCfg, validateLoadErr, snapCfg, snapCfgErr, setupCfg, setupCfgErr, setupPath, projectRoot, cmdReg, stage, verifyChecksums, scope, userCfg)
	diags := registry.Run(ctx, scope...)

	// Compute summary first so the header can reflect overall severity. The
	// summary (and therefore the exit code) is always computed over the full
	// diagnostic set — the --level filter below is display-only.
	summary := validate.Aggregate(diags)

	// Optional severity filter (--level): like --quiet, it only narrows which
	// diagnostics are shown (in both text and JSON). It never changes the
	// summary counts or the exit code.
	levelRaw, _ := cmd.Flags().GetString("level")
	levelSet, levelErr := parseSeverityLevels(levelRaw)
	if levelErr != nil {
		return levelErr
	}
	displayDiags := filterByLevels(diags, levelSet)

	// JSON mode: emit data DTO and preserve exit code via validationFailedError.
	// Diagnostics ARE the data — no error envelope is emitted for validation
	// failures (the exit code conveys severity; the envelope would be redundant).
	if flags.Output == "json" {
		data := buildValidateData(displayDiags, summary, scope)
		if err := cmdctx.WriteJSON(flags, cmd, data); err != nil {
			return err
		}
		if validate.ExitCode(summary, strict) != 0 {
			return &validationFailedError{summary: summary, strict: strict}
		}
		return nil
	}

	// Text mode: render one table per domain (skip when no rows to avoid an
	// empty bordered box).
	rows := render.FormatDiagnostics(displayDiags, quiet)
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), styleValidateHeader(validateHeader(scope, stage), summary))
	if len(rows) > 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), render.DiagnosticsByDomain(rows))
	}
	summaryLine := render.FormatSummary(summary) + fmt.Sprintf(" (scope: %s)", canonicalScope(scope))
	if partialLoadErr != nil {
		summaryLine += " (main config did not load; some validations skipped)"
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), summaryLine)

	emitFilterHint(cmd, flags, len(rows), summary, quiet, levelRaw)

	// Check if validation failed.
	if validate.ExitCode(summary, strict) != 0 {
		return &validationFailedError{summary: summary, strict: strict}
	}
	return nil
}

// errPartialLoad is a sentinel error indicating config load failed but we should
// continue running validators (they'll report file-level diagnostics).
var errPartialLoad = errors.New("partial load")

// loadForValidate loads the project config, but does NOT enforce schema validation
// at the caller level. It returns the merged config (or nil if load failed), the paths,
// and errPartialLoad if the config load failed but we should continue (to allow the
// validators to surface file-level diagnostics).
func loadForValidate(flags *cmdctx.RootFlags) (*config.DweConfig, string, string, error) {
	// First, locate the project (without schema validation).
	loc, found, err := project.Locate(flags.ConfigPath)
	if err != nil {
		return nil, "", "", cmdctx.ErrWrap("project_invalid_config", err)
	}
	if !found {
		return nil, "", "", cmdctx.ErrWrap("project_not_found", project.ErrNotFound).
			WithHint("run from a dwe project directory or pass --config")
	}

	configPath := loc.ConfigPath
	projectRoot := loc.Root

	// Now try to load the config. If it fails, return errPartialLoad so the
	// validators can still run their own per-file loaders.
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		// Return the paths anyway, and signal partial load.
		return nil, configPath, projectRoot, fmt.Errorf("%w: %w", errPartialLoad, err)
	}

	return cfg, configPath, projectRoot, nil
}

// styleValidateHeader colors the validate header by overall severity:
// red when any errors were found, yellow on warnings-only, cyan otherwise.
func styleValidateHeader(text string, summary validate.Summary) string {
	switch {
	case summary.Errors > 0:
		return styles.StyleFailed(text)
	case summary.Warnings > 0:
		return styles.StyleWarning(text)
	default:
		return styles.StyleSectionTitle(text)
	}
}

// validateHeader returns a friendly description of what DWE is checking,
// shown above the diagnostics table so the user has context for the rows.
// The wording intentionally avoids jargon like "validator" / "scope" so the
// header reads naturally to someone who has not internalised our domain
// model.
func validateHeader(scope []string, stage string) string {
	stageSuffix := ""
	if stage != "" {
		stageSuffix = fmt.Sprintf(" (stage: %s)", stage)
	}
	what := validateScopeLabel(scope)
	return fmt.Sprintf("DWE checked %s%s. Results:", what, stageSuffix)
}

// validateScopeLabel produces a human label for the scope being validated.
func validateScopeLabel(scope []string) string {
	if len(scope) == 0 {
		return "your project (config, templates, commands, environment, project checks, linters, translations, snapshots, host-bridge settings, and integration-test scenarios)"
	}
	switch scope[0] {
	case "config":
		if len(scope) > 1 {
			return "config file " + scope[1]
		}
		return "your configuration files"
	case "templates":
		if len(scope) > 1 {
			return "template pack " + scope[1]
		}
		return "your template packs"
	case "commands":
		return "your command definitions"
	case "env":
		return "environment readiness"
	case "checks":
		if len(scope) > 1 {
			return "project check " + scope[1]
		}
		return "your project checks (workspace/validate.yml)"
	case "linters":
		if len(scope) > 1 {
			return "external linter " + scope[1]
		}
		return "your external linters"
	case "i18n":
		return "your translation files (workspace/i18n/)"
	case "setup":
		return "workspace/setup.yml"
	case "snapshot":
		if len(scope) > 1 {
			return "snapshot " + scope[1]
		}
		return "your snapshot configuration and on-disk snapshots"
	case "bridge":
		return "your host-bridge service settings (bridge: blocks in service.yml)"
	case "tests":
		return "your integration-test scenarios (workspace/tests/)"
	}
	return strings.Join(scope, " ")
}

// buildRegistry assembles validators from all domains (config / templates /
// commands / env / checks). Stage filtering is applied at assembly time for
// checks; env probes have no stages and always register.
//
// scope is passed so that the checks domain only receives the validateLoadErr
// sentinel when config.validate (domain="config", id="validate") is outside
// scope. When config.validate IS in scope it already surfaces the same parse
// error, so passing the error to AllForStage as well would emit a duplicate
// diagnostic and inflate the error count.
func buildRegistry(cfg *config.DweConfig, validateCfg *config.ValidateConfig, validateLoadErr error, snapCfg *config.SnapshotConfig, snapCfgErr error, setupCfg *setup.Config, setupCfgErr error, setupPath string, baseDir string, cmdReg *usercommands.Registry, stage string, verifyChecksums bool, scope []string, userCfg *userpkg.Config) *validate.Registry {
	reg := validate.NewRegistry()
	for _, v := range valconfig.All() {
		reg.Register(v)
	}
	// Bridge domain participates in `dwe validate` only — preflight never
	// registers valbridge validators, so bridge config mistakes never block
	// unrelated lifecycle commands.
	for _, v := range valbridge.All() {
		reg.Register(v)
	}
	for _, v := range valtmpl.All() {
		reg.Register(v)
	}
	for _, v := range valcmds.All() {
		reg.Register(v)
	}
	for _, v := range valenv.All(cfg) {
		reg.Register(v)
	}
	// Load i18n translation files for validation.
	i18nProjectFiles, _ := i18n.LoadProjectBundles(baseDir)
	for _, v := range vali18n.All(i18nProjectFiles, cmdReg) {
		reg.Register(v)
	}
	// Only propagate the load error into the checks domain when config.validate
	// will not run (e.g. scope is ["checks"] or ["checks", "<id>"]). When
	// config.validate IS in scope it emits the same diagnostic already.
	checksLoadErr := validateLoadErr
	if validate.MatchScope("config", "validate", scope) {
		checksLoadErr = nil
	}
	// Explicit-by-ID escape hatch: `dwe validate checks <id>` bypasses the
	// services-gate so users can inspect a check whose target services are
	// all disabled. Normal-run (no id) keeps the gate so disabled-service
	// checks stay silent — matching preflight behaviour.
	skipServicesGate := len(scope) == 2 && scope[0] == "checks"
	// runValidate tolerates a nil cfg on the errPartialLoad path. Two things
	// to handle there: (a) avoid the nil deref on cfg.Services, and (b) avoid
	// silently dropping every services-gated check (the user can't see which
	// services are enabled when the project failed to load — better to show
	// every check so failures surface alongside the parse error). Forcing the
	// gate off on a nil cfg gives the user the maximum signal.
	var checkServices map[string]config.ServiceConfig
	if cfg != nil {
		checkServices = cfg.Services
	} else {
		skipServicesGate = true
	}
	for _, v := range valchecks.AllForStage(validateCfg, checksLoadErr, baseDir, cmdReg, stage, checkServices, skipServicesGate) {
		reg.Register(v)
	}
	for _, v := range valsnap.All(cfg, snapCfg, snapCfgErr, baseDir, cmdReg, verifyChecksums) {
		reg.Register(v)
	}
	for _, v := range valsetup.All(setupCfg, setupCfgErr, setupPath) {
		reg.Register(v)
	}
	// Same deduplication as checksLoadErr: when config.validate is in scope it
	// already surfaces the parse error, so suppress it from the linters domain
	// to avoid a duplicate diagnostic in a full `dwe validate` run.
	lintersLoadErr := validateLoadErr
	if validate.MatchScope("config", "validate", scope) {
		lintersLoadErr = nil
	}
	for _, v := range vallinters.All(validateCfg, lintersLoadErr, baseDir, userCfg) {
		reg.Register(v)
	}
	for _, v := range valtests.All() {
		reg.Register(v)
	}
	return reg
}
