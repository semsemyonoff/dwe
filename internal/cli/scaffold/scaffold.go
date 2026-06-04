// Package scaffold provides the `dwe init` command: it scaffolds a fresh DWE
// project from nothing. This package is the cli/ layer for the command — it owns
// flag parsing, interactive-vs-non-interactive mode selection, the huh form, and
// result reporting. All file-system work lives in the domain package
// internal/core/workflow/scaffold; this package is the only writer to
// stdout/stderr.
//
// The package and command name diverge by necessity: the cobra command is
// `Use: "init"`, but `init` is a reserved function name and an illegal package
// name in Go, so the package is named `scaffold` (matching the domain package).
package scaffold

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/ui/ask"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	core "github.com/semsemyonoff/dwe/internal/core/workflow/scaffold"

	huh "charm.land/huh/v2"
	"github.com/spf13/cobra"
)

// defaultPrefix is the compose/project prefix used when none is supplied.
const defaultPrefix = "dwe"

// defaultService is the starter service folder created unless --service is
// overridden (an explicit empty value scaffolds no service).
const defaultService = "app"

// prefixPattern constrains the compose/project prefix to the character set
// Docker Compose accepts for a project name: it combines with the project name
// as "${prefix}-${name}", so it must be lowercase alphanumerics plus dash /
// underscore, starting with an alphanumeric.
var prefixPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// hexColorPattern matches a 6-digit "#RRGGBB" hex color — the format
// workspace/styles.yml expects for colors.accent.
var hexColorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// validateName checks a project-name entry: required, no path separators, no
// control characters (it becomes a directory segment and a YAML scalar).
func validateName(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return errors.New("project name is required")
	}
	if strings.ContainsAny(s, `/\`) {
		return errors.New("must not contain path separators (/ or \\)")
	}
	if hasControlChars(s) {
		return errors.New("must not contain control characters")
	}
	return nil
}

// validatePrefix checks a compose/project prefix. An empty value is accepted —
// it falls back to the built-in default ("dwe").
func validatePrefix(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if !prefixPattern.MatchString(s) {
		return errors.New("must be lowercase letters, digits, '-' or '_', starting with a letter or digit")
	}
	return nil
}

// validateAccent checks the optional branding accent color. An empty value is
// accepted (the built-in palette default is used).
func validateAccent(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if !hexColorPattern.MatchString(s) {
		return errors.New(`must be a 6-digit hex color like "#2EC3EB"`)
	}
	return nil
}

// brandTextValidator returns a validator for an optional single-line branding
// string (title / tagline): empty is allowed, but control characters and line
// breaks are rejected because the value is rendered into a single-line YAML
// scalar in workspace/styles.yml.
func brandTextValidator(label string) func(string) error {
	return func(s string) error {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil
		}
		if hasControlChars(s) {
			return fmt.Errorf("%s must not contain control characters or line breaks", label)
		}
		return nil
	}
}

// hasControlChars reports whether s contains any C0/C1 control character or DEL
// (this also covers tabs, carriage returns, and newlines).
func hasControlChars(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
	})
}

// formInput carries the values a form collects (and the flag-derived defaults
// that pre-fill it). It is the seam between flag/option plumbing and the huh
// form so the interactive path can be swapped out in tests.
type formInput struct {
	Name     string
	Prefix   string
	Branding core.Branding
}

// runFormFn runs the interactive form. It is a package-level var so tests can
// replace it — exercising the abort path and the value-mapping path without
// driving a real huh form (which requires a TTY). The default implementation
// builds ask.Fields and delegates to ask.Run.
var runFormFn = func(ctx context.Context, in formInput, stdin io.Reader, stdout io.Writer) (formInput, error) {
	fields := []ask.Field{
		{
			Key:      "name",
			Kind:     ask.FieldInput,
			Title:    "Project name",
			Required: true,
			Default:  in.Name,
			Validate: validateName,
		},
		{
			Key:         "prefix",
			Kind:        ask.FieldInput,
			Title:       "Compose prefix",
			Description: "Prefixes the Docker Compose project name as \"<prefix>-<name>\"; lowercase letters, digits, '-' or '_'",
			Default:     in.Prefix,
			Validate:    validatePrefix,
		},
		{
			Key:         "brand_title",
			Kind:        ask.FieldInput,
			Title:       "Branding title (optional)",
			Description: "Headline shown in the project header (workspace/styles.yml → header.lines); leave blank for a generic header",
			Default:     in.Branding.Title,
			Validate:    brandTextValidator("title"),
		},
		{
			Key:         "tagline",
			Kind:        ask.FieldInput,
			Title:       "Tagline (optional)",
			Description: "Short subtitle rendered under the header (workspace/styles.yml → header.tagline); e.g. \"Local dev, container-orchestrated.\"",
			Default:     in.Branding.Tagline,
			Validate:    brandTextValidator("tagline"),
		},
		{
			Key:         "accent",
			Kind:        ask.FieldInput,
			Title:       "Accent color (optional)",
			Description: "Primary UI color as a 6-digit hex code, e.g. \"#2EC3EB\" (workspace/styles.yml → colors.accent)",
			Default:     in.Branding.Accent,
			Validate:    validateAccent,
		},
	}

	res, err := ask.Run(ctx, "dwe init", fields, ask.RunOptions{Input: stdin, Output: stdout})
	if err != nil {
		return formInput{}, err
	}

	return formInput{
		Name:   strings.TrimSpace(res.String("name")),
		Prefix: strings.TrimSpace(res.String("prefix")),
		Branding: core.Branding{
			Title:   strings.TrimSpace(res.String("brand_title")),
			Tagline: strings.TrimSpace(res.String("tagline")),
			Accent:  strings.TrimSpace(res.String("accent")),
		},
	}, nil
}

// confirmRecreateFn asks the user to confirm recreating a project that already
// exists in the target directory. It is a package-level var so tests can drive
// the confirm / decline / abort branches without a real TTY. The default
// delegates to widgets.RunConfirm, which returns widgets.ErrCancelled on
// Esc / Ctrl-C.
var confirmRecreateFn = func(target string) (bool, error) {
	title := fmt.Sprintf(
		"A DWE project already exists at %s.\nRecreate it from scratch? This overwrites existing files.",
		target)
	return widgets.RunConfirm(title, "Recreate", "Cancel")
}

// initFlags collects the raw flag values bound by NewCmd.
type initFlags struct {
	name        string
	prefix      string
	brandTitle  string
	tagline     string
	accent      string
	service     string
	force       bool
	useDefaults bool
}

// NewCmd builds the `dwe init` command.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	f := initFlags{}

	cmd := &cobra.Command{
		Use:   "init [name]",
		Short: "Scaffold a new DWE project",
		Long: `Scaffold a fresh DWE project in the current directory (or ./<name>/ when a
name is given). Interactive by default; falls back to flag-driven defaults when
stdin/stdout is not a TTY, or when --default / --output json is set.

If a project already exists in the target directory, init asks for confirmation
before recreating it (and recreates with --force on yes); in non-interactive mode
it refuses unless --force is passed. Otherwise it fills gaps and never overwrites
existing files unless --force is set.`,
		Example: `  dwe init
  dwe init my-project
  dwe init --name my-project --prefix acme --service api
  dwe init --default --output json
  dwe init --force            # recreate an existing project`,
		Args:         cobra.MaximumNArgs(1),
		GroupID:      groupID,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd, flags, args, f)
		},
	}

	cmd.Flags().StringVar(&f.name, "name", "", "project name (default: [name] arg or current directory name)")
	cmd.Flags().StringVar(&f.prefix, "prefix", defaultPrefix, "compose/project prefix")
	cmd.Flags().StringVar(&f.brandTitle, "brand-title", "", "branding title written to workspace/styles.yml")
	cmd.Flags().StringVar(&f.tagline, "tagline", "", "branding tagline written to workspace/styles.yml")
	cmd.Flags().StringVar(&f.accent, "accent", "", "branding accent color written to workspace/styles.yml")
	cmd.Flags().StringVar(&f.service, "service", defaultService, `starter service folder name ("" creates none)`)
	cmd.Flags().BoolVarP(&f.force, "force", "f", false, "recreate an existing project / overwrite existing files")
	cmd.Flags().BoolVarP(&f.useDefaults, "default", "d", false, "skip the interactive form and take all defaults")

	return cmd
}

