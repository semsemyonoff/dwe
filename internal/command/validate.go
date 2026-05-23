package command

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"devbox-cli/internal/config"
	"devbox-cli/internal/project"
	"devbox-cli/internal/ui"
	"devbox-cli/internal/usercommands"
	"devbox-cli/internal/validate"
	valchecks "devbox-cli/internal/validate/checks"
	valcmds "devbox-cli/internal/validate/commands"
	valconfig "devbox-cli/internal/validate/config"
	valenv "devbox-cli/internal/validate/env"
	valtmpl "devbox-cli/internal/validate/templates"

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
func newValidateCmd(flags *rootFlags) *cobra.Command {
	var strict, quiet bool
	var stage string

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate project configuration and files",
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
  devbox validate                              - all (config + templates + commands + env + checks)
  devbox validate config                       - all config validators
  devbox validate config <devbox|services|...> - specific config validator
  devbox validate templates                    - all template validators (ide, ai, git)
  devbox validate templates <ide|ai|git>       - specific template validator
  devbox validate commands                     - commands validator
  devbox validate env                          - environment readiness probes
  devbox validate checks [id]                  - project checks from devbox/validate.yml
`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, flags, strict, quiet, stage, nil)
		},
	}

	cmd.PersistentFlags().BoolVar(&strict, "strict", false, "treat warnings as errors (exit code 1)")
	cmd.PersistentFlags().BoolVar(&quiet, "quiet", false, "hide ok/info rows")
	cmd.PersistentFlags().StringVar(&stage, "stage", "", "filter checks by stage (deploy, run, stop, command)")

	// Config validators subtree.
	configCmd := &cobra.Command{
		Use:          "config",
		Short:        "Validate configuration files",
		Long:         `Check devbox.yml, services.yml, deploy.yml, reset.yml, and related config files for errors.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, flags, strict, quiet, stage, []string{"config"})
		},
	}
	configCmd.AddCommand(
		newValidateConfigSubCmd(flags, &strict, &quiet, &stage, "devbox", "Validate main devbox.yml"),
		newValidateConfigSubCmd(flags, &strict, &quiet, &stage, "services", "Validate devbox/services.yml"),
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
			return runValidate(cmd, flags, strict, quiet, stage, []string{"templates"})
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
			return runValidate(cmd, flags, strict, quiet, stage, []string{"commands"})
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
			return runValidate(cmd, flags, strict, quiet, stage, []string{"env"})
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
			return runValidate(cmd, flags, strict, quiet, stage, scope)
		},
	})

	return cmd
}

// newValidateConfigSubCmd creates a leaf command for a single config validator.
func newValidateConfigSubCmd(flags *rootFlags, strict, quiet *bool, stage *string, id, short string) *cobra.Command {
	return &cobra.Command{
		Use:          id,
		Short:        short,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, flags, *strict, *quiet, *stage, []string{"config", id})
		},
	}
}

// newValidateTemplateSubCmd creates a leaf command for a single template validator.
func newValidateTemplateSubCmd(flags *rootFlags, strict, quiet *bool, stage *string, id, short string) *cobra.Command {
	return &cobra.Command{
		Use:          id,
		Short:        short,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, flags, *strict, *quiet, *stage, []string{"templates", id})
		},
	}
}

// runValidate executes validators matching the given scope and renders diagnostics.
// scope may be nil (run all), or a list like ["config"], ["config", "deploy"], etc.
func runValidate(cmd *cobra.Command, flags *rootFlags, strict, quiet bool, stage string, scope []string) error {
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

	ctx := validate.Context{
		ProjectRoot:         projectRoot,
		ConfigPath:          configPath,
		Cfg:                 cfg,
		CommandRegistry:     cmdReg,
		ValidateCfg:         validateCfg,
		ValidateCfgWarnings: validateWarnings,
		ValidateCfgLoadErr:  validateLoadErr,
	}

	// When scope targets checks and validate.yml failed to load (not merely
	// absent), surface the parse error immediately rather than returning zero
	// diagnostics — a silent empty result would mislead the user into thinking
	// their checks passed.
	if len(scope) > 0 && scope[0] == "checks" &&
		validateLoadErr != nil && !errors.Is(validateLoadErr, os.ErrNotExist) {
		return fmt.Errorf("devbox/validate.yml: %w", validateLoadErr)
	}

	// Build the registry and run validators. Stage filtering happens at
	// assembly time for checks; env probes always run (they have no stages).
	registry := buildRegistry(cfg, validateCfg, projectRoot, cmdReg, stage)
	diags := registry.Run(ctx, scope...)

	// Render the diagnostics table (skip when no rows to avoid an empty bordered box).
	rows := ui.FormatDiagnostics(diags, quiet)
	if len(rows) > 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), ui.RenderDiagnosticsTable(rows))
	}

	// Compute summary and print it.
	summary := validate.Aggregate(diags)
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
func loadForValidate(flags *rootFlags) (*config.DevboxConfig, string, string, error) {
	// First, locate the project (without schema validation).
	loc, found, err := project.Locate(flags.configPath)
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

// buildRegistry assembles validators from all domains (config / templates /
// commands / env / checks). Stage filtering is applied at assembly time for
// checks; env probes have no stages and always register.
func buildRegistry(cfg *config.DevboxConfig, validateCfg *config.ValidateConfig, baseDir string, cmdReg *usercommands.Registry, stage string) *validate.Registry {
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
	for _, v := range valchecks.AllForStage(validateCfg, baseDir, cmdReg, stage) {
		reg.Register(v)
	}
	return reg
}
