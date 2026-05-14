package command

import (
	"errors"
	"fmt"

	"devbox-cli/internal/config"
	"devbox-cli/internal/project"
	"devbox-cli/internal/ui"
	"devbox-cli/internal/validate"
	valcmds "devbox-cli/internal/validate/commands"
	valconfig "devbox-cli/internal/validate/config"
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

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate project configuration and files",
		Long: `Check project configuration files, template packs, and command definitions for errors and warnings.

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
  devbox validate                              - all (config + templates + commands)
  devbox validate config                       - all config validators
  devbox validate config <devbox|services|...> - specific config validator
  devbox validate templates                    - all template validators (ide, ai)
  devbox validate templates <ide|ai>           - specific template validator
  devbox validate commands                     - commands validator
`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, flags, strict, quiet, nil)
		},
	}

	cmd.PersistentFlags().BoolVar(&strict, "strict", false, "treat warnings as errors (exit code 1)")
	cmd.PersistentFlags().BoolVar(&quiet, "quiet", false, "hide ok/info rows")

	// Config validators subtree.
	configCmd := &cobra.Command{
		Use:          "config",
		Short:        "Validate configuration files",
		Long:         `Check devbox.yml, services.yml, deploy.yml, reset.yml, and related config files for errors.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, flags, strict, quiet, []string{"config"})
		},
	}
	configCmd.AddCommand(
		newValidateConfigSubCmd(flags, &strict, &quiet, "devbox", "Validate main devbox.yml"),
		newValidateConfigSubCmd(flags, &strict, &quiet, "services", "Validate devbox/services.yml"),
		newValidateConfigSubCmd(flags, &strict, &quiet, "docker", "Validate devbox/docker.yml"),
		newValidateConfigSubCmd(flags, &strict, &quiet, "info", "Validate devbox/info.yml"),
		newValidateConfigSubCmd(flags, &strict, &quiet, "styles", "Validate devbox/styles.yml"),
		newValidateConfigSubCmd(flags, &strict, &quiet, "lifecycle", "Validate devbox/lifecycle.yml"),
		newValidateConfigSubCmd(flags, &strict, &quiet, "deploy", "Validate devbox/deploy.yml"),
		newValidateConfigSubCmd(flags, &strict, &quiet, "reset", "Validate devbox/reset.yml (replaces 'devbox reset config check')"),
		newValidateConfigSubCmd(flags, &strict, &quiet, "service-deploy", "Validate service deploy configs"),
	)
	cmd.AddCommand(configCmd)

	// Templates validators subtree.
	templatesCmd := &cobra.Command{
		Use:          "templates",
		Short:        "Validate template packs",
		Long:         `Check IDE and AI template packs for validity and integrity.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, flags, strict, quiet, []string{"templates"})
		},
	}
	templatesCmd.AddCommand(
		newValidateTemplateSubCmd(flags, &strict, &quiet, "ide", "Validate IDE template pack"),
		newValidateTemplateSubCmd(flags, &strict, &quiet, "ai", "Validate AI template pack"),
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
			return runValidate(cmd, flags, strict, quiet, []string{"commands"})
		},
	})

	return cmd
}

// newValidateConfigSubCmd creates a leaf command for a single config validator.
func newValidateConfigSubCmd(flags *rootFlags, strict, quiet *bool, id, short string) *cobra.Command {
	return &cobra.Command{
		Use:          id,
		Short:        short,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, flags, *strict, *quiet, []string{"config", id})
		},
	}
}

// newValidateTemplateSubCmd creates a leaf command for a single template validator.
func newValidateTemplateSubCmd(flags *rootFlags, strict, quiet *bool, id, short string) *cobra.Command {
	return &cobra.Command{
		Use:          id,
		Short:        short,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, flags, *strict, *quiet, []string{"templates", id})
		},
	}
}

// runValidate executes validators matching the given scope and renders diagnostics.
// scope may be nil (run all), or a list like ["config"], ["config", "deploy"], etc.
func runValidate(cmd *cobra.Command, flags *rootFlags, strict, quiet bool, scope []string) error {
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

	ctx := validate.Context{ProjectRoot: projectRoot, ConfigPath: configPath, Cfg: cfg}

	// Build the registry and run validators.
	registry := buildRegistry()
	diags := registry.Run(ctx, scope...)

	// Render the diagnostics table.
	rows := ui.FormatDiagnostics(diags, quiet)
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), ui.RenderDiagnosticsTable(rows))

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

// buildRegistry assembles all validators from the three domains.
func buildRegistry() *validate.Registry {
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
	return reg
}
