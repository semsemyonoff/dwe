package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"devbox-cli/internal/cli/cmdctx"
	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/project/project"
	userpkg "devbox-cli/internal/core/project/user"
	"devbox-cli/internal/core/ui"
	"devbox-cli/internal/core/usercommands"
	"devbox-cli/internal/core/validate"
	valchecks "devbox-cli/internal/core/validate/checks"
	valcmds "devbox-cli/internal/core/validate/commands"
	valconfig "devbox-cli/internal/core/validate/config"
	valenv "devbox-cli/internal/core/validate/env"
	vali18n "devbox-cli/internal/core/validate/i18n"
	vallinters "devbox-cli/internal/core/validate/linters"
	valsetup "devbox-cli/internal/core/validate/setup"
	valsnap "devbox-cli/internal/core/validate/snapshot"
	valtmpl "devbox-cli/internal/core/validate/templates"
	"devbox-cli/internal/core/workflow/setup"
	"devbox-cli/internal/shared/i18n"

	"github.com/spf13/cobra"
)

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

// newValidateCmd builds the root validate command with all subcommands.
func newValidateCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	var strict, quiet bool
	var stage string
	var verifyChecksums bool

	cmd := &cobra.Command{
		GroupID: groupID,
		Use:     "validate",
		Short:   "Validate project configuration and files",
		Long: `Check project configuration files, template packs, command definitions, environment readiness, and project checks for errors and warnings.

Validation runs statically without executing any commands or starting services. Results are
reported in a table with severity levels (ok, info, warning, error). Use --strict to
treat warnings as errors. Use --quiet to hide ok/info rows.

Severity levels:
  ✓ ok      - validation passed
  ⓘ info    - informational message (not an error)
  ⚠ warning - potential issue, may cause problems
  ✗ error   - configuration is invalid and must be fixed

Exit code:
  0 - all checks passed (or only warnings without --strict)
  1 - one or more errors, or warnings with --strict

Scope targets:
  devbox validate                              - all (config + templates + commands + env + checks + linters + translations + snapshot)
  devbox validate config                       - all config validators
  devbox validate config <devbox|services|...> - specific config validator
  devbox validate templates                    - all template validators (ide, ai, git)
  devbox validate templates <ide|ai|git>       - specific template validator
  devbox validate commands                     - commands validator
  devbox validate env                          - environment readiness probes
  devbox validate checks [id]                  - project checks from devbox/validate.yml
  devbox validate linters [id]                 - external linters from devbox/validate.yml + autodetected built-ins
  devbox validate translations                 - translation files in devbox/i18n/
  devbox validate snapshot [<name>]            - snapshot config + on-disk integrity
`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, flags, strict, quiet, stage, false, nil)
		},
	}

	cmd.PersistentFlags().BoolVar(&strict, "strict", false, "treat warnings as errors (exit code 1)")
	cmd.PersistentFlags().BoolVar(&quiet, "quiet", false, "hide ok/info rows")
	cmd.PersistentFlags().StringVar(&stage, "stage", "", "filter checks by stage (deploy, run, stop, command)")

	// Config validators subtree.
	configCmd := &cobra.Command{
		Use:          "config",
		Short:        "Validate configuration files",
		Long:         `Check devbox.yml, devbox/services/<name>/service.yml, deploy.yml, reset.yml, and related config files for errors.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, flags, strict, quiet, stage, false, []string{"config"})
		},
	}
	configCmd.AddCommand(
		newValidateConfigSubCmd(flags, &strict, &quiet, &stage, "devbox", "Validate main devbox.yml"),
		newValidateConfigSubCmd(flags, &strict, &quiet, &stage, "services", "Validate devbox/services/<name>/service.yml"),
		newValidateConfigSubCmd(flags, &strict, &quiet, &stage, "docker", "Validate devbox/docker.yml"),
		newValidateConfigSubCmd(flags, &strict, &quiet, &stage, "info", "Validate devbox/info.yml"),
		newValidateConfigSubCmd(flags, &strict, &quiet, &stage, "styles", "Validate devbox/styles.yml"),
		newValidateConfigSubCmd(flags, &strict, &quiet, &stage, "lifecycle", "Validate devbox/lifecycle.yml"),
		newValidateConfigSubCmd(flags, &strict, &quiet, &stage, "deploy", "Validate devbox/deploy.yml"),
		newValidateConfigSubCmd(flags, &strict, &quiet, &stage, "reset", "Validate devbox/reset.yml (replaces 'devbox reset config check')"),
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
	cmd.AddCommand(&cobra.Command{
		Use:          "commands",
		Short:        "Validate command definitions",
		Long:         `Check devbox/commands for syntax errors, missing references, and other issues.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, flags, strict, quiet, stage, false, []string{"commands"})
		},
	})

	// Env probes.
	cmd.AddCommand(&cobra.Command{
		Use:          "env",
		Short:        "Validate environment readiness",
		Long:         `Run built-in environment probes (docker binary, docker daemon, compose plugin, git/shell binaries, .devbox writable).`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, flags, strict, quiet, stage, false, []string{"env"})
		},
	})

	// Checks (project-defined in devbox/validate.yml).
	cmd.AddCommand(&cobra.Command{
		Use:          "checks [id]",
		Short:        "Validate project checks from devbox/validate.yml",
		Long:         `Run project-defined checks from devbox/validate.yml. With an id, runs only that check.`,
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
	cmd.AddCommand(&cobra.Command{
		Use:          "setup",
		Short:        "Validate devbox/setup.yml schema and writes: paths",
		Long:         `Check devbox/setup.yml for valid question definitions, identifier rules, and target scope constraints.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, flags, strict, quiet, stage, false, []string{"setup"})
		},
	})

	// Translation file validators (i18n domain).
	cmd.AddCommand(&cobra.Command{
		Use:          "translations",
		Short:        "Validate translation files in devbox/i18n/",
		Long:         `Check devbox/i18n/*.yml files for parse errors, orphan command/group IDs, and unknown ui.* keys.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, flags, strict, quiet, stage, false, []string{"i18n"})
		},
	})

	// External linters (shellcheck, hadolint, generic).
	cmd.AddCommand(&cobra.Command{
		Use:          "linters [id]",
		Short:        "Run external linters (shellcheck, hadolint, generic)",
		Long:         `Run external linters configured in devbox/validate.yml and autodetected built-ins (shellcheck, hadolint). With an id, runs only that linter.`,
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
		Long:         `Validate devbox/snapshot.yml and (optionally with --verify) the on-disk integrity of every snapshot under ./snapshots/. Pass a name to scope checks to a single snapshot.`,
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

	return cmd
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
			configPathForReg = filepath.Join(projectRoot, "devbox.yml")
		}
		if reg, err := usercommands.LoadRegistryFromConfigPath(configPathForReg); err == nil {
			cmdReg = reg
		}
		// Ignore registry load errors — validators will self-skip on nil.
	}

	// Single-parse point for devbox/validate.yml. The load result (including
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
		if err != nil {
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
		setupPath = filepath.Join(projectRoot, "devbox", "setup.yml")
		setupCfg, setupCfgErr = setup.LoadSetupYAML(setupPath)
	}

	// Build the registry and run validators. Stage filtering happens at
	// assembly time for checks; env probes always run (they have no stages).
	registry := buildRegistry(cfg, validateCfg, validateLoadErr, snapCfg, snapCfgErr, setupCfg, setupCfgErr, setupPath, projectRoot, cmdReg, stage, verifyChecksums, scope, userCfg)
	diags := registry.Run(ctx, scope...)

	// Compute summary first so the header can reflect overall severity.
	summary := validate.Aggregate(diags)

	// Render the diagnostics table (skip when no rows to avoid an empty bordered box).
	rows := ui.FormatDiagnostics(diags, quiet)
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), styleValidateHeader(validateHeader(scope, stage), summary))
	if len(rows) > 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), ui.RenderDiagnosticsTable(rows))
	}
	summaryLine := ui.FormatSummary(summary)
	if partialLoadErr != nil {
		summaryLine += " (main config did not load; some validations skipped)"
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), summaryLine)

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
func loadForValidate(flags *cmdctx.RootFlags) (*config.DevboxConfig, string, string, error) {
	// First, locate the project (without schema validation).
	loc, found, err := project.Locate(flags.ConfigPath)
	if err != nil {
		return nil, "", "", fmt.Errorf("locating project: %w", err)
	}
	if !found {
		return nil, "", "", project.ErrNotFound
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
		return ui.StyleFailed(text)
	case summary.Warnings > 0:
		return ui.StyleWarning(text)
	default:
		return ui.StyleSectionTitle(text)
	}
}

// validateHeader returns a friendly description of what Devbox is checking,
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
	return fmt.Sprintf("Devbox checked %s%s. Results:", what, stageSuffix)
}

// validateScopeLabel produces a human label for the scope being validated.
func validateScopeLabel(scope []string) string {
	if len(scope) == 0 {
		return "your project (config, templates, commands, environment, project checks, linters, translations, and snapshots)"
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
		return "your project checks (devbox/validate.yml)"
	case "linters":
		if len(scope) > 1 {
			return "external linter " + scope[1]
		}
		return "your external linters"
	case "i18n":
		return "your translation files (devbox/i18n/)"
	case "setup":
		return "devbox/setup.yml"
	case "snapshot":
		if len(scope) > 1 {
			return "snapshot " + scope[1]
		}
		return "your snapshot configuration and on-disk snapshots"
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
func buildRegistry(cfg *config.DevboxConfig, validateCfg *config.ValidateConfig, validateLoadErr error, snapCfg *config.SnapshotConfig, snapCfgErr error, setupCfg *setup.Config, setupCfgErr error, setupPath string, baseDir string, cmdReg *usercommands.Registry, stage string, verifyChecksums bool, scope []string, userCfg *userpkg.Config) *validate.Registry {
	reg := validate.NewRegistry()
	for _, v := range valconfig.All() {
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
	for _, v := range valchecks.AllForStage(validateCfg, checksLoadErr, baseDir, cmdReg, stage) {
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
	// to avoid a duplicate diagnostic in a full `devbox validate` run.
	lintersLoadErr := validateLoadErr
	if validate.MatchScope("config", "validate", scope) {
		lintersLoadErr = nil
	}
	for _, v := range vallinters.All(validateCfg, lintersLoadErr, baseDir, userCfg) {
		reg.Register(v)
	}
	return reg
}