// runInit resolves the scaffold Options (from flags, positional arg, and the
// interactive form when applicable), calls the domain Scaffold, and reports the
// result. The form — when run — is collected in full before any disk write, so a
// mid-form Ctrl-C leaves the disk untouched.
func runInit(cmd *cobra.Command, flags *cmdctx.RootFlags, args []string, f initFlags) error {
	// Target dir: a positional name creates ./<name>/; otherwise the cwd.
	targetDir := ""
	if len(args) > 0 {
		targetDir = args[0]
	}

	name, err := resolveName(f.name, args)
	if err != nil {
		return err
	}

	isInteractive := interactive(cmd.InOrStdin(), flags, f.useDefaults)

	// Existing-project gate: when a workspace.yml already lives in the target,
	// recreating is destructive. Require explicit consent — interactive
	// confirmation, or --force when non-interactive — before continuing.
	force := f.force
	if !force {
		absTarget, terr := core.ResolveTarget(targetDir)
		if terr != nil {
			return cmdctx.ErrWrap("scaffold_target", terr)
		}
		exists, terr := core.HasProjectConfig(absTarget)
		if terr != nil {
			return cmdctx.ErrWrap("scaffold_target", terr)
		}
		if exists {
			if !isInteractive {
				return cmdctx.Err("scaffold_project_exists",
					fmt.Sprintf("a DWE project already exists at %s", absTarget)).
					WithHint("pass --force to recreate it from scratch")
			}
			confirmed, cerr := confirmRecreateFn(absTarget)
			if cerr != nil {
				if errors.Is(cerr, widgets.ErrCancelled) {
					// Declined at the prompt — clean exit, nothing on disk.
					return nil
				}
				return cmdctx.ErrWrap("scaffold_confirm_failed", cerr)
			}
			if !confirmed {
				return nil
			}
			// Consent granted: recreate everything, overwriting in place.
			force = true
		}
	}

	in := formInput{
		Name:   name,
		Prefix: f.prefix,
		Branding: core.Branding{
			Title:   f.brandTitle,
			Tagline: f.tagline,
			Accent:  f.accent,
		},
	}

	if isInteractive {
		collected, ferr := runFormFn(cmd.Context(), in, cmd.InOrStdin(), cmd.OutOrStdout())
		if ferr != nil {
			if errors.Is(ferr, huh.ErrUserAborted) {
				// Aborted before any write — clean exit, nothing on disk.
				return nil
			}
			return cmdctx.ErrWrap("scaffold_form_failed", ferr)
		}
		in = collected
	}

	// Validate the final values uniformly. The interactive form already enforces
	// these, but flag-driven (non-interactive) values reach here unchecked.
	if verr := validateInput(in); verr != nil {
		return verr
	}

	opts := core.Options{
		TargetDir: targetDir,
		Name:      strings.TrimSpace(in.Name),
		Prefix:    strings.TrimSpace(in.Prefix),
		Service:   f.service,
		Branding:  in.Branding,
		Force:     force,
	}
	if opts.Prefix == "" {
		opts.Prefix = defaultPrefix
	}
	if opts.Name == "" {
		return cmdctx.Err("scaffold_invalid_name", "project name is required").
			WithHint("pass a name argument, --name, or run interactively")
	}

	res, err := core.Scaffold(opts)
	if err != nil {
		return cmdctx.ErrWrap("scaffold_failed", err)
	}

	return writeResult(flags, cmd, res)
}

// validateInput re-checks the resolved form/flag values before scaffolding,
// mapping any failure to a typed, user-facing error. It guards the
// non-interactive path, where flag values bypass the form's per-field
// validators.
func validateInput(in formInput) error {
	if err := validatePrefix(in.Prefix); err != nil {
		return cmdctx.Err("scaffold_invalid_prefix",
			fmt.Sprintf("invalid prefix %q: %v", strings.TrimSpace(in.Prefix), err)).
			WithHint("use lowercase letters, digits, '-' or '_'")
	}
	if err := brandTextValidator("title")(in.Branding.Title); err != nil {
		return cmdctx.Err("scaffold_invalid_branding", err.Error())
	}
	if err := brandTextValidator("tagline")(in.Branding.Tagline); err != nil {
		return cmdctx.Err("scaffold_invalid_branding", err.Error())
	}
	if err := validateAccent(in.Branding.Accent); err != nil {
		return cmdctx.Err("scaffold_invalid_accent",
			fmt.Sprintf("invalid accent color %q: %v", strings.TrimSpace(in.Branding.Accent), err)).
			WithHint(`use a 6-digit hex color like "#2EC3EB"`)
	}
	return nil
}

// resolveName resolves the project name: --name wins, then the positional [name]
// arg, then the current directory's base name.
func resolveName(nameFlag string, args []string) (string, error) {
	if n := strings.TrimSpace(nameFlag); n != "" {
		if strings.ContainsAny(n, "/\\") {
			return "", cmdctx.Err("scaffold_invalid_name",
				fmt.Sprintf("%q is not a valid project name: must not contain path separators", n)).
				WithHint(`use a simple name like "my-project"`)
		}
		return n, nil
	}
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		name := filepath.Base(strings.TrimSpace(args[0]))
		if name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
			return "", cmdctx.Err("scaffold_invalid_name",
				fmt.Sprintf("%q does not yield a usable project name", args[0])).
				WithHint(`pass a --name flag or run interactively`)
		}
		return name, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", cmdctx.ErrWrap("scaffold_cwd", err)
	}
	return filepath.Base(cwd), nil
}

// interactive reports whether the form should be shown: a real TTY on both ends,
// not --default, and not JSON output mode.
func interactive(stdin io.Reader, flags *cmdctx.RootFlags, useDefaults bool) bool {
	return widgets.IsInteractiveFn(stdin) && !useDefaults && flags.Output != "json"
}

// initJSON is the machine-readable result shape for --output json.
type initJSON struct {
	Target          string   `json:"target"`
	Created         []string `json:"created"`
	Skipped         []string `json:"skipped"`
	SymlinkFallback bool     `json:"symlink_fallback"`
	NestedWarning   bool     `json:"nested_warning"`
}

// writeResult renders the scaffold Result in the active output mode.
func writeResult(flags *cmdctx.RootFlags, cmd *cobra.Command, res core.Result) error {
	dto := initJSON{
		Target:          res.Target,
		Created:         orEmpty(res.Created),
		Skipped:         orEmpty(res.Skipped),
		SymlinkFallback: res.SymlinkFallback,
		NestedWarning:   res.NestedWarning,
	}
	return cmdctx.WriteData(flags, cmd, dto, renderInitText)
}

// renderInitText renders the human-facing summary: a nested-project warning (if
// any), grouped created/skipped lists, a symlink-fallback note, and a
// next-steps footer.
func renderInitText(d initJSON) string {
	var b strings.Builder

	if d.NestedWarning {
		b.WriteString(styles.WarningStyle().Render(
			"warning: an ancestor workspace.yml was found — this project is nested inside another DWE project"))
		b.WriteString("\n\n")
	}

	fmt.Fprintf(&b, "Initialized DWE project at %s\n", styles.StyleKey(d.Target))

	if len(d.Created) > 0 {
		b.WriteString("\n")
		b.WriteString(styles.SuccessStyle().Render("Created:"))
		b.WriteString("\n")
		for _, p := range d.Created {
			fmt.Fprintf(&b, "  + %s\n", p)
		}
	}

	if len(d.Skipped) > 0 {
		b.WriteString("\n")
		b.WriteString(styles.StyleMuted("Skipped (already present):"))
		b.WriteString("\n")
		for _, p := range d.Skipped {
			fmt.Fprintf(&b, "  · %s\n", styles.StyleMuted(p))
		}
	}

	if d.SymlinkFallback {
		b.WriteString("\n")
		b.WriteString(styles.StyleMuted(
			"note: CLAUDE.md was written as a copy of AGENTS.md (symlinks unavailable on this platform)"))
		b.WriteString("\n")
	}

	b.WriteString("\nNext steps:\n")
	b.WriteString("  1. review workspace.yml and workspace/defaults.yml\n")
	b.WriteString("  2. run `dwe validate` to check the project\n")
	b.WriteString("  3. run `dwe deploy run` to bring the stack up")

	return b.String()
}

// orEmpty returns s, or an empty (non-nil) slice when s is nil, so JSON output
// always emits `[]` rather than `null`.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
